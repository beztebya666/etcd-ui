package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestACL_PassThroughWhenEmpty(t *testing.T) {
	a := NewACL()
	called := false
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(200)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/clusters/prod/range", nil)
	h.ServeHTTP(rec, req)
	if !called || rec.Code != 200 {
		t.Fatalf("empty ACL should pass-through: code=%d called=%v", rec.Code, called)
	}
}

func TestACL_LongestPrefixWins(t *testing.T) {
	a := NewACL()
	if err := a.LoadJSON([]byte(`[
		{"user":"alice","cluster":"prod","access":"read"},
		{"user":"alice","cluster":"prod","prefix":"/svc/","access":"write"},
		{"user":"alice","cluster":"prod","prefix":"/svc/critical/","access":"admin"}
	]`)); err != nil {
		t.Fatal(err)
	}
	// Within /svc/critical/ alice has admin.
	if got := a.Resolve("alice", "prod", "/svc/critical/x"); got != AccessAdmin {
		t.Errorf("svc/critical -> %q, want admin", got)
	}
	// Within /svc/ but outside /svc/critical/ alice has write.
	if got := a.Resolve("alice", "prod", "/svc/other"); got != AccessWrite {
		t.Errorf("svc/other -> %q, want write", got)
	}
	// Outside /svc/ alice has only read.
	if got := a.Resolve("alice", "prod", "/etc/whatever"); got != AccessRead {
		t.Errorf("/etc -> %q, want read", got)
	}
	// Different user — no rule.
	if got := a.Resolve("eve", "prod", "/"); got != "" {
		t.Errorf("eve has no rule, got %q", got)
	}
}

func TestACL_WildcardCluster(t *testing.T) {
	a := NewACL()
	_ = a.LoadJSON([]byte(`[{"user":"oncall","cluster":"*","access":"admin"}]`))
	if got := a.Resolve("oncall", "prod", "/anything"); got != AccessAdmin {
		t.Errorf("wildcard cluster: got %q, want admin", got)
	}
	if got := a.Resolve("oncall", "staging", "/anything"); got != AccessAdmin {
		t.Errorf("wildcard cluster across env: got %q, want admin", got)
	}
}

func TestACL_Middleware_DeniesUnauthorizedWrite(t *testing.T) {
	a := NewACL()
	_ = a.LoadJSON([]byte(`[{"user":"alice","cluster":"prod","access":"read"}]`))
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))

	// POST under prod — write needed, alice only has read. Expect 403.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/clusters/prod/put", nil)
	req.Header.Set("X-Etcd-UI-User", "alice")
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("expected 403 for POST with read-only ACL, got %d", rec.Code)
	}
	// GET on the same cluster — allowed.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/clusters/prod/summary", nil)
	req.Header.Set("X-Etcd-UI-User", "alice")
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected 200 for GET with read ACL, got %d", rec.Code)
	}
}

func TestACL_LoadJSON_RejectsBadAccess(t *testing.T) {
	a := NewACL()
	if err := a.LoadJSON([]byte(`[{"user":"u","cluster":"c","access":"sudo"}]`)); err == nil {
		t.Fatal("expected error for invalid access value")
	}
}
