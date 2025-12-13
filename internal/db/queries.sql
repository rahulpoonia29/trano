-- name: ListActiveSchedules :many
-- Returns schedules valid for the given date to generate runs
SELECT
  schedule_id,
  train_no,
  origin_station_code,
  terminus_station_code
FROM train_schedules ts
WHERE (ts.running_days_bitmap & (1 << @weekday)) <> 0;

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
  current_status            = COALESCE(@current_status, current_status),
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

-- name: UpsertTrain :exec
-- Upserts a train record
INSERT INTO trains (
  train_no,
  train_name,
  train_type,
  zone,
  return_train_no,
  coachComposition,
  source_url,
  created_at,
  updated_at
) VALUES (
  @train_no,
  @train_name,
  @train_type,
  @zone,
  @return_train_no,
  @coachComposition,
  @source_url,
  COALESCE(@created_at, CURRENT_TIMESTAMP),
  CURRENT_TIMESTAMP
)
ON CONFLICT(train_no) DO UPDATE
SET
  train_name = excluded.train_name,
  train_type = excluded.train_type,
  zone = excluded.zone,
  return_train_no = excluded.return_train_no,
  coachComposition = excluded.coachComposition,
  source_url = excluded.source_url,
  updated_at = CURRENT_TIMESTAMP;

-- name: UpsertStation :exec
-- Upserts a station record
INSERT INTO stations (
  station_code,
  station_name,
  zone,
  division,
  address,
  elevation_m,
  lat,
  lng,
  number_of_platforms,
  station_type,
  station_category,
  track_type,
  updated_at
) VALUES (
  @station_code,
  @station_name,
  @zone,
  @division,
  @address,
  @elevation_m,
  @lat,
  @lng,
  @number_of_platforms,
  @station_type,
  @station_category,
  @track_type,
  CURRENT_TIMESTAMP
)
ON CONFLICT(station_code) DO UPDATE
SET
  station_name = excluded.station_name,
  zone = excluded.zone,
  division = excluded.division,
  address = excluded.address,
  elevation_m = excluded.elevation_m,
  lat = excluded.lat,
  lng = excluded.lng,
  number_of_platforms = excluded.number_of_platforms,
  station_type = excluded.station_type,
  station_category = excluded.station_category,
  track_type = excluded.track_type,
  updated_at = CURRENT_TIMESTAMP;

-- name: UpsertTrainSchedule :one
-- Upserts a train schedule and returns the schedule_id
INSERT INTO train_schedules (
  train_no,
  origin_station_code,
  terminus_station_code,
  origin_sch_departure_min,
  total_distance_km,
  total_runtime_min,
  running_days_bitmap,
  created_at,
  updated_at
) VALUES (
  @train_no,
  @origin_station_code,
  @terminus_station_code,
  @origin_sch_departure_min,
  @total_distance_km,
  @total_runtime_min,
  @running_days_bitmap,
  COALESCE(@created_at, CURRENT_TIMESTAMP),
  CURRENT_TIMESTAMP
)
ON CONFLICT(train_no, origin_station_code, terminus_station_code, origin_sch_departure_min) DO UPDATE
SET
  total_distance_km = excluded.total_distance_km,
  total_runtime_min = excluded.total_runtime_min,
  running_days_bitmap = excluded.running_days_bitmap,
  updated_at = CURRENT_TIMESTAMP
RETURNING schedule_id;

-- name: UpsertTrainRoute :exec
-- Upserts a train route record
INSERT INTO train_routes (
  schedule_id,
  station_code,
  distance_km,
  sch_arrival_min_from_start,
  sch_departure_min_from_start,
  stops
) VALUES (
  @schedule_id,
  @station_code,
  @distance_km,
  @sch_arrival_min_from_start,
  @sch_departure_min_from_start,
  @stops
)
ON CONFLICT(schedule_id, station_code) DO UPDATE
SET
  distance_km = excluded.distance_km,
  sch_arrival_min_from_start = excluded.sch_arrival_min_from_start,
  sch_departure_min_from_start = excluded.sch_departure_min_from_start,
  stops = excluded.stops;
