package server

import "time"

// Config holds the configuration for the unified server module.
type Config struct {
	// HTTP Configuration
	HTTPPort         int           `yaml:"http_port"`
	HTTPReadTimeout  time.Duration `yaml:"http_read_timeout"`
	HTTPWriteTimeout time.Duration `yaml:"http_write_timeout"`
	HTTPIdleTimeout  time.Duration `yaml:"http_idle_timeout"`
	EnableCORS       bool          `yaml:"enable_cors"`

	// gRPC Configuration
	GRPCPort          int  `yaml:"grpc_port"`
	GRPCMaxConcurrent uint `yaml:"grpc_max_concurrent"`
	EnableReflection  bool `yaml:"enable_reflection"`

	// Lifecycle Configuration
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

// DefaultConfig returns safe defaults for development.
func DefaultConfig() Config {
	return Config{
		HTTPPort:         8000,
		HTTPReadTimeout:  10 * time.Second,
		HTTPWriteTimeout: 10 * time.Second,
		HTTPIdleTimeout:  60 * time.Second,
		GRPCPort:         9000,
		ShutdownTimeout:  10 * time.Second,
	}
}
