package analytics

import (
	"math"
	"testing"
	"time"
	"time-series-analytics-engine/storage"
)

func TestZScoreDetector_Train(t *testing.T) {
	detector := NewZScoreDetector(3.0, 100)
	
	// Generate test data with normal distribution
	points := generateTestPoints(100, 50.0, 10.0)
	
	err := detector.Train(points)
	if err != nil {
		t.Errorf("Training failed: %v", err)
	}
	
	if detector.mean == 0 {
		t.Error("Mean should be calculated after training")
	}
	
	if detector.stdDev == 0 {
		t.Error("Standard deviation should be calculated after training")
	}
}

func TestZScoreDetector_Detect(t *testing.T) {
	detector := NewZScoreDetector(2.0, 50)
	
	// Train with normal data
	points := generateTestPoints(50, 100.0, 5.0)
	detector.Train(points)
	
	// Test normal point
	normalPoint := storage.DataPoint{
		Timestamp: time.Now(),
		Value:     102.0, // Within 2 standard deviations
	}
	
	result, err := detector.Detect(normalPoint)
	if err != nil {
		t.Errorf("Detection failed: %v", err)
	}
	
	if result.IsAnomaly {
		t.Error("Normal point should not be detected as anomaly")
	}
	
	// Test anomalous point
	anomalousPoint := storage.DataPoint{
		Timestamp: time.Now(),
		Value:     150.0, // Far from mean
	}
	
	result, err = detector.Detect(anomalousPoint)
	if err != nil {
		t.Errorf("Detection failed: %v", err)
	}
	
	if !result.IsAnomaly {
		t.Error("Anomalous point should be detected as anomaly")
	}
	
	if result.Score <= 2.0 {
		t.Errorf("Anomaly score should be > 2.0, got %f", result.Score)
	}
}

func TestIQRDetector_Detect(t *testing.T) {
	detector := NewIQRDetector(1.5, 100)
	
	// Train with data
	points := generateTestPoints(100, 50.0, 10.0)
	detector.Train(points)
	
	// Test normal point
	normalPoint := storage.DataPoint{
		Timestamp: time.Now(),
		Value:     52.0,
	}
	
	result, err := detector.Detect(normalPoint)
	if err != nil {
		t.Errorf("Detection failed: %v", err)
	}
	
	if result.IsAnomaly {
		t.Error("Normal point should not be detected as anomaly")
	}
	
	// Test outlier
	outlierPoint := storage.DataPoint{
		Timestamp: time.Now(),
		Value:     500.0, // Way outside IQR
	}
	
	result, err = detector.Detect(outlierPoint)
	if err != nil {
		t.Errorf("Detection failed: %v", err)
	}
	
	if !result.IsAnomaly {
		t.Error("Outlier should be detected as anomaly")
	}
}

func TestMovingAverageDetector_Detect(t *testing.T) {
	detector := NewMovingAverageDetector(3.0, 20)
	
	// Train with steady data
	points := generateSteadyTestPoints(20, 100.0, 2.0)
	detector.Train(points)
	
	// Test normal point
	normalPoint := storage.DataPoint{
		Timestamp: time.Now(),
		Value:     101.0,
	}
	
	result, err := detector.Detect(normalPoint)
	if err != nil {
		t.Errorf("Detection failed: %v", err)
	}
	
	if result.IsAnomaly {
		t.Error("Normal point should not be detected as anomaly")
	}
	
	// Test sudden spike
	spikePoint := storage.DataPoint{
		Timestamp: time.Now(),
		Value:     120.0, // Large deviation from moving average
	}
	
	result, err = detector.Detect(spikePoint)
	if err != nil {
		t.Errorf("Detection failed: %v", err)
	}
	
	if !result.IsAnomaly {
		t.Error("Spike should be detected as anomaly")
	}
}

