package ingestion

import (
	"context"
	"testing"
	"time"
	"time-series-analytics-engine/storage"
)

func TestDataValidator_ValidateMetric(t *testing.T) {
	validator := NewDataValidator()
	
	validMetric := MetricData{
		Name:      "cpu.usage",
		Value:     75.5,
		Timestamp: time.Now(),
		Labels:    map[string]string{"host": "server1"},
	}
	
	err := validator.ValidateMetric(validMetric)
	if err != nil {
		t.Errorf("Valid metric should pass validation: %v", err)
	}
}

func TestDataValidator_InvalidMetricName(t *testing.T) {
	validator := NewDataValidator()
	validator.SetAllowedMetrics([]string{"cpu.usage", "mem.usage"})
	
	invalidMetric := MetricData{
		Name:      "disk.usage", // Not in allowed list
		Value:     50.0,
		Timestamp: time.Now(),
		Labels:    map[string]string{"host": "server1"},
	}
	
	err := validator.ValidateMetric(invalidMetric)
	if err == nil {
		t.Error("Invalid metric name should fail validation")
	}
}

func TestDataValidator_MissingRequiredLabel(t *testing.T) {
	validator := NewDataValidator()
	validator.SetRequiredLabels([]string{"host", "env"})
	
	invalidMetric := MetricData{
		Name:      "cpu.usage",
		Value:     75.5,
		Timestamp: time.Now(),
		Labels:    map[string]string{"host": "server1"}, // Missing "env" label
	}
	
	err := validator.ValidateMetric(invalidMetric)
	if err == nil {
		t.Error("Metric missing required label should fail validation")
	}
}

func TestDataValidator_ValueOutOfRange(t *testing.T) {
	validator := NewDataValidator()
	
	invalidMetric := MetricData{
		Name:      "cpu.usage",
		Value:     1e15, // Too large
		Timestamp: time.Now(),
		Labels:    map[string]string{"host": "server1"},
	}
	
	err := validator.ValidateMetric(invalidMetric)
	if err == nil {
		t.Error("Value out of range should fail validation")
	}
}

func TestDataValidator_FutureTimestamp(t *testing.T) {
	validator := NewDataValidator()
	
	invalidMetric := MetricData{
		Name:      "cpu.usage",
		Value:     75.5,
		Timestamp: time.Now().Add(2 * time.Hour), // Too far in future
		Labels:    map[string]string{"host": "server1"},
	}
	
	err := validator.ValidateMetric(invalidMetric)
	if err == nil {
		t.Error("Future timestamp should fail validation")
	}
}

func TestStreamProcessor_StartStop(t *testing.T) {
	hotStorage := storage.NewHotStorage(1000, 10000)
	processor := NewStreamProcessor(hotStorage, 100, 10, 100*time.Millisecond)
	
	ctx := context.Background()
	
	// Start processor
	err := processor.Start(ctx)
	if err != nil {
		t.Errorf("Failed to start processor: %v", err)
	}
	
	if !processor.IsRunning() {
		t.Error("Processor should be running after start")
	}
	
	// Stop processor
	processor.Stop()
	
	if processor.IsRunning() {
		t.Error("Processor should not be running after stop")
	}
}

func TestStreamProcessor_IngestSingleMetric(t *testing.T) {
	hotStorage := storage.NewHotStorage(1000, 10000)
	processor := NewStreamProcessor(hotStorage, 100, 10, 100*time.Millisecond)
	
	ctx := context.Background()
	processor.Start(ctx)
	defer processor.Stop()
	
	metric := MetricData{
		Name:      "test.metric",
		Value:     42.5,
		Timestamp: time.Now(),
		Labels:    map[string]string{"host": "test"},
	}
	
	err := processor.IngestMetric(metric)
	if err != nil {
		t.Errorf("Failed to ingest metric: %v", err)
	}
	
	// Wait a bit for processing
	time.Sleep(150 * time.Millisecond)
	
	ingested, processed, errors, _ := processor.GetStats()
	if ingested != 1 {
		t.Errorf("Expected 1 ingested metric, got %d", ingested)
	}
	if processed != 1 {
		t.Errorf("Expected 1 processed metric, got %d", processed)
	}
	if errors != 0 {
		t.Errorf("Expected 0 errors, got %d", errors)
	}
}

