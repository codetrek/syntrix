package config

import (
	"log"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds the application configuration
type Config struct {
	Storage StorageConfig `yaml:"storage"`
	Auth    AuthConfig    `yaml:"auth"`

	API      APIConfig      `yaml:"api"`
	Realtime RealtimeConfig `yaml:"realtime"`
	Query    QueryConfig    `yaml:"query"`
	CSP      CSPConfig      `yaml:"csp"`
	Trigger  TriggerConfig  `yaml:"trigger"`
}

type APIConfig struct {
	Port            int    `yaml:"port"`
	QueryServiceURL string `yaml:"query_service_url"`
}

type RealtimeConfig struct {
	Port            int    `yaml:"port"`
	QueryServiceURL string `yaml:"query_service_url"`
}

type QueryConfig struct {
	Port          int    `yaml:"port"`
	CSPServiceURL string `yaml:"csp_service_url"`
}

type CSPConfig struct {
	Port int `yaml:"port"`
}

type TriggerConfig struct {
	NatsURL     string `yaml:"nats_url"`
	RulesFile   string `yaml:"rules_file"`
	WorkerCount int    `yaml:"worker_count"`
}

type StorageConfig struct {
	MongoURI       string `yaml:"mongo_uri"`
	DatabaseName   string `yaml:"database_name"`
	DataCollection string `yaml:"data_collection"`
	SysCollection  string `yaml:"sys_collection"`
}

type AuthConfig struct {
	AccessTokenTTL  time.Duration `yaml:"access_token_ttl"`
	RefreshTokenTTL time.Duration `yaml:"refresh_token_ttl"`
	AuthCodeTTL     time.Duration `yaml:"auth_code_ttl"`
}

// LoadConfig loads configuration from files and environment variables
// Order: defaults -> config.yml -> config.local.yml -> env vars
func LoadConfig() *Config {
	// 1. Defaults
	cfg := &Config{
		Storage: StorageConfig{
			MongoURI:       "mongodb://localhost:27017",
			DatabaseName:   "syntrix",
			DataCollection: "documents",
			SysCollection:  "sys",
		},
		Auth: AuthConfig{
			AccessTokenTTL:  15 * time.Minute,
			RefreshTokenTTL: 7 * 24 * time.Hour,
			AuthCodeTTL:     2 * time.Minute,
		},
		API: APIConfig{
			Port:            8080,
			QueryServiceURL: "http://localhost:8082",
		},
		Realtime: RealtimeConfig{
			Port:            8081,
			QueryServiceURL: "http://localhost:8082",
		},
		Query: QueryConfig{
			Port:          8082,
			CSPServiceURL: "http://localhost:8083",
		},
		CSP: CSPConfig{
			Port: 8083,
		},
		Trigger: TriggerConfig{
			NatsURL:     "nats://localhost:4222",
			RulesFile:   "triggers.json",
			WorkerCount: 16,
		},
	}

	// 2. Load config.yml
	loadFile("config.yml", cfg)

	// 3. Load config.local.yml
	loadFile("config.local.yml", cfg)

	// 4. Override with Env Vars
	if val := os.Getenv("API_PORT"); val != "" {
		if port, err := strconv.Atoi(val); err == nil {
			cfg.API.Port = port
		}
	}
	if val := os.Getenv("API_QUERY_SERVICE_URL"); val != "" {
		cfg.API.QueryServiceURL = val
	}

	if val := os.Getenv("REALTIME_PORT"); val != "" {
		if port, err := strconv.Atoi(val); err == nil {
			cfg.Realtime.Port = port
		}
	}
	if val := os.Getenv("REALTIME_QUERY_SERVICE_URL"); val != "" {
		cfg.Realtime.QueryServiceURL = val
	}

	if val := os.Getenv("QUERY_PORT"); val != "" {
		if port, err := strconv.Atoi(val); err == nil {
			cfg.Query.Port = port
		}
	}
	if val := os.Getenv("QUERY_CSP_SERVICE_URL"); val != "" {
		cfg.Query.CSPServiceURL = val
	}

	if val := os.Getenv("MONGO_URI"); val != "" {
		cfg.Storage.MongoURI = val
	}
	if val := os.Getenv("DB_NAME"); val != "" {
		cfg.Storage.DatabaseName = val
	}

	if val := os.Getenv("CSP_PORT"); val != "" {
		if port, err := strconv.Atoi(val); err == nil {
			cfg.CSP.Port = port
		}
	}

	if val := os.Getenv("TRIGGER_NATS_URL"); val != "" {
		cfg.Trigger.NatsURL = val
	}
	if val := os.Getenv("TRIGGER_RULES_FILE"); val != "" {
		cfg.Trigger.RulesFile = val
	}

	return cfg
}

func loadFile(filename string, cfg *Config) {
	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return // File doesn't exist, skip
		}
		log.Printf("Warning: Error reading %s: %v", filename, err)
		return
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		log.Printf("Warning: Error parsing %s: %v", filename, err)
	}
}
