package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Config represents the complete system configuration
type Config struct {
	Server    ServerConfig    `json:"server"`
	Storage   StorageConfig   `json:"storage"`
	Ingestion IngestionConfig `json:"ingestion"`
	Analytics AnalyticsConfig `json:"analytics"`
}

// ServerConfig contains HTTP server settings
type ServerConfig struct {
	Port         string   `json:"port"`
	ReadTimeout  Duration `json:"read_timeout"`
	WriteTimeout Duration `json:"write_timeout"`
	IdleTimeout  Duration `json:"idle_timeout"`
}

// StorageConfig contains storage layer settings
type StorageConfig struct {
	Hot  HotStorageConfig  `json:"hot"`
	Warm WarmStorageConfig `json:"warm"`
	Cold ColdStorageConfig `json:"cold"`
}

// HotStorageConfig contains hot storage settings
type HotStorageConfig struct {
	MaxSeries          int      `json:"max_series"`
	MaxPointsPerSeries int      `json:"max_points_per_series"`
	RetentionPeriod    Duration `json:"retention_period"`
	CleanupInterval    Duration `json:"cleanup_interval"`
}

// WarmStorageConfig contains warm storage settings
type WarmStorageConfig struct {
	Enabled            bool     `json:"enabled"`
	DataPath           string   `json:"data_path"`
	MaxFileSize        int64    `json:"max_file_size_mb"`
	RetentionPeriod    Duration `json:"retention_period"`
	CompactionInterval Duration `json:"compaction_interval"`
	CompressionLevel   int      `json:"compression_level"`
}

// ColdStorageConfig contains cold storage settings
type ColdStorageConfig struct {
	Enabled          bool     `json:"enabled"`
	Provider         string   `json:"provider"` // "filesystem", "s3", "gcs"
	DataPath         string   `json:"data_path"`
	RetentionPeriod  Duration `json:"retention_period"`
	CompressionLevel int      `json:"compression_level"`
}

// IngestionConfig contains data ingestion settings
type IngestionConfig struct {
	BufferSize      int              `json:"buffer_size"`
	BatchSize       int              `json:"batch_size"`
	FlushInterval   Duration         `json:"flush_interval"`
	WorkerPoolSize  int              `json:"worker_pool_size"`
	ValidationRules ValidationConfig `json:"validation"`
}

// ValidationConfig contains data validation rules
type ValidationConfig struct {
	MaxValueRange            float64  `json:"max_value_range"`
	AllowedMetrics           []string `json:"allowed_metrics"`
	RequiredLabels           []string `json:"required_labels"`
	MaxLabelLength           int      `json:"max_label_length"`
	MaxMetricNameLength      int      `json:"max_metric_name_length"`
	FutureTimestampThreshold Duration `json:"future_timestamp_threshold"`
	PastTimestampThreshold   Duration `json:"past_timestamp_threshold"`
}

// AnalyticsConfig contains analytics engine settings
type AnalyticsConfig struct {
	AnomalyDetection AnomalyDetectionConfig `json:"anomaly_detection"`
	Forecasting      ForecastingConfig      `json:"forecasting"`
}

// AnomalyDetectionConfig contains anomaly detection settings
type AnomalyDetectionConfig struct {
	Enabled        bool     `json:"enabled"`
	DefaultMethod  string   `json:"default_method"` // "zscore", "iqr", "isolation_forest"
	WindowSize     int      `json:"window_size"`
	Threshold      float64  `json:"threshold"`
	MinDataPoints  int      `json:"min_data_points"`
	UpdateInterval Duration `json:"update_interval"`
}

// ForecastingConfig contains forecasting settings
type ForecastingConfig struct {
	Enabled        bool     `json:"enabled"`
	DefaultMethod  string   `json:"default_method"` // "linear", "arima", "prophet"
	DefaultHorizon Duration `json:"default_horizon"`
}

