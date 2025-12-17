package poller

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	dbtypes "trano/internal/db"
	db "trano/internal/db/sqlc"
	"trano/internal/wimt"
)

type Config struct {
	Concurrency          int16
	Window               time.Duration
	ProxyURL             string
	StaticErrorThreshold int8
	TotalErrorThreshold  int8
}

type ErrorEntry struct {
	Reason    string `json:"reason"`
	Timestamp string `json:"timestamp"`
}

type LastStationSnapshot struct {
	Sno         int
	StationCode string
	SchArrTm    int64
	ActArrTm    int64
	SchDepTm    int64
	ActDepTm    int64
}

type CycleResult struct {
	RunID          string
	Success        bool
	ShortResponse  string
	StaticResponse bool
	APIError       bool
	UnknownError   bool
	NoCoords       bool
	CoordsLogged   bool
	BecameArrived  bool
}

// Start blocks until ctx is cancelled
// Calls executeCycle repeatedly and ensures each cycle lasts at least cfg.Window
func Start(ctx context.Context, queries *db.Queries, sqlDB *sql.DB, logger *log.Logger, cfg Config, loc *time.Location) {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	if cfg.Window <= 0 {
		cfg.Window = 1 * time.Minute
	}
	if cfg.StaticErrorThreshold <= 0 {
		cfg.StaticErrorThreshold = 10
	}
	if cfg.TotalErrorThreshold < 0 {
		cfg.TotalErrorThreshold = 5
	}

	api := wimt.NewAPIClient(cfg.ProxyURL)
	logger.Printf("poller started | workers: %d | window: %v | static_error_thres: %d | totol_error_thres: %d",
		cfg.Concurrency, cfg.Window, cfg.StaticErrorThreshold, cfg.TotalErrorThreshold)

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
	runs, err := queries.ListRunsToPoll(ctx, db.ListRunsToPollParams{
		NowTs:                   time.Now().In(loc).Format(time.DateTime),
		StaticResponseThreshold: int64(cfg.StaticErrorThreshold),
		TotalErrorThreshold:     int64(cfg.TotalErrorThreshold),
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

	resultsCh := make(chan CycleResult, len(runs))

	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.Concurrency)
	ticker := time.NewTicker(delay)
	defer ticker.Stop()

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
				result := processRun(ctx, r, queries, sqlDB, api, logger, loc)
				resultsCh <- result
			}(run)
		}
	}

	wg.Wait()
	close(resultsCh)

	agg := struct {
		Processed       int
		Success         int
		ShortNotRunning int
		ShortTimetable  int
		ShortUnknown    int
		StaticResponse  int
		APIError        int
		UnknownError    int
		NoCoords        int
		CoordsLogged    int
		BecameArrived   int
		HasStarted      int
	}{}

	for result := range resultsCh {
		agg.Processed++
		if result.Success {
			agg.Success++
			if result.CoordsLogged {
				agg.CoordsLogged++
			} else {
				agg.NoCoords++
			}
			if result.BecameArrived {
				agg.BecameArrived++
			}
		}
		switch result.ShortResponse {
		case "not_running_today":
			agg.ShortNotRunning++
		case "timetable_update":
			agg.ShortTimetable++
		case "unknown_short_response":
			agg.ShortUnknown++
		}
		if result.StaticResponse {
			agg.StaticResponse++
		}
		if result.APIError {
			agg.APIError++
		}
		if result.UnknownError {
			agg.UnknownError++
		}
	}

	logger.Printf("cycle results | processed: %d | success: %d | short_resp: %d/%d/%d (not_run/timetable/unknown) | static_resp: %d | api_err: %d | unknown_err: %d | no_coords: %d | coords_logged: %d | became_arrived: %d | has_started: %d", agg.Processed, agg.Success, agg.ShortNotRunning, agg.ShortTimetable, agg.ShortUnknown, agg.StaticResponse, agg.APIError, agg.UnknownError, agg.NoCoords, agg.CoordsLogged, agg.BecameArrived, agg.HasStarted)
	return agg.Processed
}

