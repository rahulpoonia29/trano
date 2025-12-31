package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
	"trano/internal/config"
	dbutil "trano/internal/db"
	db "trano/internal/db/sqlc"
	"trano/internal/poller"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
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

	r := chi.NewRouter()
	s.registerRoutes(r)

	s.srv = &http.Server{
		Addr:         cfg.Addr,
		Handler:      s.loggingMiddleware(r),
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

func (s *Server) registerRoutes(r chi.Router) {
	// CORS middleware
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"http://localhost:5173", "http://localhost:3000"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	r.Get("/healthz", s.healthHandler)

	r.Get("/v1/runs/{train_no}/{run_date}", s.getRunHandler)
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := fmt.Sprintf("%d", start.UnixNano())
		rec := &statusRecorder{ResponseWriter: w, status: 0, start: start, requestID: requestID}

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

// http.ResponseWriter wrapper that captures status and number of bytes written for logging
type statusRecorder struct {
	http.ResponseWriter
	status    int
	written   int64
	start     time.Time
	requestID string
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	// Set response headers
	processingTime := time.Since(r.start)
	r.ResponseWriter.Header().Set("X-Processing-Time", processingTime.String())
	r.ResponseWriter.Header().Set("X-Request-ID", r.requestID)
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

// helper for consistent JSON responses.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// best-effort encode; in the event of error there's not much we can do
	_ = json.NewEncoder(w).Encode(v)
}
