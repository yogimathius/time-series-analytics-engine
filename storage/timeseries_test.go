package storage

import (
	"math"
	"testing"
	"time"
)

func TestSeries_AddPoint(t *testing.T) {
	series := NewSeries("test.metric", map[string]string{"host": "server1"})
	
	// Add points in order
	now := time.Now()
	series.AddPoint(now, 10.0)
	series.AddPoint(now.Add(time.Minute), 20.0)
	series.AddPoint(now.Add(2*time.Minute), 30.0)
	
	if series.Size() != 3 {
		t.Errorf("Expected 3 points, got %d", series.Size())
	}
}

func TestSeries_AddPointOutOfOrder(t *testing.T) {
	series := NewSeries("test.metric", nil)
	
	now := time.Now()
	series.AddPoint(now.Add(2*time.Minute), 30.0)  // Future point
	series.AddPoint(now, 10.0)                      // Past point
	series.AddPoint(now.Add(time.Minute), 20.0)     // Middle point
	
	if series.Size() != 3 {
		t.Errorf("Expected 3 points, got %d", series.Size())
	}
	
	// Verify points are sorted by timestamp
	points := series.GetLatest(3)
	for i := 1; i < len(points); i++ {
		if points[i-1].Timestamp.After(points[i].Timestamp) {
			t.Error("Points are not sorted by timestamp")
		}
	}
}

func TestSeries_AddPointDuplicate(t *testing.T) {
	series := NewSeries("test.metric", nil)
	
	now := time.Now()
	series.AddPoint(now, 10.0)
	series.AddPoint(now, 20.0) // Same timestamp, should update
	
	if series.Size() != 1 {
		t.Errorf("Expected 1 point after duplicate, got %d", series.Size())
	}
	
	points := series.GetLatest(1)
	if points[0].Value != 20.0 {
		t.Errorf("Expected value 20.0, got %f", points[0].Value)
	}
}

func TestSeries_GetRange(t *testing.T) {
	series := NewSeries("test.metric", nil)
	
	now := time.Now()
	// Add 5 points at 1-minute intervals
	for i := 0; i < 5; i++ {
		series.AddPoint(now.Add(time.Duration(i)*time.Minute), float64(i*10))
	}
	
	// Get range from minute 1 to minute 3
	start := now.Add(time.Minute)
	end := now.Add(3 * time.Minute)
	points := series.GetRange(start, end)
	
	if len(points) != 3 {
		t.Errorf("Expected 3 points in range, got %d", len(points))
	}
	
	// Verify correct values
	expectedValues := []float64{10.0, 20.0, 30.0}
	for i, point := range points {
		if point.Value != expectedValues[i] {
			t.Errorf("Expected value %f, got %f", expectedValues[i], point.Value)
		}
	}
}

func TestSeries_GetLatest(t *testing.T) {
	series := NewSeries("test.metric", nil)
	
	now := time.Now()
	for i := 0; i < 10; i++ {
		series.AddPoint(now.Add(time.Duration(i)*time.Minute), float64(i))
	}
	
	// Get latest 3 points
	points := series.GetLatest(3)
	if len(points) != 3 {
		t.Errorf("Expected 3 latest points, got %d", len(points))
	}
	
	// Should be values 7, 8, 9
	expectedValues := []float64{7.0, 8.0, 9.0}
	for i, point := range points {
		if point.Value != expectedValues[i] {
			t.Errorf("Expected value %f, got %f", expectedValues[i], point.Value)
		}
	}
}

func TestHotStorage_AddPoint(t *testing.T) {
	hs := NewHotStorage(100, 1000)
	
	labels := map[string]string{"host": "server1", "metric": "cpu"}
	now := time.Now()
	
	err := hs.AddPoint("cpu.usage", labels, now, 75.5)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	
	if hs.GetSeriesCount() != 1 {
		t.Errorf("Expected 1 series, got %d", hs.GetSeriesCount())
	}
	
	if hs.GetTotalPoints() != 1 {
		t.Errorf("Expected 1 total point, got %d", hs.GetTotalPoints())
	}
}

func TestHotStorage_SeriesLimit(t *testing.T) {
	hs := NewHotStorage(2, 1000) // Limit to 2 series
	
	labels := map[string]string{"host": "server1"}
	now := time.Now()
	
	// Add 2 series - should succeed
	err := hs.AddPoint("metric1", labels, now, 1.0)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	
	err = hs.AddPoint("metric2", labels, now, 2.0)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	
	// Add 3rd series - should fail
	err = hs.AddPoint("metric3", labels, now, 3.0)
	if err == nil {
		t.Error("Expected error when exceeding series limit")
	}
}

func TestHotStorage_PointsPerSeriesLimit(t *testing.T) {
	hs := NewHotStorage(10, 3) // Limit to 3 points per series
	
	labels := map[string]string{"host": "server1"}
	now := time.Now()
	
	// Add 4 points to same series
	for i := 0; i < 4; i++ {
		err := hs.AddPoint("test.metric", labels, now.Add(time.Duration(i)*time.Minute), float64(i))
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
	}
	
	// Should still have only 3 points (oldest evicted)
	if hs.GetTotalPoints() != 3 {
		t.Errorf("Expected 3 total points, got %d", hs.GetTotalPoints())
	}
	
	series, exists := hs.GetSeries("test.metric")
	if !exists {
		t.Fatal("Series should exist")
	}
	
	if series.Size() != 3 {
		t.Errorf("Expected 3 points in series, got %d", series.Size())
	}
	
	// Latest point should be value 3
	latest := series.GetLatest(1)
	if len(latest) == 0 || latest[0].Value != 3.0 {
		t.Error("Expected latest point to have value 3.0")
	}
}

