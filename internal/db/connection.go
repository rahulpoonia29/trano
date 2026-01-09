package db

import (
	"database/sql"
	"database/sql/driver"
	"embed"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"

	"trano/internal/config"

	"github.com/mattn/go-sqlite3"
)

const (
	driverName = "sqlite3_spatialite"
)

//go:embed schema/*.sql
var migrationFiles embed.FS

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
				if err := conn.LoadExtension("mod_spatialite", "sqlite3_modspatialite_init"); err != nil {
					return fmt.Errorf("failed to load spatialite: %w (ensure libsqlite3-mod-spatialite is installed)", err)
				}

				// Check if spatial_ref_sys table already exists
				rows, err := conn.Query("SELECT 1 FROM sqlite_master WHERE type='table' AND name='spatial_ref_sys' LIMIT 1", nil)
				if err != nil {
					return fmt.Errorf("failed to check spatial_ref_sys existence: %w", err)
				}

				dest := make([]driver.Value, 1)
				err = rows.Next(dest)
				rows.Close()

				if err == io.EOF {
					// No rows found, table doesn't exist - initialize spatial metadata
					if _, err := conn.Exec("SELECT InitSpatialMetaData(1)", nil); err != nil {
						return fmt.Errorf("InitSpatialMetaData failed: %w", err)
					}
				} else if err != nil {
					return fmt.Errorf("failed to check for spatial_ref_sys: %w", err)
				}

				return nil
			},
		})
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
	entries, err := migrationFiles.ReadDir("schema")
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filePath := path.Join("schema", entry.Name())
		logger.Printf("applying migration: %s", filePath)

		schema, err := migrationFiles.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", filePath, err)
		}

		if _, err := dbConn.Exec(string(schema)); err != nil {
			return fmt.Errorf("failed to execute migration %s: %w", filePath, err)
		}
	}

	logger.Println("all migrations applied successfully")
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
