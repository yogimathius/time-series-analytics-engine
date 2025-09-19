package ingestion

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
	"time-series-analytics-engine/analytics"
)

// MetricData represents incoming metric data
type MetricData struct {
	Name      string            `json:"name"`
	Value     float64           `json:"value"`
	Timestamp time.Time         `json:"timestamp"`
	Labels    map[string]string `json:"labels"`
}

// DataSource represents different ingestion sources
type DataSource string

const (
	HTTPSource       DataSource = "http"
	KafkaSource      DataSource = "kafka"
	MQTTSource       DataSource = "mqtt"
	PrometheusSource DataSource = "prometheus"
	StatsDSource     DataSource = "statsd"
)

// StorageWriter interface for abstracting storage operations
type StorageWriter interface {
	AddPoint(seriesID string, labels map[string]string, timestamp time.Time, value float64) error
}

// StreamProcessor handles real-time data ingestion and processing
type StreamProcessor struct {
	storage          StorageWriter
	bufferSize       int
	batchSize        int
	flushInterval    time.Duration
	dataBuffer       []MetricData
	bufferMutex      sync.Mutex
	isRunning        bool
	stopChan         chan struct{}
	wg               sync.WaitGroup
	
	// Statistics
	stats struct {
		TotalIngested    int64
		TotalProcessed   int64
		TotalErrors      int64
		BatchesProcessed int64
		AnomaliesDetected int64
		mu               sync.RWMutex
	}
	
	// Data validation and anomaly detection
	validator       *DataValidator
	anomalyEngine   *analytics.AnomalyEngine
	anomalyEnabled  bool
}

// DataValidator handles data quality and validation
type DataValidator struct {
	maxValueRange     float64
	allowedMetricNames map[string]bool
	requiredLabels    []string
}

// NewDataValidator creates a new data validator
func NewDataValidator() *DataValidator {
	return &DataValidator{
		maxValueRange:     1e12, // Allow values up to 1 trillion
		allowedMetricNames: make(map[string]bool),
		requiredLabels:    []string{},
	}
}

// SetAllowedMetrics sets the whitelist of allowed metric names
func (dv *DataValidator) SetAllowedMetrics(names []string) {
	dv.allowedMetricNames = make(map[string]bool)
	for _, name := range names {
		dv.allowedMetricNames[name] = true
	}
}

// SetRequiredLabels sets labels that must be present on all metrics
func (dv *DataValidator) SetRequiredLabels(labels []string) {
	dv.requiredLabels = labels
}

// ValidateMetric validates a single metric data point
func (dv *DataValidator) ValidateMetric(metric MetricData) error {
	// Check if metric name is allowed (if whitelist is configured)
	if len(dv.allowedMetricNames) > 0 && !dv.allowedMetricNames[metric.Name] {
		return fmt.Errorf("metric name '%s' not allowed", metric.Name)
	}
	
	// Check required labels
	for _, label := range dv.requiredLabels {
		if _, exists := metric.Labels[label]; !exists {
			return fmt.Errorf("required label '%s' missing", label)
		}
	}
	
	// Check value range
	if metric.Value > dv.maxValueRange || metric.Value < -dv.maxValueRange {
		return fmt.Errorf("value %f outside allowed range", metric.Value)
	}
	
	// Check timestamp is not too far in future (allow up to 1 hour)
	now := time.Now()
	if metric.Timestamp.After(now.Add(time.Hour)) {
		return fmt.Errorf("timestamp too far in future")
	}
	
	// Check timestamp is not too far in past (allow up to 7 days)
	if metric.Timestamp.Before(now.Add(-7 * 24 * time.Hour)) {
		return fmt.Errorf("timestamp too far in past")
	}
	
	return nil
}

// NewStreamProcessor creates a new stream processor
func NewStreamProcessor(storage StorageWriter, bufferSize, batchSize int, flushInterval time.Duration) *StreamProcessor {
	return &StreamProcessor{
		storage:       storage,
		bufferSize:    bufferSize,
		batchSize:     batchSize,
		flushInterval: flushInterval,
		dataBuffer:    make([]MetricData, 0, bufferSize),
		stopChan:      make(chan struct{}),
		validator:     NewDataValidator(),
	}
}

// Start begins the stream processing
func (sp *StreamProcessor) Start(ctx context.Context) error {
	sp.bufferMutex.Lock()
	if sp.isRunning {
		sp.bufferMutex.Unlock()
		return fmt.Errorf("stream processor already running")
	}
	sp.isRunning = true
	sp.bufferMutex.Unlock()
	
	// Start background flush routine
	sp.wg.Add(1)
	go sp.flushRoutine(ctx)
	
	return nil
}

// Stop stops the stream processor
func (sp *StreamProcessor) Stop() {
	sp.bufferMutex.Lock()
	if !sp.isRunning {
		sp.bufferMutex.Unlock()
		return
	}
	sp.isRunning = false
	sp.bufferMutex.Unlock()
	
	close(sp.stopChan)
	sp.wg.Wait()
	
	// Flush remaining data
	sp.flushBuffer()
}

// IngestMetric adds a single metric to the processing buffer
func (sp *StreamProcessor) IngestMetric(metric MetricData) error {
	// Validate metric
	if err := sp.validator.ValidateMetric(metric); err != nil {
		sp.incrementErrorCount()
		return fmt.Errorf("validation failed: %w", err)
	}
	
	sp.bufferMutex.Lock()
	defer sp.bufferMutex.Unlock()
	
	if !sp.isRunning {
		return fmt.Errorf("stream processor not running")
	}
	
	// Check buffer capacity
	if len(sp.dataBuffer) >= sp.bufferSize {
		sp.bufferMutex.Unlock()
		sp.flushBuffer()
		sp.bufferMutex.Lock()
	}
	
	sp.dataBuffer = append(sp.dataBuffer, metric)
	sp.incrementIngestedCount()
	
	// Trigger flush if batch size reached
	if len(sp.dataBuffer) >= sp.batchSize {
		sp.bufferMutex.Unlock()
		sp.flushBuffer()
		sp.bufferMutex.Lock()
	}
	
	return nil
}

