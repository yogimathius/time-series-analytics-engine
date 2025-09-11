package storage

import (
	"fmt"
	"sync"
	"time"
)

// StorageEngine coordinates between hot and warm storage layers
type StorageEngine struct {
	hot            *HotStorage
	warm           *WarmStorage
	config         *StorageConfig
	tieringEnabled bool
	
	// Background workers
	tieringWorker  *TieringWorker
	cleanupWorker  *CleanupWorker
	
	mu sync.RWMutex
}

// StorageConfig contains configuration for the storage engine
type StorageConfig struct {
	Hot  HotStorageConfig
	Warm WarmStorageConfig
	Cold ColdStorageConfig
}

// HotStorageConfig contains hot storage configuration
type HotStorageConfig struct {
	MaxSeries          int
	MaxPointsPerSeries int
	RetentionPeriod    time.Duration
	CleanupInterval    time.Duration
}

// WarmStorageConfig contains warm storage configuration  
type WarmStorageConfig struct {
	Enabled            bool
	DataPath           string
	MaxFileSize        int64
	RetentionPeriod    time.Duration
	CompactionInterval time.Duration
	CompressionLevel   int
}

// ColdStorageConfig contains cold storage configuration
type ColdStorageConfig struct {
	Enabled          bool
	Provider         string
	DataPath         string
	RetentionPeriod  time.Duration
	CompressionLevel int
}

// TieringWorker handles automatic data tiering between storage layers
type TieringWorker struct {
	engine   *StorageEngine
	interval time.Duration
	stopChan chan struct{}
	wg       sync.WaitGroup
}

// CleanupWorker handles cleanup of expired data
type CleanupWorker struct {
	engine   *StorageEngine
	interval time.Duration
	stopChan chan struct{}
	wg       sync.WaitGroup
}

// NewStorageEngine creates a new multi-tier storage engine
func NewStorageEngine(config *StorageConfig) (*StorageEngine, error) {
	// Initialize hot storage
	hot := NewHotStorage(config.Hot.MaxSeries, config.Hot.MaxPointsPerSeries)
	
	var warm *WarmStorage
	var err error
	
	// Initialize warm storage if enabled
	if config.Warm.Enabled {
		warm, err = NewWarmStorage(
			config.Warm.DataPath,
			config.Warm.MaxFileSize,
			config.Warm.CompressionLevel,
			config.Warm.RetentionPeriod,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize warm storage: %w", err)
		}
	}
	
	engine := &StorageEngine{
		hot:            hot,
		warm:           warm,
		config:         config,
		tieringEnabled: config.Warm.Enabled,
	}
	
	// Initialize background workers
	engine.tieringWorker = &TieringWorker{
		engine:   engine,
		interval: 15 * time.Minute, // Default tiering interval
		stopChan: make(chan struct{}),
	}
	
	engine.cleanupWorker = &CleanupWorker{
		engine:   engine,
		interval: config.Hot.CleanupInterval,
		stopChan: make(chan struct{}),
	}
	
	return engine, nil
}

// AddPoint adds a data point to the storage engine
func (se *StorageEngine) AddPoint(seriesID string, labels map[string]string, timestamp time.Time, value float64) error {
	// Always write to hot storage first
	return se.hot.AddPoint(seriesID, labels, timestamp, value)
}

// GetSeries retrieves a series by ID from any storage layer
func (se *StorageEngine) GetSeries(seriesID string) (*Series, bool) {
	// Check hot storage first
	if series, exists := se.hot.GetSeries(seriesID); exists {
		return series, true
	}
	
	// TODO: Check warm storage if not found in hot
	// This would require reconstructing a Series object from warm storage data
	
	return nil, false
}

// GetRange retrieves data points within a time range across all storage layers
func (se *StorageEngine) GetRange(seriesID string, start, end time.Time) ([]DataPoint, error) {
	var allPoints []DataPoint
	
	// Get points from hot storage
	if series, exists := se.hot.GetSeries(seriesID); exists {
		hotPoints := series.GetRange(start, end)
		allPoints = append(allPoints, hotPoints...)
	}
	
	// Get points from warm storage if enabled
	if se.warm != nil {
		warmPoints, err := se.warm.ReadSeriesRange(seriesID, start, end)
		if err != nil {
			return nil, fmt.Errorf("failed to read from warm storage: %w", err)
		}
		allPoints = append(allPoints, warmPoints...)
	}
	
	// Merge and sort points from all layers
	if len(allPoints) > 1 {
		allPoints = mergeSortedPoints(allPoints)
	}
	
	return allPoints, nil
}

// GetSeriesByLabels returns series matching label filters from all storage layers
func (se *StorageEngine) GetSeriesByLabels(labelFilters map[string]string) []*Series {
	// For now, only check hot storage
	// TODO: Add warm storage series lookup
	return se.hot.GetSeriesByLabels(labelFilters)
}

// GetStorageStats returns statistics about storage usage
func (se *StorageEngine) GetStorageStats() StorageStats {
	stats := StorageStats{
		Hot: HotStorageStats{
			SeriesCount: se.hot.GetSeriesCount(),
			TotalPoints: se.hot.GetTotalPoints(),
		},
	}
	
	if se.warm != nil {
		warmInfo := se.warm.GetSeriesInfo()
		stats.Warm = WarmStorageStats{
			SeriesCount: len(warmInfo),
			FileCount:   len(warmInfo), // Simplified - one file per series
		}
	}
	
	return stats
}

// Start begins background workers for data tiering and cleanup
func (se *StorageEngine) Start() {
	if se.tieringEnabled {
		se.tieringWorker.Start()
	}
	se.cleanupWorker.Start()
}

