package httpserver

import (
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
)

// ipAllowed reports whether client IP matches any entry (exact IP or CIDR).
// Empty allow list = allow all.
func ipAllowed(client string, allow []string) bool {
	if len(allow) == 0 {
		return true
	}
	client = strings.TrimSpace(client)
	ip := net.ParseIP(client)
	for _, a := range allow {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if a == client {
			return true
		}
		if ip != nil {
			if strings.Contains(a, "/") {
				_, netw, err := net.ParseCIDR(a)
				if err == nil && netw.Contains(ip) {
					return true
				}
			} else if aip := net.ParseIP(a); aip != nil && aip.Equal(ip) {
				return true
			}
		}
	}
	return false
}

const headerRequestID = "X-Request-ID"

// withRequestID ensures every request has an X-Request-ID: reuse the client
// value when present, otherwise generate one. Always sets the response header.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get(headerRequestID))
		if id == "" {
			id = uuid.NewString()
			r.Header.Set(headerRequestID, id)
		}
		w.Header().Set(headerRequestID, id)
		next.ServeHTTP(w, r)
	})
}

// withSecurityHeaders adds conservative browser security headers on every response.
// hsts enables Strict-Transport-Security (only enable when the API is served over HTTPS).
func withSecurityHeaders(hsts bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		// API is not meant to be framed or cached as HTML; avoid long-lived caches of JSON.
		if h.Get("Cache-Control") == "" {
			h.Set("Cache-Control", "no-store")
		}
		if hsts {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// requireJSON returns false and writes 415 if Content-Type is not application/json
// for methods that typically carry a JSON body.
func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
	default:
		return true
	}
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		// allow empty for tiny clients; decoder still works
		return true
	}
	// Accept application/json and application/json; charset=utf-8
	base := strings.TrimSpace(strings.Split(ct, ";")[0])
	if !strings.EqualFold(base, "application/json") {
		writeErr(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	return true
}

// withMaxBody caps request body size (DoS guard). zero → 1 MiB.
func withMaxBody(max int64, next http.Handler) http.Handler {
	if max <= 0 {
		max = 1 << 20
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
			r.Body = http.MaxBytesReader(w, r.Body, max)
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP extracts a best-effort client address for auth rate limiting.
// Uses RemoteAddr only (not X-Forwarded-For) unless AI_CLOUDHUB_TRUST_PROXY=1.
func clientIP(r *http.Request) string {
	if strings.TrimSpace(os.Getenv("AI_CLOUDHUB_TRUST_PROXY")) == "1" {
		if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
			// first hop
			if i := strings.IndexByte(xff, ','); i >= 0 {
				xff = strings.TrimSpace(xff[:i])
			}
			if xff != "" {
				return xff
			}
		}
		if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
			return xri
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// withCORS enables CORS when AI_CLOUDHUB_CORS_ORIGINS is set (comma-separated;
// "*" allows any origin). When the env is empty, next is returned unchanged.
func withCORS(next http.Handler) http.Handler {
	raw := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_CORS_ORIGINS"))
	if raw == "" {
		return next
	}
	parts := strings.Split(raw, ",")
	allowed := make([]string, 0, len(parts))
	allowAll := false
	for _, p := range parts {
		o := strings.TrimSpace(p)
		if o == "" {
			continue
		}
		if o == "*" {
			allowAll = true
		}
		allowed = append(allowed, o)
	}
	if len(allowed) == 0 {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowAll {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if origin != "" && originAllowed(origin, allowed) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func originAllowed(origin string, allowed []string) bool {
	for _, a := range allowed {
		if a == origin {
			return true
		}
	}
	return false
}
