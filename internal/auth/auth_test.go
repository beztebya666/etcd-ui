package auth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestBasicAuth_AcceptsPlainCreds(t *testing.T) {
	t.Setenv("AUTH_USERS", "alice:plain:hunter2")
	a := New()
	if !a.Enabled() {
		t.Fatal("auth should be enabled when AUTH_USERS is set")
	}
	if !a.checkBasic("alice", "hunter2") {
		t.Error("expected hunter2 to match")
	}
	if a.checkBasic("alice", "wrong") {
		t.Error("expected wrong password to fail")
	}
	if a.checkBasic("eve", "hunter2") {
		t.Error("expected unknown user to fail")
	}
}

func TestMiddleware_PublicPathsBypassAuth(t *testing.T) {
	t.Setenv("AUTH_USERS", "alice:plain:hunter2")
	a := New()
	called := false
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(200)
	}))
	for _, p := range []string{"/healthz", "/readyz", "/metrics", "/", "/static/app.js", "/api/auth/login"} {
		called = false
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", p, nil)
		h.ServeHTTP(rec, req)
		if !called {
			t.Errorf("public path %q got %d, expected pass-through", p, rec.Code)
		}
	}
}

func TestMiddleware_ProtectedPathRequiresAuth(t *testing.T) {
	t.Setenv("AUTH_USERS", "alice:plain:hunter2")
	a := New()
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/clusters", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestMiddleware_BasicCredsIssueSessionCookie(t *testing.T) {
	t.Setenv("AUTH_USERS", "alice:plain:hunter2")
	a := New()
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/clusters", nil)
	req.SetBasicAuth("alice", "hunter2")
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "etcd-ui-session" && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected etcd-ui-session cookie to be issued after successful Basic auth")
	}
}

func TestOAuth_FlowCookieRoundTrip(t *testing.T) {
	// Construct a verifier in disabled mode so OAuth.Enabled() is false; we
	// still want to exercise the signFlow/verifyFlow round-trip directly.
	o := &OAuth{stateSecret: []byte("test-secret-test-secret-test-x")}
	in := flowPayload{Verifier: "v", State: "s", ReturnTo: "/", Exp: 1 << 40}
	sig := o.signFlow(in)
	out, err := o.verifyFlow(sig)
	if err != nil {
		t.Fatalf("verifyFlow: %v", err)
	}
	if out.Verifier != "v" || out.State != "s" || out.ReturnTo != "/" {
		t.Fatalf("round trip mismatch: %+v", out)
	}

	// Tamper-evident: flipping a char in the signature breaks it.
	bad := sig[:len(sig)-1] + "X"
	if _, err := o.verifyFlow(bad); err == nil {
		t.Fatal("expected tampered cookie to fail verifyFlow")
	}
}

func TestSanitiseReturnTo_RejectsOpenRedirect(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", "/"},
		{"/dashboard?x=1", "/dashboard?x=1"},
		{"//evil.com", "/"},
		{"https://evil.com", "/"},
		{"javascript:alert(1)", "/"},
	} {
		got := sanitiseReturnTo(c.in)
		if got != c.want {
			t.Errorf("sanitiseReturnTo(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestOIDCVerifier_ClaimsParsing(t *testing.T) {
	// Audience matcher unit test — exercise both single-string and array forms.
	if !audienceMatches("etcd-ui", "etcd-ui") {
		t.Error("string aud match should pass")
	}
	if !audienceMatches([]any{"a", "etcd-ui", "b"}, "etcd-ui") {
		t.Error("array aud match should pass")
	}
	if audienceMatches("other", "etcd-ui") {
		t.Error("non-matching string should fail")
	}
}

// Sanity check that env unset → New() still returns a working object that
// pass-throughs traffic.
func TestNew_AuthDisabled(t *testing.T) {
	os.Unsetenv("AUTH_USERS")
	os.Unsetenv("ETCD_UI_OIDC_ISSUER")
	a := New()
	if a.Enabled() {
		t.Fatal("auth should be disabled when no env is set")
	}
	called := false
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(200)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/clusters", nil)
	h.ServeHTTP(rec, req)
	if !called || rec.Code != 200 {
		t.Fatalf("disabled middleware should pass through, got code=%d called=%v", rec.Code, called)
	}
}

// audience param can be missing entirely — must not panic.
func TestNew_NoPanicOnBlankEnv(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic on blank env: %v", r)
		}
	}()
	for _, k := range []string{"AUTH_USERS", "ETCD_UI_OIDC_ISSUER", "ETCD_UI_OIDC_AUDIENCE", "ETCD_UI_OIDC_CLIENT_ID"} {
		os.Unsetenv(k)
	}
	_ = New()
	_ = strings.TrimSpace("")
}