// Stop shuts down background workers and closes storage layers
func (se *StorageEngine) Stop() error {
	// Stop background workers
	if se.tieringEnabled {
		se.tieringWorker.Stop()
	}
	se.cleanupWorker.Stop()
	
	// Close storage layers
	if se.warm != nil {
		if err := se.warm.Close(); err != nil {
			return fmt.Errorf("failed to close warm storage: %w", err)
		}
	}
	
	return nil
}

// TriggerTiering manually triggers data tiering process
func (se *StorageEngine) TriggerTiering() error {
	if !se.tieringEnabled {
		return fmt.Errorf("tiering is not enabled")
	}
	
	return se.performTiering()
}

// TriggerCleanup manually triggers cleanup of expired data
func (se *StorageEngine) TriggerCleanup() error {
	return se.performCleanup()
}

// Private methods

func (se *StorageEngine) performTiering() error {
	se.mu.Lock()
	defer se.mu.Unlock()
	
	if se.warm == nil {
		return fmt.Errorf("warm storage not initialized")
	}
	
	// Get stale series from hot storage
	cutoffTime := time.Now().Add(-se.config.Hot.RetentionPeriod)
	
	// Find series that should be moved to warm storage
	allSeries := se.hot.GetSeriesByLabels(map[string]string{})
	var seriesToTier []*Series
	
	for _, series := range allSeries {
		if series.LastSeen.Before(cutoffTime) && series.Size() > 0 {
			seriesToTier = append(seriesToTier, series)
		}
	}
	
	// Move series to warm storage
	tieredCount := 0
	for _, series := range seriesToTier {
		// Get all points from series
		points := series.GetLatest(series.Size())
		if len(points) == 0 {
			continue
		}
		
		// Write to warm storage
		if err := se.warm.WriteSeriesData(series.ID, series.Labels, points); err != nil {
			return fmt.Errorf("failed to tier series %s to warm storage: %w", series.ID, err)
		}
		
		// Remove from hot storage (this would require adding a remove method to HotStorage)
		// For now, we'll rely on the cleanup process
		tieredCount++
	}
	
	fmt.Printf("Tiered %d series from hot to warm storage\n", tieredCount)
	return nil
}

func (se *StorageEngine) performCleanup() error {
	// Clean up hot storage
	hotCleaned := se.hot.CleanupStale(se.config.Hot.RetentionPeriod)
	
	var warmCleaned int
	var err error
	
	// Clean up warm storage if enabled
	if se.warm != nil {
		warmCleaned, err = se.warm.CleanupExpired()
		if err != nil {
			return fmt.Errorf("failed to cleanup warm storage: %w", err)
		}
	}
	
	fmt.Printf("Cleaned up %d hot series and %d warm series\n", hotCleaned, warmCleaned)
	return nil
}

// Background worker implementations

func (tw *TieringWorker) Start() {
	tw.wg.Add(1)
	go tw.run()
}

func (tw *TieringWorker) Stop() {
	close(tw.stopChan)
	tw.wg.Wait()
}

func (tw *TieringWorker) run() {
	defer tw.wg.Done()
	
	ticker := time.NewTicker(tw.interval)
	defer ticker.Stop()
	
	for {
		select {
		case <-tw.stopChan:
			return
		case <-ticker.C:
			if err := tw.engine.performTiering(); err != nil {
				fmt.Printf("Tiering error: %v\n", err)
			}
		}
	}
}

func (cw *CleanupWorker) Start() {
	cw.wg.Add(1)
	go cw.run()
}

func (cw *CleanupWorker) Stop() {
	close(cw.stopChan)
	cw.wg.Wait()
}

func (cw *CleanupWorker) run() {
	defer cw.wg.Done()
	
	ticker := time.NewTicker(cw.interval)
	defer ticker.Stop()
	
	for {
		select {
		case <-cw.stopChan:
			return
		case <-ticker.C:
			if err := cw.engine.performCleanup(); err != nil {
				fmt.Printf("Cleanup error: %v\n", err)
			}
		}
	}
}

// Helper types and functions

// StorageStats represents storage layer statistics
type StorageStats struct {
	Hot  HotStorageStats  `json:"hot"`
	Warm WarmStorageStats `json:"warm"`
	Cold ColdStorageStats `json:"cold"`
}

type HotStorageStats struct {
	SeriesCount int   `json:"series_count"`
	TotalPoints int64 `json:"total_points"`
}

type WarmStorageStats struct {
	SeriesCount int   `json:"series_count"`
	FileCount   int   `json:"file_count"`
	TotalSize   int64 `json:"total_size_bytes"`
}

type ColdStorageStats struct {
	SeriesCount int   `json:"series_count"`
	TotalSize   int64 `json:"total_size_bytes"`
}

// mergeSortedPoints merges and deduplicates sorted data points from multiple sources
func mergeSortedPoints(points []DataPoint) []DataPoint {
	if len(points) <= 1 {
		return points
	}
	
	// Create a map to deduplicate by timestamp
	pointMap := make(map[int64]DataPoint)
	
	for _, point := range points {
		timestamp := point.Timestamp.UnixNano()
		// Keep the latest value for duplicate timestamps
		pointMap[timestamp] = point
	}
	
	// Convert back to slice and sort
	result := make([]DataPoint, 0, len(pointMap))
	for _, point := range pointMap {
		result = append(result, point)
	}
	
	// Sort by timestamp
	for i := 0; i < len(result)-1; i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i].Timestamp.After(result[j].Timestamp) {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	
	return result
}