func processRun(ctx context.Context, run db.ListRunsToPollRow, queries *db.Queries, sqlDB *sql.DB, api *wimt.APIClient, logger *log.Logger, loc *time.Location) CycleResult {
	var result CycleResult
	result.RunID = run.RunID

	select {
	case <-ctx.Done():
		return result
	default:
	}

	runDate, _ := time.ParseInLocation(time.DateOnly, run.RunDate, loc)
	trainNoStr := fmt.Sprintf("%05d", run.TrainNo)

	body, err := api.FetchTrainStatus(ctx, trainNoStr, run.SourceStation, run.DestinationStation, runDate)
	if err != nil {
		result = handleAPIError(ctx, queries, run, logger, loc)
		return result
	}

	bodyStr := string(body)
	if len(body) < 150 {
		result = handleShortResponse(ctx, queries, sqlDB, run, bodyStr, logger)
		return result
	}

	if !strings.Contains(bodyStr, "running_status") && !strings.Contains(bodyStr, "running status") {
		result = handleStaticResponse(ctx, queries, run, logger, loc)
		return result
	}

	var data wimt.APIResponse
	if err := json.Unmarshal(body, &data); err != nil {
		result = handleUnknownError(ctx, queries, run, logger, loc)
		return result
	}

	result = processValidResponse(ctx, queries, sqlDB, run, &data, logger, loc)
	return result
}

const (
	statusNotRunning = "not_running_today"
	statusTimetable  = "timetable_update"
	statusUnknown    = "unknown_short_response"
)

