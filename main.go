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

	"golang.org/x/time/rate"
)

const (
	schedulerInterval = 24 * time.Hour
	schedulerRunTime  = 20 // 8PM
	syncInterval      = 7 * 24 * time.Hour
	iriRateLimit      = 10 * time.Second
	iriBurst          = 15
)

type App struct {
	cfg       *config.Config
	logger    *log.Logger
	dbConn    *sql.DB
	queries   *db.Queries
	loc       *time.Location
	pollerCfg poller.Config

	apiManager *apiServerManager
	wg         sync.WaitGroup
}

func main() {
	logger := log.New(os.Stdout, "[trano] ", log.LstdFlags|log.Lshortfile)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	app, err := initializeApp(logger)
	if err != nil {
		logger.Fatalf("failed to initialize application: %v", err)
	}
	defer app.cleanup()

	if err := app.runInitialSetup(ctx); err != nil {
		logger.Fatalf("initial setup failed: %v", err)
	}

	app.startAllServices(ctx)

	<-ctx.Done()
	app.shutdown()
}

func initializeApp(logger *log.Logger) (*App, error) {
	cfg := config.Load()
	logger.Printf("configuration loaded | db_path: %s | timezone: %s", cfg.Database.Path, cfg.Timezone)

	dbConn, err := dbutil.OpenDatabase(cfg.Database, dbutil.DefaultDatabaseOptions(), logger)
	if err != nil {
		return nil, err
	}

	queries := db.New(dbConn)

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		_ = dbConn.Close()
		return nil, err
	}

	pollerCfg := poller.Config{
		Concurrency:          cfg.Poller.Concurrency,
		Window:               cfg.Poller.Window,
		ProxyURL:             cfg.Poller.ProxyURL,
		StaticErrorThreshold: cfg.Poller.StaticErrorThreshold,
		TotalErrorThreshold:  cfg.Poller.TotalErrorThreshold,
	}

	return &App{
		cfg:       cfg,
		logger:    logger,
		dbConn:    dbConn,
		queries:   queries,
		loc:       loc,
		pollerCfg: pollerCfg,
	}, nil
}

func (app *App) cleanup() {
	if err := app.dbConn.Close(); err != nil {
		app.logger.Printf("error closing database: %v", err)
	}
}

func (app *App) runInitialSetup(ctx context.Context) error {
	urls := loadTrainURLs(false)
	if len(urls) == 0 {
		app.logger.Println("warning: no train URLs loaded, skipping initial sync")
		return nil
	}

	client := iri.NewClient(
		rate.NewLimiter(rate.Every(iriRateLimit), iriBurst),
		nil,
	)

	app.logger.Printf("running initial sync with %d trains", len(urls))
	if err := client.ExecuteSyncCycle(ctx, app.dbConn, app.logger, int(app.cfg.Syncer.Concurrency), urls); err != nil {
		return err
	}
	app.logger.Println("initial sync completed")

	startTime := time.Now().In(app.loc)
	app.logger.Printf("running initial schedule generation for %s", startTime.Format(time.DateOnly))
	if err := app.queries.GenerateRunsForDate(ctx, db.GenerateRunsForDateParams{
		RunDate: startTime.Format(time.DateOnly),
		Weekday: int64(startTime.Weekday()),
	}); err != nil {
		app.logger.Printf("warning: initial schedule generation failed: %v", err)
	}

	return nil
}

func (app *App) startAllServices(ctx context.Context) {
	app.startScheduler(ctx)
	app.startIRISyncManager(ctx)
	app.startPoller(ctx)
	app.startAPIServer(ctx)
}

func (app *App) startScheduler(ctx context.Context) {
	app.wg.Add(1)
	go func() {
		defer app.wg.Done()
		app.logger.Println("starting scheduler")
		runScheduler(ctx, app.queries, app.logger, app.loc)
		app.logger.Println("scheduler stopped")
	}()
}

func (app *App) startIRISyncManager(ctx context.Context) {
	urls := loadTrainURLs(false)
	if len(urls) == 0 {
		app.logger.Println("warning: no train URLs loaded, IRI sync manager will not start")
		return
	}

	client := iri.NewClient(
		rate.NewLimiter(rate.Every(iriRateLimit), iriBurst),
		nil,
	)

	app.wg.Add(1)
	go func() {
		defer app.wg.Done()
		app.logger.Println("starting IRI sync manager")
		runIRISyncManager(ctx, app.dbConn, app.logger, app.cfg, urls, client)
		app.logger.Println("IRI sync manager stopped")
	}()
}

func (app *App) startPoller(ctx context.Context) {
	app.wg.Add(1)
	go func() {
		defer app.wg.Done()
		app.logger.Println("starting poller")
		poller.Start(ctx, app.queries, app.dbConn, app.logger, app.pollerCfg, app.loc)
		app.logger.Println("poller stopped")
	}()
}

func (app *App) startAPIServer(ctx context.Context) {
	app.apiManager = newAPIServerManager(app.cfg, app.pollerCfg, app.logger)
	app.apiManager.start()

	app.wg.Add(1)
	go func() {
		defer app.wg.Done()
		app.handleSIGHUP(ctx)
	}()
}

