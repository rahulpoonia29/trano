package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Database DatabaseConfig
	Poller   PollerConfig
	Syncer   SyncerConfig
	Timezone string
}

type DatabaseConfig struct {
	Path                  string
	MaxOpenConnections    int
	MaxIdleConnections    int
	ConnectionMaxLifetime time.Duration
	ConnectionMaxIdleTime time.Duration
}

type PollerConfig struct {
	Concurrency          int16
	Window               time.Duration
	ProxyURL             string
	StaticErrorThreshold int8
	TotalErrorThreshold  int8
}

type SyncerConfig struct {
	Concurrency int16
}

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
			Concurrency:          int16(getEnvAsInt("POLLER_CONCURRENCY", 50)),
			Window:               getEnvAsDuration("POLLER_WINDOW", 1*time.Minute),
			ProxyURL:             getEnv("PROXY_URL", ""),
			StaticErrorThreshold: int8(getEnvAsInt("POLLER_STATIC_ERROR_THRESHOLD", 10)),
			TotalErrorThreshold:  int8(getEnvAsInt("POLLER_TOTAL_ERROR_THRESHOLD", 5)),
		},
		Syncer: SyncerConfig{
			Concurrency: int16(getEnvAsInt("SYNCER_CONCURRENCY", 2)),
		},
		Timezone: getEnv("TIMEZONE", "Asia/Kolkata"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	if valueStr := os.Getenv(key); valueStr != "" {
		if value, err := strconv.Atoi(valueStr); err == nil {
			return value
		}
	}
	return defaultValue
}

func getEnvAsDuration(key string, defaultValue time.Duration) time.Duration {
	if valueStr := os.Getenv(key); valueStr != "" {
		if value, err := time.ParseDuration(valueStr); err == nil {
			return value
		}
	}
	return defaultValue
}