// DefaultConfig returns a configuration with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port:         ":8080",
			ReadTimeout:  Duration{30 * time.Second},
			WriteTimeout: Duration{30 * time.Second},
			IdleTimeout:  Duration{120 * time.Second},
		},
		Storage: StorageConfig{
			Hot: HotStorageConfig{
				MaxSeries:          100000,
				MaxPointsPerSeries: 10000,
				RetentionPeriod:    Duration{6 * time.Hour},
				CleanupInterval:    Duration{30 * time.Minute},
			},
			Warm: WarmStorageConfig{
				Enabled:            true,
				DataPath:           "./data/warm",
				MaxFileSize:        100, // MB
				RetentionPeriod:    Duration{30 * 24 * time.Hour}, // 30 days
				CompactionInterval: Duration{6 * time.Hour},
				CompressionLevel:   6,
			},
			Cold: ColdStorageConfig{
				Enabled:          false,
				Provider:         "filesystem",
				DataPath:         "./data/cold",
				RetentionPeriod:  Duration{365 * 24 * time.Hour}, // 1 year
				CompressionLevel: 9,
			},
		},
		Ingestion: IngestionConfig{
			BufferSize:     1000,
			BatchSize:      100,
			FlushInterval:  Duration{5 * time.Second},
			WorkerPoolSize: 4,
			ValidationRules: ValidationConfig{
				MaxValueRange:            1e12,
				AllowedMetrics:           []string{}, // Empty means allow all
				RequiredLabels:           []string{},
				MaxLabelLength:           256,
				MaxMetricNameLength:      512,
				FutureTimestampThreshold: Duration{time.Hour},
				PastTimestampThreshold:   Duration{7 * 24 * time.Hour},
			},
		},
		Analytics: AnalyticsConfig{
			AnomalyDetection: AnomalyDetectionConfig{
				Enabled:        true,
				DefaultMethod:  "zscore",
				WindowSize:     100,
				Threshold:      3.0,
				MinDataPoints:  10,
				UpdateInterval: Duration{5 * time.Minute},
			},
			Forecasting: ForecastingConfig{
				Enabled:        true,
				DefaultMethod:  "linear",
				DefaultHorizon: Duration{24 * time.Hour},
			},
		},
	}
}

// LoadFromFile loads configuration from a JSON file
func LoadFromFile(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", filename, err)
	}

	config := DefaultConfig()
	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", filename, err)
	}

	return config, nil
}

// LoadFromEnv loads configuration from environment variables
func LoadFromEnv() *Config {
	config := DefaultConfig()

	// Server configuration
	if port := os.Getenv("TSENGINE_PORT"); port != "" {
		config.Server.Port = port
	}

	// Storage configuration
	if maxSeries := os.Getenv("TSENGINE_HOT_MAX_SERIES"); maxSeries != "" {
		if val, err := parseIntFromEnv(maxSeries); err == nil {
			config.Storage.Hot.MaxSeries = val
		}
	}

	if maxPoints := os.Getenv("TSENGINE_HOT_MAX_POINTS"); maxPoints != "" {
		if val, err := parseIntFromEnv(maxPoints); err == nil {
			config.Storage.Hot.MaxPointsPerSeries = val
		}
	}

	if warmPath := os.Getenv("TSENGINE_WARM_DATA_PATH"); warmPath != "" {
		config.Storage.Warm.DataPath = warmPath
	}

	if coldPath := os.Getenv("TSENGINE_COLD_DATA_PATH"); coldPath != "" {
		config.Storage.Cold.DataPath = coldPath
	}

	// Ingestion configuration
	if bufferSize := os.Getenv("TSENGINE_BUFFER_SIZE"); bufferSize != "" {
		if val, err := parseIntFromEnv(bufferSize); err == nil {
			config.Ingestion.BufferSize = val
		}
	}

	if batchSize := os.Getenv("TSENGINE_BATCH_SIZE"); batchSize != "" {
		if val, err := parseIntFromEnv(batchSize); err == nil {
			config.Ingestion.BatchSize = val
		}
	}

	return config
}

