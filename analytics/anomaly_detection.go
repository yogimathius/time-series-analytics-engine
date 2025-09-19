package analytics

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
	"time-series-analytics-engine/storage"
)

// AnomalyDetector interface defines anomaly detection methods
type AnomalyDetector interface {
	Name() string
	Train(points []storage.DataPoint) error
	Detect(point storage.DataPoint) (AnomalyResult, error)
	GetThreshold() float64
	SetThreshold(threshold float64)
}

// AnomalyResult represents the result of anomaly detection
type AnomalyResult struct {
	IsAnomaly     bool      `json:"is_anomaly"`
	Score         float64   `json:"score"`
	Threshold     float64   `json:"threshold"`
	Method        string    `json:"method"`
	Timestamp     time.Time `json:"timestamp"`
	Value         float64   `json:"value"`
	ExpectedRange Range     `json:"expected_range,omitempty"`
}

// Range represents an expected value range
type Range struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// ZScoreDetector implements anomaly detection using Z-Score method
type ZScoreDetector struct {
	name       string
	threshold  float64
	windowSize int
	mean       float64
	stdDev     float64
	values     []float64
	mu         sync.RWMutex
}

// NewZScoreDetector creates a new Z-Score anomaly detector
func NewZScoreDetector(threshold float64, windowSize int) *ZScoreDetector {
	return &ZScoreDetector{
		name:       "zscore",
		threshold:  threshold,
		windowSize: windowSize,
		values:     make([]float64, 0, windowSize),
	}
}

func (z *ZScoreDetector) Name() string {
	return z.name
}

func (z *ZScoreDetector) GetThreshold() float64 {
	z.mu.RLock()
	defer z.mu.RUnlock()
	return z.threshold
}

func (z *ZScoreDetector) SetThreshold(threshold float64) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.threshold = threshold
}

func (z *ZScoreDetector) Train(points []storage.DataPoint) error {
	z.mu.Lock()
	defer z.mu.Unlock()

	if len(points) == 0 {
		return fmt.Errorf("no data points provided for training")
	}

	// Extract values
	values := make([]float64, len(points))
	for i, point := range points {
		values[i] = point.Value
	}

	// Keep only the most recent windowSize values
	if len(values) > z.windowSize {
		values = values[len(values)-z.windowSize:]
	}

	z.values = values
	z.updateStatistics()
	return nil
}

func (z *ZScoreDetector) Detect(point storage.DataPoint) (AnomalyResult, error) {
	z.mu.Lock()
	defer z.mu.Unlock()

	if len(z.values) < 3 {
		// Need minimum data points for statistical analysis
		return AnomalyResult{
			IsAnomaly: false,
			Score:     0,
			Threshold: z.threshold,
			Method:    z.name,
			Timestamp: point.Timestamp,
			Value:     point.Value,
		}, nil
	}

	// Calculate Z-score
	zScore := math.Abs(point.Value-z.mean) / z.stdDev
	isAnomaly := zScore > z.threshold

	// Update sliding window
	z.values = append(z.values, point.Value)
	if len(z.values) > z.windowSize {
		z.values = z.values[1:]
	}
	z.updateStatistics()

	return AnomalyResult{
		IsAnomaly: isAnomaly,
		Score:     zScore,
		Threshold: z.threshold,
		Method:    z.name,
		Timestamp: point.Timestamp,
		Value:     point.Value,
		ExpectedRange: Range{
			Min: z.mean - z.threshold*z.stdDev,
			Max: z.mean + z.threshold*z.stdDev,
		},
	}, nil
}

func (z *ZScoreDetector) updateStatistics() {
	if len(z.values) == 0 {
		return
	}

	// Calculate mean
	sum := 0.0
	for _, v := range z.values {
		sum += v
	}
	z.mean = sum / float64(len(z.values))

	// Calculate standard deviation
	if len(z.values) == 1 {
		z.stdDev = 0
		return
	}

	variance := 0.0
	for _, v := range z.values {
		diff := v - z.mean
		variance += diff * diff
	}
	variance /= float64(len(z.values) - 1)
	z.stdDev = math.Sqrt(variance)

	// Prevent division by zero
	if z.stdDev == 0 {
		z.stdDev = 1e-10
	}
}

// IQRDetector implements anomaly detection using Interquartile Range method
type IQRDetector struct {
	name       string
	multiplier float64
	windowSize int
	values     []float64
	mu         sync.RWMutex
}

// NewIQRDetector creates a new IQR anomaly detector
func NewIQRDetector(multiplier float64, windowSize int) *IQRDetector {
	return &IQRDetector{
		name:       "iqr",
		multiplier: multiplier,
		windowSize: windowSize,
		values:     make([]float64, 0, windowSize),
	}
}

func (iqr *IQRDetector) Name() string {
	return iqr.name
}

func (iqr *IQRDetector) GetThreshold() float64 {
	iqr.mu.RLock()
	defer iqr.mu.RUnlock()
	return iqr.multiplier
}

func (iqr *IQRDetector) SetThreshold(threshold float64) {
	iqr.mu.Lock()
	defer iqr.mu.Unlock()
	iqr.multiplier = threshold
}

