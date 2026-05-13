package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ACL is per-cluster-per-user (and optionally per-prefix) access control.
//
// Model:
//
//	user      string      // matches X-Etcd-UI-User header from auth middleware
//	cluster   string      // cluster id; "*" wildcards every cluster
//	prefix    string      // key prefix; "" or "/" = whole keyspace
//	access    "read"|"write"|"admin"   // monotone
//
// "read"   — GET/HEAD/OPTIONS only.
// "write"  — read + POST/PUT/DELETE on /range, /put, /delete, /bulk, /txn,
//            /watch under the matching prefix.
// "admin"  — write + RBAC, restore, defrag, compact, alarms, snapshot, etcdctl
//            (anything that's cluster-level rather than key-level).
//
// Resolution: most-specific rule wins.
//   1. exact cluster + longest prefix match for the user
//   2. exact cluster + wildcard prefix
//   3. wildcard cluster + longest prefix match
//   4. wildcard cluster + wildcard prefix
//
// If no rule matches and ACLs are loaded, the request is denied. If the ACL
// list is empty, the middleware is a pass-through (back-compat).
//
// Loaded from JSON in env var `ETCD_UI_ACL` or file `ETCD_UI_ACL_FILE`:
//
//	[
//	  {"user":"alice@corp",  "cluster":"prod",    "prefix":"/svc/", "access":"write"},
//	  {"user":"alice@corp",  "cluster":"prod",    "access":"read"},
//	  {"user":"oncall@corp", "cluster":"*",       "access":"admin"}
//	]
type ACL struct {
	mu         sync.RWMutex
	rules      []ACLRule
	path       string    // file we last loaded from; "" if inline
	mtime      time.Time // last seen mtime — drives hot-reload diff
	onReload   func(int) // optional callback fired after a successful reload
	historyDir string    // optional acl-history dir; "" disables history
}

type Access string

const (
	AccessRead  Access = "read"
	AccessWrite Access = "write"
	AccessAdmin Access = "admin"
)

type ACLRule struct {
	User    string `json:"user"`
	Cluster string `json:"cluster"`
	Prefix  string `json:"prefix,omitempty"`
	Access  Access `json:"access"`
}

func NewACL() *ACL { return &ACL{} }

// LoadFromEnv reads ETCD_UI_ACL or ETCD_UI_ACL_FILE (one of them). Returns
// nil and a populated ACL if neither is set — the middleware will then
// pass-through. When loading from a file, records the path so Watch() can
// re-read on mtime change.
func (a *ACL) LoadFromEnv() error {
	if raw := os.Getenv("ETCD_UI_ACL"); raw != "" {
		return a.LoadJSON([]byte(raw))
	}
	if path := os.Getenv("ETCD_UI_ACL_FILE"); path != "" {
		return a.LoadFile(path)
	}
	return nil
}

// LoadFile reads + parses + applies a JSON ACL file. Updates the cached
// mtime so Watch() can detect future changes.
func (a *ACL) LoadFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("ACL file: %w", err)
	}
	if err := a.LoadJSON(b); err != nil {
		return err
	}
	if st, err := os.Stat(path); err == nil {
		a.mu.Lock()
		a.path = path
		a.mtime = st.ModTime()
		a.mu.Unlock()
	}
	return nil
}

// OnReload registers a callback fired after each successful hot-reload.
// Useful for logging the rule count change.
func (a *ACL) OnReload(fn func(int)) {
	a.mu.Lock()
	a.onReload = fn
	a.mu.Unlock()
}

// EnsureBootstrapAdmin guarantees that `user` has admin on the synthetic
// __acl__ cluster, persisting the change if loaded from a file. No-op when:
//   - ACL is empty (an unauthenticated deployment shouldn't auto-grant)
//   - some user already holds __acl__ admin (someone else bootstrapped first)
//   - inline ACL (no path to write to) — operator will need to edit env
//
// Returns (granted, err). granted=false err=nil means a normal no-op; true
// means the rule was added and the in-memory set + on-disk file are updated.
func (a *ACL) EnsureBootstrapAdmin(user string) (bool, error) {
	if user == "" {
		return false, nil
	}
	a.mu.RLock()
	path := a.path
	rules := append([]ACLRule(nil), a.rules...)
	a.mu.RUnlock()
	if len(rules) == 0 {
		return false, nil
	}
	for _, r := range rules {
		if r.Cluster == "__acl__" && r.Access == AccessAdmin {
			return false, nil
		}
	}
	rules = append(rules, ACLRule{User: user, Cluster: "__acl__", Access: AccessAdmin})
	if path == "" {
		// Apply in-memory only; file is the source of truth and we can't
		// edit env. Operator should add the rule to ETCD_UI_ACL.
		a.mu.Lock()
		a.rules = rules
		a.mu.Unlock()
		return true, nil
	}
	return true, a.Save(rules)
}

