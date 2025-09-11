package storage

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// DataPoint represents a single time-series data point
type DataPoint struct {
	Timestamp time.Time
	Value     float64
}

// Series represents a time series with metadata
type Series struct {
	ID       string
	Labels   map[string]string
	Points   []DataPoint
	LastSeen time.Time
	mu       sync.RWMutex
}

// NewSeries creates a new time series
func NewSeries(id string, labels map[string]string) *Series {
	if labels == nil {
		labels = make(map[string]string)
	}
	return &Series{
		ID:       id,
		Labels:   labels,
		Points:   make([]DataPoint, 0),
		LastSeen: time.Now(),
	}
}

// AddPoint adds a data point to the series (thread-safe)
func (s *Series) AddPoint(timestamp time.Time, value float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	point := DataPoint{Timestamp: timestamp, Value: value}
	
	// Insert in sorted order (by timestamp)
	pos := sort.Search(len(s.Points), func(i int) bool {
		return s.Points[i].Timestamp.After(timestamp)
	})
	
	// Check for duplicate timestamp
	if pos > 0 && s.Points[pos-1].Timestamp.Equal(timestamp) {
		s.Points[pos-1].Value = value // Update existing point
		return
	}
	
	// Insert at correct position
	s.Points = append(s.Points, DataPoint{})
	copy(s.Points[pos+1:], s.Points[pos:])
	s.Points[pos] = point
	s.LastSeen = time.Now()
}

// GetRange returns points within a time range
func (s *Series) GetRange(start, end time.Time) []DataPoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	// Find start index
	startIdx := sort.Search(len(s.Points), func(i int) bool {
		return s.Points[i].Timestamp.After(start) || s.Points[i].Timestamp.Equal(start)
	})
	
	if startIdx >= len(s.Points) {
		return nil
	}
	
	// Find end index
	endIdx := sort.Search(len(s.Points), func(i int) bool {
		return s.Points[i].Timestamp.After(end)
	})
	
	if endIdx > len(s.Points) {
		endIdx = len(s.Points)
	}
	
	if startIdx >= endIdx {
		return nil
	}
	
	// Return copy to avoid race conditions
	result := make([]DataPoint, endIdx-startIdx)
	copy(result, s.Points[startIdx:endIdx])
	return result
}

// GetLatest returns the most recent N points
func (s *Series) GetLatest(count int) []DataPoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if count <= 0 || len(s.Points) == 0 {
		return nil
	}
	
	start := len(s.Points) - count
	if start < 0 {
		start = 0
	}
	
	result := make([]DataPoint, len(s.Points)-start)
	copy(result, s.Points[start:])
	return result
}

// Size returns the number of data points
func (s *Series) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.Points)
}

// HotStorage represents the in-memory hot storage layer
type HotStorage struct {
	series       map[string]*Series
	maxSeries    int
	maxPointsPerSeries int
	mu           sync.RWMutex
	totalPoints  int64
}

// NewHotStorage creates a new hot storage instance
func NewHotStorage(maxSeries, maxPointsPerSeries int) *HotStorage {
	return &HotStorage{
		series:             make(map[string]*Series),
		maxSeries:          maxSeries,
		maxPointsPerSeries: maxPointsPerSeries,
	}
}

// AddPoint adds a data point to a series
func (hs *HotStorage) AddPoint(seriesID string, labels map[string]string, timestamp time.Time, value float64) error {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	
	series, exists := hs.series[seriesID]
	if !exists {
		// Check series limit
		if len(hs.series) >= hs.maxSeries {
			return fmt.Errorf("series limit exceeded: %d", hs.maxSeries)
		}
		
		series = NewSeries(seriesID, labels)
		hs.series[seriesID] = series
	}
	
	// Check points per series limit
	if series.Size() >= hs.maxPointsPerSeries {
		// Remove oldest point
		series.mu.Lock()
		if len(series.Points) > 0 {
			series.Points = series.Points[1:]
		}
		series.mu.Unlock()
	} else {
		hs.totalPoints++
	}
	
	series.AddPoint(timestamp, value)
	return nil
}

// GetSeries returns a series by ID
func (hs *HotStorage) GetSeries(seriesID string) (*Series, bool) {
	hs.mu.RLock()
	defer hs.mu.RUnlock()
	
	series, exists := hs.series[seriesID]
	return series, exists
}

// GetSeriesByLabels returns series matching label filters
func (hs *HotStorage) GetSeriesByLabels(labelFilters map[string]string) []*Series {
	hs.mu.RLock()
	defer hs.mu.RUnlock()
	
	var result []*Series
	
	for _, series := range hs.series {
		if matchesLabels(series.Labels, labelFilters) {
			result = append(result, series)
		}
	}
	
	return result
}

// GetSeriesCount returns the number of series in storage
func (hs *HotStorage) GetSeriesCount() int {
	hs.mu.RLock()
	defer hs.mu.RUnlock()
	return len(hs.series)
}

// GetTotalPoints returns the total number of data points
func (hs *HotStorage) GetTotalPoints() int64 {
	hs.mu.RLock()
	defer hs.mu.RUnlock()
	return hs.totalPoints
}

// CleanupStale removes series that haven't received data recently
func (hs *HotStorage) CleanupStale(maxAge time.Duration) int {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	
	now := time.Now()
	var staleIDs []string
	
	for id, series := range hs.series {
		if now.Sub(series.LastSeen) > maxAge {
			staleIDs = append(staleIDs, id)
			hs.totalPoints -= int64(series.Size())
		}
	}
	
	for _, id := range staleIDs {
		delete(hs.series, id)
	}
	
	return len(staleIDs)
}

// Basic aggregation functions
type AggregationFunc func([]DataPoint) float64

var (
	// Avg calculates the average value
	Avg AggregationFunc = func(points []DataPoint) float64 {
		if len(points) == 0 {
			return math.NaN()
		}
		sum := 0.0
		for _, p := range points {
			sum += p.Value
		}
		return sum / float64(len(points))
	}
	
	// Max finds the maximum value
	Max AggregationFunc = func(points []DataPoint) float64 {
		if len(points) == 0 {
			return math.NaN()
		}
		max := points[0].Value
		for _, p := range points {
			if p.Value > max {
				max = p.Value
			}
		}
		return max
	}
	
	// Min finds the minimum value
	Min AggregationFunc = func(points []DataPoint) float64 {
		if len(points) == 0 {
			return math.NaN()
		}
		min := points[0].Value
		for _, p := range points {
			if p.Value < min {
				min = p.Value
			}
		}
		return min
	}
	
	// Sum calculates the sum of all values
	Sum AggregationFunc = func(points []DataPoint) float64 {
		sum := 0.0
		for _, p := range points {
			sum += p.Value
		}
		return sum
	}
)

// Aggregate applies an aggregation function to a time range
func (s *Series) Aggregate(start, end time.Time, aggFunc AggregationFunc) float64 {
	points := s.GetRange(start, end)
	return aggFunc(points)
}

// SeriesInfo represents metadata about a series
type SeriesInfo struct {
	ID       string            `json:"id"`
	Labels   map[string]string `json:"labels"`
	Size     int               `json:"size"`
	LastSeen time.Time         `json:"last_seen"`
}

// Helper function to match label filters
func matchesLabels(seriesLabels, filters map[string]string) bool {
	for key, value := range filters {
		if seriesLabels[key] != value {
			return false
		}
	}
	return true
}