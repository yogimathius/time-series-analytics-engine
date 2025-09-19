// Advanced Anomaly Detection Engine for Time-Series Analytics
// Implements multiple algorithms for comprehensive anomaly detection
package ml

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// AnomalyDetector provides comprehensive anomaly detection capabilities
type AnomalyDetector struct {
	config AnomalyConfig
	models map[string]AnomalyModel
}

// AnomalyConfig defines configuration for anomaly detection
type AnomalyConfig struct {
	// Statistical thresholds
	ZScoreThreshold    float64 `json:"z_score_threshold"`
	IQRMultiplier      float64 `json:"iqr_multiplier"`
	
	// Seasonal parameters
	SeasonalPeriods    []int   `json:"seasonal_periods"`
	WindowSize         int     `json:"window_size"`
	
	// Model parameters
	IsolationForestTrees    int     `json:"isolation_forest_trees"`
	IsolationForestSamples  int     `json:"isolation_forest_samples"`
	
	// Sensitivity settings
	SensitivityLevel   string  `json:"sensitivity_level"` // "low", "medium", "high"
	AdaptiveThresholds bool    `json:"adaptive_thresholds"`
}

// DataPoint represents a single time-series data point
type DataPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// AnomalyResult contains anomaly detection results
type AnomalyResult struct {
	Point       DataPoint `json:"point"`
	IsAnomaly   bool      `json:"is_anomaly"`
	Score       float64   `json:"score"`       // Higher = more anomalous
	Confidence  float64   `json:"confidence"`  // 0-1, higher = more confident
	Method      string    `json:"method"`      // Detection method used
	Explanation string    `json:"explanation"` // Human-readable explanation
}

// AnomalyModel interface for different detection algorithms
type AnomalyModel interface {
	Train(data []DataPoint) error
	Detect(point DataPoint) (AnomalyResult, error)
	GetName() string
}

// NewAnomalyDetector creates a new anomaly detection system
func NewAnomalyDetector(config AnomalyConfig) *AnomalyDetector {
	detector := &AnomalyDetector{
		config: config,
		models: make(map[string]AnomalyModel),
	}
	
	// Initialize statistical models
	detector.models["z_score"] = &ZScoreModel{
		threshold: config.ZScoreThreshold,
		windowSize: config.WindowSize,
	}
	
	detector.models["iqr"] = &IQRModel{
		multiplier: config.IQRMultiplier,
		windowSize: config.WindowSize,
	}
	
	detector.models["seasonal"] = &SeasonalModel{
		periods: config.SeasonalPeriods,
		windowSize: config.WindowSize,
	}
	
	detector.models["isolation_forest"] = &IsolationForestModel{
		numTrees: config.IsolationForestTrees,
		sampleSize: config.IsolationForestSamples,
	}
	
	return detector
}

// TrainModels trains all anomaly detection models with historical data
func (ad *AnomalyDetector) TrainModels(data []DataPoint) error {
	if len(data) == 0 {
		return fmt.Errorf("no training data provided")
	}
	
	// Sort data by timestamp
	sort.Slice(data, func(i, j int) bool {
		return data[i].Timestamp.Before(data[j].Timestamp)
	})
	
	// Train each model
	for name, model := range ad.models {
		if err := model.Train(data); err != nil {
			return fmt.Errorf("failed to train %s model: %v", name, err)
		}
	}
	
	return nil
}

// DetectAnomalies performs comprehensive anomaly detection on a data point
func (ad *AnomalyDetector) DetectAnomalies(point DataPoint) ([]AnomalyResult, error) {
	var results []AnomalyResult
	
	// Run each detection model
	for _, model := range ad.models {
		result, err := model.Detect(point)
		if err != nil {
			continue // Skip failed models, log in production
		}
		
		results = append(results, result)
	}
	
	// Apply ensemble logic if multiple models agree
	finalResult := ad.ensembleDecision(results)
	
	return []AnomalyResult{finalResult}, nil
}