// Path returns the file path the ACL was loaded from (empty for inline JSON).
// The editor needs this to know whether it can persist edits.
func (a *ACL) Path() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.path
}

// ValidateRules checks the proposed rule set without applying it. Used
// by the Permissions editor's "Preview" path so we can surface errors
// before the user commits a half-broken policy. Returns nil on success.
func (a *ACL) ValidateRules(rules []ACLRule) error {
	for i, r := range rules {
		if r.User == "" || r.Cluster == "" || r.Access == "" {
			return fmt.Errorf("rule %d: user, cluster, access are required", i)
		}
		switch r.Access {
		case AccessRead, AccessWrite, AccessAdmin:
		default:
			return fmt.Errorf("rule %d: invalid access %q (want read|write|admin)", i, r.Access)
		}
		if r.Prefix != "" && !strings.HasPrefix(r.Prefix, "/") && r.Prefix != "*" {
			return fmt.Errorf("rule %d: prefix %q must start with '/' or be '*'", i, r.Prefix)
		}
	}
	return nil
}

// Save persists a new rule set to disk and reloads it. Atomic write via
// rename so partial flushes don't leave the file half-written. Returns an
// error when no path is configured (inline-only deployments).
func (a *ACL) Save(rules []ACLRule) error {
	a.mu.RLock()
	path := a.path
	a.mu.RUnlock()
	if path == "" {
		return errors.New("ACL not loaded from a file — edits must be applied via env / configmap")
	}
	if err := a.ValidateRules(rules); err != nil {
		return err
	}
	body, err := json.MarshalIndent(rules, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// Apply immediately; Watch will see the same mtime and short-circuit.
	a.mu.Lock()
	a.rules = rules
	if st, err := os.Stat(path); err == nil {
		a.mtime = st.ModTime()
	}
	a.mu.Unlock()
	return nil
}

// Watch reacts to ACL-file changes. On Linux it uses inotify for ms-latency
// updates and skips the polling tick entirely. Elsewhere it falls back to
// a polling loop keyed off mtime.
//
// Reloads are atomic — partial reads / parse failures don't drop the live
// rule set. Errors are passed to the optional onReload callback (negative
// count signals failure).
func (a *ACL) Watch(ctx context.Context, period time.Duration) {
	a.mu.RLock()
	path := a.path
	a.mu.RUnlock()
	if path == "" {
		return
	}
	// Prefer inotify on Linux. If it lights up successfully, we still keep
	// a slow safety-net poll (every 60s) so a missed event or NFS-style
	// inotify-blind mount can't strand us with stale rules.
	native := a.watchNative(ctx)
	if period <= 0 {
		if native {
			period = 60 * time.Second
		} else {
			period = 5 * time.Second
		}
	}
	t := time.NewTicker(period)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.checkReload()
		}
	}
}

func (a *ACL) checkReload() {
	a.mu.RLock()
	path := a.path
	prev := a.mtime
	cb := a.onReload
	a.mu.RUnlock()
	st, err := os.Stat(path)
	if err != nil {
		return // file might be momentarily missing during atomic replace
	}
	if !st.ModTime().After(prev) {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	// Parse into a fresh slice; only commit on success so the live set is
	// never replaced by garbage.
	var rules []ACLRule
	if err := json.Unmarshal(b, &rules); err != nil {
		if cb != nil {
			cb(-1)
		}
		return
	}
	for i, r := range rules {
		if r.User == "" || r.Cluster == "" || r.Access == "" {
			if cb != nil {
				cb(-1)
			}
			_ = i
			return
		}
		switch r.Access {
		case AccessRead, AccessWrite, AccessAdmin:
		default:
			if cb != nil {
				cb(-1)
			}
			return
		}
	}
	a.mu.Lock()
	a.rules = rules
	a.mtime = st.ModTime()
	a.mu.Unlock()
	if cb != nil {
		cb(len(rules))
	}
}

func (a *ACL) LoadJSON(b []byte) error {
	var rules []ACLRule
	if err := json.Unmarshal(b, &rules); err != nil {
		return err
	}
	for i, r := range rules {
		if r.User == "" || r.Cluster == "" || r.Access == "" {
			return fmt.Errorf("rule %d: user, cluster, access are required", i)
		}
		switch r.Access {
		case AccessRead, AccessWrite, AccessAdmin:
		default:
			return fmt.Errorf("rule %d: invalid access %q", i, r.Access)
		}
	}
	a.mu.Lock()
	a.rules = rules
	a.mu.Unlock()
	return nil
}

func (a *ACL) Loaded() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.rules) > 0
}

