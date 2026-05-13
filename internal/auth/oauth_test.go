package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

// Mini-IdP for tests. Supports:
//   GET  /.well-known/openid-configuration
//   GET  /jwks
//   POST /token   (grant_type=authorization_code|refresh_token)
type mockIdP struct {
	server   *httptest.Server
	priv     *rsa.PrivateKey
	kid      string
	clientID string

	mu        sync.Mutex
	refreshed int
	rotations []string
}

func newMockIdP(t *testing.T, clientID string) *mockIdP {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	m := &mockIdP{priv: priv, kid: "test-kid-1", clientID: clientID}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", m.discovery)
	mux.HandleFunc("/jwks", m.jwks)
	mux.HandleFunc("/token", m.token)
	m.server = httptest.NewServer(mux)
	return m
}

func (m *mockIdP) URL() string { return m.server.URL }
func (m *mockIdP) Close()      { m.server.Close() }

func (m *mockIdP) discovery(w http.ResponseWriter, _ *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer":                 m.server.URL,
		"authorization_endpoint": m.server.URL + "/authorize",
		"token_endpoint":         m.server.URL + "/token",
		"jwks_uri":               m.server.URL + "/jwks",
	})
}

func (m *mockIdP) jwks(w http.ResponseWriter, _ *http.Request) {
	n := base64.RawURLEncoding.EncodeToString(m.priv.N.Bytes())
	eBytes := big.NewInt(int64(m.priv.E)).Bytes()
	e := base64.RawURLEncoding.EncodeToString(eBytes)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA", "kid": m.kid, "alg": "RS256", "n": n, "e": e,
		}},
	})
}

func (m *mockIdP) mintIDToken(sub string) string {
	header := map[string]any{"alg": "RS256", "kid": m.kid, "typ": "JWT"}
	claims := map[string]any{
		"iss":   m.server.URL,
		"sub":   sub,
		"email": sub,
		"aud":   m.clientID,
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
	}
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	signing := base64.RawURLEncoding.EncodeToString(h) + "." +
		base64.RawURLEncoding.EncodeToString(c)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, m.priv, crypto.SHA256, sum[:])
	if err != nil {
		panic(err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (m *mockIdP) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		m.mu.Lock()
		rt := fmt.Sprintf("rt-%d", len(m.rotations))
		m.rotations = append(m.rotations, rt)
		m.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id_token":      m.mintIDToken("alice@corp"),
			"access_token":  "at-1",
			"refresh_token": rt,
			"expires_in":    3600,
		})
	case "refresh_token":
		m.mu.Lock()
		m.refreshed++
		old := r.PostForm.Get("refresh_token")
		if len(m.rotations) == 0 || m.rotations[len(m.rotations)-1] != old {
			m.mu.Unlock()
			http.Error(w, `{"error":"invalid_grant","error_description":"stale refresh_token"}`, 400)
			return
		}
		rt := fmt.Sprintf("rt-%d", len(m.rotations))
		m.rotations = append(m.rotations, rt)
		m.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id_token":      m.mintIDToken("alice@corp"),
			"refresh_token": rt,
			"expires_in":    3600,
		})
	default:
		http.Error(w, "bad grant_type", 400)
	}
}

func TestOAuth_CodeExchange_IssuesSessionAndRefresh(t *testing.T) {
	idp := newMockIdP(t, "etcd-ui")
	defer idp.Close()

	t.Setenv("ETCD_UI_OIDC_ISSUER", idp.URL())
	t.Setenv("ETCD_UI_OIDC_AUDIENCE", "etcd-ui")
	t.Setenv("ETCD_UI_OIDC_CLIENT_ID", "etcd-ui")
	t.Setenv("ETCD_UI_OIDC_CLIENT_SECRET", "shh")
	t.Setenv("ETCD_UI_OIDC_REDIRECT_URL", "http://etcd-ui.test/api/auth/oidc/callback")
	t.Setenv("AUTH_SESSION_SECRET", "test-secret-test-secret-test-secret")

	a := New()
	if !a.OIDCLoginConfigured() {
		t.Fatal("OAuth not configured despite all env present")
	}

	mux := http.NewServeMux()
	a.OAuth().Mount(routerAdapter{mux})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cli := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// 1) Hit /login — captures the flow cookie + state in the redirect.
	resp, err := cli.Get(srv.URL + "/api/auth/oidc/login?returnTo=/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var flow *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "etcd-ui-oauth-flow" {
			flow = c
		}
	}
	if flow == nil {
		t.Fatal("login didn't set the flow cookie")
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	state := loc.Query().Get("state")
	if state == "" {
		t.Fatal("authorization redirect missing state")
	}

	// 2) Bounce back with code=anything.
	cbReq, _ := http.NewRequest("GET",
		srv.URL+"/api/auth/oidc/callback?code=abc&state="+state, nil)
	cbReq.AddCookie(flow)
	cbResp, err := cli.Do(cbReq)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(cbResp.Body)
	cbResp.Body.Close()
	if cbResp.StatusCode != http.StatusFound {
		t.Fatalf("callback status %d: %s", cbResp.StatusCode, string(body))
	}
	var sessionCookie, refreshCookie *http.Cookie
	for _, c := range cbResp.Cookies() {
		switch c.Name {
		case "etcd-ui-session":
			sessionCookie = c
		case "etcd-ui-refresh":
			refreshCookie = c
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatal("callback didn't set session cookie")
	}
	if refreshCookie == nil || refreshCookie.Value == "" {
		t.Fatal("callback didn't set refresh cookie")
	}

	// 3) /refresh rotates both cookies.
	rfReq, _ := http.NewRequest("POST", srv.URL+"/api/auth/oidc/refresh", nil)
	rfReq.AddCookie(refreshCookie)
	rfResp, err := cli.Do(rfReq)
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(rfResp.Body)
	rfResp.Body.Close()
	if rfResp.StatusCode != 204 {
		t.Fatalf("refresh status %d: %s", rfResp.StatusCode, string(body2))
	}
	var newRefresh *http.Cookie
	for _, c := range rfResp.Cookies() {
		if c.Name == "etcd-ui-refresh" {
			newRefresh = c
		}
	}
	if newRefresh == nil {
		t.Fatal("refresh didn't rotate the refresh cookie")
	}
	if newRefresh.Value == refreshCookie.Value {
		t.Fatal("refresh cookie was not rotated (value identical)")
	}

	// 4) Re-using the OLD refresh cookie must fail.
	rfReq2, _ := http.NewRequest("POST", srv.URL+"/api/auth/oidc/refresh", nil)
	rfReq2.AddCookie(refreshCookie)
	rfResp2, _ := cli.Do(rfReq2)
	if rfResp2.StatusCode != 401 {
		t.Fatalf("burned refresh should give 401, got %d", rfResp2.StatusCode)
	}
}

// routerAdapter adapts net/http.ServeMux to the small interface OAuth.Mount
// expects. Avoids pulling chi into the test.
type routerAdapter struct{ mux *http.ServeMux }

func (r routerAdapter) Get(path string, h http.HandlerFunc) {
	r.mux.Handle(path, methodOnly("GET", h))
}
func (r routerAdapter) Post(path string, h http.HandlerFunc) {
	r.mux.Handle(path, methodOnly("POST", h))
}

func methodOnly(method string, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			http.Error(w, "method not allowed", 405)
			return
		}
		h(w, r)
	})
}