// SaveToFile saves configuration to a JSON file
func (c *Config) SaveToFile(filename string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file %s: %w", filename, err)
	}

	return nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	// Validate server config
	if c.Server.Port == "" {
		return fmt.Errorf("server port cannot be empty")
	}

	// Validate hot storage config
	if c.Storage.Hot.MaxSeries <= 0 {
		return fmt.Errorf("hot storage max series must be positive")
	}
	if c.Storage.Hot.MaxPointsPerSeries <= 0 {
		return fmt.Errorf("hot storage max points per series must be positive")
	}

	// Validate warm storage config
	if c.Storage.Warm.Enabled && c.Storage.Warm.DataPath == "" {
		return fmt.Errorf("warm storage data path cannot be empty when enabled")
	}

	// Validate cold storage config
	if c.Storage.Cold.Enabled && c.Storage.Cold.DataPath == "" {
		return fmt.Errorf("cold storage data path cannot be empty when enabled")
	}

	// Validate ingestion config
	if c.Ingestion.BufferSize <= 0 {
		return fmt.Errorf("ingestion buffer size must be positive")
	}
	if c.Ingestion.BatchSize <= 0 {
		return fmt.Errorf("ingestion batch size must be positive")
	}
	if c.Ingestion.WorkerPoolSize <= 0 {
		return fmt.Errorf("ingestion worker pool size must be positive")
	}

	return nil
}

// GetStorageDataPaths returns all configured data paths
func (c *Config) GetStorageDataPaths() []string {
	var paths []string
	
	if c.Storage.Warm.Enabled && c.Storage.Warm.DataPath != "" {
		paths = append(paths, c.Storage.Warm.DataPath)
	}
	
	if c.Storage.Cold.Enabled && c.Storage.Cold.DataPath != "" {
		paths = append(paths, c.Storage.Cold.DataPath)
	}
	
	return paths
}

// EnsureDataDirectories creates necessary data directories
func (c *Config) EnsureDataDirectories() error {
	paths := c.GetStorageDataPaths()
	
	for _, path := range paths {
		if err := os.MkdirAll(path, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", path, err)
		}
	}
	
	return nil
}

// Helper functions
func parseIntFromEnv(s string) (int, error) {
	var result int
	if _, err := fmt.Sscanf(s, "%d", &result); err != nil {
		return 0, err
	}
	return result, nil
}

// ConfigManager handles configuration loading and hot-reloading
type ConfigManager struct {
	config   *Config
	filename string
	watchers []func(*Config)
}

// NewConfigManager creates a new configuration manager
func NewConfigManager(filename string) (*ConfigManager, error) {
	var config *Config
	var err error
	
	if filename != "" && fileExists(filename) {
		config, err = LoadFromFile(filename)
		if err != nil {
			return nil, fmt.Errorf("failed to load config from file: %w", err)
		}
	} else {
		config = LoadFromEnv()
	}
	
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	
	if err := config.EnsureDataDirectories(); err != nil {
		return nil, fmt.Errorf("failed to create data directories: %w", err)
	}
	
	return &ConfigManager{
		config:   config,
		filename: filename,
		watchers: make([]func(*Config), 0),
	}, nil
}

// GetConfig returns the current configuration
func (cm *ConfigManager) GetConfig() *Config {
	return cm.config
}

// AddWatcher adds a function to be called when configuration changes
func (cm *ConfigManager) AddWatcher(fn func(*Config)) {
	cm.watchers = append(cm.watchers, fn)
}

// Reload reloads the configuration from file
func (cm *ConfigManager) Reload() error {
	if cm.filename == "" || !fileExists(cm.filename) {
		return fmt.Errorf("no config file to reload")
	}
	
	newConfig, err := LoadFromFile(cm.filename)
	if err != nil {
		return fmt.Errorf("failed to reload config: %w", err)
	}
	
	if err := newConfig.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	
	cm.config = newConfig
	
	// Notify watchers
	for _, watcher := range cm.watchers {
		watcher(newConfig)
	}
	
	return nil
}

// fileExists checks if a file exists
func fileExists(filename string) bool {
	_, err := os.Stat(filename)
	return !os.IsNotExist(err)
}