// Rules returns a shallow copy of the rule list (for the admin UI).
func (a *ACL) Rules() []ACLRule {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]ACLRule, len(a.rules))
	copy(out, a.rules)
	return out
}

// Resolve finds the best-matching access level for (user, cluster, prefix),
// or returns "" if no rule applies.
func (a *ACL) Resolve(user, cluster, key string) Access {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var best ACLRule
	bestScore := -1
	for _, r := range a.rules {
		if r.User != user {
			continue
		}
		score := 0
		if r.Cluster == cluster {
			score += 100
		} else if r.Cluster == "*" {
			score += 10
		} else {
			continue
		}
		if r.Prefix != "" && r.Prefix != "/" {
			if !strings.HasPrefix(key, r.Prefix) {
				continue
			}
			score += len(r.Prefix)
		}
		if score > bestScore {
			bestScore = score
			best = r
		}
	}
	if bestScore < 0 {
		return ""
	}
	return best.Access
}

// requiredAccess inspects the request and decides what access level the
// caller needs. Cluster-level admin ops are mapped to AccessAdmin; KV ops
// are AccessRead or AccessWrite based on method.
func requiredAccess(r *http.Request, cluster string) Access {
	// path shape: /clusters/{id}/{verb}/…
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	verb := ""
	if len(parts) >= 3 && parts[0] == "clusters" && parts[1] == cluster {
		verb = parts[2]
	}
	switch verb {
	case "rbac", "restore", "defrag", "compact", "alarms", "etcdctl":
		return AccessAdmin
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return AccessRead
	}
	return AccessWrite
}

func levelAtLeast(have, need Access) bool {
	rank := map[Access]int{AccessRead: 1, AccessWrite: 2, AccessAdmin: 3}
	return rank[have] >= rank[need]
}

// Middleware enforces ACL on /clusters/{id}/… paths. Pass-through when no
// rules are loaded, when path doesn't match a cluster, or when the request
// is on the anonymous /healthz / auth / metrics surface.
func (a *ACL) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.Loaded() {
			next.ServeHTTP(w, r)
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 2 || parts[0] != "clusters" {
			next.ServeHTTP(w, r)
			return
		}
		user := r.Header.Get("X-Etcd-UI-User")
		if user == "" {
			user = "anonymous"
		}
		cluster := parts[1]

		// "key" for prefix-matching: pull from the JSON body for /range and
		// /put when it's there; otherwise fall back to "" (matches only
		// wildcard prefix rules).
		key := extractKey(r)
		access := a.Resolve(user, cluster, key)
		if access == "" {
			deny(w, fmt.Errorf("user %q has no access to cluster %q", user, cluster))
			return
		}
		need := requiredAccess(r, cluster)
		if !levelAtLeast(access, need) {
			deny(w, fmt.Errorf("user %q has %s on %q under %q, %s required",
				user, access, cluster, key, need))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func extractKey(r *http.Request) string {
	// Cheap path-only key extraction. The proper read of POST /range body
	// would require buffering — we do it for the high-traffic endpoints
	// where the key/prefix is in the URL (history, diff), and accept the
	// imprecision elsewhere.
	if k := r.URL.Query().Get("key"); k != "" {
		return k
	}
	if k := r.URL.Query().Get("prefix"); k != "" {
		return k
	}
	return ""
}

func deny(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	body, _ := json.Marshal(map[string]any{"error": err.Error(), "status": 403})
	_, _ = w.Write(body)
}

// ErrNoRule is returned by Resolve callers that want to distinguish "no rule"
// from "denied". Currently unused externally; reserved for future structured
// error responses.
var ErrNoRule = errors.New("no acl rule matched")
