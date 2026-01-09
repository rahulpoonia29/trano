-- name: UpsertTrain :exec
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
ON CONFLICT(train_no) DO UPDATE SET
    train_name = excluded.train_name,
    train_type = excluded.train_type,
    zone = excluded.zone,
    return_train_no = excluded.return_train_no,
    coachComposition = excluded.coachComposition,
    source_url = excluded.source_url,
    updated_at = CURRENT_TIMESTAMP;

-- name: UpsertStation :exec
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
ON CONFLICT(station_code) DO UPDATE SET
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
ON CONFLICT(
    train_no,
    origin_station_code,
    terminus_station_code,
    origin_sch_departure_min
) DO UPDATE SET
    total_distance_km = excluded.total_distance_km,
    total_runtime_min = excluded.total_runtime_min,
    running_days_bitmap = excluded.running_days_bitmap,
    updated_at = CURRENT_TIMESTAMP
RETURNING schedule_id;

-- name: UpsertTrainRoute :exec
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
ON CONFLICT(schedule_id, station_code) DO UPDATE SET
    distance_km = excluded.distance_km,
    sch_arrival_min_from_start = excluded.sch_arrival_min_from_start,
    sch_departure_min_from_start = excluded.sch_departure_min_from_start,
    stops = excluded.stops;

-- name: UpsertTrainRun :exec
INSERT INTO train_runs (
    run_id,
    schedule_id,
    train_no,
    run_date,
    created_at,
    updated_at
) VALUES (
    @run_id,
    @schedule_id,
    @train_no,
    @run_date,
    CURRENT_TIMESTAMP,
    CURRENT_TIMESTAMP
)
ON CONFLICT(run_id) DO UPDATE SET
    schedule_id = excluded.schedule_id,
    updated_at = CURRENT_TIMESTAMP;

-- name: GenerateRunsForDate :exec
INSERT INTO train_runs (
    run_id,
    schedule_id,
    train_no,
    run_date
)
SELECT
    -- run_id as "<train_no>_<YYYY-MM-DD>"
    printf('%d_%s', ts.train_no, @run_date) AS run_id,
    ts.schedule_id,
    ts.train_no,
    @run_date
FROM train_schedules ts
JOIN trains t
    ON ts.train_no = t.train_no
WHERE (ts.running_days_bitmap & (1 << @weekday)) <> 0
ON CONFLICT (train_no, run_date) DO NOTHING;
