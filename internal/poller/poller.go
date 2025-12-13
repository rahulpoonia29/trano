package poller

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	db "trano/internal/db/sqlc"
	"trano/internal/wimt"
)

type Config struct {
	Concurrency    int16
	Window         time.Duration
	ProxyURL       string
	ErrorThreshold int16
}

type ErrorEntry struct {
	Reason    string `json:"reason"`
	Timestamp string `json:"timestamp"`
}

// Start blocks until ctx is cancelled.
// Calls executeCycle repeatedly and ensures each cycle lasts at least cfg.Window
func Start(ctx context.Context, queries *db.Queries, sqlDB *sql.DB, logger *log.Logger, cfg Config, loc *time.Location) {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	if cfg.Window <= 0 {
		cfg.Window = 2 * time.Minute
	}
	if cfg.ErrorThreshold <= 0 {
		cfg.ErrorThreshold = 3
	}

	api := wimt.NewAPIClient(cfg.ProxyURL)
	logger.Printf("poller started | workers: %d | window: %v | error_threshold: %d", cfg.Concurrency, cfg.Window, cfg.ErrorThreshold)

	for {
		select {
		case <-ctx.Done():
			logger.Println("poller shutting down")
			return
		default:
			start := time.Now()
			count := executeCycle(ctx, queries, sqlDB, api, logger, cfg, loc)
			elapsed := time.Since(start)

			// ensure each cycle is at least cfg.Window
			if elapsed < cfg.Window {
				sleep := cfg.Window - elapsed
				select {
				case <-time.After(sleep):
					logger.Printf("cycle completed | processed: %d | elapsed: %v | sleeping: %v", count, elapsed, sleep)
				case <-ctx.Done():
					logger.Println("poller shutting down")
					return
				}
			} else {
				logger.Printf("cycle completed | processed: %d | elapsed: %v", count, elapsed)
			}
		}
	}
}

func executeCycle(ctx context.Context, queries *db.Queries, sqlDB *sql.DB, api *wimt.APIClient, logger *log.Logger, cfg Config, loc *time.Location) int {
	// fetch runs to poll using local time
	runs, err := queries.ListRunsToPoll(ctx, db.ListRunsToPollParams{
		TargetDate: time.Now().In(loc).Format(time.DateOnly),
		Threshold:  int64(cfg.ErrorThreshold),
	})
	if err != nil {
		logger.Printf("failed to list runs to poll: %v", err)
		return 0
	}
	if len(runs) == 0 {
		return 0
	}

	// rate limit: spread work across the window with minimum inter-request delay
	delay := max(cfg.Window/time.Duration(len(runs)), 20*time.Millisecond)
	delay = delay.Round(time.Millisecond)
	logger.Printf("cycle start | targets: %d | rate_delay: %v", len(runs), delay)

	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.Concurrency)
	ticker := time.NewTicker(delay)
	defer ticker.Stop()

	processed := 0
loop:
	for _, run := range runs {
		select {
		case <-ctx.Done():
			break loop
		case <-ticker.C:
			sem <- struct{}{}
			wg.Add(1)

			go func(r db.ListRunsToPollRow) {
				defer wg.Done()
				defer func() { <-sem }()
				if err := processRun(ctx, r, queries, sqlDB, api, logger); err != nil {
					logger.Printf("processRun error for %s: %v", r.RunID, err)
				}
			}(run)

			processed++
		}
	}

	wg.Wait()
	return processed
}

func processRun(ctx context.Context, run db.ListRunsToPollRow, queries *db.Queries, sqlDB *sql.DB, api *wimt.APIClient, logger *log.Logger) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	runDate, _ := time.Parse(time.DateOnly, run.RunDate)
	trainNoStr := fmt.Sprintf("%05d", run.TrainNo)

	body, err := api.FetchTrainStatus(ctx, trainNoStr, run.SourceStation, run.DestinationStation, runDate)
	if err != nil {
		return fmt.Errorf("API fetch failed: %w", err)
	}

	if len(body) < 150 {
		return handleShortResponse(ctx, queries, run, body, logger)
	}

	bodyStr := string(body)
	if !strings.Contains(bodyStr, "running_status") && !strings.Contains(bodyStr, "running status") {
		return handleStaticResponse(ctx, queries, run, bodyStr, logger)
	}

	var data wimt.APIResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("unmarshal failed: %w", err)
	}

	return processValidResponse(ctx, queries, sqlDB, run, &data, logger)
}

func handleShortResponse(ctx context.Context, queries *db.Queries, run db.ListRunsToPollRow, body []byte, logger *log.Logger) error {
	bodyStr := string(body)

	var endReason string
	var errorMsg string

	switch {
	case strings.Contains(bodyStr, "not running"):
		endReason = "not_running_update_bitmap"
		errorMsg = "not_running_today: " + bodyStr
	case strings.Contains(bodyStr, "update the timetable"):
		endReason = "not_running_update_timetable"
		errorMsg = "timetable_update_needed: " + bodyStr
	default:
		logger.Printf("unexpected short response for %s: %s", run.RunID, bodyStr)
		return nil
	}

	updatedErrors, err := appendToErrorsJSON(run.Errors, errorMsg)
	if err != nil {
		logger.Printf("failed to append error for %s: %v", run.RunID, err)
		return nil
	}

	if err := queries.UpdateRunStatus(ctx, db.UpdateRunStatusParams{
		RunID:         run.RunID,
		HasArrived:    1,
		CurrentStatus: sql.NullString{String: endReason, Valid: true},
		Errors:        updatedErrors,
	}); err != nil {
		return fmt.Errorf("failed to update run status: %w", err)
	}

	return nil
}