func TestAnomalyEngine(t *testing.T) {
	config := AnomalyConfig{
		DefaultMethod:       "zscore",
		DefaultThreshold:    3.0,
		DefaultWindowSize:   50,
		MaxResultsPerSeries: 100,
	}
	
	engine := NewAnomalyEngine(config)
	
	seriesID := "test.series"
	points := generateTestPoints(50, 100.0, 5.0)
	
	// Train detector
	err := engine.TrainDetector(seriesID, points)
	if err != nil {
		t.Errorf("Training failed: %v", err)
	}
	
	// Test normal detection
	normalPoint := storage.DataPoint{
		Timestamp: time.Now(),
		Value:     102.0,
	}
	
	result, err := engine.DetectAnomaly(seriesID, normalPoint)
	if err != nil {
		t.Errorf("Detection failed: %v", err)
	}
	
	if result.IsAnomaly {
		t.Error("Normal point should not be anomalous")
	}
	
	// Test anomaly detection
	anomalyPoint := storage.DataPoint{
		Timestamp: time.Now(),
		Value:     150.0,
	}
	
	result, err = engine.DetectAnomaly(seriesID, anomalyPoint)
	if err != nil {
		t.Errorf("Detection failed: %v", err)
	}
	
	if !result.IsAnomaly {
		t.Error("Anomalous point should be detected")
	}
	
	// Check stored results
	anomalies := engine.GetAnomalies(seriesID, 10)
	if len(anomalies) != 2 { // Both detections should be stored
		t.Errorf("Expected 2 stored results, got %d", len(anomalies))
	}
	
	// Check that the last result is the anomaly
	if !anomalies[len(anomalies)-1].IsAnomaly {
		t.Error("Last result should be anomalous")
	}
}

func TestAnomalyEngine_MultipleDetectors(t *testing.T) {
	config := AnomalyConfig{
		DefaultMethod:       "iqr",
		DefaultThreshold:    1.5,
		DefaultWindowSize:   30,
		MaxResultsPerSeries: 50,
	}
	
	engine := NewAnomalyEngine(config)
	
	// Add custom detector for one series
	zscore := NewZScoreDetector(2.5, 40)
	engine.AddDetector("custom.series", zscore)
	
	// Test that custom detector is used
	detector := engine.GetOrCreateDetector("custom.series")
	if detector.Name() != "zscore" {
		t.Errorf("Expected zscore detector, got %s", detector.Name())
	}
	
	// Test that default detector is created for new series
	detector = engine.GetOrCreateDetector("default.series")
	if detector.Name() != "iqr" {
		t.Errorf("Expected iqr detector, got %s", detector.Name())
	}
}

func TestPercentileCalculation(t *testing.T) {
	data := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	
	// Test quartiles
	q1 := percentile(data, 25)
	q2 := percentile(data, 50)
	q3 := percentile(data, 75)
	
	if q2 != 5.5 { // Median
		t.Errorf("Expected median 5.5, got %f", q2)
	}
	
	if q1 > q2 || q2 > q3 {
		t.Error("Quartiles should be in ascending order")
	}
	
	// Test edge cases
	singleValue := []float64{42}
	result := percentile(singleValue, 50)
	if result != 42 {
		t.Errorf("Expected 42, got %f", result)
	}
	
	empty := []float64{}
	result = percentile(empty, 50)
	if result != 0 {
		t.Errorf("Expected 0 for empty slice, got %f", result)
	}
}

func TestZScoreDetector_WindowSize(t *testing.T) {
	windowSize := 5
	detector := NewZScoreDetector(2.0, windowSize)
	
	// Add more points than window size
	points := generateTestPoints(10, 100.0, 5.0)
	detector.Train(points)
	
	// Verify window size is respected
	if len(detector.values) > windowSize {
		t.Errorf("Values array should not exceed window size %d, got %d", 
			windowSize, len(detector.values))
	}
	
	// Add more points through detection
	for i := 0; i < 10; i++ {
		point := storage.DataPoint{
			Timestamp: time.Now(),
			Value:     float64(100 + i),
		}
		detector.Detect(point)
	}
	
	// Window should still be respected
	if len(detector.values) > windowSize {
		t.Errorf("Values array should not exceed window size after detections %d, got %d", 
			windowSize, len(detector.values))
	}
}

