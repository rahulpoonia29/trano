package main

import (
	"bufio"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"trano/internal/config"
	db "trano/internal/db/sqlc"
	"trano/internal/iri"
	"trano/internal/schedular"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/time/rate"
)

const (
	schedulerInterval = 24 * time.Hour // Run scheduler once per day
	syncInterval      = 7 * 24 * time.Hour
)

func main() {
	testFlag := flag.Bool("test", false, "Run in test mode with single URL")
	flag.Parse()

	logger := log.New(os.Stdout, "[trano] ", log.LstdFlags|log.Lshortfile)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg := config.Load()
	logger.Printf("configuration loaded | db_path: %s | timezone: %s", cfg.Database.Path, cfg.Timezone)

	dbConn, err := initDatabase(cfg.Database, logger)
	if err != nil {
		logger.Fatalf("failed to initialize database: %v", err)
	}
	defer func() {
		if err := dbConn.Close(); err != nil {
			logger.Printf("error closing database: %v", err)
		}
	}()

	configureConnectionPool(dbConn, cfg.Database, logger)
	// queries := db.New(dbConn)

	// loc, err := time.LoadLocation(cfg.Timezone)
	// if err != nil {
	// 	logger.Fatalf("failed to load timezone: %v", err)
	// }

	// pollerCfg := poller.Config{
	// 	Concurrency:    cfg.Poller.Concurrency,
	// 	Window:         cfg.Poller.Window,
	// 	ProxyURL:       cfg.Poller.ProxyURL,
	// 	ErrorThreshold: cfg.Poller.ErrorThreshold,
	// }

	urls := loadTrainURLs(*testFlag)
	if len(urls) == 0 {
		logger.Println("no train urls configured for sync")
		return
	}

	// Updated client creation to match new NewClient signature
	client := iri.NewClient(rate.NewLimiter(rate.Every(10*time.Second), 15), nil)

	logger.Printf("running initial sync with %d trains", len(urls))
	if err := client.ExecuteSyncCycle(ctx, dbConn, logger, int(cfg.Syncer.Concurrency), urls); err != nil {
		logger.Fatalf("initial sync failed: %v", err)
	}
	logger.Println("initial sync completed")

	// startTime := time.Now().In(loc)
	// logger.Printf("running initial schedule generation for %s", startTime.Format(time.DateOnly))
	// schedular.GenerateRunsForDate(ctx, queries, logger, startTime)

	// logger.Println("starting sync manager")
	// go runSyncManager(ctx, dbConn, logger, cfg, urls, client)

	// logger.Println("starting scheduler")
	// go runSchedulerTicker(ctx, queries, logger, loc)

	// logger.Println("starting poller")
	// go poller.Start(ctx, queries, dbConn, logger, pollerCfg, loc)

	// <-ctx.Done()
	// logger.Println("shutdown signal received, cleaning up...")

	// shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	// defer shutdownCancel()

	// <-shutdownCtx.Done()
	// logger.Println("application stopped")
}

func initDatabase(dbCfg config.DatabaseConfig, logger *log.Logger) (*sql.DB, error) {
	// Ensure data directory exists
	dataDir := filepath.Dir(dbCfg.Path)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	// WAL mode enables better concurrency and foreign_keys
	dsn := fmt.Sprintf("file:%s?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_cache_size=2000", dbCfg.Path)

	dbConn, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := dbConn.Ping(); err != nil {
		dbConn.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	logger.Printf("database opened: %s", dbCfg.Path)

	if err := applyMigrations(dbConn, logger); err != nil {
		dbConn.Close()
		return nil, fmt.Errorf("failed to apply migrations: %w", err)
	}

	var journalMode string
	if err := dbConn.QueryRow("PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		logger.Printf("warning: failed to check journal mode: %v", err)
	} else {
		logger.Printf("journal mode: %s", journalMode)
	}

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

// applyMigrations reads and executes the schema.sql file
func applyMigrations(dbConn *sql.DB, logger *log.Logger) error {
	schemaPath := "./internal/db/schema.sql"
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

func runSchedulerTicker(ctx context.Context, queries *db.Queries, logger *log.Logger, loc *time.Location) {
	// Calculate time until next 8PM local time
	startTime := time.Now().In(loc)
	nextRun := time.Date(startTime.Year(), startTime.Month(), startTime.Day(), 20, 0, 0, 0, loc)
	if startTime.After(nextRun) {
		nextRun = nextRun.Add(24 * time.Hour)
	}
	delay := time.Until(nextRun)

	logger.Printf("next scheduler run at %s (in %v)", nextRun.Format(time.RFC3339), delay)

	select {
	case <-time.After(delay):
	case <-ctx.Done():
		logger.Println("scheduler ticker shutting down")
		return
	}

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

func runSyncManager(ctx context.Context, dbConn *sql.DB, logger *log.Logger, cfg *config.Config, urls []string, client *iri.Client) {
	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Println("sync manager shutting down")
			return
		case <-ticker.C:
			logger.Printf("starting scheduled sync with %d trains", len(urls))
			if err := client.ExecuteSyncCycle(ctx, dbConn, logger, int(cfg.Syncer.Concurrency), urls); err != nil {
				logger.Printf("scheduled sync failed: %v", err)
			} else {
				logger.Println("scheduled sync completed")
			}
		}
	}
}

func loadTrainURLs(isTest bool) []string {
	if isTest {
		return []string{
			"https://indiarailinfo.com/train/7539",
		}
	}

	file, err := os.Open("./data/train_urls.csv")
	// file, err := os.Open("./data/hmm.csv")
	if err != nil {
		log.Printf("failed to open train_urls.csv: %v", err)
		return nil
	}
	defer file.Close()

	var urls []string

	scanner := bufio.NewScanner(file)
	// Skip header
	if scanner.Scan() {
	}

	for scanner.Scan() {
		line := scanner.Text()
		// Expecting: train_no,source_url
		fields := strings.SplitN(line, ",", 2)
		if len(fields) != 2 {
			continue
		}
		url := strings.TrimSpace(fields[1])
		if url != "" {
			urls = append(urls, url)
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("error reading train_urls.csv: %v", err)
	}
	return urls
}