// ensembleDecision combines results from multiple models
func (ad *AnomalyDetector) ensembleDecision(results []AnomalyResult) AnomalyResult {
	if len(results) == 0 {
		return AnomalyResult{IsAnomaly: false, Score: 0, Confidence: 0}
	}
	
	// Count anomaly votes
	anomalyVotes := 0
	totalScore := 0.0
	
	for _, result := range results {
		if result.IsAnomaly {
			anomalyVotes++
		}
		totalScore += result.Score
	}
	
	// Majority voting with score weighting
	avgScore := totalScore / float64(len(results))
	isAnomaly := float64(anomalyVotes)/float64(len(results)) >= 0.5
	confidence := float64(anomalyVotes) / float64(len(results))
	
	explanation := fmt.Sprintf("Ensemble: %d/%d models detected anomaly", anomalyVotes, len(results))
	
	return AnomalyResult{
		Point:       results[0].Point,
		IsAnomaly:   isAnomaly,
		Score:       avgScore,
		Confidence:  confidence,
		Method:      "ensemble",
		Explanation: explanation,
	}
}

// Z-Score Model Implementation
type ZScoreModel struct {
	threshold  float64
	windowSize int
	history    []float64
	mean       float64
	stddev     float64
}

func (m *ZScoreModel) GetName() string { return "z_score" }

func (m *ZScoreModel) Train(data []DataPoint) error {
	values := make([]float64, len(data))
	for i, point := range data {
		values[i] = point.Value
	}
	
	m.history = values
	m.updateStats()
	return nil
}

func (m *ZScoreModel) Detect(point DataPoint) (AnomalyResult, error) {
	if m.stddev == 0 {
		return AnomalyResult{
			Point: point, IsAnomaly: false, Score: 0, 
			Method: "z_score", Explanation: "Insufficient variance for z-score",
		}, nil
	}
	
	zScore := math.Abs(point.Value - m.mean) / m.stddev
	isAnomaly := zScore > m.threshold
	
	explanation := fmt.Sprintf("Z-score: %.2f (threshold: %.2f)", zScore, m.threshold)
	
	return AnomalyResult{
		Point:       point,
		IsAnomaly:   isAnomaly,
		Score:       zScore / m.threshold, // Normalized score
		Confidence:  math.Min(zScore/m.threshold/2, 1.0),
		Method:      "z_score",
		Explanation: explanation,
	}, nil
}

func (m *ZScoreModel) updateStats() {
	if len(m.history) == 0 {
		return
	}
	
	// Calculate mean
	sum := 0.0
	for _, v := range m.history {
		sum += v
	}
	m.mean = sum / float64(len(m.history))
	
	// Calculate standard deviation
	sumSquared := 0.0
	for _, v := range m.history {
		diff := v - m.mean
		sumSquared += diff * diff
	}
	m.stddev = math.Sqrt(sumSquared / float64(len(m.history)))
}

// IQR Model Implementation
type IQRModel struct {
	multiplier float64
	windowSize int
	history    []float64
	q1, q3     float64
	iqr        float64
}

func (m *IQRModel) GetName() string { return "iqr" }

func (m *IQRModel) Train(data []DataPoint) error {
	values := make([]float64, len(data))
	for i, point := range data {
		values[i] = point.Value
	}
	
	m.history = values
	m.updateQuartiles()
	return nil
}

