package httpx

import (
	"net/http"
	"strings"
	"sync"
)

// ReadonlyGuard refuses mutating requests against any cluster listed in the
// configured set (or any cluster whose own ReadOnly flag is true). Cluster
// IDs are extracted from the URL path:  /clusters/{id}/...
type ReadonlyGuard struct {
	mu       sync.RWMutex
	clusters map[string]bool
}

func NewReadonlyGuard(initial []string) *ReadonlyGuard {
	g := &ReadonlyGuard{clusters: map[string]bool{}}
	for _, id := range initial {
		g.clusters[id] = true
	}
	return g
}

// Set replaces the read-only cluster set atomically.
func (g *ReadonlyGuard) Set(ids []string) {
	next := make(map[string]bool, len(ids))
	for _, id := range ids {
		next[id] = true
	}
	g.mu.Lock()
	g.clusters = next
	g.mu.Unlock()
}

func (g *ReadonlyGuard) IsReadOnly(id string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.clusters[id]
}

func (g *ReadonlyGuard) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.Method
		if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		// path shapes: /clusters/{id}/...
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) >= 2 && parts[0] == "clusters" {
			if g.IsReadOnly(parts[1]) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"cluster is read-only","status":403}`))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
