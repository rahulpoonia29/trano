package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"trano/internal/config"
	dbutil "trano/internal/db"
	db "trano/internal/db/sqlc"
	"trano/internal/poller"
)

type Server struct {
	cfg config.ServerConfig

	pollerCfg poller.Config
	queries   *db.Queries
	db        *sql.DB
	logger    *log.Logger

	srv *http.Server
}

func NewServer(cfg config.ServerConfig, dbCfg config.DatabaseConfig, pollerCfg poller.Config, logger *log.Logger) (*Server, error) {
	dbConn, err := dbutil.OpenDatabase(dbCfg, dbutil.DefaultDatabaseOptions(), logger)
	if err != nil {
		return nil, err
	}
	queries := db.New(dbConn)

	s := &Server{
		cfg:       cfg,
		pollerCfg: pollerCfg,
		queries:   queries,
		db:        dbConn,
		logger:    logger,
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	s.srv = &http.Server{
		Addr:         cfg.Addr,
		Handler:      s.loggingMiddleware(mux),
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	return s, nil
}

func (s *Server) Start() error {
	s.logger.Printf("api: starting server on %s", s.srv.Addr)
	err := s.srv.ListenAndServe()
	if err == http.ErrServerClosed {
		s.logger.Printf("api: server stopped")
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Print("api: shutting down server")
	if err := s.srv.Shutdown(ctx); err != nil {
		s.logger.Printf("api: error during server shutdown: %v", err)
		return err
	}
	if s.db != nil {
		if err := s.db.Close(); err != nil {
			s.logger.Printf("api: error closing database: %v", err)
		} else {
			s.logger.Print("api: database connection closed")
		}
	}
	return nil
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", s.healthHandler)
	mux.HandleFunc("/readyz", s.readyHandler)

	mux.HandleFunc("/v1/poll/runs", s.listRunsToPollHandler)
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// attempts to ping the databas
func (s *Server) readyHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if s.db == nil {
		http.Error(w, "no database configured", http.StatusServiceUnavailable)
		return
	}
	if err := s.db.PingContext(ctx); err != nil {
		s.logger.Printf("api: readiness check failed: %v", err)
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// listRunsToPollHandler returns a JSON array of runs that the poller would
// attempt to poll given the provided time and thresholds. It relies on the
// existing sqlc-generated query ListRunsToPoll.
func (s *Server) listRunsToPollHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	// parse `now` param (accept RFC3339 or the familiar SQL datetime layout).
	var now time.Time
	if nowStr := q.Get("now"); nowStr != "" {
		var err error
		now, err = time.Parse(time.RFC3339, nowStr)
		if err != nil {
			now, err = time.Parse(time.DateTime, nowStr)
			if err != nil {
				http.Error(w, "invalid 'now' parameter; use RFC3339 or '2006-01-02 15:04:05'", http.StatusBadRequest)
				return
			}
		}
	} else {
		now = time.Now()
	}

	// thresholds (allow overriding via query params)
	staticThres := int64(s.pollerCfg.StaticErrorThreshold)
	if v := q.Get("static_threshold"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			staticThres = parsed
		} else {
			http.Error(w, "'static_threshold' must be an integer", http.StatusBadRequest)
			return
		}
	}

	totalThres := int64(s.pollerCfg.TotalErrorThreshold)
	if v := q.Get("total_threshold"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			totalThres = parsed
		} else {
			http.Error(w, "'total_threshold' must be an integer", http.StatusBadRequest)
			return
		}
	}

	rows, err := s.queries.ListRunsToPoll(ctx, db.ListRunsToPollParams{
		NowTs:                   now.Format(time.DateTime),
		StaticResponseThreshold: staticThres,
		TotalErrorThreshold:     totalThres,
	})
	if err != nil {
		s.logger.Printf("api: failed to list runs to poll: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, rows)
}

// loggingMiddleware instruments requests with simple structured logging and
// recovers from panics so one handler cannot take down the server.
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 0}

		defer func() {
			// recover from panics in handlers
			if recov := recover(); recov != nil {
				s.logger.Printf("api: panic recovered: %v", recov)
				http.Error(rec, "internal server error", http.StatusInternalServerError)
			}
			// log request outcome
			s.logger.Printf("%s %s %d %d %s", r.Method, r.URL.Path, rec.status, rec.written, time.Since(start))
		}()

		next.ServeHTTP(rec, r)
	})
}

// statusRecorder is an http.ResponseWriter wrapper that captures status and
// number of bytes written for logging purposes.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written int64
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		// default status if WriteHeader wasn't called explicitly
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.written += int64(n)
	return n, err
}

// writeJSON is a small helper for consistent JSON responses.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// best-effort encode; in the event of error there's not much we can do
	_ = json.NewEncoder(w).Encode(v)
}