func handleStaticResponse(ctx context.Context, queries *db.Queries, run db.ListRunsToPollRow, bodyStr string, logger *log.Logger) error {
	errorMsg := "static_response"

	updatedErrors, err := appendToErrorsJSON(run.Errors, errorMsg)
	if err != nil {
		logger.Printf("failed to append static response error for %s: %v", run.RunID, err)
	}

	if err := queries.UpdateRunStatus(ctx, db.UpdateRunStatusParams{
		RunID:  run.RunID,
		Errors: updatedErrors,
	}); err != nil {
		return fmt.Errorf("failed to update run status for static response: %w", err)
	}

	logger.Printf("logged static response for %s", run.RunID)
	return nil
}

func processValidResponse(ctx context.Context, queries *db.Queries, sqlDB *sql.DB, run db.ListRunsToPollRow, data *wimt.APIResponse, logger *log.Logger) error {
	if data.LastUpdateIsoDate == "" {
		logger.Printf("empty timestamp for %s - skipping", run.RunID)
		return nil
	}

	// Parse API time (must succeed)
	apiTime, err := time.Parse(time.RFC3339, data.LastUpdateIsoDate)
	if err != nil {
		logger.Printf("invalid API timestamp for %s: %v - skipping", run.RunID, err)
		return nil
	}

	// API data is newer than current
	if run.LastUpdateTimestampIso.Valid && run.LastUpdateTimestampIso.String != "" {
		dbTime, err := time.Parse(time.RFC3339, run.LastUpdateTimestampIso.String)
		if err != nil {
			logger.Printf("invalid DB timestamp for %s: %v - proceeding with API data", run.RunID, err)
		} else if !apiTime.After(dbTime) {
			return nil // skip
		}
	}

	if data.Lat == nil || data.Lng == nil {
		logger.Printf("missing coordinates for %s - skipping", run.RunID)
		return nil
	}

	lat, lng := *data.Lat, *data.Lng

	if lat == 0.0 && lng == 0.0 {
		logger.Printf("zero coordinates for %s - skipping", run.RunID)
		return nil
	}
	// India bounding box (6°N to 37°N, 68°E to 97°E)
	if lat < 6.0 || lat > 37.0 || lng < 68.0 || lng > 97.0 {
		logger.Printf("coordinates out of bounds for %s: (%.6f, %.6f)", run.RunID, lat, lng)
		return nil
	}

	status := strings.ToLower(data.RunningStatus)
	if status == "" {
		status = strings.ToLower(data.RunningStatusAlt)
	}

	var endReason string
	var isEnded bool
	var hasArrived int64

	switch status {
	case "end", "completed":
		isEnded = true
		hasArrived = 1
		endReason = "completed"
	case "cancelled":
		isEnded = true
		hasArrived = 1
		endReason = "cancelled"
	case "terminated":
		isEnded = true
		hasArrived = 1
		endReason = "terminated_short"
	default:
		isEnded = false
		hasArrived = 0
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	txQueries := queries.WithTx(tx)

	if err := txQueries.UpdateRunStatus(ctx, db.UpdateRunStatusParams{
		RunID:         run.RunID,
		HasStarted:    1,
		HasArrived:    hasArrived,
		CurrentStatus:     sql.NullString{String: endReason, Valid: isEnded},
		Lat:           sql.NullFloat64{Float64: lat, Valid: true},
		Lng:           sql.NullFloat64{Float64: lng, Valid: true},
		LastUpdateIso: sql.NullString{String: data.LastUpdateIsoDate, Valid: true},
	}); err != nil {
		return fmt.Errorf("failed to update run status: %w", err)
	}

	if err := txQueries.LogRunLocation(ctx, db.LogRunLocationParams{
		RunID:        run.RunID,
		Lat:          lat,
		Lng:          lng,
		TimestampIso: data.LastUpdateIsoDate,
	}); err != nil {
		return fmt.Errorf("failed to log location: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func appendToErrorsJSON(currentErrors json.RawMessage, reason string) (json.RawMessage, error) {
	var errors []ErrorEntry

	if len(currentErrors) > 0 {
		if err := json.Unmarshal(currentErrors, &errors); err != nil {
			// If unmarshal fails, start fresh
			errors = []ErrorEntry{}
		}
	}

	errors = append(errors, ErrorEntry{
		Reason:    reason,
		Timestamp: time.Now().Format(time.RFC3339),
	})

	errorsJSON, err := json.Marshal(errors)
	if err != nil {
		return nil, err
	}

	return json.RawMessage(errorsJSON), nil
}
