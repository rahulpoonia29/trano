package middleware

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

// wrap an http.ResponseWriter to track response status and size.
type StatusRecorder struct {
	http.ResponseWriter
	Status    int
	Written   int64
	Start     time.Time
	RequestID string
}

// captures the status code and injects tracing headers.
func (r *StatusRecorder) WriteHeader(status int) {
	r.Status = status
	r.ResponseWriter.Header().Set("X-Request-ID", r.RequestID)
	r.ResponseWriter.Header().Set("X-Processing-Time", time.Since(r.Start).String())
	r.ResponseWriter.WriteHeader(status)
}

func (r *StatusRecorder) Write(b []byte) (int, error) {
	if r.Status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(b)
	r.Written += int64(n)
	return n, err
}

func Logging(logger *log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" {
				requestID = fmt.Sprintf("req_%d", start.UnixNano())
			}

			rec := &StatusRecorder{
				ResponseWriter: w,
				Start:          start,
				RequestID:      requestID,
			}

			defer func() {
				if err := recover(); err != nil {
					logger.Printf("PANIC [%s] %s %s: %v", requestID, r.Method, r.URL.Path, err)
					http.Error(rec, "Internal Server Error", http.StatusInternalServerError)
				}

				// Log request details
				logger.Printf("[%s] %s %s %d %d bytes %s",
					requestID, r.Method, r.URL.Path, rec.Status, rec.Written, time.Since(start))
			}()

			next.ServeHTTP(rec, r)
		})
	}
}