func handleShortResponse(
	ctx context.Context,
	queries *db.Queries,
	sqlDB *sql.DB,
	run db.ListRunsToPollRow,
	bodyStr string,
	logger *log.Logger,
) CycleResult {
	var result CycleResult
	result.RunID = run.RunID

	switch {
	case strings.Contains(bodyStr, "not running"):
		result.ShortResponse = statusNotRunning
	case strings.Contains(bodyStr, "update the timetable"):
		result.ShortResponse = statusTimetable
	default:
		result.ShortResponse = statusUnknown
		logger.Printf("unexpected short response for %s: %s", run.RunID, bodyStr)
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		logger.Printf("failed to begin tx for short-response update for %s: %v", run.RunID, err)
		return result
	}
	defer tx.Rollback()

	txQueries := queries.WithTx(tx)

	if err := txQueries.UpdateRunStatus(ctx, db.UpdateRunStatusParams{
		RunID:         run.RunID,
		HasArrived:    1,
		CurrentStatus: sql.NullString{String: result.ShortResponse, Valid: true},
	}); err != nil {
		return result
	}

	// update bitmap
	if result.ShortResponse == statusNotRunning {
		if err := txQueries.ClearRunningDayBitForDate(ctx, db.ClearRunningDayBitForDateParams{
			ScheduleID: run.ScheduleID,
			RunDate:    run.RunDate,
		}); err != nil {
			logger.Printf("failed to clear running day bit for schedule %d (run %s): %v", run.ScheduleID, run.RunID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return result
	}

	return result
}

func handleStaticResponse(
	ctx context.Context,
	queries *db.Queries,
	run db.ListRunsToPollRow,
	_ *log.Logger,
	loc *time.Location,
) CycleResult {
	var result CycleResult
	result.RunID = run.RunID
	result.StaticResponse = true

	run.Errors.StaticResponse.Count++
	run.Errors.StaticResponse.LastSeen = time.Now().In(loc).Format(time.RFC3339)

	if err := queries.UpdateRunStatus(ctx, db.UpdateRunStatusParams{
		RunID:  run.RunID,
		Errors: run.Errors,
	}); err != nil {
		return result
	}

	return result
}

func handleAPIError(
	ctx context.Context,
	queries *db.Queries,
	run db.ListRunsToPollRow,
	_ *log.Logger,
	loc *time.Location,
) CycleResult {
	var result CycleResult
	result.RunID = run.RunID
	result.APIError = true

	if run.Errors.APIError == nil {
		run.Errors.APIError = &dbtypes.ErrorCounter{}
	}
	run.Errors.APIError.Count++
	run.Errors.APIError.LastSeen = time.Now().In(loc).Format(time.RFC3339)

	if err := queries.UpdateRunStatus(ctx, db.UpdateRunStatusParams{
		RunID:  run.RunID,
		Errors: run.Errors,
	}); err != nil {
		return result
	}
	return result
}

func handleUnknownError(
	ctx context.Context,
	queries *db.Queries,
	run db.ListRunsToPollRow,
	_ *log.Logger,
	loc *time.Location,
) CycleResult {
	var result CycleResult
	result.RunID = run.RunID
	result.UnknownError = true

	if run.Errors.UnknownError == nil {
		run.Errors.UnknownError = &dbtypes.ErrorCounter{}
	}
	run.Errors.UnknownError.Count++
	run.Errors.UnknownError.LastSeen = time.Now().In(loc).Format(time.RFC3339)

	if err := queries.UpdateRunStatus(ctx, db.UpdateRunStatusParams{
		RunID:  run.RunID,
		Errors: run.Errors,
	}); err != nil {
		return result
	}
	return result
}

func processValidResponse(
	ctx context.Context,
	queries *db.Queries,
	sqlDB *sql.DB,
	run db.ListRunsToPollRow,
	data *wimt.APIResponse,
	logger *log.Logger,
	loc *time.Location,
) CycleResult {
	var result CycleResult
	result.RunID = run.RunID
	result.Success = true

	type RunStatus struct {
		Canonical  string
		IsTerminal bool
	}

	// Map of known statuses to their canonical form and terminality
	var statusMap = map[string]RunStatus{
		"end":         {"completed", true},
		"cancelled":   {"cancelled", true},
		"terminated":  {"terminated", true},
		"rescheduled": {"rescheduled", false},
	}

	raw := strings.ToLower(strings.TrimSpace(data.RunningStatus))
	if raw == "" {
		raw = strings.ToLower(strings.TrimSpace(data.RunningStatusAlt))
	}

	status, ok := statusMap[raw]
	if !ok {
		if raw == "" {
			status = RunStatus{Canonical: "unknown", IsTerminal: false}
		} else {
			status = RunStatus{Canonical: raw, IsTerminal: false}
		}
	}

	var apiTime *time.Time
	lastUpdateIso := sql.NullString{Valid: false}
	if data.LastUpdateIsoDate != "" {
		if t, err := time.Parse(time.RFC3339, data.LastUpdateIsoDate); err == nil {
			apiTime = &t
			lastUpdateIso = sql.NullString{String: t.In(loc).Format(time.RFC3339), Valid: true}
		}
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return result
	}
	defer tx.Rollback()

	txq := queries.WithTx(tx)

	hasArrived := int64(0)
	if status.IsTerminal {
		hasArrived = 1
	}

	var currStn *wimt.DaySchedule
	for i := range data.DaysSchedule {
		if data.DaysSchedule[i].CurStn != nil && *data.DaysSchedule[i].CurStn {
			currStn = &data.DaysSchedule[i]
			break
		}
	}

	finalSNO := sql.NullString{Valid: false}

	updateFinalSNO := func(incoming string) {
		finalSNO = sql.NullString{String: incoming, Valid: true}
	}

	switch {
	case currStn == nil || currStn.Sno < 0 || currStn.StationCode == "":
	default:
		incomingSNO, err := SnoStrFromDaySchedule(currStn)
		if err != nil {
			logger.Printf("failed to get SNO for run %s: %v", run.RunID, err)
			break
		}

		if !run.LastUpdatedSno.Valid || run.LastUpdatedSno.String == "" {
			updateFinalSNO(incomingSNO)
			break
		}

		existingParts := strings.Split(run.LastUpdatedSno.String, "|")
		if len(existingParts) == 0 {
			updateFinalSNO(incomingSNO)
			break
		}

		if existingSno, err := strconv.Atoi(existingParts[0]); err == nil && currStn.Sno > existingSno {
			updateFinalSNO(incomingSNO)
		}
	}

	// Determine if the incoming API time is newer than the DB's last update timestamp
	locationAllowed := false
	if apiTime != nil {
		if !run.LastUpdateTimestampIso.Valid || run.LastUpdateTimestampIso.String == "" {
			locationAllowed = true
		} else {
			dbTime, err := time.Parse(time.RFC3339, run.LastUpdateTimestampIso.String)
			if err != nil {
				locationAllowed = true // trust API if DB is corrupt
			} else {
				locationAllowed = apiTime.In(loc).After(dbTime.In(loc))
			}
		}
	}

	var lat, lng sql.NullInt64
	if locationAllowed {
		// Try to extract a valid location from the API response
		if data.Lat != nil && data.Lng != nil {
			latVal, lngVal := *data.Lat, *data.Lng
			if !(latVal == 0 && lngVal == 0) && latVal >= 6.0 && latVal <= 37.0 && lngVal >= 68.0 && lngVal <= 97.0 {
				// Convert to U6 precision (multiply by 1,000,000)
				latInt := int64(latVal * 1e6)
				lngInt := int64(lngVal * 1e6)
				distanceInt := int64(data.Distance * 1e4)

				lat = sql.NullInt64{Int64: latInt, Valid: true}
				lng = sql.NullInt64{Int64: lngInt, Valid: true}

				_ = txq.LogRunLocation(ctx, db.LogRunLocationParams{
					RunID:              run.RunID,
					LatU6:              latInt,
					LngU6:              lngInt,
					DistanceKmU4:       distanceInt,
					SegmentStationCode: currStn.StationCode,
					AtStation:          !data.DepartedCurStn,
					TimestampIso:       lastUpdateIso.String,
				})
			}
		}
	}

	if err := txq.UpdateRunStatus(ctx, db.UpdateRunStatusParams{
		RunID:          run.RunID,
		HasStarted:     1,
		HasArrived:     hasArrived,
		CurrentStatus:  sql.NullString{String: status.Canonical, Valid: true},
		Lat:            lat,
		Lng:            lng,
		LastUpdatedSno: finalSNO,
		LastUpdateIso:  lastUpdateIso,
	}); err != nil {
		return result
	}

	if err := tx.Commit(); err != nil {
		return result
	}
	// logger.Printf("run %s processed successfully", run.RunID)

	if hasArrived == 1 {
		result.BecameArrived = true
	}
	if lat.Valid {
		result.CoordsLogged = true
	} else {
		result.NoCoords = true
	}

	return result
}

func SnoStrFromDaySchedule(currStn *wimt.DaySchedule) (string, error) {
	if currStn == nil {
		return "", fmt.Errorf("current station is nil")
	}
	sno := currStn.Sno
	stationCode := currStn.StationCode

	schArrTm := currStn.SchArrivalTm
	actArrTm := currStn.ActualArrivalTm
	schDepTm := currStn.SchDepartureTm
	actDepTm := currStn.ActualDepartureTm

	// If any field fails, return error
	if sno < 0 ||
		stationCode == "" ||
		schArrTm <= 0 ||
		actArrTm < 0 ||
		schDepTm <= 0 ||
		actDepTm < 0 {

		return "", fmt.Errorf("invalid field(s): sno=%d, stationCode=%q, schArrTm=%d, actArrTm=%d, schDepTm=%d, actDepTm=%d",
			sno, stationCode, schArrTm, actArrTm, schDepTm, actDepTm)
	}

	return fmt.Sprintf(
		"%d|%s|%d|%d|%d|%d",
		sno,
		stationCode,
		schArrTm,
		actArrTm,
		schDepTm,
		actDepTm,
	), nil
}
