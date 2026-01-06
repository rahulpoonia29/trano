-- name: ListRunsToPoll :many
-- Fetch active runs with error threshold and start-time gating
SELECT
    tr.run_id,
    tr.train_no,
    tr.run_date,
    tr.last_known_lat_u6,
    tr.last_known_lng_u6,
    tr.last_updated_sno,
    tr.last_update_timestamp_ISO,
    COALESCE(tr.errors, '{}') AS errors,
    ts.schedule_id,
    ts.origin_station_code AS source_station,
    ts.terminus_station_code AS destination_station
FROM train_runs tr
JOIN train_schedules ts
    ON tr.schedule_id = ts.schedule_id
WHERE tr.has_arrived = 0
  AND date(tr.run_date) <= date(@now_ts)
  AND date(tr.run_date) >= date(@now_ts, '-5 days')
  AND COALESCE(json_extract(tr.errors, '$.static_response.count'), 0)
        < CAST(@static_response_threshold AS INTEGER)
  AND (
        COALESCE(json_extract(tr.errors, '$.static_response.count'), 0) +
        COALESCE(json_extract(tr.errors, '$.api_error.count'), 0) +
        COALESCE(json_extract(tr.errors, '$.unknown.count'), 0)
      ) < CAST(@total_error_threshold AS INTEGER)
  AND datetime(
        tr.run_date || ' ' ||
        printf(
            '%02d:%02d',
            ts.origin_sch_departure_min / 60,
            ts.origin_sch_departure_min % 60
        )
      ) <= datetime(@now_ts)
ORDER BY tr.last_update_timestamp_ISO ASC NULLS FIRST;

-- name: GetRunSnap :one
-- Snap raw GPS to route and compute linear reference bearing
WITH snapped AS (
  SELECT
    ST_ClosestPoint(
      trg.route_geom,
      ST_Transform(MakePoint(@lng, @lat, 4326), 7755)
    ) AS snappt,
    trg.route_geom
  FROM train_runs tr
  JOIN train_route_geometries trg
    ON tr.schedule_id = trg.schedule_id
  WHERE tr.run_id = @run_id
    AND ST_IsValid(trg.route_geom) = 1
),
fraccalc AS (
  SELECT
    snappt,
    route_geom,
    ST_Distance(ST_StartPoint(route_geom), snappt) /
      NULLIF(ST_Length(route_geom), 1.0) AS frac
  FROM snapped
),
bearingcalc AS (
  SELECT
    snappt,
    route_geom,
    frac,
    CASE
      WHEN frac >= 0.999 THEN
        ST_Azimuth(
          ST_Line_Interpolate_Point(route_geom, MAX(0.0, frac - 0.0005)),
          snappt
        )
      ELSE
        ST_Azimuth(
          snappt,
          ST_Line_Interpolate_Point(route_geom, MIN(1.0, frac + 0.0005))
        )
    END AS bearing_rad
  FROM fraccalc
)
SELECT
  CAST(X(ST_Transform(snappt, 4326)) * 1000000 AS INTEGER) AS snapped_lng_u6,
  CAST(Y(ST_Transform(snappt, 4326)) * 1000000 AS INTEGER) AS snapped_lat_u6,
  CAST(frac * 10000 AS INTEGER) AS route_frac_u4,
  CAST(ROUND(Degrees(bearing_rad)) % 360 AS INTEGER) AS bearing_deg
FROM bearingcalc;

-- name: UpdateRunStatus :exec
-- Partial, idempotent update of run state
UPDATE train_runs
SET
    has_started = COALESCE(@has_started, has_started),
    has_arrived = COALESCE(@has_arrived, has_arrived),
    current_status = COALESCE(@current_status, current_status),
    last_known_lat_u6 = COALESCE(@lat_u6, last_known_lat_u6),
    last_known_lng_u6 = COALESCE(@lng_u6, last_known_lng_u6),
    last_known_snapped_lat_u6 = COALESCE(@snapped_lat_u6, last_known_snapped_lat_u6),
    last_known_snapped_lng_u6 = COALESCE(@snapped_lng_u6, last_known_snapped_lng_u6),
    last_route_frac_u4 = COALESCE(@route_frac_u4, last_route_frac_u4),
    last_bearing_deg = COALESCE(@bearing_deg, last_bearing_deg),
    last_known_distance_km_u4 = COALESCE(@distance_km_u4, last_known_distance_km_u4),
    errors = COALESCE(@errors, errors),
    last_updated_sno = COALESCE(@last_updated_sno, last_updated_sno),
    last_update_timestamp_ISO = COALESCE(@last_update_iso, last_update_timestamp_ISO),
    updated_at = CURRENT_TIMESTAMP
WHERE run_id = @run_id;

-- name: LogRunLocation :exec
INSERT INTO train_run_locations (
    run_id,
    lat_u6,
    lng_u6,
    snapped_lat_u6,
    snapped_lng_u6,
    distance_km_u4,
    segment_station_code,
    at_station,
    timestamp_ISO
) VALUES (
    @run_id,
    @lat_u6,
    @lng_u6,
    @snapped_lat_u6,
    @snapped_lng_u6,
    @distance_km_u4,
    @segment_station_code,
    @at_station,
    @timestamp_iso
)
ON CONFLICT(run_id, timestamp_ISO) DO NOTHING;

-- name: ClearRunningDayBitForDate :exec
UPDATE train_schedules
SET
    running_days_bitmap =
        running_days_bitmap &
        ~(1 << CAST(strftime('%w', @run_date) AS INTEGER)),
    updated_at = CURRENT_TIMESTAMP
WHERE schedule_id = @schedule_id
  AND (
        running_days_bitmap &
        (1 << CAST(strftime('%w', @run_date) AS INTEGER))
      ) <> 0;

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
    printf('%d_%s', ts.train_no, @run_date) AS run_id,
    ts.schedule_id,
    ts.train_no,
    @run_date
FROM train_schedules ts
JOIN trains t
    ON ts.train_no = t.train_no
WHERE (ts.running_days_bitmap & (1 << @weekday)) <> 0
ON CONFLICT (train_no, run_date) DO NOTHING;

-- name: GetTrainsInViewport :many
-- Returns data for active trains within viewport bounds
SELECT 
    tr.train_no,
    tr.last_known_snapped_lat_u6 AS lat_u6,
    tr.last_known_snapped_lng_u6 AS lng_u6,
    tr.last_bearing_deg AS bearing_deg,
    tr.current_status,
    tr.last_update_timestamp_iso,
    t.train_name,
    t.train_type
FROM train_runs tr
JOIN trains t ON tr.train_no = t.train_no
WHERE tr.has_arrived = 0
  AND tr.last_known_snapped_lat_u6 IS NOT NULL
  AND tr.last_known_snapped_lng_u6 IS NOT NULL
  -- Spatial bounds filter (with u6 encoding)
  AND tr.last_known_snapped_lat_u6 >= @min_lat_u6
  AND tr.last_known_snapped_lat_u6 <= @max_lat_u6
  AND tr.last_known_snapped_lng_u6 >= @min_lng_u6
  AND tr.last_known_snapped_lng_u6 <= @max_lng_u6
  -- Only recent updates (avoid stale data)
  AND datetime(tr.last_update_timestamp_iso) > datetime('now', '-10 minutes')
ORDER BY tr.last_update_timestamp_iso DESC;