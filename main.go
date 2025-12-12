package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"trano/internal/config"
	db "trano/internal/db/sqlc"
	"trano/internal/poller"
	"trano/internal/schedular"

	_ "github.com/mattn/go-sqlite3"
)

const (
	schedulerInterval = 24 * time.Hour // Run scheduler once per day
)

func main() {
	logger := log.New(os.Stdout, "[trano] ", log.LstdFlags|log.Lshortfile)

	// Setup context with signal handling
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger.Println("starting application")

	// Load configuration
	cfg := config.Load()
	logger.Printf("configuration loaded | db_path: %s | timezone: %s", cfg.Database.Path, cfg.Timezone)

	// Initialize database
	dbConn, err := initDatabase(cfg.Database, logger)
	if err != nil {
		logger.Fatalf("failed to initialize database: %v", err)
	}
	defer func() {
		if err := dbConn.Close(); err != nil {
			logger.Printf("error closing database: %v", err)
		}
	}()

	// Configure database connection pool
	configureConnectionPool(dbConn, cfg.Database, logger)

	// Create queries instance
	queries := db.New(dbConn)

	// Setup timezone
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		logger.Fatalf("failed to load timezone: %v", err)
	}

	// Configure poller from config
	pollerCfg := poller.Config{
		Concurrency:    cfg.Poller.Concurrency,
		Window:         cfg.Poller.Window,
		ProxyURL:       cfg.Poller.ProxyURL,
		ErrorThreshold: cfg.Poller.ErrorThreshold,
	}

	// Start poller
	logger.Println("starting poller")
	go poller.Start(ctx, queries, dbConn, logger, pollerCfg, loc)

	// Start scheduler ticker
	logger.Println("starting scheduler")
	go runSchedulerTicker(ctx, queries, logger, loc)

	// Wait for shutdown signal
	<-ctx.Done()
	logger.Println("shutdown signal received, cleaning up...")

	// Give goroutines time to finish gracefully
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	<-shutdownCtx.Done()
	logger.Println("application stopped")
}

// initDatabase initializes the SQLite database with proper configuration
func initDatabase(dbCfg config.DatabaseConfig, logger *log.Logger) (*sql.DB, error) {
	// Ensure data directory exists
	dataDir := filepath.Dir(dbCfg.Path)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	// Open database with SQLite-specific parameters
	// WAL mode enables better concurrency
	// foreign_keys enforces referential integrity
	dsn := fmt.Sprintf("file:%s?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_cache_size=2000", dbCfg.Path)

	dbConn, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Verify connection
	if err := dbConn.Ping(); err != nil {
		dbConn.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	logger.Printf("database opened: %s", dbCfg.Path)

	// Apply migrations
	if err := applyMigrations(dbConn, logger); err != nil {
		dbConn.Close()
		return nil, fmt.Errorf("failed to apply migrations: %w", err)
	}

	// Verify WAL mode is enabled
	var journalMode string
	if err := dbConn.QueryRow("PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		logger.Printf("warning: failed to check journal mode: %v", err)
	} else {
		logger.Printf("journal mode: %s", journalMode)
	}

	return dbConn, nil
}

// configureConnectionPool sets optimal connection pool parameters
func configureConnectionPool(dbConn *sql.DB, dbCfg config.DatabaseConfig, logger *log.Logger) {
	dbConn.SetMaxOpenConns(dbCfg.MaxOpenConnections)
	dbConn.SetMaxIdleConns(dbCfg.MaxIdleConnections)
	dbConn.SetConnMaxLifetime(dbCfg.ConnectionMaxLifetime)
	dbConn.SetConnMaxIdleTime(dbCfg.ConnectionMaxIdleTime)

	logger.Printf("connection pool configured | max_open: %d | max_idle: %d | max_lifetime: %v | max_idle_time: %v",
		dbCfg.MaxOpenConnections, dbCfg.MaxIdleConnections, dbCfg.ConnectionMaxLifetime, dbCfg.ConnectionMaxIdleTime)
}

// applyMigrations reads and executes the schema.sql file
func applyMigrations(dbConn *sql.DB, logger *log.Logger) error {
	schemaPath := "./internal/db/schema.sql"

	// Read schema file
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("failed to read schema file: %w", err)
	}

	// Execute schema
	if _, err := dbConn.Exec(string(schema)); err != nil {
		return fmt.Errorf("failed to execute schema: %w", err)
	}

	logger.Println("migrations applied successfully")
	return nil
}

// runSchedulerTicker runs the scheduler at regular intervals
func runSchedulerTicker(ctx context.Context, queries *db.Queries, logger *log.Logger, loc *time.Location) {
	// Run immediately on startup
	startTime := time.Now().In(loc)
	logger.Printf("running initial schedule generation for %s", startTime.Format(time.DateOnly))
	schedular.GenerateRunsForDate(ctx, queries, logger, startTime)

	// Calculate time until next 8PM local time
	nextRun := time.Date(startTime.Year(), startTime.Month(), startTime.Day(), 20, 0, 0, 0, loc)
	if startTime.After(nextRun) {
		nextRun = nextRun.Add(24 * time.Hour)
	}
	delay := time.Until(nextRun)

	logger.Printf("next scheduler run at %s (in %v)", nextRun.Format(time.RFC3339), delay)

	// Wait until next scheduled run
	select {
	case <-time.After(delay):
	case <-ctx.Done():
		logger.Println("scheduler ticker shutting down")
		return
	}

	// Create ticker for daily runs at the same time
	ticker := time.NewTicker(schedulerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Println("scheduler ticker shutting down")
			return
		case tick := <-ticker.C:
			runDate := tick.In(loc)
			logger.Printf("running scheduled generation for %s", runDate.Format(time.DateOnly))
			schedular.GenerateRunsForDate(ctx, queries, logger, runDate)
		}
	}
}
