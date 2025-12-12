-- name: ListActiveSchedules :many
-- Returns schedules valid for the given date to generate runs
SELECT
  schedule_id,
  train_no,
  origin_station_code,
  terminus_station_code
FROM train_schedules ts
WHERE (ts.running_days_bitmap & (1 << :weekday)) <> 0;

-- name: UpsertTrainRun :exec
-- Creates a run instance. run_id format: trainNo_YYYY-MM-DD
INSERT INTO train_runs (
  run_id,
  schedule_id,
  train_no,
  run_date,
  has_started,
  has_arrived,
  created_at,
  updated_at
) VALUES (
  @run_id,
  @schedule_id,
  @train_no,
  @run_date,
  0,
  0,
  CURRENT_TIMESTAMP,
  CURRENT_TIMESTAMP
)
ON CONFLICT(run_id) DO UPDATE
SET
  schedule_id = excluded.schedule_id,
  updated_at = CURRENT_TIMESTAMP;

-- name: ListRunsToPoll :many
-- Fetches active runs with error threshold check. Join to get source/dest for API params.
SELECT
    tr.run_id,
    tr.train_no,
    tr.run_date,
    tr.last_known_lat,
    tr.last_known_lng,
    tr.last_update_timestamp_ISO,
    tr.errors,
    ts.origin_station_code AS source_station,
    ts.terminus_station_code AS destination_station
FROM train_runs tr
JOIN train_schedules ts ON tr.schedule_id = ts.schedule_id
WHERE tr.has_arrived = 0
    AND tr.run_date = CAST(@target_date AS TEXT)
    AND COALESCE(json_array_length(tr.errors), 0) < CAST(@threshold AS INTEGER)
ORDER BY tr.last_update_timestamp_ISO ASC NULLS FIRST;

-- name: UpdateRunStatus :exec
-- Updates the main run state
UPDATE train_runs
SET
  has_started               = COALESCE(@has_started, has_started),
  has_arrived               = COALESCE(@has_arrived, has_arrived),
  end_reason                = COALESCE(@end_reason, end_reason),
  last_known_lat            = COALESCE(@lat, last_known_lat),
  last_known_lng            = COALESCE(@lng, last_known_lng),
  last_update_timestamp_ISO = COALESCE(@last_update_iso, last_update_timestamp_ISO),
  errors                    = COALESCE(@errors, errors),
  updated_at                = CURRENT_TIMESTAMP
WHERE run_id = @run_id;

-- name: LogRunLocation :exec
-- Inserts into the time-series tracking table
INSERT INTO train_run_locations (
  run_id, lat, lng, timestamp_ISO
) VALUES (
  @run_id, @lat, @lng, @timestamp_iso
)
ON CONFLICT(run_id, timestamp_ISO) DO NOTHING;
