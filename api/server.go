package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
	"time-series-analytics-engine/analytics/ml"
	"time-series-analytics-engine/ingestion"
	"time-series-analytics-engine/storage"

	"github.com/gorilla/mux"
)

// StorageReader interface for abstracting storage read operations
type StorageReader interface {
	GetSeries(seriesID string) (*storage.Series, bool)
	GetSeriesByLabels(labelFilters map[string]string) []*storage.Series
	GetRange(seriesID string, start, end time.Time) ([]storage.DataPoint, error)
	GetStorageStats() storage.StorageStats
}

// Server represents the HTTP API server
type Server struct {
	router           *mux.Router
	storage          StorageReader
	streamProcessor  *ingestion.StreamProcessor
	anomalyDetector  *ml.AnomalyDetector
	forecastEngine   *ml.ForecastEngine
}

// NewServer creates a new API server
func NewServer(storage StorageReader, streamProcessor *ingestion.StreamProcessor) *Server {
	// Initialize ML components
	anomalyConfig := ml.AnomalyConfig{
		ZScoreThreshold:    3.0,
		IQRMultiplier:      1.5,
		SeasonalPeriods:    []int{24, 168}, // Hourly and daily patterns
		WindowSize:         100,
		SensitivityLevel:   "medium",
		AdaptiveThresholds: true,
	}
	
	forecastConfig := ml.ForecastConfig{
		DefaultHorizon:     24,
		ConfidenceLevel:    0.95,
		SeasonalPeriods:    []int{24, 168},
		EnableARIMA:        true,
		EnableSeasonalETS:  true,
		EnableHoltWinters:  true,
		EnableLinearTrend:  true,
		EnsembleMethod:     "weighted",
		MaxDataPoints:      10000,
		CacheTimeout:       5, // 5 minutes
	}
	
	server := &Server{
		router:          mux.NewRouter(),
		storage:         storage,
		streamProcessor: streamProcessor,
		anomalyDetector: ml.NewAnomalyDetector(anomalyConfig),
		forecastEngine:  ml.NewForecastEngine(forecastConfig),
	}
	
	server.setupRoutes()
	return server
}

// ServeHTTP implements the http.Handler interface
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Add CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	
	// Handle preflight requests
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	
	// Add response headers
	w.Header().Set("Content-Type", "application/json")
	
	s.router.ServeHTTP(w, r)
}

// setupRoutes configures all API routes
func (s *Server) setupRoutes() {
	// API v1 routes
	api := s.router.PathPrefix("/api/v1").Subrouter()
	
	// Metric ingestion endpoints
	api.HandleFunc("/metrics", s.ingestMetric).Methods("POST")
	api.HandleFunc("/metrics/batch", s.ingestBatch).Methods("POST")
	
	// Query endpoints
	api.HandleFunc("/series", s.listSeries).Methods("GET")
	api.HandleFunc("/query", s.queryData).Methods("GET")
	
	// Analytics endpoints
	api.HandleFunc("/analytics/anomaly", s.detectAnomalies).Methods("POST")
	api.HandleFunc("/analytics/forecast", s.generateForecast).Methods("POST")
	
	// System endpoints
	api.HandleFunc("/stats", s.getStats).Methods("GET")
	
	// Health check
	s.router.HandleFunc("/health", s.healthCheck).Methods("GET")
	
	// Root endpoint
	s.router.HandleFunc("/", s.rootHandler).Methods("GET")
}

