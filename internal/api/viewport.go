package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"
	db "trano/internal/db/sqlc"
)

type TrainViewportResponse struct {
	Trains    []db.GetTrainsInViewportRow `json:"trains"`
	Count     int                         `json:"count"`
	Bounds    ViewportBounds              `json:"bounds"`
	Timestamp string                      `json:"timestamp"`
}

type ViewportBounds struct {
	MinLat float64 `json:"min_lat"`
	MaxLat float64 `json:"max_lat"`
	MinLng float64 `json:"min_lng"`
	MaxLng float64 `json:"max_lng"`
}

// GET /v1/trains/viewport?min_lat={lat}&max_lat={lat}&min_lng={lng}&max_lng={lng}&buffer={deg}
func (s *Server) getTrainsInViewportHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query()

	// Parse required bounds parameters
	minLat, err := parseFloat(query.Get("min_lat"), "min_lat")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	maxLat, err := parseFloat(query.Get("max_lat"), "max_lat")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	minLng, err := parseFloat(query.Get("min_lng"), "min_lng")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	maxLng, err := parseFloat(query.Get("max_lng"), "max_lng")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Optional buffer (in degrees, default 0.5° ≈ 55km)
	buffer := 0.5
	if bufferStr := query.Get("buffer"); bufferStr != "" {
		if b, err := strconv.ParseFloat(bufferStr, 64); err == nil && b >= 0 && b <= 5 {
			buffer = b
		}
	}

	// Apply buffer to bounds
	minLat -= buffer
	maxLat += buffer
	minLng -= buffer
	maxLng += buffer

	// Validate bounds
	if minLat < -90 || maxLat > 90 || minLng < -180 || maxLng > 180 {
		http.Error(w, "invalid coordinates", http.StatusBadRequest)
		return
	}
	if minLat >= maxLat || minLng >= maxLng {
		http.Error(w, "min values must be less than max values", http.StatusBadRequest)
		return
	}

	// Convert to u6 encoding (multiply by 1e6)
	minLatU6 := int64(minLat * 1e6)
	maxLatU6 := int64(maxLat * 1e6)
	minLngU6 := int64(minLng * 1e6)
	maxLngU6 := int64(maxLng * 1e6)

	// Query database
	trains, err := s.queries.GetTrainsInViewport(ctx, db.GetTrainsInViewportParams{
		MinLatU6: sql.NullInt64{Int64: minLatU6, Valid: true},
		MaxLatU6: sql.NullInt64{Int64: maxLatU6, Valid: true},
		MinLngU6: sql.NullInt64{Int64: minLngU6, Valid: true},
		MaxLngU6: sql.NullInt64{Int64: maxLngU6, Valid: true},
	})

	if err != nil {
		s.logger.Printf("api: viewport query failed: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Build response
	resp := TrainViewportResponse{
		Trains: trains,
		Count:  len(trains),
		Bounds: ViewportBounds{
			MinLat: minLat,
			MaxLat: maxLat,
			MinLng: minLng,
			MaxLng: maxLng,
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	writeJSON(w, http.StatusOK, resp)
}

// Helper to parse float from query param
func parseFloat(s, name string) (float64, error) {
	if s == "" {
		return 0, errors.New(name + " is required")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, errors.New("invalid " + name)
	}
	return f, nil
}
