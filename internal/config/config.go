package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds all application configuration
type Config struct {
	Database DatabaseConfig
	Poller   PollerConfig
	Timezone string
}

// DatabaseConfig holds database-specific configuration
type DatabaseConfig struct {
	Path                  string
	MaxOpenConnections    int
	MaxIdleConnections    int
	ConnectionMaxLifetime time.Duration
	ConnectionMaxIdleTime time.Duration
}

// PollerConfig holds poller-specific configuration
type PollerConfig struct {
	Concurrency    int16
	Window         time.Duration
	ProxyURL       string
	ErrorThreshold int16
}

// Load reads configuration from environment variables with sensible defaults
func Load() *Config {
	return &Config{
		Database: DatabaseConfig{
			Path:                  getEnv("DB_PATH", "./data/trano.db"),
			MaxOpenConnections:    getEnvAsInt("DB_MAX_OPEN_CONNS", 25),
			MaxIdleConnections:    getEnvAsInt("DB_MAX_IDLE_CONNS", 5),
			ConnectionMaxLifetime: getEnvAsDuration("DB_CONN_MAX_LIFETIME", 5*time.Minute),
			ConnectionMaxIdleTime: getEnvAsDuration("DB_CONN_MAX_IDLE_TIME", 1*time.Minute),
		},
		Poller: PollerConfig{
			Concurrency:    int16(getEnvAsInt("POLLER_CONCURRENCY", 10)),
			Window:         getEnvAsDuration("POLLER_WINDOW", 2*time.Minute),
			ProxyURL:       getEnv("PROXY_URL", ""),
			ErrorThreshold: int16(getEnvAsInt("POLLER_ERROR_THRESHOLD", 3)),
		},
		Timezone: getEnv("TIMEZONE", "Asia/Kolkata"),
	}
}

// getEnv retrieves an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvAsInt retrieves an environment variable as an integer or returns a default value
func getEnvAsInt(key string, defaultValue int) int {
	if valueStr := os.Getenv(key); valueStr != "" {
		if value, err := strconv.Atoi(valueStr); err == nil {
			return value
		}
	}
	return defaultValue
}

// getEnvAsDuration retrieves an environment variable as a duration or returns a default value
func getEnvAsDuration(key string, defaultValue time.Duration) time.Duration {
	if valueStr := os.Getenv(key); valueStr != "" {
		if value, err := time.ParseDuration(valueStr); err == nil {
			return value
		}
	}
	return defaultValue
}
