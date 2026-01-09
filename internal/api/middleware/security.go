package middleware

import (
	"net/http"
)

func Security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prevent site from being embedded in frames (Clickjacking protection)
		w.Header().Set("X-Frame-Options", "DENY")

		// Prevent browsers from sniffing MIME types away from the declared Content-Type
		w.Header().Set("X-Content-Type-Options", "nosniff")

		// Enable browser XSS filtering
		w.Header().Set("X-XSS-Protection", "1; mode=block")

		// Content Security Policy: restrictive by default
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none';")

		// Referrer policy: do not leak information to other sites
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Strict Transport Security (HSTS): only if on HTTPS (usually handled by proxy/load balancer)
		// w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")

		next.ServeHTTP(w, r)
	})
}
