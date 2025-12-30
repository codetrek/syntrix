package config

import (
	"testing"
	"time"
)

func TestDefaultPullerConfig(t *testing.T) {
	cfg := DefaultPullerConfig()

	if cfg.GRPC.Address != ":50051" {
		t.Errorf("GRPC.Address = %q, want %q", cfg.GRPC.Address, ":50051")
	}

	if cfg.GRPC.MaxConnections != 100 {
		t.Errorf("GRPC.MaxConnections = %d, want %d", cfg.GRPC.MaxConnections, 100)
	}

	if len(cfg.Backends) != 1 || cfg.Backends[0].Name != "default_mongo" {
		t.Errorf("Backends = %v, want single 'default_mongo'", cfg.Backends)
	}

	if cfg.Checkpoint.Backend != "mongodb" {
		t.Errorf("Checkpoint.Backend = %q, want %q", cfg.Checkpoint.Backend, "mongodb")
	}

	if cfg.Checkpoint.Interval != time.Second {
		t.Errorf("Checkpoint.Interval = %v, want %v", cfg.Checkpoint.Interval, time.Second)
	}

	if cfg.Consumer.CatchUpThreshold != 100000 {
		t.Errorf("Consumer.CatchUpThreshold = %d, want %d", cfg.Consumer.CatchUpThreshold, 100000)
	}

	if cfg.Cleaner.Retention != time.Hour {
		t.Errorf("Cleaner.Retention = %v, want %v", cfg.Cleaner.Retention, time.Hour)
	}

	if cfg.Bootstrap.Mode != "from_now" {
		t.Errorf("Bootstrap.Mode = %q, want %q", cfg.Bootstrap.Mode, "from_now")
	}

	// Default config should be valid
	if err := cfg.Validate(); err != nil {
		t.Errorf("DefaultPullerConfig().Validate() error = %v", err)
	}
}

func TestPullerConfig_Validate(t *testing.T) {
	validCfg := func() PullerConfig {
		return DefaultPullerConfig()
	}

	tests := []struct {
		name    string
		modify  func(*PullerConfig)
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid default config",
			modify:  func(c *PullerConfig) {},
			wantErr: false,
		},
		{
			name:    "empty grpc address",
			modify:  func(c *PullerConfig) { c.GRPC.Address = "" },
			wantErr: true,
			errMsg:  "grpc.address",
		},
		{
			name:    "zero max connections",
			modify:  func(c *PullerConfig) { c.GRPC.MaxConnections = 0 },
			wantErr: true,
			errMsg:  "max_connections",
		},
		{
			name:    "negative max connections",
			modify:  func(c *PullerConfig) { c.GRPC.MaxConnections = -1 },
			wantErr: true,
			errMsg:  "max_connections",
		},
		{
			name:    "empty backends",
			modify:  func(c *PullerConfig) { c.Backends = nil },
			wantErr: true,
			errMsg:  "backends",
		},
		{
			name: "backend with empty name",
			modify: func(c *PullerConfig) {
				c.Backends = []PullerBackendConfig{{Name: ""}}
			},
			wantErr: true,
			errMsg:  "name is required",
		},
		{
			name: "backend with both include and exclude",
			modify: func(c *PullerConfig) {
				c.Backends = []PullerBackendConfig{{
					Name:               "test",
					IncludeCollections: []string{"a"},
					ExcludeCollections: []string{"b"},
				}}
			},
			wantErr: true,
			errMsg:  "either include_collections or exclude_collections",
		},
		{
			name:    "invalid checkpoint backend",
			modify:  func(c *PullerConfig) { c.Checkpoint.Backend = "invalid" },
			wantErr: true,
			errMsg:  "mongodb' or 'etcd",
		},
		{
			name:    "etcd checkpoint backend is valid",
			modify:  func(c *PullerConfig) { c.Checkpoint.Backend = "etcd" },
			wantErr: false,
		},
		{
			name:    "zero checkpoint interval",
			modify:  func(c *PullerConfig) { c.Checkpoint.Interval = 0 },
			wantErr: true,
			errMsg:  "interval",
		},
		{
			name:    "zero event count",
			modify:  func(c *PullerConfig) { c.Checkpoint.EventCount = 0 },
			wantErr: true,
			errMsg:  "event_count",
		},
		{
			name:    "empty buffer path",
			modify:  func(c *PullerConfig) { c.Buffer.Path = "" },
			wantErr: true,
			errMsg:  "path",
		},
		{
			name:    "zero catch up threshold",
			modify:  func(c *PullerConfig) { c.Consumer.CatchUpThreshold = 0 },
			wantErr: true,
			errMsg:  "catch_up_threshold",
		},
		{
			name:    "zero cleaner interval",
			modify:  func(c *PullerConfig) { c.Cleaner.Interval = 0 },
			wantErr: true,
			errMsg:  "interval",
		},
		{
			name:    "zero retention",
			modify:  func(c *PullerConfig) { c.Cleaner.Retention = 0 },
			wantErr: true,
			errMsg:  "retention",
		},
		{
			name:    "invalid bootstrap mode",
			modify:  func(c *PullerConfig) { c.Bootstrap.Mode = "invalid" },
			wantErr: true,
			errMsg:  "from_now' or 'from_beginning",
		},
		{
			name:    "from_beginning bootstrap mode is valid",
			modify:  func(c *PullerConfig) { c.Bootstrap.Mode = "from_beginning" },
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validCfg()
			tt.modify(&cfg)

			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && err != nil && tt.errMsg != "" {
				if !containsStr(err.Error(), tt.errMsg) {
					t.Errorf("Validate() error = %q, should contain %q", err.Error(), tt.errMsg)
				}
			}
		})
	}
}

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		input   string
		want    int64
		wantErr bool
	}{
		{"1", 1, false},
		{"100", 100, false},
		{"1B", 1, false},
		{"1KiB", 1024, false},
		{"1K", 1024, false},
		{"1MiB", 1024 * 1024, false},
		{"1M", 1024 * 1024, false},
		{"1GiB", 1024 * 1024 * 1024, false},
		{"1G", 1024 * 1024 * 1024, false},
		{"10GiB", 10 * 1024 * 1024 * 1024, false},
		{"1TiB", 1024 * 1024 * 1024 * 1024, false},
		{"1T", 1024 * 1024 * 1024 * 1024, false},
		{"", 0, true},
		{"GiB", 0, true},  // no number
		{"1XiB", 0, true}, // unknown unit
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseByteSize(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseByteSize(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseByteSize(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