func (iqr *IQRDetector) Train(points []storage.DataPoint) error {
	iqr.mu.Lock()
	defer iqr.mu.Unlock()

	if len(points) == 0 {
		return fmt.Errorf("no data points provided for training")
	}

	// Extract values
	values := make([]float64, len(points))
	for i, point := range points {
		values[i] = point.Value
	}

	// Keep only the most recent windowSize values
	if len(values) > iqr.windowSize {
		values = values[len(values)-iqr.windowSize:]
	}

	iqr.values = values
	return nil
}

func (iqr *IQRDetector) Detect(point storage.DataPoint) (AnomalyResult, error) {
	iqr.mu.Lock()
	defer iqr.mu.Unlock()

	if len(iqr.values) < 4 {
		// Need minimum data points for quartile calculation
		return AnomalyResult{
			IsAnomaly: false,
			Score:     0,
			Threshold: iqr.multiplier,
			Method:    iqr.name,
			Timestamp: point.Timestamp,
			Value:     point.Value,
		}, nil
	}

	// Calculate quartiles
	sortedValues := make([]float64, len(iqr.values))
	copy(sortedValues, iqr.values)
	sort.Float64s(sortedValues)

	q1 := percentile(sortedValues, 25)
	q3 := percentile(sortedValues, 75)
	iqrRange := q3 - q1

	// Calculate bounds
	lowerBound := q1 - iqr.multiplier*iqrRange
	upperBound := q3 + iqr.multiplier*iqrRange

	isAnomaly := point.Value < lowerBound || point.Value > upperBound

	// Calculate anomaly score as distance from nearest bound
	var score float64
	if point.Value < lowerBound {
		score = (lowerBound - point.Value) / iqrRange
	} else if point.Value > upperBound {
		score = (point.Value - upperBound) / iqrRange
	} else {
		score = 0
	}

	// Update sliding window
	iqr.values = append(iqr.values, point.Value)
	if len(iqr.values) > iqr.windowSize {
		iqr.values = iqr.values[1:]
	}

	return AnomalyResult{
		IsAnomaly: isAnomaly,
		Score:     score,
		Threshold: iqr.multiplier,
		Method:    iqr.name,
		Timestamp: point.Timestamp,
		Value:     point.Value,
		ExpectedRange: Range{
			Min: lowerBound,
			Max: upperBound,
		},
	}, nil
}

// MovingAverageDetector implements anomaly detection using moving average deviation
type MovingAverageDetector struct {
	name           string
	threshold      float64
	windowSize     int
	deviationLimit float64
	values         []float64
	mu             sync.RWMutex
}

// NewMovingAverageDetector creates a new moving average anomaly detector
func NewMovingAverageDetector(threshold float64, windowSize int) *MovingAverageDetector {
	return &MovingAverageDetector{
		name:           "moving_average",
		threshold:      threshold,
		windowSize:     windowSize,
		deviationLimit: threshold,
		values:         make([]float64, 0, windowSize),
	}
}

func (ma *MovingAverageDetector) Name() string {
	return ma.name
}

func (ma *MovingAverageDetector) GetThreshold() float64 {
	ma.mu.RLock()
	defer ma.mu.RUnlock()
	return ma.threshold
}

func (ma *MovingAverageDetector) SetThreshold(threshold float64) {
	ma.mu.Lock()
	defer ma.mu.Unlock()
	ma.threshold = threshold
	ma.deviationLimit = threshold
}

func (ma *MovingAverageDetector) Train(points []storage.DataPoint) error {
	ma.mu.Lock()
	defer ma.mu.Unlock()

	if len(points) == 0 {
		return fmt.Errorf("no data points provided for training")
	}

	// Extract values
	values := make([]float64, len(points))
	for i, point := range points {
		values[i] = point.Value
	}

	// Keep only the most recent windowSize values
	if len(values) > ma.windowSize {
		values = values[len(values)-ma.windowSize:]
	}

	ma.values = values
	return nil
}

func (ma *MovingAverageDetector) Detect(point storage.DataPoint) (AnomalyResult, error) {
	ma.mu.Lock()
	defer ma.mu.Unlock()

	if len(ma.values) < 2 {
		// Need minimum data points
		return AnomalyResult{
			IsAnomaly: false,
			Score:     0,
			Threshold: ma.threshold,
			Method:    ma.name,
			Timestamp: point.Timestamp,
			Value:     point.Value,
		}, nil
	}

	// Calculate moving average
	sum := 0.0
	for _, v := range ma.values {
		sum += v
	}
	average := sum / float64(len(ma.values))

	// Calculate mean absolute deviation
	mad := 0.0
	for _, v := range ma.values {
		mad += math.Abs(v - average)
	}
	mad /= float64(len(ma.values))

	// Calculate deviation from average
	deviation := math.Abs(point.Value - average)
	score := deviation / (mad + 1e-10) // Prevent division by zero

	isAnomaly := score > ma.threshold

	// Update sliding window
	ma.values = append(ma.values, point.Value)
	if len(ma.values) > ma.windowSize {
		ma.values = ma.values[1:]
	}

	return AnomalyResult{
		IsAnomaly: isAnomaly,
		Score:     score,
		Threshold: ma.threshold,
		Method:    ma.name,
		Timestamp: point.Timestamp,
		Value:     point.Value,
		ExpectedRange: Range{
			Min: average - ma.deviationLimit*mad,
			Max: average + ma.deviationLimit*mad,
		},
	}, nil
}

