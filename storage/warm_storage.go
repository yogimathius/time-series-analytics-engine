package storage

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// WarmStorage provides persistent storage for time-series data
type WarmStorage struct {
	dataPath         string
	maxFileSize      int64
	compressionLevel int
	retentionPeriod  time.Duration
	mu               sync.RWMutex
	files            map[string]*WarmFile // series_id -> file
	compactionMu     sync.Mutex
}

// WarmFile represents a single warm storage file for a series
type WarmFile struct {
	SeriesID     string
	FilePath     string
	FileSize     int64
	LastModified time.Time
	IndexEntries []IndexEntry
	file         *os.File
	mu           sync.RWMutex
}

// IndexEntry represents an index entry for efficient data access
type IndexEntry struct {
	Timestamp time.Time
	Offset    int64
	Length    int32
}

// WarmDataBlock represents a compressed block of data points
type WarmDataBlock struct {
	SeriesID  string      `json:"series_id"`
	Labels    map[string]string `json:"labels"`
	StartTime time.Time   `json:"start_time"`
	EndTime   time.Time   `json:"end_time"`
	Count     int         `json:"count"`
	Points    []DataPoint `json:"points"`
}

// NewWarmStorage creates a new warm storage instance
func NewWarmStorage(dataPath string, maxFileSize int64, compressionLevel int, retentionPeriod time.Duration) (*WarmStorage, error) {
	if err := os.MkdirAll(dataPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	ws := &WarmStorage{
		dataPath:         dataPath,
		maxFileSize:      maxFileSize * 1024 * 1024, // Convert MB to bytes
		compressionLevel: compressionLevel,
		retentionPeriod:  retentionPeriod,
		files:            make(map[string]*WarmFile),
	}

	// Load existing files
	if err := ws.loadExistingFiles(); err != nil {
		return nil, fmt.Errorf("failed to load existing files: %w", err)
	}

	return ws, nil
}

// WriteSeriesData writes time-series data to warm storage
func (ws *WarmStorage) WriteSeriesData(seriesID string, labels map[string]string, points []DataPoint) error {
	if len(points) == 0 {
		return nil
	}

	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Get or create warm file for series
	warmFile, err := ws.getOrCreateFile(seriesID)
	if err != nil {
		return fmt.Errorf("failed to get warm file: %w", err)
	}

	// Create data block
	block := WarmDataBlock{
		SeriesID:  seriesID,
		Labels:    labels,
		StartTime: points[0].Timestamp,
		EndTime:   points[len(points)-1].Timestamp,
		Count:     len(points),
		Points:    points,
	}

	// Compress and write data block
	return ws.writeDataBlock(warmFile, &block)
}

// ReadSeriesRange reads time-series data from warm storage within a time range
func (ws *WarmStorage) ReadSeriesRange(seriesID string, start, end time.Time) ([]DataPoint, error) {
	ws.mu.RLock()
	warmFile, exists := ws.files[seriesID]
	ws.mu.RUnlock()

	if !exists {
		return nil, nil // No data for this series
	}

	return ws.readDataRange(warmFile, start, end)
}

// GetSeriesInfo returns information about stored series
func (ws *WarmStorage) GetSeriesInfo() []SeriesInfo {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	var info []SeriesInfo
	for seriesID, warmFile := range ws.files {
		warmFile.mu.RLock()
		seriesInfo := SeriesInfo{
			ID:       seriesID,
			Labels:   make(map[string]string), // Would need to read from file for actual labels
			Size:     len(warmFile.IndexEntries),
			LastSeen: warmFile.LastModified,
		}
		warmFile.mu.RUnlock()
		info = append(info, seriesInfo)
	}

	return info
}

// Compact performs compaction on warm storage files
func (ws *WarmStorage) Compact() error {
	ws.compactionMu.Lock()
	defer ws.compactionMu.Unlock()

	ws.mu.RLock()
	filesToCompact := make([]*WarmFile, 0, len(ws.files))
	for _, warmFile := range ws.files {
		if ws.needsCompaction(warmFile) {
			filesToCompact = append(filesToCompact, warmFile)
		}
	}
	ws.mu.RUnlock()

	for _, warmFile := range filesToCompact {
		if err := ws.compactFile(warmFile); err != nil {
			return fmt.Errorf("failed to compact file %s: %w", warmFile.FilePath, err)
		}
	}

	return nil
}

// CleanupExpired removes expired data based on retention policy
func (ws *WarmStorage) CleanupExpired() (int, error) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	cutoffTime := time.Now().Add(-ws.retentionPeriod)
	var expiredFiles []string
	cleanedCount := 0

	for seriesID, warmFile := range ws.files {
		warmFile.mu.RLock()
		if warmFile.LastModified.Before(cutoffTime) {
			expiredFiles = append(expiredFiles, seriesID)
		}
		warmFile.mu.RUnlock()
	}

	for _, seriesID := range expiredFiles {
		warmFile := ws.files[seriesID]
		if err := ws.removeFile(warmFile); err != nil {
			return cleanedCount, fmt.Errorf("failed to remove expired file: %w", err)
		}
		delete(ws.files, seriesID)
		cleanedCount++
	}

	return cleanedCount, nil
}