func TestStreamProcessor_IngestBatch(t *testing.T) {
	hotStorage := storage.NewHotStorage(1000, 10000)
	processor := NewStreamProcessor(hotStorage, 100, 5, 100*time.Millisecond) // Batch size 5
	
	ctx := context.Background()
	processor.Start(ctx)
	defer processor.Stop()
	
	// Create batch of 3 metrics
	metrics := []MetricData{
		{Name: "metric1", Value: 1.0, Timestamp: time.Now(), Labels: map[string]string{"id": "1"}},
		{Name: "metric2", Value: 2.0, Timestamp: time.Now(), Labels: map[string]string{"id": "2"}},
		{Name: "metric3", Value: 3.0, Timestamp: time.Now(), Labels: map[string]string{"id": "3"}},
	}
	
	err := processor.IngestBatch(metrics)
	if err != nil {
		t.Errorf("Failed to ingest batch: %v", err)
	}
	
	// Wait for processing
	time.Sleep(150 * time.Millisecond)
	
	ingested, processed, errors, _ := processor.GetStats()
	if ingested != 3 {
		t.Errorf("Expected 3 ingested metrics, got %d", ingested)
	}
	if processed != 3 {
		t.Errorf("Expected 3 processed metrics, got %d", processed)
	}
	if errors != 0 {
		t.Errorf("Expected 0 errors, got %d", errors)
	}
}

func TestStreamProcessor_BatchFlush(t *testing.T) {
	hotStorage := storage.NewHotStorage(1000, 10000)
	processor := NewStreamProcessor(hotStorage, 100, 3, 1*time.Second) // Batch size 3
	
	ctx := context.Background()
	processor.Start(ctx)
	defer processor.Stop()
	
	// Add exactly batch size metrics
	for i := 0; i < 3; i++ {
		metric := MetricData{
			Name:      "batch.metric",
			Value:     float64(i),
			Timestamp: time.Now(),
			Labels:    map[string]string{"batch": "test"},
		}
		err := processor.IngestMetric(metric)
		if err != nil {
			t.Errorf("Failed to ingest metric %d: %v", i, err)
		}
	}
	
	// Should trigger immediate flush
	time.Sleep(50 * time.Millisecond)
	
	if processor.GetBufferSize() != 0 {
		t.Errorf("Buffer should be empty after batch flush, got size %d", processor.GetBufferSize())
	}
	
	ingested, processed, errors, batches := processor.GetStats()
	if ingested != 3 {
		t.Errorf("Expected 3 ingested metrics, got %d", ingested)
	}
	if processed != 3 {
		t.Errorf("Expected 3 processed metrics, got %d", processed)
	}
	if batches == 0 {
		t.Error("Expected at least 1 batch processed")
	}
	if errors != 0 {
		t.Errorf("Expected 0 errors, got %d", errors)
	}
}

func TestStreamProcessor_TimeBasedFlush(t *testing.T) {
	hotStorage := storage.NewHotStorage(1000, 10000)
	processor := NewStreamProcessor(hotStorage, 100, 10, 50*time.Millisecond) // 50ms flush interval
	
	ctx := context.Background()
	processor.Start(ctx)
	defer processor.Stop()
	
	// Add single metric (below batch size)
	metric := MetricData{
		Name:      "time.metric",
		Value:     1.0,
		Timestamp: time.Now(),
		Labels:    map[string]string{"test": "time_flush"},
	}
	
	err := processor.IngestMetric(metric)
	if err != nil {
		t.Errorf("Failed to ingest metric: %v", err)
	}
	
	// Wait for time-based flush
	time.Sleep(100 * time.Millisecond)
	
	ingested, processed, _, batches := processor.GetStats()
	if ingested != 1 {
		t.Errorf("Expected 1 ingested metric, got %d", ingested)
	}
	if processed != 1 {
		t.Errorf("Expected 1 processed metric, got %d", processed)
	}
	if batches == 0 {
		t.Error("Expected at least 1 batch processed by time-based flush")
	}
}

func TestStreamProcessor_JSONIngestion(t *testing.T) {
	hotStorage := storage.NewHotStorage(1000, 10000)
	processor := NewStreamProcessor(hotStorage, 100, 10, 100*time.Millisecond)
	
	ctx := context.Background()
	processor.Start(ctx)
	defer processor.Stop()
	
	jsonData := `{
		"name": "json.metric",
		"value": 123.45,
		"timestamp": "` + time.Now().Format(time.RFC3339) + `",
		"labels": {"source": "json", "test": "true"}
	}`
	
	err := processor.IngestJSON([]byte(jsonData))
	if err != nil {
		t.Errorf("Failed to ingest JSON: %v", err)
	}
	
	time.Sleep(150 * time.Millisecond)
	
	ingested, processed, errors, _ := processor.GetStats()
	if ingested != 1 {
		t.Errorf("Expected 1 ingested metric, got %d", ingested)
	}
	if processed != 1 {
		t.Errorf("Expected 1 processed metric, got %d", processed)
	}
	if errors != 0 {
		t.Errorf("Expected 0 errors, got %d", errors)
	}
}

