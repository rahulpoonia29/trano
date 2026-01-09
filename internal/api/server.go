package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"trano/internal/api/handlers"
	"trano/internal/api/middleware"
	"trano/internal/config"
	dbutil "trano/internal/db"
	db "trano/internal/db/sqlc"
	"trano/internal/poller"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

type Server struct {
	cfg    config.ServerConfig
	logger *log.Logger
	db     *sql.DB
	srv    *http.Server

	// Handlers
	trainHandler *handlers.TrainHandler
}

func NewServer(cfg config.ServerConfig, dbCfg config.DatabaseConfig, pollerCfg poller.Config, logger *log.Logger) (*Server, error) {
	dbConn, err := dbutil.OpenDatabase(dbCfg, dbutil.DefaultDatabaseOptions(), logger)
	if err != nil {
		return nil, err
	}
	queries := db.New(dbConn)

	trainHandler := handlers.NewTrainHandler(queries, dbConn, logger)

	s := &Server{
		cfg:          cfg,
		logger:       logger,
		db:           dbConn,
		trainHandler: trainHandler,
	}

	r := chi.NewRouter()
	s.setupMiddleware(r)
	s.registerRoutes(r)

	s.srv = &http.Server{
		Addr:         cfg.Addr,
		Handler:      r,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	return s, nil
}

// configures the global middleware stack
func (s *Server) setupMiddleware(r chi.Router) {
	r.Use(chiMiddleware.Recoverer)
	r.Use(chiMiddleware.RealIP)

	r.Use(middleware.Logging(s.logger))
	r.Use(middleware.Security)

	// CORS
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"http://localhost:5173", "http://localhost:3000", "https://trano-frontend.vercel.app"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token", "X-Request-ID"},
		ExposedHeaders:   []string{"Link", "X-Request-ID", "X-Processing-Time"},
		AllowCredentials: true,
		MaxAge:           300,
	}))
}

func (s *Server) registerRoutes(r chi.Router) {
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		s.writeJSON(w, http.StatusOK, map[string]string{
			"status":    "ok",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	})

	r.Route("/v1", func(r chi.Router) {
		r.Get("/trains/live", s.trainHandler.GetLiveTrains)
	})
}

func (s *Server) Start() error {
	s.logger.Printf("api: starting server on %s", s.srv.Addr)
	if err := s.srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Print("api: shutting down server...")

	if err := s.srv.Shutdown(ctx); err != nil {
		s.logger.Printf("api: server shutdown error: %v", err)
	}

	if s.db != nil {
		if err := s.db.Close(); err != nil {
			s.logger.Printf("api: database close error: %v", err)
		} else {
			s.logger.Print("api: database connection closed")
		}
	}

	return nil
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logger.Printf("api: failed to encode response: %v", err)
	}
}
