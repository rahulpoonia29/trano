package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"
	db "trano/internal/db/sqlc"

	"github.com/go-chi/chi/v5"
)

// returns TrainRun for a given train number and date.
// URL: GET /v1/runs/{train_no}/{run_date}
// Example: /v1/runs/12817/2025-05-10
func (s *Server) getRunHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	trainNoStr := chi.URLParam(r, "train_no")
	runDateStr := chi.URLParam(r, "run_date")

	trainNo, err := strconv.ParseInt(trainNoStr, 10, 64)
	if err != nil || trainNo <= 0 {
		http.Error(w, "invalid 'train_no' parameter", http.StatusBadRequest)
		return
	}

	// validate date (expect YYYY-MM-DD)
	if _, err := time.Parse("2006-01-02", runDateStr); err != nil {
		http.Error(w, "invalid 'run_date' parameter; expected YYYY-MM-DD", http.StatusBadRequest)
		return
	}

	runID := fmt.Sprintf("%d_%s", trainNo, runDateStr)

	var tr db.TrainRun
	var currentStatus sql.NullString
	row := s.db.QueryRowContext(ctx, `SELECT run_id, schedule_id, train_no, run_date, has_started, has_arrived, current_status, last_known_lat_u6, last_known_lng_u6, last_known_snapped_lat_u6, last_known_snapped_lng_u6, last_route_frac_u4, last_bearing_deg, last_known_distance_km_u4, last_updated_sno, errors, last_update_timestamp_ISO, created_at, updated_at FROM train_runs WHERE run_id = ?`, runID)
	if err := row.Scan(
		&tr.RunID,
		&tr.ScheduleID,
		&tr.TrainNo,
		&tr.RunDate,
		&tr.HasStarted,
		&tr.HasArrived,
		&currentStatus,
		&tr.LastKnownLatU6,
		&tr.LastKnownLngU6,
		&tr.LastKnownSnappedLatU6,
		&tr.LastKnownSnappedLngU6,
		&tr.LastRouteFracU4,
		&tr.LastBearingDeg,
		&tr.LastKnownDistanceKmU4,
		&tr.LastUpdatedSno,
		&tr.Errors,
		&tr.LastUpdateTimestampIso,
		&tr.CreatedAt,
		&tr.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "run not found", http.StatusNotFound)
			return
		}
		s.logger.Printf("api: failed to load run %s: %v", runID, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if currentStatus.Valid {
		tr.CurrentStatus = currentStatus.String
	} else {
		tr.CurrentStatus = nil
	}

	writeJSON(w, http.StatusOK, tr)
}