// AnomalyEngine manages multiple anomaly detectors for different series
type AnomalyEngine struct {
	detectors map[string]AnomalyDetector
	results   map[string][]AnomalyResult
	config    AnomalyConfig
	mu        sync.RWMutex
}

// AnomalyConfig contains configuration for anomaly detection
type AnomalyConfig struct {
	DefaultMethod      string  `json:"default_method"`
	DefaultThreshold   float64 `json:"default_threshold"`
	DefaultWindowSize  int     `json:"default_window_size"`
	MaxResultsPerSeries int    `json:"max_results_per_series"`
}

// NewAnomalyEngine creates a new anomaly detection engine
func NewAnomalyEngine(config AnomalyConfig) *AnomalyEngine {
	return &AnomalyEngine{
		detectors: make(map[string]AnomalyDetector),
		results:   make(map[string][]AnomalyResult),
		config:    config,
	}
}

// AddDetector adds a detector for a specific series
func (ae *AnomalyEngine) AddDetector(seriesID string, detector AnomalyDetector) {
	ae.mu.Lock()
	defer ae.mu.Unlock()
	ae.detectors[seriesID] = detector
}

// GetOrCreateDetector gets existing detector or creates a new one using default config
func (ae *AnomalyEngine) GetOrCreateDetector(seriesID string) AnomalyDetector {
	ae.mu.Lock()
	defer ae.mu.Unlock()

	if detector, exists := ae.detectors[seriesID]; exists {
		return detector
	}

	// Create default detector based on configuration
	var detector AnomalyDetector
	switch ae.config.DefaultMethod {
	case "iqr":
		detector = NewIQRDetector(ae.config.DefaultThreshold, ae.config.DefaultWindowSize)
	case "moving_average":
		detector = NewMovingAverageDetector(ae.config.DefaultThreshold, ae.config.DefaultWindowSize)
	default:
		detector = NewZScoreDetector(ae.config.DefaultThreshold, ae.config.DefaultWindowSize)
	}

	ae.detectors[seriesID] = detector
	return detector
}

// TrainDetector trains a detector with historical data
func (ae *AnomalyEngine) TrainDetector(seriesID string, points []storage.DataPoint) error {
	detector := ae.GetOrCreateDetector(seriesID)
	return detector.Train(points)
}

// DetectAnomaly detects anomalies in a data point
func (ae *AnomalyEngine) DetectAnomaly(seriesID string, point storage.DataPoint) (AnomalyResult, error) {
	detector := ae.GetOrCreateDetector(seriesID)
	result, err := detector.Detect(point)
	
	if err != nil {
		return result, err
	}

	// Store result
	ae.storeResult(seriesID, result)
	
	return result, nil
}

// GetAnomalies returns recent anomalies for a series
func (ae *AnomalyEngine) GetAnomalies(seriesID string, limit int) []AnomalyResult {
	ae.mu.RLock()
	defer ae.mu.RUnlock()

	results, exists := ae.results[seriesID]
	if !exists {
		return nil
	}

	// Return most recent anomalies
	start := len(results) - limit
	if start < 0 {
		start = 0
	}

	return results[start:]
}

// GetAllAnomalies returns recent anomalies for all series
func (ae *AnomalyEngine) GetAllAnomalies(limit int) map[string][]AnomalyResult {
	ae.mu.RLock()
	defer ae.mu.RUnlock()

	result := make(map[string][]AnomalyResult)
	for seriesID, results := range ae.results {
		start := len(results) - limit
		if start < 0 {
			start = 0
		}
		result[seriesID] = results[start:]
	}

	return result
}

func (ae *AnomalyEngine) storeResult(seriesID string, result AnomalyResult) {
	ae.mu.Lock()
	defer ae.mu.Unlock()

	if _, exists := ae.results[seriesID]; !exists {
		ae.results[seriesID] = make([]AnomalyResult, 0)
	}

	ae.results[seriesID] = append(ae.results[seriesID], result)

	// Limit stored results
	maxResults := ae.config.MaxResultsPerSeries
	if maxResults > 0 && len(ae.results[seriesID]) > maxResults {
		ae.results[seriesID] = ae.results[seriesID][len(ae.results[seriesID])-maxResults:]
	}
}

// Helper function to calculate percentile
func percentile(sortedData []float64, p float64) float64 {
	if len(sortedData) == 0 {
		return 0
	}
	if len(sortedData) == 1 {
		return sortedData[0]
	}

	index := (p / 100.0) * float64(len(sortedData)-1)
	lower := int(math.Floor(index))
	upper := int(math.Ceil(index))

	if lower == upper {
		return sortedData[lower]
	}

	// Linear interpolation
	weight := index - float64(lower)
	return sortedData[lower]*(1-weight) + sortedData[upper]*weight
}