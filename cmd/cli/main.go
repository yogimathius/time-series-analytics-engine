package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultServerURL = "http://localhost:8080"
	version         = "0.1.0"
)

type CLIConfig struct {
	ServerURL string
	Verbose   bool
}

type MetricData struct {
	Name      string            `json:"name"`
	Value     float64           `json:"value"`
	Timestamp string            `json:"timestamp,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

func main() {
	var (
		serverURL = flag.String("server", defaultServerURL, "Time-series server URL")
		verbose   = flag.Bool("v", false, "Verbose output")
		command   = flag.String("cmd", "", "Command to execute")
		help      = flag.Bool("help", false, "Show help")
	)
	flag.Parse()

	if *help || *command == "" {
		showHelp()
		return
	}

	config := CLIConfig{
		ServerURL: *serverURL,
		Verbose:   *verbose,
	}

	args := flag.Args()

	switch *command {
	case "ingest":
		handleIngest(config, args)
	case "query":
		handleQuery(config, args)
	case "anomaly":
		handleAnomaly(config, args)
	case "forecast":
		handleForecast(config, args)
	case "series":
		handleSeries(config, args)
	case "stats":
		handleStats(config)
	case "health":
		handleHealth(config)
	case "demo":
		handleDemo(config, args)
	case "benchmark":
		handleBenchmark(config, args)
	default:
		fmt.Printf("Unknown command: %s\n", *command)
		showHelp()
		os.Exit(1)
	}
}

func showHelp() {
	fmt.Printf(`Time-Series Analytics Engine CLI v%s

USAGE:
    tsdb-cli --cmd <command> [options] [args]

COMMANDS:
    ingest    - Ingest metrics into the time-series database
    query     - Query time-series data
    anomaly   - Detect anomalies in time-series data
    forecast  - Generate forecasts for time-series data
    series    - List all time series
    stats     - Show system statistics
    health    - Check system health
    demo      - Run demo with sample data
    benchmark - Run performance benchmarks

INGESTION:
    tsdb-cli --cmd ingest --metric cpu.usage --value 85.5
    tsdb-cli --cmd ingest --metric cpu.usage --value 85.5 --labels "host=server1,env=prod"
    
    # Ingest random demo data
    tsdb-cli --cmd demo --series 5 --points 1000

QUERYING:
    tsdb-cli --cmd query --series cpu.usage --start -1h
    tsdb-cli --cmd query --series memory.usage --start 2024-01-01T00:00:00Z --end 2024-01-01T23:59:59Z

ANALYTICS:
    tsdb-cli --cmd anomaly --series cpu.usage --start -24h
    tsdb-cli --cmd forecast --series cpu.usage --horizon 24

MONITORING:
    tsdb-cli --cmd series
    tsdb-cli --cmd stats
    tsdb-cli --cmd health

OPTIONS:
    --server   Server URL (default: http://localhost:8080)
    --v        Verbose output
    --help     Show this help message

EXAMPLES:
    # Start with demo data
    tsdb-cli --cmd demo --series 3 --points 500
    
    # Analyze the demo data
    tsdb-cli --cmd anomaly --series demo.cpu.usage
    tsdb-cli --cmd forecast --series demo.cpu.usage --horizon 12
    
    # Monitor system
    tsdb-cli --cmd stats
    tsdb-cli --cmd health

`, version)
}

func handleIngest(config CLIConfig, args []string) {
	var (
		metric    = getArg(args, "--metric", "")
		value     = getArg(args, "--value", "0")
		labels    = getArg(args, "--labels", "")
		timestamp = getArg(args, "--timestamp", "")
	)

	if metric == "" {
		fmt.Println("Error: --metric is required")
		return
	}

	// Parse value
	var valueFloat float64
	if _, err := fmt.Sscanf(value, "%f", &valueFloat); err != nil {
		fmt.Printf("Error: Invalid value '%s': %v\n", value, err)
		return
	}

	// Parse labels
	labelMap := make(map[string]string)
	if labels != "" {
		pairs := strings.Split(labels, ",")
		for _, pair := range pairs {
			kv := strings.Split(pair, "=")
			if len(kv) == 2 {
				labelMap[kv[0]] = kv[1]
			}
		}
	}

	// Create metric data
	data := MetricData{
		Name:   metric,
		Value:  valueFloat,
		Labels: labelMap,
	}

	if timestamp != "" {
		data.Timestamp = timestamp
	}

	// Send request
	if err := sendMetric(config, data); err != nil {
		fmt.Printf("Error ingesting metric: %v\n", err)
		return
	}

	fmt.Printf("✓ Ingested metric: %s = %.2f\n", metric, valueFloat)
	if len(labelMap) > 0 {
		fmt.Printf("  Labels: %v\n", labelMap)
	}
}

func handleQuery(config CLIConfig, args []string) {
	var (
		series = getArg(args, "--series", "")
		start  = getArg(args, "--start", "-1h")
		end    = getArg(args, "--end", "")
		limit  = getArg(args, "--limit", "")
	)

	if series == "" {
		fmt.Println("Error: --series is required")
		return
	}

	url := fmt.Sprintf("%s/api/v1/query?series=%s&start=%s", config.ServerURL, series, start)
	if end != "" {
		url += "&end=" + end
	}
	if limit != "" {
		url += "&limit=" + limit
	}

	resp, err := http.Get(url)
	if err != nil {
		fmt.Printf("Error querying data: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading response: %v\n", err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Query failed: %s\n", string(body))
		return
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Printf("Error parsing response: %v\n", err)
		return
	}

	fmt.Printf("Query Results for series: %s\n", series)
	fmt.Printf("Points: %v\n", result["count"])
	if config.Verbose {
		prettyJSON, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(prettyJSON))
	}
}

func handleAnomaly(config CLIConfig, args []string) {
	var (
		series = getArg(args, "--series", "")
		start  = getArg(args, "--start", "-24h")
		end    = getArg(args, "--end", "")
	)

	if series == "" {
		fmt.Println("Error: --series is required")
		return
	}

	reqData := map[string]string{
		"series_id": series,
		"start":     start,
	}
	if end != "" {
		reqData["end"] = end
	}

	jsonData, _ := json.Marshal(reqData)
	url := fmt.Sprintf("%s/api/v1/analytics/anomaly", config.ServerURL)

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		fmt.Printf("Error detecting anomalies: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading response: %v\n", err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Anomaly detection failed: %s\n", string(body))
		return
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Printf("Error parsing response: %v\n", err)
		return
	}

	fmt.Printf("🔍 Anomaly Detection Results for: %s\n", series)
	fmt.Printf("Anomalies Found: %v\n", result["count"])
	fmt.Printf("Analyzed Points: %v\n", result["analyzed_points"])

	if config.Verbose {
		prettyJSON, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(prettyJSON))
	}
}

func handleForecast(config CLIConfig, args []string) {
	var (
		series  = getArg(args, "--series", "")
		horizon = getArg(args, "--horizon", "24")
		start   = getArg(args, "--start", "-7d")
		end     = getArg(args, "--end", "")
	)

	if series == "" {
		fmt.Println("Error: --series is required")
		return
	}

	var horizonInt int
	if _, err := fmt.Sscanf(horizon, "%d", &horizonInt); err != nil {
		fmt.Printf("Error: Invalid horizon '%s': %v\n", horizon, err)
		return
	}

	reqData := map[string]interface{}{
		"series_id": series,
		"horizon":   horizonInt,
		"start":     start,
	}
	if end != "" {
		reqData["end"] = end
	}

	jsonData, _ := json.Marshal(reqData)
	url := fmt.Sprintf("%s/api/v1/analytics/forecast", config.ServerURL)

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		fmt.Printf("Error generating forecast: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading response: %v\n", err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Forecast generation failed: %s\n", string(body))
		return
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Printf("Error parsing response: %v\n", err)
		return
	}

	fmt.Printf("📈 Forecast Results for: %s\n", series)
	fmt.Printf("Forecast Horizon: %d steps\n", horizonInt)
	fmt.Printf("Training Points: %v\n", result["training_points"])

	if config.Verbose {
		prettyJSON, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(prettyJSON))
	}
}

func handleSeries(config CLIConfig, args []string) {
	url := fmt.Sprintf("%s/api/v1/series", config.ServerURL)

	resp, err := http.Get(url)
	if err != nil {
		fmt.Printf("Error listing series: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading response: %v\n", err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Failed to list series: %s\n", string(body))
		return
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Printf("Error parsing response: %v\n", err)
		return
	}

	fmt.Printf("📊 Time Series Summary\n")
	fmt.Printf("Total Series: %v\n", result["count"])

	if config.Verbose {
		prettyJSON, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(prettyJSON))
	}
}

func handleStats(config CLIConfig) {
	url := fmt.Sprintf("%s/api/v1/stats", config.ServerURL)

	resp, err := http.Get(url)
	if err != nil {
		fmt.Printf("Error getting stats: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading response: %v\n", err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Failed to get stats: %s\n", string(body))
		return
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Printf("Error parsing response: %v\n", err)
		return
	}

	fmt.Printf("📈 System Statistics\n")
	prettyJSON, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(prettyJSON))
}

func handleHealth(config CLIConfig) {
	url := fmt.Sprintf("%s/health", config.ServerURL)

	resp, err := http.Get(url)
	if err != nil {
		fmt.Printf("❌ Health check failed: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading response: %v\n", err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("❌ System unhealthy: %s\n", string(body))
		return
	}

	fmt.Println("✅ System is healthy")
	if config.Verbose {
		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err == nil {
			prettyJSON, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(prettyJSON))
		}
	}
}

func handleDemo(config CLIConfig, args []string) {
	var (
		seriesCount = getArg(args, "--series", "3")
		pointsCount = getArg(args, "--points", "100")
	)

	var numSeries, numPoints int
	if _, err := fmt.Sscanf(seriesCount, "%d", &numSeries); err != nil {
		fmt.Printf("Error: Invalid series count '%s': %v\n", seriesCount, err)
		return
	}
	if _, err := fmt.Sscanf(pointsCount, "%d", &numPoints); err != nil {
		fmt.Printf("Error: Invalid points count '%s': %v\n", pointsCount, err)
		return
	}

	fmt.Printf("🚀 Generating demo data: %d series with %d points each\n", numSeries, numPoints)

	rand.Seed(time.Now().UnixNano())
	seriesNames := []string{"demo.cpu.usage", "demo.memory.usage", "demo.network.throughput", "demo.disk.io", "demo.temperature"}

	for i := 0; i < numSeries && i < len(seriesNames); i++ {
		seriesName := seriesNames[i]
		fmt.Printf("📊 Generating data for %s...\n", seriesName)

		baseValue := 50.0 + rand.Float64()*50 // 50-100 range
		
		for j := 0; j < numPoints; j++ {
			// Generate realistic time-series data with trends and noise
			timestamp := time.Now().Add(time.Duration(-numPoints+j) * time.Minute)
			
			// Add seasonal pattern + noise + gradual trend
			seasonal := 10 * math.Sin(float64(j)*2*math.Pi/24) // 24-point cycle
			noise := (rand.Float64() - 0.5) * 10
			trend := float64(j) * 0.1
			value := baseValue + seasonal + noise + trend

			// Occasionally add anomalies
			if rand.Float64() < 0.05 {
				value += (rand.Float64() - 0.5) * 50 // Random spike/dip
			}

			data := MetricData{
				Name:      seriesName,
				Value:     value,
				Timestamp: timestamp.Format(time.RFC3339),
				Labels: map[string]string{
					"environment": "demo",
					"host":        fmt.Sprintf("server-%d", (i%3)+1),
				},
			}

			if err := sendMetric(config, data); err != nil {
				fmt.Printf("Error ingesting demo data: %v\n", err)
				return
			}

			if j%50 == 0 {
				fmt.Printf("  Progress: %d/%d points\n", j, numPoints)
			}
		}
		
		fmt.Printf("✅ Completed %s\n", seriesName)
	}

	fmt.Printf("🎉 Demo data generation complete!\n")
	fmt.Printf("\nTry these commands:\n")
	fmt.Printf("  tsdb-cli --cmd series\n")
	fmt.Printf("  tsdb-cli --cmd query --series demo.cpu.usage --start -2h\n")
	fmt.Printf("  tsdb-cli --cmd anomaly --series demo.cpu.usage\n")
	fmt.Printf("  tsdb-cli --cmd forecast --series demo.cpu.usage --horizon 12\n")
}

func handleBenchmark(config CLIConfig, args []string) {
	var (
		duration    = getArg(args, "--duration", "30s")
		concurrency = getArg(args, "--concurrency", "10")
		metricName  = getArg(args, "--metric", "benchmark.test")
	)

	dur, err := time.ParseDuration(duration)
	if err != nil {
		fmt.Printf("Error: Invalid duration '%s': %v\n", duration, err)
		return
	}

	var concurrent int
	if _, err := fmt.Sscanf(concurrency, "%d", &concurrent); err != nil {
		fmt.Printf("Error: Invalid concurrency '%s': %v\n", concurrency, err)
		return
	}

	fmt.Printf("🏃 Running benchmark: %d concurrent clients for %s\n", concurrent, duration)
	
	start := time.Now()
	done := make(chan bool, concurrent)
	totalRequests := make(chan int, concurrent)

	// Launch concurrent workers
	for i := 0; i < concurrent; i++ {
		go func(workerID int) {
			requests := 0
			for time.Since(start) < dur {
				data := MetricData{
					Name:  metricName,
					Value: rand.Float64() * 100,
					Labels: map[string]string{
						"worker_id": fmt.Sprintf("%d", workerID),
						"benchmark": "true",
					},
				}

				if sendMetric(config, data) == nil {
					requests++
				}

				time.Sleep(time.Millisecond * 10) // Small delay to avoid overwhelming
			}
			totalRequests <- requests
			done <- true
		}(i)
	}

	// Wait for all workers to complete
	total := 0
	for i := 0; i < concurrent; i++ {
		<-done
		total += <-totalRequests
	}

	elapsed := time.Since(start)
	rps := float64(total) / elapsed.Seconds()

	fmt.Printf("📊 Benchmark Results:\n")
	fmt.Printf("  Duration: %v\n", elapsed)
	fmt.Printf("  Total Requests: %d\n", total)
	fmt.Printf("  Requests/sec: %.2f\n", rps)
	fmt.Printf("  Concurrent Workers: %d\n", concurrent)
}

func sendMetric(config CLIConfig, data MetricData) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/api/v1/metrics", config.ServerURL)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func getArg(args []string, flag, defaultValue string) string {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return defaultValue
}

