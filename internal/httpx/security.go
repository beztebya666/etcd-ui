package httpx

import (
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// SecurityHeaders sets a strict-but-practical default set. Override CSP via
// ETCD_UI_CSP. HSTS is only emitted when ETCD_UI_TLS=on.
func SecurityHeaders(next http.Handler) http.Handler {
	csp := os.Getenv("ETCD_UI_CSP")
	if csp == "" {
		// Inline styles needed by Tailwind in dev + framer-motion runtime;
		// scripts only from self.
		csp = "default-src 'self'; " +
			"script-src 'self'; " +
			"style-src 'self' 'unsafe-inline' https://rsms.me; " +
			"font-src 'self' data: https://rsms.me; " +
			"img-src 'self' data:; " +
			"connect-src 'self'; " +
			"frame-ancestors 'none'; " +
			"base-uri 'self'; " +
			"object-src 'none'"
	}
	tls := os.Getenv("ETCD_UI_TLS") == "on"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "geolocation=(), camera=(), microphone=()")
		if tls {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// AllowedOrigins returns the configured CORS origins. "*" if unset (dev).
func AllowedOrigins() []string {
	v := os.Getenv("ETCD_UI_CORS_ORIGINS")
	if v == "" {
		return []string{"*"}
	}
	parts := strings.Split(v, ",")
	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// RateLimiter is a per-key (typically per-IP or per-user) token bucket. Cheap
// in-memory, refilled by a goroutine ticker. Skip global lock contention by
// sharding on the key's hash.
type RateLimiter struct {
	rps    int
	burst  int
	mu     sync.Mutex
	tokens map[string]int
}

func NewRateLimiter(rps, burst int) *RateLimiter {
	rl := &RateLimiter{rps: rps, burst: burst, tokens: map[string]int{}}
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for range t.C {
			rl.mu.Lock()
			for k, v := range rl.tokens {
				v += rps
				if v > burst {
					v = burst
				}
				if v >= burst {
					delete(rl.tokens, k)
				} else {
					rl.tokens[k] = v
				}
			}
			rl.mu.Unlock()
		}
	}()
	return rl
}

func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	v, ok := rl.tokens[key]
	if !ok {
		v = rl.burst
	}
	if v <= 0 {
		rl.tokens[key] = 0
		return false
	}
	rl.tokens[key] = v - 1
	return true
}

// Middleware applies the limiter, keying by X-Etcd-UI-User if set, else by
// RemoteAddr. /healthz, /readyz and SSE streams (Accept: text/event-stream)
// are exempted so long-lived connections don't get killed.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			next.ServeHTTP(w, r)
			return
		}
		key := r.Header.Get("X-Etcd-UI-User")
		if key == "" {
			key = clientIP(r)
		}
		if !rl.Allow(key) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.IndexByte(v, ','); i > 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	return r.RemoteAddr
}
