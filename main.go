package main

import (
	"bufio"
	"context"
	"database/sql"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
	"trano/internal/api"
	"trano/internal/config"
	dbutil "trano/internal/db"
	db "trano/internal/db/sqlc"
	"trano/internal/iri"
	"trano/internal/poller"

	_ "github.com/mattn/go-sqlite3"
)

const (
	schedulerInterval = 24 * time.Hour
	syncInterval      = 7 * 24 * time.Hour
)

func main() {
	logger := log.New(os.Stdout, "[trano] ", log.LstdFlags|log.Lshortfile)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg := config.Load()
	logger.Printf("configuration loaded | db_path: %s | timezone: %s", cfg.Database.Path, cfg.Timezone)

	dbConn, err := dbutil.OpenDatabase(cfg.Database, dbutil.DefaultDatabaseOptions(), logger)
	if err != nil {
		logger.Fatalf("failed to initialize database: %v", err)
	}
	defer func() {
		if err := dbConn.Close(); err != nil {
			logger.Printf("error closing database: %v", err)
		}
	}()

	queries := db.New(dbConn)

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		logger.Fatalf("failed to load timezone: %v", err)
	}

	pollerCfg := poller.Config{
		Concurrency:          cfg.Poller.Concurrency,
		Window:               cfg.Poller.Window,
		ProxyURL:             cfg.Poller.ProxyURL,
		StaticErrorThreshold: cfg.Poller.StaticErrorThreshold,
		TotalErrorThreshold:  cfg.Poller.TotalErrorThreshold,
	}

	// urls := loadTrainURLs(false)
	// client := iri.NewClient(rate.NewLimiter(rate.Every(10*time.Second), 15), nil)

	// logger.Printf("running initial sync with %d trains", len(urls))
	// if err := client.ExecuteSyncCycle(ctx, dbConn, logger, int(cfg.Syncer.Concurrency), urls); err != nil {
	// 	logger.Fatalf("initial sync failed: %v", err)
	// }
	// logger.Println("initial sync completed")

	// startTime := time.Now().In(loc)
	// logger.Printf("running initial schedule generation for %s", startTime.Format(time.DateOnly))
	// queries.GenerateRunsForDate(ctx, db.GenerateRunsForDateParams{
	// 	RunDate: startTime.Format(time.DateOnly),
	// 	Weekday: int(startTime.Weekday()),
	// })

	// logger.Println("starting sync manager")
	// go runSyncManager(ctx, dbConn, logger, cfg, urls, client)

	// logger.Println("starting scheduler")
	// go runSchedulerTicker(ctx, queries, logger, loc)

	logger.Println("starting api server")
	var apiSrv *api.Server
	var apiSrvMu sync.Mutex

	startAPIServer := func() {
		go func() {
			// If existing server is present, shut it down
			apiSrvMu.Lock()
			old := apiSrv
			apiSrvMu.Unlock()

			if old != nil {
				logger.Println("api: shutting down existing server for restart")
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
				defer shutdownCancel()
				if err := old.Shutdown(shutdownCtx); err != nil {
					logger.Printf("api: error shutting down existing server: %v", err)
				} else {
					logger.Println("api: existing server shut down")
				}
			}

			srv, err := api.NewServer(cfg.Server, cfg.Database, pollerCfg, logger)
			if err != nil {
				logger.Printf("api: failed to initialize server: %v", err)
				return
			}

			apiSrvMu.Lock()
			apiSrv = srv
			apiSrvMu.Unlock()

			go func(s *api.Server) {
				if err := s.Start(); err != nil {
					logger.Printf("api server failed: %v", err)
				}
			}(srv)
		}()
	}

	// initial start
	startAPIServer()

	// SIGHUP handling: restart the API server without affecting other components.
	sighupCh := make(chan os.Signal, 1)
	signal.Notify(sighupCh, syscall.SIGHUP)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-sighupCh:
				logger.Println("SIGHUP received: restarting api server")
				startAPIServer()
			}
		}
	}()

	logger.Println("starting poller")
	go poller.Start(ctx, queries, dbConn, logger, pollerCfg, loc)

	<-ctx.Done()
	logger.Println("shutdown signal received, cleaning up...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer shutdownCancel()

	// gracefully stop the API server if it's running
	apiSrvMu.Lock()
	srvToShutdown := apiSrv
	apiSrvMu.Unlock()
	if srvToShutdown != nil {
		if err := srvToShutdown.Shutdown(shutdownCtx); err != nil {
			logger.Printf("error shutting down api server: %v", err)
		} else {
			logger.Println("api server shut down")
		}
	}

	// wait for remaining background work or timeout
	<-shutdownCtx.Done()
	logger.Println("application stopped")
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
			queries.GenerateRunsForDate(ctx, db.GenerateRunsForDateParams{
				RunDate: runDate.String(),
				Weekday: int(runDate.Weekday()),
			})
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
