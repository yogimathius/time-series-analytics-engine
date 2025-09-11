package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"time-series-analytics-engine/api"
	"time-series-analytics-engine/config"
	"time-series-analytics-engine/ingestion"
	"time-series-analytics-engine/storage"
)

func main() {
	log.Println("Starting Time-Series Analytics Engine...")

	// Load configuration
	configManager, err := config.NewConfigManager("config.json")
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}
	cfg := configManager.GetConfig()
	log.Printf("Configuration loaded successfully")

	// Initialize storage engine with multi-tier support
	storageConfig := &storage.StorageConfig{
		Hot: storage.HotStorageConfig{
			MaxSeries:          cfg.Storage.Hot.MaxSeries,
			MaxPointsPerSeries: cfg.Storage.Hot.MaxPointsPerSeries,
			RetentionPeriod:    cfg.Storage.Hot.RetentionPeriod.Duration,
			CleanupInterval:    cfg.Storage.Hot.CleanupInterval.Duration,
		},
		Warm: storage.WarmStorageConfig{
			Enabled:            cfg.Storage.Warm.Enabled,
			DataPath:           cfg.Storage.Warm.DataPath,
			MaxFileSize:        cfg.Storage.Warm.MaxFileSize,
			RetentionPeriod:    cfg.Storage.Warm.RetentionPeriod.Duration,
			CompactionInterval: cfg.Storage.Warm.CompactionInterval.Duration,
			CompressionLevel:   cfg.Storage.Warm.CompressionLevel,
		},
		Cold: storage.ColdStorageConfig{
			Enabled:          cfg.Storage.Cold.Enabled,
			Provider:         cfg.Storage.Cold.Provider,
			DataPath:         cfg.Storage.Cold.DataPath,
			RetentionPeriod:  cfg.Storage.Cold.RetentionPeriod.Duration,
			CompressionLevel: cfg.Storage.Cold.CompressionLevel,
		},
	}

	storageEngine, err := storage.NewStorageEngine(storageConfig)
	if err != nil {
		log.Fatalf("Failed to initialize storage engine: %v", err)
	}
	log.Printf("Storage engine initialized (hot: %d series, warm: %s)", 
		cfg.Storage.Hot.MaxSeries, 
		func() string {
			if cfg.Storage.Warm.Enabled {
				return "enabled"
			}
			return "disabled"
		}())

	// Start storage engine background workers
	storageEngine.Start()
	defer func() {
		if err := storageEngine.Stop(); err != nil {
			log.Printf("Error stopping storage engine: %v", err)
		}
	}()

	// Initialize stream processor with configuration
	streamProcessor := ingestion.NewStreamProcessor(
		storageEngine, // Pass storage engine instead of just hot storage
		cfg.Ingestion.BufferSize,
		cfg.Ingestion.BatchSize,
		cfg.Ingestion.FlushInterval.Duration,
	)

	// Configure data validation rules
	streamProcessor.GetValidator().SetAllowedMetrics(cfg.Ingestion.ValidationRules.AllowedMetrics)
	streamProcessor.GetValidator().SetRequiredLabels(cfg.Ingestion.ValidationRules.RequiredLabels)

	log.Printf("Stream processor initialized (buffer: %d, batch: %d, flush: %v)", 
		cfg.Ingestion.BufferSize, cfg.Ingestion.BatchSize, cfg.Ingestion.FlushInterval.Duration)

	// Start stream processor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := streamProcessor.Start(ctx); err != nil {
		log.Fatalf("Failed to start stream processor: %v", err)
	}
	log.Println("Stream processor started")

	// Initialize HTTP API
	apiServer := api.NewServer(storageEngine, streamProcessor)
	log.Println("HTTP API server initialized")

	// Create HTTP server with configuration
	server := &http.Server{
		Addr:         cfg.Server.Port,
		Handler:      apiServer,
		ReadTimeout:  cfg.Server.ReadTimeout.Duration,
		WriteTimeout: cfg.Server.WriteTimeout.Duration,
		IdleTimeout:  cfg.Server.IdleTimeout.Duration,
	}

	// Start server in goroutine
	go func() {
		log.Printf("Starting HTTP server on %s", cfg.Server.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	// Setup graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Print startup information
	printStartupInfo(cfg.Server.Port, cfg)

	// Wait for shutdown signal
	<-quit
	log.Println("Shutting down server...")

	// Stop stream processor
	streamProcessor.Stop()
	log.Println("Stream processor stopped")

	// Shutdown HTTP server with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}

	log.Println("Server gracefully stopped")
}

func printStartupInfo(port string, cfg *config.Config) {
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("🚀 Time-Series Analytics Engine Started")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("📊 HTTP API: http://localhost%s\n", port)
	
	fmt.Println("\n🔧 Configuration:")
	fmt.Printf("  Hot Storage:  %d series, %d points/series\n", 
		cfg.Storage.Hot.MaxSeries, cfg.Storage.Hot.MaxPointsPerSeries)
	fmt.Printf("  Warm Storage: %s (%s)\n", 
		func() string {
			if cfg.Storage.Warm.Enabled {
				return "enabled"
			}
			return "disabled"
		}(), cfg.Storage.Warm.DataPath)
	fmt.Printf("  Ingestion:    buffer=%d, batch=%d, flush=%v\n",
		cfg.Ingestion.BufferSize, cfg.Ingestion.BatchSize, cfg.Ingestion.FlushInterval.Duration)
	
	fmt.Println("\n📋 Available Endpoints:")
	fmt.Printf("  POST %s/api/v1/metrics        - Ingest single metric\n", port)
	fmt.Printf("  POST %s/api/v1/metrics/batch  - Ingest metric batch\n", port)
	fmt.Printf("  GET  %s/api/v1/series         - List time series\n", port)
	fmt.Printf("  GET  %s/api/v1/query          - Query time series data\n", port)
	fmt.Printf("  GET  %s/api/v1/stats          - System statistics\n", port)
	fmt.Printf("  GET  %s/health                - Health check\n", port)
	
	fmt.Println("\n📊 Example Usage:")
	fmt.Println("  # Ingest a metric")
	fmt.Printf(`  curl -X POST http://localhost%s/api/v1/metrics \`, port)
	fmt.Println(`
       -H "Content-Type: application/json" \
       -d '{
         "name": "cpu.usage",
         "value": 75.5,
         "timestamp": "` + time.Now().Format(time.RFC3339) + `",
         "labels": {"host": "server1", "env": "prod"}
       }'`)

	fmt.Println("\n  # Query metrics")
	fmt.Printf(`  curl "http://localhost%s/api/v1/query?series=cpu.usage&start=-1h"`, port)

	fmt.Println("\n  # Check system stats")
	fmt.Printf(`  curl http://localhost%s/api/v1/stats`, port)

	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("✅ Ready to accept requests!")
	fmt.Println("💡 Press Ctrl+C to gracefully shutdown")
	fmt.Println(strings.Repeat("=", 60) + "\n")
}