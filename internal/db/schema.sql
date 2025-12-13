PRAGMA foreign_keys = ON;

-- TRAINS
CREATE TABLE
    IF NOT EXISTS trains (
        train_no INTEGER PRIMARY KEY,
        train_name TEXT NOT NULL,
        train_type TEXT NOT NULL, -- e.g. 'EMU', 'Duronto'
        zone TEXT, -- rake zone
        return_train_no INTEGER,
        coachComposition TEXT, -- comma seperated: "L,EOG,B1,B2,B3,S1,S2,S3,S4,GEN,SLR"
        source_url TEXT NOT NULL,
        created_at TEXT DEFAULT (CURRENT_TIMESTAMP), -- ISO: YYYY-MM-DD HH:MM:SS
        updated_at TEXT DEFAULT (CURRENT_TIMESTAMP) -- ISO: YYYY-MM-DD HH:MM:SS
    );

-- STATIONS
CREATE TABLE
    IF NOT EXISTS stations (
        station_code TEXT PRIMARY KEY,
        station_name TEXT NOT NULL,
        zone TEXT,
        division TEXT, -- 'SDAH', 'BKN'
        address TEXT,
        elevation_m REAL,
        lat REAL,
        lng REAL,
        number_of_platforms INTEGER,
        station_type TEXT, -- 'Terminus', 'Junction', 'Regular'
        station_category TEXT, -- 'NSG-1'
        track_type TEXT,
        created_at TEXT DEFAULT (CURRENT_TIMESTAMP),
        updated_at TEXT DEFAULT (CURRENT_TIMESTAMP)
    );

-- TRAIN SCHEDULE
CREATE TABLE
    IF NOT EXISTS train_schedules (
        schedule_id INTEGER PRIMARY KEY AUTOINCREMENT,
        train_no INTEGER NOT NULL,
        origin_station_code TEXT NOT NULL,
        terminus_station_code TEXT NOT NULL,
        -- timetable times at origin/terminus as minutes from midnight (0..1439)
        origin_sch_departure_min INTEGER NOT NULL CHECK (
            origin_sch_departure_min >= 0
            AND origin_sch_departure_min < 1440
        ),
        total_distance_km REAL NOT NULL,
        total_runtime_min INTEGER NOT NULL CHECK (total_runtime_min >= 0),
        -- running days bitmask (Sun to Sat, bits 0 to 6)
        running_days_bitmap INTEGER NOT NULL CHECK (
            running_days_bitmap >= 0
            AND running_days_bitmap <= 127
        ),
        created_at TEXT DEFAULT (CURRENT_TIMESTAMP), -- ISO: YYYY-MM-DD HH:MM:SS
        updated_at TEXT DEFAULT (CURRENT_TIMESTAMP), -- ISO: YYYY-MM-DD HH:MM:SS
        UNIQUE (train_no, origin_station_code, terminus_station_code, origin_sch_departure_min),
        FOREIGN KEY (train_no) REFERENCES trains (train_no) ON DELETE CASCADE,
        FOREIGN KEY (origin_station_code) REFERENCES stations (station_code),
        FOREIGN KEY (terminus_station_code) REFERENCES stations (station_code)
    );

CREATE INDEX IF NOT EXISTS idx_train_schedules_train ON train_schedules (train_no);

-- TRAIN ROUTES (per-stop static route for a given schedule)
CREATE TABLE
    IF NOT EXISTS train_routes (
        schedule_id INTEGER NOT NULL,
        station_code TEXT NOT NULL,
        distance_km REAL NOT NULL, -- from origin station
        -- minutes since origin DEPARTURE for arrival/departure at this station
        sch_arrival_min_from_start INTEGER NOT NULL CHECK (sch_arrival_min_from_start >= 0),
        sch_departure_min_from_start INTEGER NOT NULL CHECK (
            sch_departure_min_from_start >= sch_arrival_min_from_start
        ),
        stops INTEGER NOT NULL DEFAULT 1 CHECK (stops IN (0, 1)), -- 1 = scheduled stop, 0 = pass-through/technical halt
        PRIMARY KEY (schedule_id, station_code),
        FOREIGN KEY (schedule_id) REFERENCES train_schedules (schedule_id) ON DELETE CASCADE,
        FOREIGN KEY (station_code) REFERENCES stations (station_code)
    );

CREATE INDEX IF NOT EXISTS idx_train_routes_station ON train_routes (station_code);

CREATE TABLE
    IF NOT EXISTS train_runs (
        run_id TEXT PRIMARY KEY, -- e.g. "12817_2025-05-10"
        schedule_id INTEGER NOT NULL,
        train_no INTEGER NOT NULL,
        run_date TEXT NOT NULL, -- ISO: YYYY-MM-DD (date at origin)
        has_started INTEGER NOT NULL DEFAULT 0 CHECK (has_started IN (0, 1)),
        has_arrived INTEGER NOT NULL DEFAULT 0 CHECK (has_arrived IN (0, 1)),
        current_status TEXT, -- e.g. "Running", "Diverted", "Rescheduled"
        last_known_lat REAL,
        last_known_lng REAL,
        last_update_timestamp_ISO TEXT, -- ISO format, API example: 2025-12-10T22:04:33.019687+05:30
        errors JSON,
        created_at TEXT DEFAULT (CURRENT_TIMESTAMP), -- ISO: YYYY-MM-DD HH:MM:SS
        updated_at TEXT DEFAULT (CURRENT_TIMESTAMP), -- ISO: YYYY-MM-DD HH:MM:SS
        FOREIGN KEY (schedule_id) REFERENCES train_schedules (schedule_id) ON DELETE CASCADE,
        FOREIGN KEY (train_no) REFERENCES trains (train_no) ON DELETE CASCADE,
        UNIQUE (train_no, run_date)
    );

CREATE INDEX IF NOT EXISTS idx_train_runs_schedule_date ON train_runs (schedule_id, run_date);
CREATE INDEX IF NOT EXISTS idx_train_runs_poll ON train_runs (has_arrived, run_date, last_update_timestamp_ISO);

-- TRAIN RUN LOCATIONS (time-series tracking per run)
CREATE TABLE
    IF NOT EXISTS train_run_locations (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        run_id TEXT NOT NULL,
        lat REAL NOT NULL,
        lng REAL NOT NULL,
        timestamp_ISO TEXT NOT NULL, -- ISO timestamp
        FOREIGN KEY (run_id) REFERENCES train_runs (run_id) ON DELETE CASCADE,
        UNIQUE (run_id, timestamp_ISO)
    );