// Close closes all open files and shuts down warm storage
func (ws *WarmStorage) Close() error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	for _, warmFile := range ws.files {
		if warmFile.file != nil {
			warmFile.file.Close()
		}
	}

	return nil
}

// Private methods

func (ws *WarmStorage) loadExistingFiles() error {
	pattern := filepath.Join(ws.dataPath, "*.tsw") // .tsw = time-series warm
	files, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("failed to glob files: %w", err)
	}

	for _, filePath := range files {
		seriesID := ws.extractSeriesID(filePath)
		warmFile, err := ws.loadFile(seriesID, filePath)
		if err != nil {
			// Log error but continue with other files
			fmt.Printf("Warning: failed to load file %s: %v\n", filePath, err)
			continue
		}
		ws.files[seriesID] = warmFile
	}

	return nil
}

func (ws *WarmStorage) extractSeriesID(filePath string) string {
	base := filepath.Base(filePath)
	ext := filepath.Ext(base)
	return base[:len(base)-len(ext)]
}

func (ws *WarmStorage) loadFile(seriesID, filePath string) (*WarmFile, error) {
	stat, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	warmFile := &WarmFile{
		SeriesID:     seriesID,
		FilePath:     filePath,
		FileSize:     stat.Size(),
		LastModified: stat.ModTime(),
		IndexEntries: make([]IndexEntry, 0),
	}

	// Load index entries by reading file
	if err := ws.loadIndexEntries(warmFile); err != nil {
		return nil, fmt.Errorf("failed to load index entries: %w", err)
	}

	return warmFile, nil
}

func (ws *WarmStorage) loadIndexEntries(warmFile *WarmFile) error {
	file, err := os.Open(warmFile.FilePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	var offset int64 = 0
	reader := bufio.NewReader(file)

	for {
		// Read block header to get timestamp and length
		blockHeader, err := ws.readBlockHeader(reader)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read block header: %w", err)
		}

		// Add index entry
		warmFile.IndexEntries = append(warmFile.IndexEntries, IndexEntry{
			Timestamp: blockHeader.StartTime,
			Offset:    offset,
			Length:    blockHeader.Length,
		})

		// Skip block data
		if _, err := reader.Discard(int(blockHeader.Length)); err != nil {
			return fmt.Errorf("failed to skip block data: %w", err)
		}

		offset += int64(blockHeader.Length) + 24 // 24 bytes for header
	}

	// Sort index entries by timestamp
	sort.Slice(warmFile.IndexEntries, func(i, j int) bool {
		return warmFile.IndexEntries[i].Timestamp.Before(warmFile.IndexEntries[j].Timestamp)
	})

	return nil
}

type blockHeader struct {
	StartTime time.Time
	Length    int32
}

func (ws *WarmStorage) readBlockHeader(reader *bufio.Reader) (*blockHeader, error) {
	// Read timestamp (8 bytes)
	var timestamp int64
	if err := binary.Read(reader, binary.LittleEndian, &timestamp); err != nil {
		return nil, err
	}

	// Read length (4 bytes)
	var length int32
	if err := binary.Read(reader, binary.LittleEndian, &length); err != nil {
		return nil, err
	}

	return &blockHeader{
		StartTime: time.Unix(timestamp/1000, (timestamp%1000)*1000000), // Convert from milliseconds
		Length:    length,
	}, nil
}

func (ws *WarmStorage) getOrCreateFile(seriesID string) (*WarmFile, error) {
	if warmFile, exists := ws.files[seriesID]; exists {
		return warmFile, nil
	}

	filePath := filepath.Join(ws.dataPath, seriesID+".tsw")
	warmFile := &WarmFile{
		SeriesID:     seriesID,
		FilePath:     filePath,
		FileSize:     0,
		LastModified: time.Now(),
		IndexEntries: make([]IndexEntry, 0),
	}

	ws.files[seriesID] = warmFile
	return warmFile, nil
}