func TestAnomalyResult_ExpectedRange(t *testing.T) {
	detector := NewZScoreDetector(2.0, 50)
	
	// Train with known data
	points := generateTestPoints(50, 100.0, 10.0) // Mean ~100, StdDev ~10
	detector.Train(points)
	
	testPoint := storage.DataPoint{
		Timestamp: time.Now(),
		Value:     105.0,
	}
	
	result, err := detector.Detect(testPoint)
	if err != nil {
		t.Errorf("Detection failed: %v", err)
	}
	
	// Check that expected range is reasonable
	expectedMin := result.ExpectedRange.Min
	expectedMax := result.ExpectedRange.Max
	
	if expectedMin >= expectedMax {
		t.Error("Expected range min should be less than max")
	}
	
	// Range should be reasonable (positive width)
	rangeWidth := expectedMax - expectedMin
	if rangeWidth <= 0 {
		t.Errorf("Expected range width should be positive, got %f", rangeWidth)
	}
	
	// Range should be roughly 2 * threshold * stddev, allowing for variation in test data
	if rangeWidth < 10.0 || rangeWidth > 100.0 {
		t.Errorf("Expected range width should be reasonable (10-100), got %f", rangeWidth)
	}
}

// Helper functions for testing

func generateTestPoints(count int, mean, stddev float64) []storage.DataPoint {
	points := make([]storage.DataPoint, count)
	now := time.Now()
	
	for i := 0; i < count; i++ {
		// Simple random number generation for testing
		value := mean + stddev*math.Sin(float64(i)*0.1) + stddev*0.3*math.Cos(float64(i)*0.7)
		points[i] = storage.DataPoint{
			Timestamp: now.Add(time.Duration(i) * time.Second),
			Value:     value,
		}
	}
	
	return points
}

func generateSteadyTestPoints(count int, base, variation float64) []storage.DataPoint {
	points := make([]storage.DataPoint, count)
	now := time.Now()
	
	for i := 0; i < count; i++ {
		// Generate steady values with small variation
		value := base + variation*math.Sin(float64(i)*0.2)
		points[i] = storage.DataPoint{
			Timestamp: now.Add(time.Duration(i) * time.Second),
			Value:     value,
		}
	}
	
	return points
}

// Benchmark tests

func BenchmarkZScoreDetector_Detect(b *testing.B) {
	detector := NewZScoreDetector(3.0, 100)
	points := generateTestPoints(100, 100.0, 10.0)
	detector.Train(points)
	
	testPoint := storage.DataPoint{
		Timestamp: time.Now(),
		Value:     105.0,
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		detector.Detect(testPoint)
	}
}

func BenchmarkIQRDetector_Detect(b *testing.B) {
	detector := NewIQRDetector(1.5, 100)
	points := generateTestPoints(100, 100.0, 10.0)
	detector.Train(points)
	
	testPoint := storage.DataPoint{
		Timestamp: time.Now(),
		Value:     105.0,
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		detector.Detect(testPoint)
	}
}

func BenchmarkMovingAverageDetector_Detect(b *testing.B) {
	detector := NewMovingAverageDetector(3.0, 50)
	points := generateSteadyTestPoints(50, 100.0, 2.0)
	detector.Train(points)
	
	testPoint := storage.DataPoint{
		Timestamp: time.Now(),
		Value:     105.0,
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		detector.Detect(testPoint)
	}
}

func BenchmarkAnomalyEngine_DetectAnomaly(b *testing.B) {
	config := AnomalyConfig{
		DefaultMethod:       "zscore",
		DefaultThreshold:    3.0,
		DefaultWindowSize:   100,
		MaxResultsPerSeries: 1000,
	}
	
	engine := NewAnomalyEngine(config)
	seriesID := "benchmark.series"
	points := generateTestPoints(100, 100.0, 10.0)
	engine.TrainDetector(seriesID, points)
	
	testPoint := storage.DataPoint{
		Timestamp: time.Now(),
		Value:     105.0,
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.DetectAnomaly(seriesID, testPoint)
	}
}