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
	"time-series-analytics-engine/ingestion"
	"time-series-analytics-engine/storage"
)

func main() {
	// Configuration
	const (
		serverPort    = ":8080"
		maxSeries     = 100000
		maxPoints     = 10000
		bufferSize    = 1000
		batchSize     = 100
		flushInterval = 5 * time.Second
	)

	log.Println("Starting Time-Series Analytics Engine...")

	// Initialize storage
	hotStorage := storage.NewHotStorage(maxSeries, maxPoints)
	log.Printf("Initialized hot storage (max series: %d, max points per series: %d)", maxSeries, maxPoints)

	// Initialize stream processor
	streamProcessor := ingestion.NewStreamProcessor(hotStorage, bufferSize, batchSize, flushInterval)
	log.Printf("Initialized stream processor (buffer: %d, batch: %d, flush: %v)", bufferSize, batchSize, flushInterval)

	// Start stream processor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := streamProcessor.Start(ctx); err != nil {
		log.Fatalf("Failed to start stream processor: %v", err)
	}
	log.Println("Stream processor started")

	// Initialize HTTP API
	apiServer := api.NewServer(hotStorage, streamProcessor)
	log.Println("HTTP API server initialized")

	// Create HTTP server
	server := &http.Server{
		Addr:    serverPort,
		Handler: apiServer,
	}

	// Start server in goroutine
	go func() {
		log.Printf("Starting HTTP server on %s", serverPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	// Setup graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Print startup information
	printStartupInfo(serverPort)

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

func printStartupInfo(port string) {
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("🚀 Time-Series Analytics Engine Started")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("📊 HTTP API: http://localhost%s\n", port)
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