func (app *App) handleSIGHUP(ctx context.Context) {
	sighupCh := make(chan os.Signal, 1)
	signal.Notify(sighupCh, syscall.SIGHUP)
	defer signal.Stop(sighupCh)

	for {
		select {
		case <-ctx.Done():
			return
		case <-sighupCh:
			app.logger.Println("SIGHUP received: restarting API server")
			app.apiManager.restart()
		}
	}
}

func (app *App) shutdown() {
	app.logger.Println("shutdown signal received, cleaning up...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), app.cfg.Server.ShutdownTimeout)
	defer cancel()

	if app.apiManager != nil {
		if err := app.apiManager.shutdown(shutdownCtx); err != nil {
			app.logger.Printf("error shutting down API server: %v", err)
		} else {
			app.logger.Println("API server shut down")
		}
	}

	done := make(chan struct{})
	go func() {
		app.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		app.logger.Println("all services stopped gracefully")
	case <-shutdownCtx.Done():
		app.logger.Println("shutdown timeout reached")
	}

	app.logger.Println("application stopped")
}

// API Server Manager
type apiServerManager struct {
	cfg       *config.Config
	pollerCfg poller.Config
	logger    *log.Logger
	mu        sync.Mutex
	srv       *api.Server
}

func newAPIServerManager(cfg *config.Config, pollerCfg poller.Config, logger *log.Logger) *apiServerManager {
	return &apiServerManager{
		cfg:       cfg,
		pollerCfg: pollerCfg,
		logger:    logger,
	}
}

func (m *apiServerManager) start() {
	go func() {
		m.mu.Lock()
		old := m.srv
		m.mu.Unlock()

		if old != nil {
			m.shutdownExisting(old)
		}

		srv, err := api.NewServer(m.cfg.Server, m.cfg.Database, m.pollerCfg, m.logger)
		if err != nil {
			m.logger.Printf("api: failed to initialize server: %v", err)
			return
		}

		m.mu.Lock()
		m.srv = srv
		m.mu.Unlock()

		m.logger.Println("api server started")
		if err := srv.Start(); err != nil {
			m.logger.Printf("api server failed: %v", err)
		}
	}()
}

func (m *apiServerManager) restart() {
	m.start()
}

func (m *apiServerManager) shutdownExisting(srv *api.Server) {
	m.logger.Println("api: shutting down existing server for restart")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), m.cfg.Server.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		m.logger.Printf("api: error shutting down existing server: %v", err)
	} else {
		m.logger.Println("api: existing server shut down")
	}
}

func (m *apiServerManager) shutdown(ctx context.Context) error {
	m.mu.Lock()
	srv := m.srv
	m.mu.Unlock()

	if srv != nil {
		return srv.Shutdown(ctx)
	}
	return nil
}

// Scheduler
func runScheduler(ctx context.Context, queries *db.Queries, logger *log.Logger, loc *time.Location) {
	nextRun := calculateNextRunTime(loc, schedulerRunTime)
	delay := time.Until(nextRun)
	logger.Printf("scheduler: next run at %s (in %v)", nextRun.Format(time.RFC3339), delay)

	select {
	case <-time.After(delay):
		runScheduleGeneration(ctx, queries, logger, time.Now().In(loc))
	case <-ctx.Done():
		return
	}

	ticker := time.NewTicker(schedulerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case tick := <-ticker.C:
			runScheduleGeneration(ctx, queries, logger, tick.In(loc))
		}
	}
}

func runScheduleGeneration(ctx context.Context, queries *db.Queries, logger *log.Logger, runTime time.Time) {
	runDate := runTime.Format(time.DateOnly)
	logger.Printf("scheduler: generating runs for %s", runDate)

	err := queries.GenerateRunsForDate(ctx, db.GenerateRunsForDateParams{
		RunDate: runDate,
		Weekday: int64(runTime.Weekday()),
	})

	if err != nil {
		logger.Printf("scheduler: generation failed: %v", err)
		return
	}

	logger.Printf("scheduler: generation completed for %s", runDate)
}

func calculateNextRunTime(loc *time.Location, hour int) time.Time {
	now := time.Now().In(loc)
	nextRun := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, loc)
	if now.After(nextRun) {
		nextRun = nextRun.Add(24 * time.Hour)
	}
	return nextRun
}

// IRI Sync Manager
func runIRISyncManager(ctx context.Context, dbConn *sql.DB, logger *log.Logger, cfg *config.Config, urls []string, client *iri.Client) {
	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runIRISync(ctx, dbConn, logger, cfg, urls, client)
		}
	}
}

func runIRISync(ctx context.Context, dbConn *sql.DB, logger *log.Logger, cfg *config.Config, urls []string, client *iri.Client) {
	logger.Printf("iri_sync: starting sync with %d trains", len(urls))

	if err := client.ExecuteSyncCycle(ctx, dbConn, logger, int(cfg.Syncer.Concurrency), urls); err != nil {
		logger.Printf("iri_sync: sync failed: %v", err)
		return
	}

	logger.Println("iri_sync: sync completed successfully")
}

// Train URLs Loader
func loadTrainURLs(isTest bool) []string {
	if isTest {
		return []string{"https://indiarailinfo.com/train/7539"}
	}

	file, err := os.Open("./data/train_urls.csv")
	if err != nil {
		log.Printf("failed to open train_urls.csv: %v", err)
		return nil
	}
	defer file.Close()

	var urls []string
	scanner := bufio.NewScanner(file)

	if scanner.Scan() {
		// Skip header
	}

	for scanner.Scan() {
		line := scanner.Text()
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
