package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"trano/internal/config"

	"github.com/mattn/go-sqlite3"
)

const (
	driverName = "sqlite3_spatialite"
	schemaPath = "./schema.sql"
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

func init() {
	sql.Register(driverName,
		&sqlite3.SQLiteDriver{
			ConnectHook: func(conn *sqlite3.SQLiteConn) error {
				if err := loadSpatialite(conn); err != nil {
					return fmt.Errorf("spatialite initialization failed: %w", err)
				}
				return nil
			},
		})
}

func loadSpatialite(conn *sqlite3.SQLiteConn) error {
	if _, err := conn.Exec("SELECT load_extension('mod_spatialite')", nil); err != nil {
		return fmt.Errorf("load_extension failed: %w (ensure libsqlite3-mod-spatialite is installed)", err)
	}

	if _, err := conn.Exec("SELECT InitSpatialMetaData(1)", nil); err != nil {
		return fmt.Errorf("InitSpatialMetaData failed: %w", err)
	}

	return nil
}

func buildDSN(dbPath string, opts DatabaseOptions) string {
	return fmt.Sprintf(
		"file:%s?_foreign_keys=%v&_journal_mode=%s&_busy_timeout=%d&_synchronous=%s&_cache_size=%d&_extensions=1",
		dbPath,
		opts.ForeignKeysEnabled,
		opts.JournalMode,
		opts.BusyTimeout,
		opts.Synchronous,
		opts.CacheSize,
	)
}

func OpenDatabase(dbCfg config.DatabaseConfig, opts DatabaseOptions, logger *log.Logger) (*sql.DB, error) {
	if err := ensureDataDirectory(dbCfg.Path); err != nil {
		return nil, err
	}

	dsn := buildDSN(dbCfg.Path, opts)
	dbConn, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
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

	if err := verifyJournalMode(dbConn, logger); err != nil {
		logger.Printf("warning: %v", err)
	}

	configureConnectionPool(dbConn, dbCfg, logger)
	return dbConn, nil
}

func ensureDataDirectory(dbPath string) error {
	dataDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}
	return nil
}

func configureConnectionPool(dbConn *sql.DB, dbCfg config.DatabaseConfig, logger *log.Logger) {
	dbConn.SetMaxOpenConns(dbCfg.MaxOpenConnections)
	dbConn.SetMaxIdleConns(dbCfg.MaxIdleConnections)
	dbConn.SetConnMaxLifetime(dbCfg.ConnectionMaxLifetime)
	dbConn.SetConnMaxIdleTime(dbCfg.ConnectionMaxIdleTime)

	logger.Printf("connection pool configured | max_open: %d | max_idle: %d | max_lifetime: %v | max_idle_time: %v",
		dbCfg.MaxOpenConnections,
		dbCfg.MaxIdleConnections,
		dbCfg.ConnectionMaxLifetime,
		dbCfg.ConnectionMaxIdleTime)
}

func applyMigrations(dbConn *sql.DB, logger *log.Logger) error {
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

func verifyJournalMode(dbConn *sql.DB, logger *log.Logger) error {
	var journalMode string
	if err := dbConn.QueryRow("PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		return fmt.Errorf("failed to check journal mode: %w", err)
	}

	logger.Printf("journal mode: %s", journalMode)
	return nil
}
