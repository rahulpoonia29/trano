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
