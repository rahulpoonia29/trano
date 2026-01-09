PRAGMA foreign_keys = ON;

-- TRAIN RUN (one physical run on a specific date)
CREATE TABLE
    IF NOT EXISTS train_runs (
        run_id TEXT PRIMARY KEY, -- e.g. "12817_2025-05-10"
        schedule_id INTEGER NOT NULL,
        train_no INTEGER NOT NULL,
        run_date TEXT NOT NULL, -- ISO: YYYY-MM-DD (date at origin)
        has_started INTEGER NOT NULL DEFAULT 0 CHECK (has_started IN (0, 1)),
        has_arrived INTEGER NOT NULL DEFAULT 0 CHECK (has_arrived IN (0, 1)),
        current_status TEXT DEFAULT "unknown" NOT NULL, -- e.g. "Running", "Rescheduled"

        last_known_lat_u6 INTEGER,
        last_known_lng_u6 INTEGER,

        last_known_snapped_lat_u6 INTEGER,
        last_known_snapped_lng_u6 INTEGER,
        
        last_route_frac_u4 INTEGER,
        last_bearing_deg INTEGER,

        last_known_distance_km_u4 INTEGER,
        last_updated_sno TEXT,

        errors TEXT DEFAULT '{}',
        last_update_timestamp_ISO TEXT,
        created_at TEXT DEFAULT (CURRENT_TIMESTAMP) NOT NULL,
        updated_at TEXT DEFAULT (CURRENT_TIMESTAMP) NOT NULL,
        FOREIGN KEY (schedule_id) REFERENCES train_schedules (schedule_id) ON DELETE CASCADE,
        FOREIGN KEY (train_no) REFERENCES trains (train_no) ON DELETE CASCADE,
        UNIQUE (train_no, run_date)
    );

CREATE INDEX IF NOT EXISTS idx_train_runs_schedule_date ON train_runs (schedule_id, run_date);

CREATE INDEX IF NOT EXISTS idx_train_runs_poll ON train_runs (has_arrived, run_date, last_update_timestamp_ISO);

CREATE INDEX IF NOT EXISTS idx_train_runs_active_map 
ON train_runs (has_arrived, last_known_snapped_lat_u6, last_known_snapped_lng_u6) 
WHERE has_arrived = 0 AND last_known_snapped_lat_u6 IS NOT NULL AND last_known_snapped_lng_u6 IS NOT NULL;

-- TIME SERIES LOCATION LOG
CREATE TABLE
    IF NOT EXISTS train_run_locations (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        run_id TEXT NOT NULL,

        lat_u6 INTEGER NOT NULL,
        lng_u6 INTEGER NOT NULL,
        snapped_lat_u6 INTEGER,
        snapped_lng_u6 INTEGER,

        distance_km_u4 INTEGER NOT NULL,
        segment_station_code TEXT NOT NULL,
        at_station INTEGER NOT NULL DEFAULT 0,

        timestamp_ISO TEXT NOT NULL,
        FOREIGN KEY (run_id) REFERENCES train_runs (run_id) ON DELETE CASCADE,
        UNIQUE (run_id, timestamp_ISO)
    );