// IngestBatch processes multiple metrics at once
func (sp *StreamProcessor) IngestBatch(metrics []MetricData) error {
	for _, metric := range metrics {
		if err := sp.IngestMetric(metric); err != nil {
			log.Printf("Error ingesting metric %s: %v", metric.Name, err)
			// Continue processing other metrics
		}
	}
	return nil
}

// IngestJSON processes JSON-encoded metric data
func (sp *StreamProcessor) IngestJSON(jsonData []byte) error {
	var metric MetricData
	if err := json.Unmarshal(jsonData, &metric); err != nil {
		sp.incrementErrorCount()
		return fmt.Errorf("JSON parsing failed: %w", err)
	}
	
	return sp.IngestMetric(metric)
}

// flushRoutine runs in background to periodically flush the buffer
func (sp *StreamProcessor) flushRoutine(ctx context.Context) {
	defer sp.wg.Done()
	
	ticker := time.NewTicker(sp.flushInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-sp.stopChan:
			return
		case <-ticker.C:
			sp.flushBuffer()
		}
	}
}

// flushBuffer writes buffered data to storage
func (sp *StreamProcessor) flushBuffer() {
	sp.bufferMutex.Lock()
	if len(sp.dataBuffer) == 0 {
		sp.bufferMutex.Unlock()
		return
	}
	
	// Copy buffer for processing
	batch := make([]MetricData, len(sp.dataBuffer))
	copy(batch, sp.dataBuffer)
	
	// Clear buffer
	sp.dataBuffer = sp.dataBuffer[:0]
	sp.bufferMutex.Unlock()
	
	// Process batch
	sp.processBatch(batch)
	sp.incrementBatchCount()
}

// processBatch writes a batch of metrics to storage
func (sp *StreamProcessor) processBatch(batch []MetricData) {
	for _, metric := range batch {
		err := sp.storage.AddPoint(
			metric.Name,
			metric.Labels,
			metric.Timestamp,
			metric.Value,
		)
		
		if err != nil {
			log.Printf("Error storing metric %s: %v", metric.Name, err)
			sp.incrementErrorCount()
		} else {
			sp.incrementProcessedCount()
		}
	}
}

// Statistics methods
func (sp *StreamProcessor) incrementIngestedCount() {
	sp.stats.mu.Lock()
	sp.stats.TotalIngested++
	sp.stats.mu.Unlock()
}

func (sp *StreamProcessor) incrementProcessedCount() {
	sp.stats.mu.Lock()
	sp.stats.TotalProcessed++
	sp.stats.mu.Unlock()
}

func (sp *StreamProcessor) incrementErrorCount() {
	sp.stats.mu.Lock()
	sp.stats.TotalErrors++
	sp.stats.mu.Unlock()
}

func (sp *StreamProcessor) incrementBatchCount() {
	sp.stats.mu.Lock()
	sp.stats.BatchesProcessed++
	sp.stats.mu.Unlock()
}

// GetStats returns current processing statistics
func (sp *StreamProcessor) GetStats() (int64, int64, int64, int64) {
	sp.stats.mu.RLock()
	defer sp.stats.mu.RUnlock()
	return sp.stats.TotalIngested, sp.stats.TotalProcessed, sp.stats.TotalErrors, sp.stats.BatchesProcessed
}

// HTTP endpoint handler for metric ingestion
func (sp *StreamProcessor) HandleHTTPMetric(jsonData []byte) error {
	return sp.IngestJSON(jsonData)
}

// Batch HTTP endpoint handler
func (sp *StreamProcessor) HandleHTTPBatch(jsonData []byte) error {
	var metrics []MetricData
	if err := json.Unmarshal(jsonData, &metrics); err != nil {
		sp.incrementErrorCount()
		return fmt.Errorf("JSON parsing failed: %w", err)
	}
	
	return sp.IngestBatch(metrics)
}

// Simulate Prometheus scrape format ingestion
func (sp *StreamProcessor) IngestPrometheusFormat(metricName string, labels map[string]string, value float64, timestamp time.Time) error {
	metric := MetricData{
		Name:      metricName,
		Value:     value,
		Timestamp: timestamp,
		Labels:    labels,
	}
	
	return sp.IngestMetric(metric)
}

// Simulate StatsD format ingestion
func (sp *StreamProcessor) IngestStatsDFormat(statsdLine string) error {
	// Simple StatsD parser (gauge format: metric_name:value|g)
	// This is a simplified implementation for testing
	metric := MetricData{
		Name:      "statsd.metric", // Would parse from statsdLine
		Value:     42.0,            // Would parse from statsdLine
		Timestamp: time.Now(),
		Labels:    map[string]string{"source": "statsd"},
	}
	
	return sp.IngestMetric(metric)
}

// GetBufferSize returns current buffer utilization
func (sp *StreamProcessor) GetBufferSize() int {
	sp.bufferMutex.Lock()
	defer sp.bufferMutex.Unlock()
	return len(sp.dataBuffer)
}

// IsRunning returns whether the processor is active
func (sp *StreamProcessor) IsRunning() bool {
	sp.bufferMutex.Lock()
	defer sp.bufferMutex.Unlock()
	return sp.isRunning
}

// GetValidator returns the data validator for configuration
func (sp *StreamProcessor) GetValidator() *DataValidator {
	return sp.validator
}