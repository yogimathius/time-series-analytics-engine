package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
	"time-series-analytics-engine/ingestion"
	"time-series-analytics-engine/storage"

	"github.com/gorilla/mux"
)

// Server represents the HTTP API server
type Server struct {
	router          *mux.Router
	hotStorage      *storage.HotStorage
	streamProcessor *ingestion.StreamProcessor
}

// NewServer creates a new API server
func NewServer(hotStorage *storage.HotStorage, streamProcessor *ingestion.StreamProcessor) *Server {
	server := &Server{
		router:          mux.NewRouter(),
		hotStorage:      hotStorage,
		streamProcessor: streamProcessor,
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
	Series []SeriesInfo `json:"series"`
	Count  int          `json:"count"`
}

// SeriesInfo represents metadata about a series
type SeriesInfo struct {
	ID       string            `json:"id"`
	Labels   map[string]string `json:"labels"`
	Size     int               `json:"size"`
	LastSeen time.Time         `json:"last_seen"`
}

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
	allSeries := s.hotStorage.GetSeriesByLabels(map[string]string{})
	
	var seriesList []SeriesInfo
	for _, series := range allSeries {
		seriesList = append(seriesList, SeriesInfo{
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
	
	// Get series
	series, exists := s.hotStorage.GetSeries(seriesID)
	if !exists {
		http.Error(w, "Series not found", http.StatusNotFound)
		return
	}
	
	// Get data points in range
	points := series.GetRange(startTime, endTime)
	
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
	
	response := QueryResponse{
		Series: seriesID,
		Labels: series.Labels,
		Points: points,
		Count:  len(points),
	}
	
	json.NewEncoder(w).Encode(response)
}

// getStats returns system statistics
func (s *Server) getStats(w http.ResponseWriter, r *http.Request) {
	ingested, processed, errors, batches := s.streamProcessor.GetStats()
	
	response := StatsResponse{
		Storage: struct {
			SeriesCount  int   `json:"series_count"`
			TotalPoints  int64 `json:"total_points"`
		}{
			SeriesCount: s.hotStorage.GetSeriesCount(),
			TotalPoints: s.hotStorage.GetTotalPoints(),
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
			"POST /api/v1/metrics":        "Ingest single metric",
			"POST /api/v1/metrics/batch":  "Ingest metric batch",
			"GET  /api/v1/series":         "List time series",
			"GET  /api/v1/query":          "Query time series data",
			"GET  /api/v1/stats":          "System statistics",
			"GET  /health":                "Health check",
		},
	}
	
	json.NewEncoder(w).Encode(info)
}