func TestHotStorage_GetSeriesByLabels(t *testing.T) {
	hs := NewHotStorage(100, 1000)
	now := time.Now()
	
	// Add series with different labels
	hs.AddPoint("cpu.usage", map[string]string{"host": "server1", "env": "prod"}, now, 80.0)
	hs.AddPoint("mem.usage", map[string]string{"host": "server1", "env": "dev"}, now, 60.0)
	hs.AddPoint("disk.usage", map[string]string{"host": "server2", "env": "prod"}, now, 40.0)
	
	// Filter by host
	series := hs.GetSeriesByLabels(map[string]string{"host": "server1"})
	if len(series) != 2 {
		t.Errorf("Expected 2 series for server1, got %d", len(series))
	}
	
	// Filter by env
	series = hs.GetSeriesByLabels(map[string]string{"env": "prod"})
	if len(series) != 2 {
		t.Errorf("Expected 2 series for prod env, got %d", len(series))
	}
	
	// Filter by both
	series = hs.GetSeriesByLabels(map[string]string{"host": "server1", "env": "prod"})
	if len(series) != 1 {
		t.Errorf("Expected 1 series for server1+prod, got %d", len(series))
	}
}

func TestHotStorage_CleanupStale(t *testing.T) {
	hs := NewHotStorage(100, 1000)
	now := time.Now()
	
	// Add some series
	hs.AddPoint("metric1", nil, now, 1.0)
	hs.AddPoint("metric2", nil, now, 2.0)
	
	// Manually set one series as stale
	series, _ := hs.GetSeries("metric1")
	series.LastSeen = now.Add(-2 * time.Hour) // 2 hours ago
	
	// Cleanup stale series (older than 1 hour)
	removed := hs.CleanupStale(time.Hour)
	
	if removed != 1 {
		t.Errorf("Expected 1 stale series removed, got %d", removed)
	}
	
	if hs.GetSeriesCount() != 1 {
		t.Errorf("Expected 1 series remaining, got %d", hs.GetSeriesCount())
	}
	
	// Remaining series should be metric2
	_, exists := hs.GetSeries("metric2")
	if !exists {
		t.Error("metric2 should still exist")
	}
}

func TestAggregationFunctions(t *testing.T) {
	series := NewSeries("test.metric", nil)
	now := time.Now()
	
	// Add test data: values 10, 20, 30, 40, 50
	for i := 0; i < 5; i++ {
		series.AddPoint(now.Add(time.Duration(i)*time.Minute), float64((i+1)*10))
	}
	
	start := now
	end := now.Add(5 * time.Minute)
	
	// Test Avg
	avg := series.Aggregate(start, end, Avg)
	expected := 30.0 // (10+20+30+40+50)/5
	if avg != expected {
		t.Errorf("Expected avg %f, got %f", expected, avg)
	}
	
	// Test Max
	max := series.Aggregate(start, end, Max)
	if max != 50.0 {
		t.Errorf("Expected max 50, got %f", max)
	}
	
	// Test Min
	min := series.Aggregate(start, end, Min)
	if min != 10.0 {
		t.Errorf("Expected min 10, got %f", min)
	}
	
	// Test Sum
	sum := series.Aggregate(start, end, Sum)
	if sum != 150.0 {
		t.Errorf("Expected sum 150, got %f", sum)
	}
}

func TestAggregationFunctions_EmptyRange(t *testing.T) {
	series := NewSeries("test.metric", nil)
	now := time.Now()
	
	// Test aggregation on empty range
	start := now
	end := now.Add(time.Hour)
	
	avg := series.Aggregate(start, end, Avg)
	if !math.IsNaN(avg) {
		t.Errorf("Expected NaN for avg of empty range, got %f", avg)
	}
	
	max := series.Aggregate(start, end, Max)
	if !math.IsNaN(max) {
		t.Errorf("Expected NaN for max of empty range, got %f", max)
	}
	
	sum := series.Aggregate(start, end, Sum)
	if sum != 0.0 {
		t.Errorf("Expected 0 for sum of empty range, got %f", sum)
	}
}

func BenchmarkSeries_AddPoint(b *testing.B) {
	series := NewSeries("bench.metric", nil)
	now := time.Now()
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		series.AddPoint(now.Add(time.Duration(i)*time.Microsecond), float64(i))
	}
}

func BenchmarkHotStorage_AddPoint(b *testing.B) {
	hs := NewHotStorage(10000, 10000)
	labels := map[string]string{"host": "server1"}
	now := time.Now()
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hs.AddPoint("bench.metric", labels, now.Add(time.Duration(i)*time.Microsecond), float64(i))
	}
}

func BenchmarkSeries_GetRange(b *testing.B) {
	series := NewSeries("bench.metric", nil)
	now := time.Now()
	
	// Add 10000 points
	for i := 0; i < 10000; i++ {
		series.AddPoint(now.Add(time.Duration(i)*time.Second), float64(i))
	}
	
	start := now.Add(1000 * time.Second)
	end := now.Add(2000 * time.Second)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		series.GetRange(start, end)
	}
}