// MetricRequest represents an incoming metric request
type MetricRequest struct {
	Name      string            `json:"name"`
	Value     float64           `json:"value"`
	Timestamp string            `json:"timestamp,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// BatchRequest represents a batch of metrics
type BatchRequest struct {
	Metrics []MetricRequest `json:"metrics"`
}

// QueryResponse represents a query result
type QueryResponse struct {
	Series  string                   `json:"series"`
	Labels  map[string]string        `json:"labels"`
	Points  []storage.DataPoint      `json:"points"`
	Count   int                      `json:"count"`
}

// SeriesListResponse represents the series list response
type SeriesListResponse struct {
	Series []storage.SeriesInfo `json:"series"`
	Count  int                  `json:"count"`
}

// Using SeriesInfo from storage package

// StatsResponse represents system statistics
type StatsResponse struct {
	Storage struct {
		SeriesCount  int   `json:"series_count"`
		TotalPoints  int64 `json:"total_points"`
	} `json:"storage"`
	Ingestion struct {
		TotalIngested    int64 `json:"total_ingested"`
		TotalProcessed   int64 `json:"total_processed"`
		TotalErrors      int64 `json:"total_errors"`
		BatchesProcessed int64 `json:"batches_processed"`
		BufferSize       int   `json:"buffer_size"`
		IsRunning        bool  `json:"is_running"`
	} `json:"ingestion"`
	System struct {
		StartTime time.Time `json:"start_time"`
		Uptime    string    `json:"uptime"`
	} `json:"system"`
}

var startTime = time.Now()

// ingestMetric handles single metric ingestion
func (s *Server) ingestMetric(w http.ResponseWriter, r *http.Request) {
	var req MetricRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	
	// Parse timestamp
	var timestamp time.Time
	var err error
	if req.Timestamp == "" {
		timestamp = time.Now()
	} else {
		timestamp, err = time.Parse(time.RFC3339, req.Timestamp)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid timestamp format: %v", err), http.StatusBadRequest)
			return
		}
	}
	
	// Create metric data
	metric := ingestion.MetricData{
		Name:      req.Name,
		Value:     req.Value,
		Timestamp: timestamp,
		Labels:    req.Labels,
	}
	
	// Ingest metric
	if err := s.streamProcessor.IngestMetric(metric); err != nil {
		http.Error(w, fmt.Sprintf("Failed to ingest metric: %v", err), http.StatusInternalServerError)
		return
	}
	
	// Success response
	response := map[string]interface{}{
		"status":    "success",
		"message":   "Metric ingested successfully",
		"timestamp": timestamp.Format(time.RFC3339),
	}
	
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)
}

// ingestBatch handles batch metric ingestion
func (s *Server) ingestBatch(w http.ResponseWriter, r *http.Request) {
	var req BatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	
	if len(req.Metrics) == 0 {
		http.Error(w, "Empty batch", http.StatusBadRequest)
		return
	}
	
	var metrics []ingestion.MetricData
	for _, m := range req.Metrics {
		var timestamp time.Time
		var err error
		if m.Timestamp == "" {
			timestamp = time.Now()
		} else {
			timestamp, err = time.Parse(time.RFC3339, m.Timestamp)
			if err != nil {
				http.Error(w, fmt.Sprintf("Invalid timestamp format: %v", err), http.StatusBadRequest)
				return
			}
		}
		
		metrics = append(metrics, ingestion.MetricData{
			Name:      m.Name,
			Value:     m.Value,
			Timestamp: timestamp,
			Labels:    m.Labels,
		})
	}
	
	// Ingest batch
	if err := s.streamProcessor.IngestBatch(metrics); err != nil {
		http.Error(w, fmt.Sprintf("Failed to ingest batch: %v", err), http.StatusInternalServerError)
		return
	}
	
	// Success response
	response := map[string]interface{}{
		"status":  "success",
		"message": fmt.Sprintf("Batch of %d metrics ingested successfully", len(metrics)),
		"count":   len(metrics),
	}
	
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)
}

// listSeries returns a list of all time series
func (s *Server) listSeries(w http.ResponseWriter, r *http.Request) {
	// Get all series (this is a simplified implementation)
	// In a real system, you'd want pagination and filtering
	allSeries := s.storage.GetSeriesByLabels(map[string]string{})
	
	var seriesList []storage.SeriesInfo
	for _, series := range allSeries {
		seriesList = append(seriesList, storage.SeriesInfo{
			ID:       series.ID,
			Labels:   series.Labels,
			Size:     series.Size(),
			LastSeen: series.LastSeen,
		})
	}
	
	response := SeriesListResponse{
		Series: seriesList,
		Count:  len(seriesList),
	}
	
	json.NewEncoder(w).Encode(response)
}

// queryData handles time series data queries
func (s *Server) queryData(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	
	seriesID := query.Get("series")
	if seriesID == "" {
		http.Error(w, "Missing 'series' parameter", http.StatusBadRequest)
		return
	}
	
	// Parse time range parameters
	startParam := query.Get("start")
	endParam := query.Get("end")
	limitParam := query.Get("limit")
	
	var startTime, endTime time.Time
	var err error
	
	if startParam != "" {
		// Support relative time like "-1h", "-30m", etc.
		if startParam[0] == '-' {
			duration, err := time.ParseDuration(startParam[1:])
			if err != nil {
				http.Error(w, fmt.Sprintf("Invalid start duration: %v", err), http.StatusBadRequest)
				return
			}
			startTime = time.Now().Add(-duration)
		} else {
			startTime, err = time.Parse(time.RFC3339, startParam)
			if err != nil {
				http.Error(w, fmt.Sprintf("Invalid start time format: %v", err), http.StatusBadRequest)
				return
			}
		}
	} else {
		// Default to last hour
		startTime = time.Now().Add(-time.Hour)
	}
	
	if endParam != "" {
		endTime, err = time.Parse(time.RFC3339, endParam)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid end time format: %v", err), http.StatusBadRequest)
			return
		}
	} else {
		endTime = time.Now()
	}
	
	// Get data points in range
	points, err := s.storage.GetRange(seriesID, startTime, endTime)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to query data: %v", err), http.StatusInternalServerError)
		return
	}
	
	if len(points) == 0 {
		// Check if series exists at all
		if _, exists := s.storage.GetSeries(seriesID); !exists {
			http.Error(w, "Series not found", http.StatusNotFound)
			return
		}
	}
	
	// Apply limit if specified
	if limitParam != "" {
		limit, err := strconv.Atoi(limitParam)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid limit: %v", err), http.StatusBadRequest)
			return
		}
		if limit > 0 && len(points) > limit {
			// Get the most recent points
			points = points[len(points)-limit:]
		}
	}
	
	// Get series metadata for labels
	var labels map[string]string
	if series, exists := s.storage.GetSeries(seriesID); exists {
		labels = series.Labels
	} else {
		labels = make(map[string]string)
	}
	
	response := QueryResponse{
		Series: seriesID,
		Labels: labels,
		Points: points,
		Count:  len(points),
	}
	
	json.NewEncoder(w).Encode(response)
}

// getStats returns system statistics
func (s *Server) getStats(w http.ResponseWriter, r *http.Request) {
	ingested, processed, errors, batches := s.streamProcessor.GetStats()
	storageStats := s.storage.GetStorageStats()
	
	response := StatsResponse{
		Storage: struct {
			SeriesCount  int   `json:"series_count"`
			TotalPoints  int64 `json:"total_points"`
		}{
			SeriesCount: storageStats.Hot.SeriesCount,
			TotalPoints: storageStats.Hot.TotalPoints,
		},
		Ingestion: struct {
			TotalIngested    int64 `json:"total_ingested"`
			TotalProcessed   int64 `json:"total_processed"`
			TotalErrors      int64 `json:"total_errors"`
			BatchesProcessed int64 `json:"batches_processed"`
			BufferSize       int   `json:"buffer_size"`
			IsRunning        bool  `json:"is_running"`
		}{
			TotalIngested:    ingested,
			TotalProcessed:   processed,
			TotalErrors:      errors,
			BatchesProcessed: batches,
			BufferSize:       s.streamProcessor.GetBufferSize(),
			IsRunning:        s.streamProcessor.IsRunning(),
		},
		System: struct {
			StartTime time.Time `json:"start_time"`
			Uptime    string    `json:"uptime"`
		}{
			StartTime: startTime,
			Uptime:    time.Since(startTime).String(),
		},
	}
	
	json.NewEncoder(w).Encode(response)
}

// healthCheck returns health status
func (s *Server) healthCheck(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().Format(time.RFC3339),
		"uptime":    time.Since(startTime).String(),
		"services": map[string]string{
			"storage":   "healthy",
			"ingestion": func() string {
				if s.streamProcessor.IsRunning() {
					return "healthy"
				}
				return "unhealthy"
			}(),
		},
	}
	
	json.NewEncoder(w).Encode(health)
}

// rootHandler provides API information
func (s *Server) rootHandler(w http.ResponseWriter, r *http.Request) {
	info := map[string]interface{}{
		"name":        "Time-Series Analytics Engine",
		"version":     "0.1.0",
		"description": "High-performance time-series data storage and analytics",
		"endpoints": map[string]string{
			"POST /api/v1/metrics":           "Ingest single metric",
			"POST /api/v1/metrics/batch":     "Ingest metric batch",
			"GET  /api/v1/series":            "List time series",
			"GET  /api/v1/query":             "Query time series data",
			"POST /api/v1/analytics/anomaly": "Detect anomalies in time series",
			"POST /api/v1/analytics/forecast": "Generate forecasts for time series",
			"GET  /api/v1/stats":             "System statistics",
			"GET  /health":                   "Health check",
		},
	}
	
	json.NewEncoder(w).Encode(info)
}

// AnomalyRequest represents an anomaly detection request
type AnomalyRequest struct {
	SeriesID string `json:"series_id"`
	Start    string `json:"start,omitempty"`
	End      string `json:"end,omitempty"`
}

// ForecastRequest represents a forecasting request
type ForecastRequest struct {
	SeriesID string `json:"series_id"`
	Horizon  int    `json:"horizon,omitempty"`
	Start    string `json:"start,omitempty"`
	End      string `json:"end,omitempty"`
}

// detectAnomalies handles anomaly detection requests
func (s *Server) detectAnomalies(w http.ResponseWriter, r *http.Request) {
	var req AnomalyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	
	if req.SeriesID == "" {
		http.Error(w, "Missing series_id parameter", http.StatusBadRequest)
		return
	}
	
	// Parse time range
	var startTime, endTime time.Time
	var err error
	
	if req.Start != "" {
		if req.Start[0] == '-' {
			duration, err := time.ParseDuration(req.Start[1:])
			if err != nil {
				http.Error(w, fmt.Sprintf("Invalid start duration: %v", err), http.StatusBadRequest)
				return
			}
			startTime = time.Now().Add(-duration)
		} else {
			startTime, err = time.Parse(time.RFC3339, req.Start)
			if err != nil {
				http.Error(w, fmt.Sprintf("Invalid start time format: %v", err), http.StatusBadRequest)
				return
			}
		}
	} else {
		startTime = time.Now().Add(-24 * time.Hour) // Default to last 24 hours
	}
	
	if req.End != "" {
		endTime, err = time.Parse(time.RFC3339, req.End)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid end time format: %v", err), http.StatusBadRequest)
			return
		}
	} else {
		endTime = time.Now()
	}
	
	// Get training data
	points, err := s.storage.GetRange(req.SeriesID, startTime, endTime)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get training data: %v", err), http.StatusInternalServerError)
		return
	}
	
	if len(points) == 0 {
		http.Error(w, "No data found for the specified series and time range", http.StatusNotFound)
		return
	}
	
	// Convert storage.DataPoint to ml.DataPoint
	mlPoints := make([]ml.DataPoint, len(points))
	for i, point := range points {
		mlPoints[i] = ml.DataPoint{
			Timestamp: point.Timestamp,
			Value:     point.Value,
			Labels:    make(map[string]string), // Add series labels if needed
		}
	}
	
	// Train the anomaly detector
	if err := s.anomalyDetector.TrainModels(mlPoints); err != nil {
		http.Error(w, fmt.Sprintf("Failed to train anomaly detector: %v", err), http.StatusInternalServerError)
		return
	}
	
	// Detect anomalies on the most recent points
	var anomalies []ml.AnomalyResult
	recentPoints := mlPoints
	if len(recentPoints) > 100 {
		recentPoints = recentPoints[len(recentPoints)-100:] // Analyze last 100 points
	}
	
	for _, point := range recentPoints {
		results, err := s.anomalyDetector.DetectAnomalies(point)
		if err != nil {
			continue // Skip failed detections
		}
		for _, result := range results {
			if result.IsAnomaly {
				anomalies = append(anomalies, result)
			}
		}
	}
	
	response := map[string]interface{}{
		"series_id":      req.SeriesID,
		"anomalies":      anomalies,
		"count":          len(anomalies),
		"analyzed_points": len(recentPoints),
		"time_range": map[string]string{
			"start": startTime.Format(time.RFC3339),
			"end":   endTime.Format(time.RFC3339),
		},
	}
	
	json.NewEncoder(w).Encode(response)
}

// generateForecast handles forecasting requests
func (s *Server) generateForecast(w http.ResponseWriter, r *http.Request) {
	var req ForecastRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	
	if req.SeriesID == "" {
		http.Error(w, "Missing series_id parameter", http.StatusBadRequest)
		return
	}
	
	horizon := req.Horizon
	if horizon == 0 {
		horizon = 24 // Default to 24 steps ahead
	}
	
	// Parse time range for training data
	var startTime, endTime time.Time
	var err error
	
	if req.Start != "" {
		if req.Start[0] == '-' {
			duration, err := time.ParseDuration(req.Start[1:])
			if err != nil {
				http.Error(w, fmt.Sprintf("Invalid start duration: %v", err), http.StatusBadRequest)
				return
			}
			startTime = time.Now().Add(-duration)
		} else {
			startTime, err = time.Parse(time.RFC3339, req.Start)
			if err != nil {
				http.Error(w, fmt.Sprintf("Invalid start time format: %v", err), http.StatusBadRequest)
				return
			}
		}
	} else {
		startTime = time.Now().Add(-7 * 24 * time.Hour) // Default to last 7 days
	}
	
	if req.End != "" {
		endTime, err = time.Parse(time.RFC3339, req.End)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid end time format: %v", err), http.StatusBadRequest)
			return
		}
	} else {
		endTime = time.Now()
	}
	
	// Get training data
	points, err := s.storage.GetRange(req.SeriesID, startTime, endTime)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get training data: %v", err), http.StatusInternalServerError)
		return
	}
	
	if len(points) < 10 {
		http.Error(w, "Insufficient data for forecasting (minimum 10 points required)", http.StatusBadRequest)
		return
	}
	
	// Convert storage.DataPoint to ml.DataPoint
	mlPoints := make([]ml.DataPoint, len(points))
	for i, point := range points {
		mlPoints[i] = ml.DataPoint{
			Timestamp: point.Timestamp,
			Value:     point.Value,
			Labels:    make(map[string]string),
		}
	}
	
	// Train the forecasting models
	if err := s.forecastEngine.TrainModels(mlPoints); err != nil {
		http.Error(w, fmt.Sprintf("Failed to train forecasting models: %v", err), http.StatusInternalServerError)
		return
	}
	
	// Generate forecast
	forecast, err := s.forecastEngine.Forecast(horizon)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to generate forecast: %v", err), http.StatusInternalServerError)
		return
	}
	
	response := map[string]interface{}{
		"series_id":       req.SeriesID,
		"forecast":        forecast,
		"training_points": len(mlPoints),
		"training_range": map[string]string{
			"start": startTime.Format(time.RFC3339),
			"end":   endTime.Format(time.RFC3339),
		},
	}
	
	json.NewEncoder(w).Encode(response)
}