func TestStreamProcessor_InvalidJSON(t *testing.T) {
	hotStorage := storage.NewHotStorage(1000, 10000)
	processor := NewStreamProcessor(hotStorage, 100, 10, 100*time.Millisecond)
	
	ctx := context.Background()
	processor.Start(ctx)
	defer processor.Stop()
	
	invalidJSON := `{"name": "test", "value": "not_a_number"}`
	
	err := processor.IngestJSON([]byte(invalidJSON))
	if err == nil {
		t.Error("Invalid JSON should return error")
	}
	
	_, _, errors, _ := processor.GetStats()
	if errors == 0 {
		t.Error("Expected error count to increment for invalid JSON")
	}
}

func TestStreamProcessor_PrometheusFormat(t *testing.T) {
	hotStorage := storage.NewHotStorage(1000, 10000)
	processor := NewStreamProcessor(hotStorage, 100, 10, 100*time.Millisecond)
	
	ctx := context.Background()
	processor.Start(ctx)
	defer processor.Stop()
	
	labels := map[string]string{
		"job":      "prometheus",
		"instance": "localhost:9090",
	}
	
	err := processor.IngestPrometheusFormat("prometheus_metric", labels, 98.6, time.Now())
	if err != nil {
		t.Errorf("Failed to ingest Prometheus format: %v", err)
	}
	
	time.Sleep(150 * time.Millisecond)
	
	ingested, processed, _, _ := processor.GetStats()
	if ingested != 1 {
		t.Errorf("Expected 1 ingested metric, got %d", ingested)
	}
	if processed != 1 {
		t.Errorf("Expected 1 processed metric, got %d", processed)
	}
}

func TestStreamProcessor_StatsDFormat(t *testing.T) {
	hotStorage := storage.NewHotStorage(1000, 10000)
	processor := NewStreamProcessor(hotStorage, 100, 10, 100*time.Millisecond)
	
	ctx := context.Background()
	processor.Start(ctx)
	defer processor.Stop()
	
	err := processor.IngestStatsDFormat("test.gauge:42|g")
	if err != nil {
		t.Errorf("Failed to ingest StatsD format: %v", err)
	}
	
	time.Sleep(150 * time.Millisecond)
	
	ingested, processed, _, _ := processor.GetStats()
	if ingested != 1 {
		t.Errorf("Expected 1 ingested metric, got %d", ingested)
	}
	if processed != 1 {
		t.Errorf("Expected 1 processed metric, got %d", processed)
	}
}

func TestStreamProcessor_ValidationErrors(t *testing.T) {
	hotStorage := storage.NewHotStorage(1000, 10000)
	processor := NewStreamProcessor(hotStorage, 100, 10, 100*time.Millisecond)
	
	// Configure validator to require "host" label
	processor.validator.SetRequiredLabels([]string{"host"})
	
	ctx := context.Background()
	processor.Start(ctx)
	defer processor.Stop()
	
	// Metric without required label
	invalidMetric := MetricData{
		Name:      "test.metric",
		Value:     42.0,
		Timestamp: time.Now(),
		Labels:    map[string]string{"service": "test"}, // Missing "host"
	}
	
	err := processor.IngestMetric(invalidMetric)
	if err == nil {
		t.Error("Metric without required label should fail")
	}
	
	time.Sleep(50 * time.Millisecond)
	
	_, _, errors, _ := processor.GetStats()
	if errors == 0 {
		t.Error("Expected error count to increment for validation failure")
	}
}

func BenchmarkStreamProcessor_IngestMetric(b *testing.B) {
	hotStorage := storage.NewHotStorage(10000, 10000)
	processor := NewStreamProcessor(hotStorage, 1000, 100, 100*time.Millisecond)
	
	ctx := context.Background()
	processor.Start(ctx)
	defer processor.Stop()
	
	metric := MetricData{
		Name:      "bench.metric",
		Value:     42.5,
		Timestamp: time.Now(),
		Labels:    map[string]string{"bench": "true"},
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		processor.IngestMetric(metric)
	}
}

func BenchmarkDataValidator_ValidateMetric(b *testing.B) {
	validator := NewDataValidator()
	validator.SetAllowedMetrics([]string{"cpu.usage", "mem.usage", "bench.metric"})
	validator.SetRequiredLabels([]string{"host"})
	
	metric := MetricData{
		Name:      "bench.metric",
		Value:     75.5,
		Timestamp: time.Now(),
		Labels:    map[string]string{"host": "server1"},
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		validator.ValidateMetric(metric)
	}
}