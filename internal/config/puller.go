package config

import (
	"errors"
	"fmt"
	"time"
)

// PullerConfig holds configuration for the Puller service.
type PullerConfig struct {
	// gRPC Server configuration
	GRPC PullerGRPCConfig `yaml:"grpc"`

	// Backend configurations - references storage.backends by name
	Backends []PullerBackendConfig `yaml:"backends"`

	// Checkpoint configuration
	Checkpoint CheckpointConfig `yaml:"checkpoint"`

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

// PullerGRPCConfig holds gRPC server configuration.
type PullerGRPCConfig struct {
	Address        string `yaml:"address"`
	MaxConnections int    `yaml:"max_connections"`
}

// PullerBackendConfig references a storage backend and adds puller-specific settings.
type PullerBackendConfig struct {
	// Name references storage.backends[name]
	Name string `yaml:"name"`

	// Collection filtering (applied via MongoDB aggregation pipeline)
	// Only ONE of include/exclude should be specified
	IncludeCollections []string `yaml:"include_collections"`
	ExcludeCollections []string `yaml:"exclude_collections"`
}

// CheckpointConfig holds checkpoint persistence configuration.
type CheckpointConfig struct {
	// Backend type: "mongodb" or "etcd"
	Backend string `yaml:"backend"`

	// Time-based checkpoint interval
	Interval time.Duration `yaml:"interval"`

	// Event-based checkpoint count
	EventCount int `yaml:"event_count"`
}

// BufferConfig holds PebbleDB event buffer configuration.
type BufferConfig struct {
	// Path for PebbleDB storage (per-backend subdirs created automatically)
	Path string `yaml:"path"`

	// Maximum size of the buffer (e.g., "10GiB", "1TiB")
	MaxSize string `yaml:"max_size"`

	// BatchSize is the max number of events per batch.
	BatchSize int `yaml:"batch_size"`

	// BatchInterval is the max time to wait before flushing a batch.
	BatchInterval time.Duration `yaml:"batch_interval"`

	// QueueSize is the buffer for pending writes.
	QueueSize int `yaml:"queue_size"`
}

// ConsumerConfig holds consumer management configuration.
type ConsumerConfig struct {
	// Number of events behind to trigger catch-up mode
	CatchUpThreshold int `yaml:"catch_up_threshold"`

	// Enable coalescing when consumer is catching up
	CoalesceOnCatchUp bool `yaml:"coalesce_on_catch_up"`
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

// DefaultPullerConfig returns sensible defaults for PullerConfig.
func DefaultPullerConfig() PullerConfig {
	return PullerConfig{
		GRPC: PullerGRPCConfig{
			Address:        ":50051",
			MaxConnections: 100,
		},
		Backends: []PullerBackendConfig{
			{
				Name:               "default_mongo",
				IncludeCollections: nil,
				ExcludeCollections: []string{"_system", "logs"},
			},
		},
		Checkpoint: CheckpointConfig{
			Backend:    "pebble",
			Interval:   1 * time.Second,
			EventCount: 1000,
		},
		Buffer: BufferConfig{
			Path:          "/var/lib/puller/events",
			MaxSize:       "10GiB",
			BatchSize:     100,
			BatchInterval: 5 * time.Millisecond,
			QueueSize:     1000,
		},
		Consumer: ConsumerConfig{
			CatchUpThreshold:  100000,
			CoalesceOnCatchUp: true,
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
			Port: 8080,
			Path: "/health",
		},
	}
}

// Validate validates the PullerConfig.
func (c *PullerConfig) Validate() error {
	if c.GRPC.Address == "" {
		return errors.New("puller.grpc.address is required")
	}

	if c.GRPC.MaxConnections <= 0 {
		return errors.New("puller.grpc.max_connections must be positive")
	}

	if len(c.Backends) == 0 {
		return errors.New("puller.backends must have at least one backend")
	}

	for i, b := range c.Backends {
		if b.Name == "" {
			return fmt.Errorf("puller.backends[%d].name is required", i)
		}
		if len(b.IncludeCollections) > 0 && len(b.ExcludeCollections) > 0 {
			return fmt.Errorf("puller.backends[%d]: specify either include_collections or exclude_collections, not both", i)
		}
	}

	if c.Checkpoint.Backend != "pebble" {
		return fmt.Errorf("puller.checkpoint.backend must be 'pebble', got %q", c.Checkpoint.Backend)
	}

	if c.Checkpoint.Interval <= 0 {
		return errors.New("puller.checkpoint.interval must be positive")
	}

	if c.Checkpoint.EventCount <= 0 {
		return errors.New("puller.checkpoint.event_count must be positive")
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

	var num int64
	for _, c := range numStr {
		num = num*10 + int64(c-'0')
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

	return num * multiplier, nil
}