func (ws *WarmStorage) writeDataBlock(warmFile *WarmFile, block *WarmDataBlock) error {
	warmFile.mu.Lock()
	defer warmFile.mu.Unlock()

	// Open file for appending
	if warmFile.file == nil {
		file, err := os.OpenFile(warmFile.FilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("failed to open file for writing: %w", err)
		}
		warmFile.file = file
	}

	// Serialize and compress block
	jsonData, err := json.Marshal(block)
	if err != nil {
		return fmt.Errorf("failed to marshal block: %w", err)
	}

	var compressedData bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&compressedData, ws.compressionLevel)
	if err != nil {
		return fmt.Errorf("failed to create gzip writer: %w", err)
	}

	if _, err := gzipWriter.Write(jsonData); err != nil {
		return fmt.Errorf("failed to compress data: %w", err)
	}

	if err := gzipWriter.Close(); err != nil {
		return fmt.Errorf("failed to close gzip writer: %w", err)
	}

	// Write block header
	timestamp := block.StartTime.UnixNano() / 1000000 // Convert to milliseconds
	if err := binary.Write(warmFile.file, binary.LittleEndian, timestamp); err != nil {
		return fmt.Errorf("failed to write timestamp: %w", err)
	}

	dataLength := int32(compressedData.Len())
	if err := binary.Write(warmFile.file, binary.LittleEndian, dataLength); err != nil {
		return fmt.Errorf("failed to write data length: %w", err)
	}

	// Write compressed data
	offset := warmFile.FileSize
	if _, err := warmFile.file.Write(compressedData.Bytes()); err != nil {
		return fmt.Errorf("failed to write compressed data: %w", err)
	}

	// Update file metadata
	warmFile.FileSize += int64(8 + 4 + dataLength) // timestamp + length + data
	warmFile.LastModified = time.Now()

	// Add index entry
	warmFile.IndexEntries = append(warmFile.IndexEntries, IndexEntry{
		Timestamp: block.StartTime,
		Offset:    offset,
		Length:    dataLength,
	})

	// Sort index entries
	sort.Slice(warmFile.IndexEntries, func(i, j int) bool {
		return warmFile.IndexEntries[i].Timestamp.Before(warmFile.IndexEntries[j].Timestamp)
	})

	return nil
}

func (ws *WarmStorage) readDataRange(warmFile *WarmFile, start, end time.Time) ([]DataPoint, error) {
	warmFile.mu.RLock()
	defer warmFile.mu.RUnlock()

	var allPoints []DataPoint

	// Find relevant index entries
	for _, entry := range warmFile.IndexEntries {
		if entry.Timestamp.After(end) {
			break
		}
		if entry.Timestamp.Before(start) {
			continue
		}

		// Read and decompress block
		block, err := ws.readDataBlock(warmFile, entry)
		if err != nil {
			return nil, fmt.Errorf("failed to read data block: %w", err)
		}

		// Filter points by time range
		for _, point := range block.Points {
			if (point.Timestamp.Equal(start) || point.Timestamp.After(start)) &&
				(point.Timestamp.Equal(end) || point.Timestamp.Before(end)) {
				allPoints = append(allPoints, point)
			}
		}
	}

	// Sort points by timestamp
	sort.Slice(allPoints, func(i, j int) bool {
		return allPoints[i].Timestamp.Before(allPoints[j].Timestamp)
	})

	return allPoints, nil
}

func (ws *WarmStorage) readDataBlock(warmFile *WarmFile, entry IndexEntry) (*WarmDataBlock, error) {
	// Open file for reading
	file, err := os.Open(warmFile.FilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file for reading: %w", err)
	}
	defer file.Close()

	// Seek to data position (skip header)
	if _, err := file.Seek(entry.Offset+12, io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek to data position: %w", err)
	}

	// Read compressed data
	compressedData := make([]byte, entry.Length)
	if _, err := io.ReadFull(file, compressedData); err != nil {
		return nil, fmt.Errorf("failed to read compressed data: %w", err)
	}

	// Decompress data
	gzipReader, err := gzip.NewReader(bytes.NewReader(compressedData))
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzipReader.Close()

	jsonData, err := io.ReadAll(gzipReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read decompressed data: %w", err)
	}

	// Deserialize block
	var block WarmDataBlock
	if err := json.Unmarshal(jsonData, &block); err != nil {
		return nil, fmt.Errorf("failed to unmarshal block: %w", err)
	}

	return &block, nil
}

func (ws *WarmStorage) needsCompaction(warmFile *WarmFile) bool {
	warmFile.mu.RLock()
	defer warmFile.mu.RUnlock()
	
	// Simple compaction strategy: compact if file has many small blocks
	return len(warmFile.IndexEntries) > 100
}

func (ws *WarmStorage) compactFile(warmFile *WarmFile) error {
	// Implement file compaction logic here
	// This would involve reading all blocks, merging them, and rewriting the file
	// For now, this is a placeholder
	return nil
}

func (ws *WarmStorage) removeFile(warmFile *WarmFile) error {
	if warmFile.file != nil {
		warmFile.file.Close()
	}
	return os.Remove(warmFile.FilePath)
}