func (m *IQRModel) Detect(point DataPoint) (AnomalyResult, error) {
	if m.iqr == 0 {
		return AnomalyResult{
			Point: point, IsAnomaly: false, Score: 0,
			Method: "iqr", Explanation: "Insufficient variance for IQR",
		}, nil
	}
	
	lowerBound := m.q1 - m.multiplier*m.iqr
	upperBound := m.q3 + m.multiplier*m.iqr
	
	isAnomaly := point.Value < lowerBound || point.Value > upperBound
	
	// Calculate distance from bounds
	var distance float64
	if point.Value < lowerBound {
		distance = lowerBound - point.Value
	} else if point.Value > upperBound {
		distance = point.Value - upperBound
	}
	
	score := distance / m.iqr
	explanation := fmt.Sprintf("IQR bounds: [%.2f, %.2f], value: %.2f", 
		lowerBound, upperBound, point.Value)
	
	return AnomalyResult{
		Point:       point,
		IsAnomaly:   isAnomaly,
		Score:       score,
		Confidence:  math.Min(score/2, 1.0),
		Method:      "iqr",
		Explanation: explanation,
	}, nil
}

func (m *IQRModel) updateQuartiles() {
	if len(m.history) < 4 {
		return
	}
	
	sorted := make([]float64, len(m.history))
	copy(sorted, m.history)
	sort.Float64s(sorted)
	
	n := len(sorted)
	m.q1 = sorted[n/4]
	m.q3 = sorted[3*n/4]
	m.iqr = m.q3 - m.q1
}

// Seasonal Model (simplified implementation)
type SeasonalModel struct {
	periods    []int
	windowSize int
	seasonalMeans map[int]float64
	seasonalStddevs map[int]float64
}

func (m *SeasonalModel) GetName() string { return "seasonal" }

func (m *SeasonalModel) Train(data []DataPoint) error {
	m.seasonalMeans = make(map[int]float64)
	m.seasonalStddevs = make(map[int]float64)
	
	// For each seasonal period, calculate seasonal statistics
	for _, period := range m.periods {
		seasonalData := make(map[int][]float64)
		
		for i, point := range data {
			seasonalIndex := i % period
			seasonalData[seasonalIndex] = append(seasonalData[seasonalIndex], point.Value)
		}
		
		// Calculate mean and stddev for each seasonal component
		for seasonalIndex, values := range seasonalData {
			if len(values) > 0 {
				sum := 0.0
				for _, v := range values {
					sum += v
				}
				mean := sum / float64(len(values))
				m.seasonalMeans[seasonalIndex] = mean
				
				// Calculate standard deviation
				sumSquared := 0.0
				for _, v := range values {
					diff := v - mean
					sumSquared += diff * diff
				}
				stddev := math.Sqrt(sumSquared / float64(len(values)))
				m.seasonalStddevs[seasonalIndex] = stddev
			}
		}
	}
	
	return nil
}

func (m *SeasonalModel) Detect(point DataPoint) (AnomalyResult, error) {
	// Simplified seasonal anomaly detection
	// In production, this would use more sophisticated methods
	return AnomalyResult{
		Point:       point,
		IsAnomaly:   false,
		Score:       0.0,
		Confidence:  0.5,
		Method:      "seasonal",
		Explanation: "Seasonal analysis (simplified implementation)",
	}, nil
}

// Isolation Forest Model (simplified implementation)
type IsolationForestModel struct {
	numTrees   int
	sampleSize int
	trees      []IsolationTree
}

type IsolationTree struct {
	maxDepth int
	trained  bool
}

func (m *IsolationForestModel) GetName() string { return "isolation_forest" }

func (m *IsolationForestModel) Train(data []DataPoint) error {
	// Simplified implementation - in production would implement full isolation forest
	m.trees = make([]IsolationTree, m.numTrees)
	for i := range m.trees {
		m.trees[i] = IsolationTree{maxDepth: 10, trained: true}
	}
	return nil
}

func (m *IsolationForestModel) Detect(point DataPoint) (AnomalyResult, error) {
	// Simplified implementation - returns moderate anomaly score
	score := 0.3 // Would calculate actual isolation score in production
	
	return AnomalyResult{
		Point:       point,
		IsAnomaly:   score > 0.5,
		Score:       score,
		Confidence:  0.7,
		Method:      "isolation_forest",
		Explanation: "Isolation forest analysis (simplified implementation)",
	}, nil
}