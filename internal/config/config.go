package config

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds the application configuration
type Config struct {
	Storage StorageConfig `yaml:"storage"`
	Auth    AuthConfig    `yaml:"auth"`

	Gateway GatewayConfig `yaml:"gateway"`
	Query   QueryConfig   `yaml:"query"`
	CSP     CSPConfig     `yaml:"csp"`
	Trigger TriggerConfig `yaml:"trigger"`
}

type GatewayConfig struct {
	Port            int               `yaml:"port"`
	QueryServiceURL string            `yaml:"query_service_url"`
	Auth            GatewayAuthConfig `yaml:"auth"`
}

type GatewayAuthConfig struct {
	Issuer   string        `yaml:"issuer"`
	Audience string        `yaml:"audience"`
	JWKSURL  string        `yaml:"jwks_url"`
	CacheTTL time.Duration `yaml:"cache_ttl"`
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
	Backends map[string]BackendConfig `yaml:"backends"`
	Topology TopologyConfig           `yaml:"topology"`
}

type BackendConfig struct {
	Type  string      `yaml:"type"` // "mongo"
	Mongo MongoConfig `yaml:"mongo"`
}

type TopologyConfig struct {
	Document   DocumentTopology   `yaml:"document"`
	User       CollectionTopology `yaml:"user"`
	Revocation CollectionTopology `yaml:"revocation"`
}

type BaseTopology struct {
	Strategy string `yaml:"strategy"` // "single", "read_write_split"
	Primary  string `yaml:"primary"`
	Replica  string `yaml:"replica"`
}

type DocumentTopology struct {
	BaseTopology        `yaml:",inline"`
	DataCollection      string        `yaml:"data_collection"`
	SysCollection       string        `yaml:"sys_collection"`
	SoftDeleteRetention time.Duration `yaml:"soft_delete_retention"`
}

type CollectionTopology struct {
	BaseTopology `yaml:",inline"`
	Collection   string `yaml:"collection"`
}

type MongoConfig struct {
	URI          string `yaml:"uri"`
	DatabaseName string `yaml:"database_name"`
}

type AuthConfig struct {
	AccessTokenTTL  time.Duration `yaml:"access_token_ttl"`
	RefreshTokenTTL time.Duration `yaml:"refresh_token_ttl"`
	AuthCodeTTL     time.Duration `yaml:"auth_code_ttl"`
	RulesFile       string        `yaml:"rules_file"`
	PrivateKeyFile  string        `yaml:"private_key_file"`
}

// LoadConfig loads configuration from files and environment variables
// Order: defaults -> config.yml -> config.local.yml -> env vars
func LoadConfig() *Config {
	// 1. Defaults
	cfg := &Config{
		Storage: StorageConfig{
			Backends: map[string]BackendConfig{
				"default_mongo": {
					Type: "mongo",
					Mongo: MongoConfig{
						URI:          "mongodb://localhost:27017",
						DatabaseName: "syntrix",
					},
				},
			},
			Topology: TopologyConfig{
				Document: DocumentTopology{
					BaseTopology: BaseTopology{
						Strategy: "single",
						Primary:  "default_mongo",
					},
					DataCollection:      "documents",
					SysCollection:       "sys",
					SoftDeleteRetention: 5 * time.Minute,
				},
				User: CollectionTopology{
					BaseTopology: BaseTopology{
						Strategy: "single",
						Primary:  "default_mongo",
					},
					Collection: "users",
				},
				Revocation: CollectionTopology{
					BaseTopology: BaseTopology{
						Strategy: "single",
						Primary:  "default_mongo",
					},
					Collection: "revocations",
				},
			},
		},
		Auth: AuthConfig{
			AccessTokenTTL:  15 * time.Minute,
			RefreshTokenTTL: 7 * 24 * time.Hour,
			AuthCodeTTL:     2 * time.Minute,
			RulesFile:       "security.yaml",
			PrivateKeyFile:  "keys/auth_private.pem",
		},
		Gateway: GatewayConfig{
			Port:            8080,
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
	loadFile("config/config.yml", cfg)

	// 3. Load config.local.yml
	loadFile("config/config.local.yml", cfg)

	// 4. Override with Env Vars
	if val := os.Getenv("GATEWAY_PORT"); val != "" {
		if port, err := strconv.Atoi(val); err == nil {
			cfg.Gateway.Port = port
		}
	}
	if val := os.Getenv("GATEWAY_QUERY_SERVICE_URL"); val != "" {
		cfg.Gateway.QueryServiceURL = val
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
		if backend, ok := cfg.Storage.Backends["default_mongo"]; ok {
			backend.Mongo.URI = val
			cfg.Storage.Backends["default_mongo"] = backend
		}
	}
	if val := os.Getenv("DB_NAME"); val != "" {
		if backend, ok := cfg.Storage.Backends["default_mongo"]; ok {
			backend.Mongo.DatabaseName = val
			cfg.Storage.Backends["default_mongo"] = backend
		}
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

	// 5. Resolve paths relative to config directory
	cfg.resolvePaths()

	return cfg
}

func (c *Config) resolvePaths() {
	configDir := "config"
	c.Auth.RulesFile = resolvePath(configDir, c.Auth.RulesFile)
	c.Auth.PrivateKeyFile = resolvePath(configDir, c.Auth.PrivateKeyFile)
	c.Trigger.RulesFile = resolvePath(configDir, c.Trigger.RulesFile)
}

func resolvePath(base, path string) string {
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(base, path)
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
