package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"trano/internal/config"

	_ "github.com/mattn/go-sqlite3"
)

type DatabaseOptions struct {
	ForeignKeysEnabled bool
	JournalMode        string
	BusyTimeout        int
	Synchronous        string
	CacheSize          int
}

func DefaultDatabaseOptions() DatabaseOptions {
	return DatabaseOptions{
		ForeignKeysEnabled: true,
		JournalMode:        "WAL", // for concurrency
		BusyTimeout:        5000,
		Synchronous:        "NORMAL", // idk seems good
		CacheSize:          20000,    // 20MB cache
	}
}

func buildDSN(dbPath string, opts DatabaseOptions) string {
	return fmt.Sprintf(
		"file:%s?_foreign_keys=%v&_journal_mode=%s&_busy_timeout=%d&_synchronous=%s&_cache_size=%d",
		dbPath,
		opts.ForeignKeysEnabled,
		opts.JournalMode,
		opts.BusyTimeout,
		opts.Synchronous,
		opts.CacheSize,
	)
}

func OpenDatabase(dbCfg config.DatabaseConfig, opts DatabaseOptions, logger *log.Logger) (*sql.DB, error) {
	dataDir := filepath.Dir(dbCfg.Path)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	dsn := buildDSN(dbCfg.Path, opts)
	dbConn, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Attempt to load SpatiaLite extension, return error on fail
	// the caller may choose to treat that as fatal or continue based on their needs
	if _, err = dbConn.Exec("SELECT load_extension('mod_spatialite')"); err != nil {
		logger.Printf("failed to load spatialite: %v. Ensure libsqlite3-mod-spatialite is installed.", err)
		_ = dbConn.Close()
		return nil, err
	}

	// Init Spatial Metadata if it hasn't been created already
	if _, err = dbConn.Exec("SELECT InitSpatialMetaData(1);"); err != nil {
		logger.Printf("InitSpatialMetaData failed: %v", err)
	}

	if err := dbConn.Ping(); err != nil {
		_ = dbConn.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}
	logger.Printf("database opened: %s", dbCfg.Path)

	if err := applyMigrations(dbConn, logger); err != nil {
		_ = dbConn.Close()
		return nil, fmt.Errorf("failed to apply migrations: %w", err)
	}

	if jm, err := checkJournalMode(dbConn); err != nil {
		logger.Printf("warning: failed to check journal mode: %v", err)
	} else {
		logger.Printf("journal mode: %s", jm)
	}

	configureConnectionPool(dbConn, dbCfg, logger)

	return dbConn, nil
}

func configureConnectionPool(dbConn *sql.DB, dbCfg config.DatabaseConfig, logger *log.Logger) {
	dbConn.SetMaxOpenConns(dbCfg.MaxOpenConnections)
	dbConn.SetMaxIdleConns(dbCfg.MaxIdleConnections)
	dbConn.SetConnMaxLifetime(dbCfg.ConnectionMaxLifetime)
	dbConn.SetConnMaxIdleTime(dbCfg.ConnectionMaxIdleTime)

	logger.Printf("connection pool configured | max_open: %d | max_idle: %d | max_lifetime: %v | max_idle_time: %v",
		dbCfg.MaxOpenConnections, dbCfg.MaxIdleConnections, dbCfg.ConnectionMaxLifetime, dbCfg.ConnectionMaxIdleTime)
}

func applyMigrations(dbConn *sql.DB, logger *log.Logger) error {
	schemaPath := "./schema.sql"
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("failed to read schema file: %w", err)
	}
	if _, err := dbConn.Exec(string(schema)); err != nil {
		return fmt.Errorf("failed to execute schema: %w", err)
	}
	logger.Println("migrations applied successfully")
	return nil
}

func checkJournalMode(dbConn *sql.DB) (string, error) {
	var journalMode string
	if err := dbConn.QueryRow("PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		return "", err
	}
	return journalMode, nil
}
