# Trano Setup Guide

Complete guide for setting up and running the Trano train tracking application.

---

## 📋 Prerequisites

- Go 1.21 or higher
- SQLite3
- sqlc (for code generation)

---

## 🚀 Quick Start

### 1. Clone and Setup

```bash
cd hmm
cp .env.example .env
# Edit .env with your configuration
```

### 2. Install Dependencies

```bash
go mod download
```

### 3. Generate Database Code

```bash
sqlc generate
```

### 4. Run Application

```bash
go run main.go
```

The application will:
- Create `./data/` directory if it doesn't exist
- Initialize SQLite database with WAL mode
- Apply migrations from `internal/db/schema.sql`
- Start the poller service
- Start the scheduler (runs daily at midnight IST)

---

## 📂 Directory Structure

```
hmm/
├── data/                    # Database files (created on first run)
│   ├── trano.db            # Main SQLite database
│   ├── trano.db-wal        # Write-Ahead Log
│   └── trano.db-shm        # Shared memory file
├── internal/
│   ├── config/             # Configuration management
│   ├── db/
│   │   ├── schema.sql      # Database schema
│   │   ├── queries.sql     # SQL queries
│   │   └── sqlc/           # Generated code
│   ├── poller/             # Train status polling service
│   ├── schedular/          # Daily run generation
│   └── wimt/               # API client
├── docs/                   # Documentation
├── .env.example            # Example environment config
└── main.go                 # Application entry point
```

---

## ⚙️ Configuration

### Environment Variables

All configuration is done via environment variables or `.env` file:

#### Database Settings

| Variable | Default | Description |
|----------|---------|-------------|
| `DB_PATH` | `./data/trano.db` | Path to SQLite database file |
| `DB_MAX_OPEN_CONNS` | `25` | Maximum open connections |
| `DB_MAX_IDLE_CONNS` | `5` | Maximum idle connections |
| `DB_CONN_MAX_LIFETIME` | `5m` | Maximum connection lifetime |
| `DB_CONN_MAX_IDLE_TIME` | `1m` | Maximum connection idle time |

#### Poller Settings

| Variable | Default | Description |
|----------|---------|-------------|
| `POLLER_CONCURRENCY` | `10` | Number of concurrent API requests |
| `POLLER_WINDOW` | `2m` | Minimum time between poll cycles |
| `POLLER_ERROR_THRESHOLD` | `3` | Max errors before stopping polls for a run |

#### Other Settings

| Variable | Default | Description |
|----------|---------|-------------|
| `TIMEZONE` | `Asia/Kolkata` | Application timezone (IANA format) |
| `PROXY_URL` | _(empty)_ | HTTP proxy URL (optional) |

### Duration Format

Duration values use Go's duration format:
- `30s` = 30 seconds
- `2m` = 2 minutes
- `1h30m` = 1 hour 30 minutes
- `24h` = 24 hours

---

## 🗄️ Database

### SQLite Configuration

The application uses SQLite with the following optimizations:

- **WAL Mode** (`journal_mode=WAL`): Better concurrency for read/write operations
- **Foreign Keys** (`foreign_keys=on`): Enforces referential integrity
- **Busy Timeout** (`busy_timeout=5000`): 5-second timeout for locked database
- **Synchronous Mode** (`synchronous=NORMAL`): Balanced durability vs performance
- **Cache Size** (`cache_size=2000`): ~8MB memory cache (2000 pages × 4KB)

### Schema Overview

**Core Tables:**
- `trains` - Train master data
- `stations` - Station master data
- `train_schedules` - Static schedules with running days bitmap
- `train_routes` - Per-station timing for each schedule
- `train_runs` - Daily run instances (one per train per date)
- `train_run_locations` - Time-series location tracking

### Migrations

Migrations are automatically applied on startup from `internal/db/schema.sql`. The schema uses:
- `CREATE TABLE IF NOT EXISTS` - Safe to run repeatedly
- Foreign keys with `ON DELETE CASCADE` - Automatic cleanup
- Check constraints for data validation
- Indexes for query performance

To manually apply migrations:
```bash
sqlite3 data/trano.db < internal/db/schema.sql
```

### Backup

```bash
# Online backup (while app is running)
sqlite3 data/trano.db ".backup data/backup.db"

# Copy files (stop app first)
cp data/trano.db data/backup.db
cp data/trano.db-wal data/backup.db-wal
cp data/trano.db-shm data/backup.db-shm
```

### Inspect Database

```bash
# Open database
sqlite3 data/trano.db

# Useful commands
.tables                          # List tables
.schema train_runs              # Show table schema
SELECT * FROM train_runs LIMIT 5;  # Query data
.quit                           # Exit
```

---

## 🔄 Services

### Poller Service

**Purpose**: Continuously polls train location data from API

**Behavior:**
- Fetches list of active runs (not arrived, below error threshold)
- Polls each run at regular intervals (spread across `POLLER_WINDOW`)
- Limits concurrent requests to `POLLER_CONCURRENCY`
- Updates run status and location in database (atomic transaction)
- Stops polling runs that:
  - Have arrived at destination
  - Are cancelled/terminated
  - Exceed error threshold

**Rate Limiting:**
```
Rate = POLLER_WINDOW / number_of_runs
Example: 2 minutes / 100 runs = 1.2 seconds between requests
```

**Concurrency:**
- Semaphore limits parallel API calls
- Each request gets its own goroutine
- Database transactions ensure atomicity

### Scheduler Service

**Purpose**: Generates daily run instances from schedules

**Behavior:**
- Runs on startup (generates runs for today)
- Then runs daily at midnight (in configured timezone)
- Queries schedules with matching running_days_bitmap
- Creates/updates train_run entries for the date
- Uses upsert pattern (safe to run multiple times)

**Running Days Bitmap:**
```
Bit 0 = Sunday
Bit 1 = Monday
Bit 2 = Tuesday
Bit 3 = Wednesday
Bit 4 = Thursday
Bit 5 = Friday
Bit 6 = Saturday

Example: 127 (0b1111111) = Every day
Example: 62 (0b0111110) = Weekdays only
Example: 65 (0b1000001) = Weekends only
```

---

## 🏃 Running in Production

### Systemd Service

Create `/etc/systemd/system/trano.service`:

```ini
[Unit]
Description=Trano Train Tracking Service
After=network.target

[Service]
Type=simple
User=trano
WorkingDirectory=/opt/trano
Environment="DB_PATH=/var/lib/trano/trano.db"
ExecStart=/opt/trano/t
