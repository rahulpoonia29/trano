PRAGMA foreign_keys = ON;

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
