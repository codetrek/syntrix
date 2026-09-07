package config

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"time"

	services "github.com/syntrixbase/syntrix/internal/services/config"
)

// Config holds configuration for the Puller service.
type Config struct {
	// gRPC Server configuration
	GRPC GRPCConfig `yaml:"grpc"`

	// Backend configurations - references storage.backends by name
	Backends []PullerBackendConfig `yaml:"backends"`

	// Event buffer configuration
	Buffer BufferConfig `yaml:"buffer"`

	// Consumer management configuration
	Consumer ConsumerConfig `yaml:"consumer"`

	// Event cleanup configuration
	Cleaner CleanerConfig `yaml:"cleaner"`

	// Bootstrap configuration
	Bootstrap BootstrapConfig `yaml:"bootstrap"`

	// Observability configuration
	Metrics MetricsConfig `yaml:"metrics"`
	Health  HealthConfig  `yaml:"health"`
}

// GRPCConfig holds gRPC server configuration.
// Puller registers with the shared server; this config controls subscriptions.
type GRPCConfig struct {
	MaxConnections int `yaml:"max_connections"`
	// ChannelSize is the size of the subscriber channel.
	// Defaults to 10000 if not set.
	ChannelSize int `yaml:"channel_size"`
	// HeartbeatInterval is the interval at which the server sends heartbeat
	// events to connected clients to keep connections alive.
	// A heartbeat is a PullerEvent with nil ChangeEvent.
	// Defaults to 30 seconds through the service configuration lifecycle.
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`
}

// PullerBackendConfig references a storage backend and adds puller-specific settings.
type PullerBackendConfig struct {
	// Name references storage.backends[name]
	Name string `yaml:"name"`
	// SourceID binds this stream to a stable operator-assigned source identity.
	SourceID string `yaml:"source_id"`

	// Collections to pull (whitelist)
	Collections []string `yaml:"collections"`
}

// BufferConfig holds PebbleDB event buffer configuration.
type BufferConfig struct {
	// Path for PebbleDB storage (per-backend subdirs created automatically)
	Path string `yaml:"path"`

	// Maximum retained logical record bytes (e.g., "10GiB"). Pebble physical
	// disk usage also includes WAL, compaction and versions held by readers.
	MaxSize string `yaml:"max_size"`

	// BatchSize is the max number of events per batch.
	BatchSize int `yaml:"batch_size"`

	// BatchInterval limits formation wait from the oldest admitted event.
	// Storage synchronization and existing backlog can extend delivery latency.
	BatchInterval time.Duration `yaml:"batch_interval"`

	// QueueSize includes pending, batching and synchronizing events until
	// committed memory publication completes.
	QueueSize  int   `yaml:"queue_size"`
	QueueBytes int64 `yaml:"queue_bytes"`
	BatchBytes int64 `yaml:"batch_bytes"`
}

// ConsumerConfig holds consumer management configuration.
type ConsumerConfig struct {
	// Number of events behind to trigger catch-up mode
	CatchUpThreshold int `yaml:"catch_up_threshold"`

	// Enable coalescing when consumer is catching up
	CoalesceOnCatchUp bool  `yaml:"coalesce_on_catch_up"`
	QueueBytes        int64 `yaml:"queue_bytes"`
	PageSize          int   `yaml:"page_size"`
	PageBytes         int64 `yaml:"page_bytes"`
}

// CleanerConfig holds event cleanup configuration.
type CleanerConfig struct {
	// How often to run cleanup
	Interval time.Duration `yaml:"interval"`

	// How long to retain events
	Retention time.Duration `yaml:"retention"`
}

// BootstrapConfig holds first-run configuration.
type BootstrapConfig struct {
	// Bootstrap mode: "from_now" or "from_beginning"
	Mode string `yaml:"mode"`
}

// MetricsConfig holds Prometheus metrics configuration.
type MetricsConfig struct {
	Port int    `yaml:"port"`
	Path string `yaml:"path"`
}

// HealthConfig holds health check endpoint configuration.
type HealthConfig struct {
	Port int    `yaml:"port"`
	Path string `yaml:"path"`
}

// DefaultConfig returns sensible defaults for PullerConfig.
func DefaultConfig() Config {
	return Config{
		GRPC: GRPCConfig{
			MaxConnections:    100,
			ChannelSize:       10000,
			HeartbeatInterval: 30 * time.Second,
		},
		Backends: []PullerBackendConfig{
			{
				Name:        "default_mongo",
				SourceID:    "default-mongo",
				Collections: []string{"documents"},
			},
		},
		Buffer: BufferConfig{
			Path:          "data/puller/events",
			MaxSize:       "10GiB",
			BatchSize:     100,
			BatchInterval: 10 * time.Millisecond,
			QueueSize:     10000,
			QueueBytes:    64 << 20,
			BatchBytes:    16 << 20,
		},
		Consumer: ConsumerConfig{
			CatchUpThreshold:  100000,
			CoalesceOnCatchUp: true,
			QueueBytes:        64 << 20,
			PageSize:          100,
			PageBytes:         16 << 20,
		},
		Cleaner: CleanerConfig{
			Interval:  1 * time.Minute,
			Retention: 1 * time.Hour,
		},
		Bootstrap: BootstrapConfig{
			Mode: "from_now",
		},
		Metrics: MetricsConfig{
			Port: 9090,
			Path: "/metrics",
		},
		Health: HealthConfig{
			Port: 8081,
			Path: "/health",
		},
	}
}

// Validate validates the PullerConfig.
func (c *Config) Validate(_ services.DeploymentMode) error {
	if c.GRPC.MaxConnections <= 0 {
		return errors.New("puller.grpc.max_connections must be positive")
	}

	if len(c.Backends) == 0 {
		return errors.New("puller.backends must have at least one backend")
	}

	seenNames := make(map[string]bool)
	seenSources := make(map[string]bool)
	for i, b := range c.Backends {
		if b.Name == "" {
			return fmt.Errorf("puller.backends[%d].name is required", i)
		}
		if b.SourceID == "" {
			return fmt.Errorf("puller.backends[%d].source_id is required", i)
		}
		if seenNames[b.Name] || seenSources[b.SourceID] {
			return errors.New("puller backend names and source IDs must be unique")
		}
		seenNames[b.Name], seenSources[b.SourceID] = true, true
		if len(b.Collections) == 0 {
			return fmt.Errorf("puller.backends[%d].collections must specify at least one collection", i)
		}
	}

	if c.GRPC.ChannelSize <= 0 || c.GRPC.HeartbeatInterval < 0 {
		return errors.New("puller channel_size must be positive and heartbeat_interval nonnegative")
	}
	if c.Buffer.QueueBytes <= 0 || c.Buffer.BatchBytes <= 0 || c.Buffer.BatchBytes > c.Buffer.QueueBytes {
		return errors.New("puller buffer byte limits must be positive and batch_bytes must not exceed queue_bytes")
	}
	if c.Consumer.QueueBytes <= 0 || c.Consumer.PageSize <= 0 || c.Consumer.PageBytes <= 0 {
		return errors.New("puller consumer queue_bytes, page_size and page_bytes must be positive")
	}
	if c.Consumer.PageBytes < c.Buffer.BatchBytes {
		return errors.New("puller consumer page_bytes must accommodate a maximum-sized event")
	}
	if size, err := ParseByteSize(c.Buffer.MaxSize); err != nil || size <= 0 {
		return fmt.Errorf("puller.buffer.max_size must be a positive byte size: %s", c.Buffer.MaxSize)
	}
	if c.Buffer.Path == "" {
		return errors.New("puller.buffer.path is required")
	}

	if c.Buffer.BatchSize <= 0 {
		return errors.New("puller.buffer.batch_size must be positive")
	}

	if c.Buffer.BatchInterval <= 0 {
		return errors.New("puller.buffer.batch_interval must be positive")
	}

	if c.Buffer.QueueSize <= 0 {
		return errors.New("puller.buffer.queue_size must be positive")
	}

	if c.Consumer.CatchUpThreshold <= 0 {
		return errors.New("puller.consumer.catch_up_threshold must be positive")
	}

	if c.Cleaner.Interval <= 0 {
		return errors.New("puller.cleaner.interval must be positive")
	}

	if c.Cleaner.Retention <= 0 {
		return errors.New("puller.cleaner.retention must be positive")
	}

	if c.Bootstrap.Mode != "from_now" && c.Bootstrap.Mode != "from_beginning" {
		return fmt.Errorf("puller.bootstrap.mode must be 'from_now' or 'from_beginning', got %q", c.Bootstrap.Mode)
	}

	return nil
}

// ApplyDefaults fills in zero values with defaults.
func (c *Config) ApplyDefaults() {
	defaults := DefaultConfig()
	if c.GRPC.ChannelSize == 0 {
		c.GRPC.ChannelSize = defaults.GRPC.ChannelSize
	}
	if c.Buffer.QueueBytes == 0 {
		c.Buffer.QueueBytes = defaults.Buffer.QueueBytes
	}
	if c.Buffer.BatchBytes == 0 {
		c.Buffer.BatchBytes = defaults.Buffer.BatchBytes
	}
	if c.Consumer.QueueBytes == 0 {
		c.Consumer.QueueBytes = defaults.Consumer.QueueBytes
	}
	if c.Consumer.PageSize == 0 {
		c.Consumer.PageSize = defaults.Consumer.PageSize
	}
	if c.Consumer.PageBytes == 0 {
		c.Consumer.PageBytes = defaults.Consumer.PageBytes
	}
	if c.GRPC.MaxConnections == 0 {
		c.GRPC.MaxConnections = defaults.GRPC.MaxConnections
	}
	if c.GRPC.HeartbeatInterval == 0 {
		c.GRPC.HeartbeatInterval = defaults.GRPC.HeartbeatInterval
	}
	if len(c.Backends) == 0 {
		c.Backends = defaults.Backends
	}
	if c.Buffer.Path == "" {
		c.Buffer.Path = defaults.Buffer.Path
	}
	if c.Buffer.MaxSize == "" {
		c.Buffer.MaxSize = defaults.Buffer.MaxSize
	}
	if c.Buffer.BatchSize == 0 {
		c.Buffer.BatchSize = defaults.Buffer.BatchSize
	}
	if c.Buffer.BatchInterval == 0 {
		c.Buffer.BatchInterval = defaults.Buffer.BatchInterval
	}
	if c.Buffer.QueueSize == 0 {
		c.Buffer.QueueSize = defaults.Buffer.QueueSize
	}
	if c.Consumer.CatchUpThreshold == 0 {
		c.Consumer.CatchUpThreshold = defaults.Consumer.CatchUpThreshold
	}
	if c.Cleaner.Interval == 0 {
		c.Cleaner.Interval = defaults.Cleaner.Interval
	}
	if c.Cleaner.Retention == 0 {
		c.Cleaner.Retention = defaults.Cleaner.Retention
	}
	if c.Bootstrap.Mode == "" {
		c.Bootstrap.Mode = defaults.Bootstrap.Mode
	}
	if c.Metrics.Port == 0 {
		c.Metrics.Port = defaults.Metrics.Port
	}
	if c.Metrics.Path == "" {
		c.Metrics.Path = defaults.Metrics.Path
	}
	if c.Health.Port == 0 {
		c.Health.Port = defaults.Health.Port
	}
	if c.Health.Path == "" {
		c.Health.Path = defaults.Health.Path
	}
}

// ApplyEnvOverrides applies environment variable overrides.
// No env vars for puller config currently.
func (c *Config) ApplyEnvOverrides() { _ = c }

// ResolvePaths resolves relative paths using the given directories.
// - configDir: not used for puller config (no config-related paths)
// - dataDir: base directory for runtime data paths (buffer.path)
func (c *Config) ResolvePaths(configDir, dataDir string) {
	_ = configDir // puller has no config-related paths
	if c.Buffer.Path != "" && !filepath.IsAbs(c.Buffer.Path) {
		c.Buffer.Path = filepath.Join(dataDir, c.Buffer.Path)
	}
}

// ParseByteSize parses a human-readable byte size string (e.g., "10GiB", "1TiB").
// Returns the size in bytes.
func ParseByteSize(s string) (int64, error) {
	if s == "" {
		return 0, errors.New("empty size string")
	}

	// Find where the number ends and the unit begins
	var numEnd int
	for i, c := range s {
		if c < '0' || c > '9' {
			numEnd = i
			break
		}
		numEnd = i + 1
	}

	if numEnd == 0 {
		return 0, fmt.Errorf("invalid size string: %q", s)
	}

	numStr := s[:numEnd]
	unit := s[numEnd:]

	num, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid byte count: %w", err)
	}

	multiplier := int64(1)
	switch unit {
	case "", "B":
		multiplier = 1
	case "KiB", "K":
		multiplier = 1024
	case "MiB", "M":
		multiplier = 1024 * 1024
	case "GiB", "G":
		multiplier = 1024 * 1024 * 1024
	case "TiB", "T":
		multiplier = 1024 * 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("unknown size unit: %q", unit)
	}

	if num > math.MaxInt64/multiplier {
		return 0, errors.New("byte size overflows int64")
	}
	return num * multiplier, nil
}
