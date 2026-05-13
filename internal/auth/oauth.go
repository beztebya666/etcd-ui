package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yourorg/etcd-ui/internal/tracing"
)

// OAuth implements the OIDC authorization-code flow with PKCE + state CSRF
// protection. Designed as a thin layer on top of OIDCVerifier — we delegate
// ID token validation to that path, this file only handles the *login dance*.
//
// Configure on top of the OIDC env vars:
//
//	ETCD_UI_OIDC_CLIENT_ID=etcd-ui
//	ETCD_UI_OIDC_CLIENT_SECRET=…           (optional for public PKCE clients)
//	ETCD_UI_OIDC_REDIRECT_URL=https://etcd-ui.example.com/api/auth/oidc/callback
//	ETCD_UI_OIDC_SCOPES=openid email profile     (default)
//	ETCD_UI_OIDC_POST_LOGIN_REDIRECT=/             (default — back to SPA root)
//
// Endpoints exposed (wired into the gateway router):
//
//	GET  /api/auth/oidc/login     — start the dance
//	GET  /api/auth/oidc/callback  — finish it, set session cookie
//	POST /api/auth/logout         — clear session
type OAuth struct {
	verifier     *OIDCVerifier
	session      *SessionCookie
	firstLogin   func(string)
	clientID     string
	clientSecret string
	redirectURL  string
	scopes       string
	postLogin    string
	stateSecret  []byte
	client       *http.Client

	// Discovered endpoints; lazy-fetched on first use, refreshed every 15 min.
	authEndpoint  string
	tokenEndpoint string
	discoveredAt  time.Time

	// Replay protection. The flow cookie is HMAC-signed and short-lived
	// (10 min), but an attacker who steals the cookie + state can still
	// replay the callback inside that window. We track every successfully
	// consumed state for the same TTL — second use → 400.
	usedStates struct {
		sync.Mutex
		seen map[string]time.Time
	}
}

func NewOAuth(v *OIDCVerifier, sc *SessionCookie) *OAuth {
	if v == nil || !v.Enabled() {
		return &OAuth{}
	}
	secret := os.Getenv("AUTH_SESSION_SECRET")
	if secret == "" {
		buf := make([]byte, 32)
		_, _ = rand.Read(buf)
		secret = base64.RawURLEncoding.EncodeToString(buf)
	}
	scopes := os.Getenv("ETCD_UI_OIDC_SCOPES")
	if scopes == "" {
		// Include offline_access by default — it's the standard way to ask
		// for a refresh_token. IdPs that don't support it just ignore the
		// scope. Operators can override with ETCD_UI_OIDC_SCOPES.
		scopes = "openid email profile offline_access"
	}
	postLogin := os.Getenv("ETCD_UI_OIDC_POST_LOGIN_REDIRECT")
	if postLogin == "" {
		postLogin = "/"
	}
	return &OAuth{
		verifier:     v,
		session:      sc,
		clientID:     os.Getenv("ETCD_UI_OIDC_CLIENT_ID"),
		clientSecret: os.Getenv("ETCD_UI_OIDC_CLIENT_SECRET"),
		redirectURL:  os.Getenv("ETCD_UI_OIDC_REDIRECT_URL"),
		scopes:       scopes,
		postLogin:    postLogin,
		stateSecret:  []byte(secret),
		client: &http.Client{
			Timeout:   10 * time.Second,
			Transport: tracing.WrapTransport(http.DefaultTransport),
		},
	}
}

// Enabled returns true when the full code-flow is configured. The Bearer-only
// path through OIDCVerifier may still work if Enabled() is false here.
func (o *OAuth) Enabled() bool {
	return o.verifier != nil && o.verifier.Enabled() && o.clientID != "" && o.redirectURL != ""
}

// Mount wires the three routes on the given mux. Safe to call even when
// !Enabled() — it installs handlers that return 404, so the SPA can probe and
// hide the "Sign in with SSO" button.
func (o *OAuth) Mount(mux interface {
	Get(string, http.HandlerFunc)
	Post(string, http.HandlerFunc)
}) {
	mux.Get("/api/auth/oidc/login", o.handleLogin)
	mux.Get("/api/auth/oidc/callback", o.handleCallback)
	mux.Post("/api/auth/oidc/refresh", o.handleRefresh)
	mux.Post("/api/auth/logout", o.handleLogout)
}

const refreshCookieName = "etcd-ui-refresh"

// refreshPayload is what we store in the HttpOnly refresh cookie. The token
// itself is opaque — the IdP defines its lifetime. We carry an explicit Exp
// so we can short-circuit obviously-expired cookies without a network call.
type refreshPayload struct {
	Token string `json:"t"`
	User  string `json:"u"`
	Exp   int64  `json:"e"`
}

func (o *OAuth) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !o.Enabled() {
		http.NotFound(w, r)
		return
	}
	if err := o.discover(r.Context()); err != nil {
		http.Error(w, "oidc discovery failed: "+err.Error(), 502)
		return
	}

	verifier := randomURLSafe(64)
	challenge := pkceChallenge(verifier)
	state := randomURLSafe(32)
	returnTo := sanitiseReturnTo(r.URL.Query().Get("returnTo"))
	// silent=1 marks this flow as initiated from an iframe-driven prompt=none
	// retry. The callback uses it to postMessage back to the parent instead
	// of redirecting (which an iframe can't do meaningfully).
	silent := r.URL.Query().Get("silent") == "1"

	flow := flowPayload{
		Verifier: verifier,
		State:    state,
		ReturnTo: returnTo,
		Silent:   silent,
		Exp:      time.Now().Add(10 * time.Minute).Unix(),
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "etcd-ui-oauth-flow",
		Value:    o.signFlow(flow),
		Path:     "/api/auth/oidc",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   isSecure(r),
		SameSite: http.SameSiteLaxMode,
	})

	q := url.Values{}
	q.Set("client_id", o.clientID)
	q.Set("redirect_uri", o.redirectURL)
	q.Set("response_type", "code")
	q.Set("scope", o.scopes)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	if silent {
		// Tells the IdP: "use the existing SSO session if any; do NOT show
		// a login page". On no-session this returns login_required, which
		// surfaces here as ?error=login_required in the callback.
		q.Set("prompt", "none")
	}

	http.Redirect(w, r, o.authEndpoint+"?"+q.Encode(), http.StatusFound)
}

func (o *OAuth) handleCallback(w http.ResponseWriter, r *http.Request) {
	if !o.Enabled() {
		http.NotFound(w, r)
		return
	}
	// Read the flow cookie first so we know whether this is a silent (iframe)
	// flow. On silent-flow errors we still need to postMessage success/fail
	// to the parent — bailing with http.Error would leave the parent waiting
	// for its 8s timeout.
	c, cookieErr := r.Cookie("etcd-ui-oauth-flow")
	var flow flowPayload
	if cookieErr == nil {
		var perr error
		flow, perr = o.verifyFlow(c.Value)
		if perr != nil {
			cookieErr = perr
		}
	}

	// Surface IdP-side errors directly — usually invalid client / redirect,
	// or "login_required" for a silent flow where the SSO session is gone.
	if errCode := r.URL.Query().Get("error"); errCode != "" {
		desc := r.URL.Query().Get("error_description")
		if flow.Silent {
			o.silentPostMessage(w, false, fmt.Sprintf("%s: %s", errCode, desc))
			return
		}
		http.Error(w, fmt.Sprintf("oauth error: %s — %s", errCode, desc), 400)
		return
	}

	if cookieErr != nil {
		if flow.Silent {
			o.silentPostMessage(w, false, "missing/invalid flow cookie")
			return
		}
		http.Error(w, "missing flow cookie (session expired or third-party cookies blocked)", 400)
		return
	}
	if !subtleEq(r.URL.Query().Get("state"), flow.State) {
		http.Error(w, "state mismatch — likely CSRF", 400)
		return
	}
	if o.markStateUsed(flow.State) {
		// State already consumed → likely replay (stolen cookie/code).
		http.Error(w, "state already consumed — replay rejected", 400)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", 400)
		return
	}

	if err := o.discover(r.Context()); err != nil {
		http.Error(w, "oidc discovery failed: "+err.Error(), 502)
		return
	}

	// Exchange code + verifier → id_token.
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", o.redirectURL)
	form.Set("client_id", o.clientID)
	form.Set("code_verifier", flow.Verifier)
	if o.clientSecret != "" {
		form.Set("client_secret", o.clientSecret)
	}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, o.tokenEndpoint,
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := o.client.Do(req)
	if err != nil {
		http.Error(w, "token endpoint unreachable: "+err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	var tok struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		http.Error(w, "bad token response: "+err.Error(), 502)
		return
	}
	if tok.Error != "" {
		http.Error(w, "token error: "+tok.Error+" — "+tok.ErrorDesc, 400)
		return
	}
	if tok.IDToken == "" {
		http.Error(w, "no id_token in response", 502)
		return
	}

	user, ok := o.verifier.Verify(r.Context(), tok.IDToken)
	if !ok {
		http.Error(w, "id_token verification failed", 401)
		return
	}
	if o.firstLogin != nil {
		o.firstLogin(user)
	}
	o.session.Issue(w, r, user, "oidc")
	o.issueRefreshCookie(w, r, user, tok.RefreshToken)

	// Clear the flow cookie now that we're done.
	http.SetCookie(w, &http.Cookie{
		Name: "etcd-ui-oauth-flow", Value: "", Path: "/api/auth/oidc", MaxAge: -1,
		HttpOnly: true, Secure: isSecure(r), SameSite: http.SameSiteLaxMode,
	})
	if flow.Silent {
		o.silentPostMessage(w, true, "")
		return
	}
	http.Redirect(w, r, flow.ReturnTo, http.StatusFound)
}

// silentPostMessage writes a tiny HTML document that posts the result to
// the parent window (the SPA iframe parent) and closes itself. Same-origin
// only — we don't pass an explicit targetOrigin so the browser uses the
// parent's origin automatically.
func (o *OAuth) silentPostMessage(w http.ResponseWriter, ok bool, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// CSP-safe: no inline-script-via-attribute, just one minimal script tag.
	// The SPA's CSP must allow this nonce-less self-script — gateway's
	// SecurityHeaders config already permits 'self' for script-src on the
	// auth surface.
	body, _ := json.Marshal(map[string]any{
		"type":  "etcd-ui:silent-auth",
		"ok":    ok,
		"error": errMsg,
	})
	_, _ = w.Write([]byte(
		`<!doctype html><meta charset="utf-8"><title>silent auth</title>` +
			`<script>try{parent.postMessage(` + string(body) +
			`,location.origin);}catch(e){}</script>`,
	))
}

// handleRefresh exchanges the stored refresh_token for a fresh id_token (and
// usually a rotated refresh_token), then re-issues the session cookie. The
// SPA calls this proactively before the session expires; a 401 means "drop
// to /login".
func (o *OAuth) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if !o.Enabled() {
		http.NotFound(w, r)
		return
	}
	c, err := r.Cookie(refreshCookieName)
	if err != nil {
		http.Error(w, "no refresh cookie", 401)
		return
	}
	rp, err := o.verifyRefresh(c.Value)
	if err != nil {
		http.Error(w, "bad refresh cookie: "+err.Error(), 401)
		return
	}

	if err := o.discover(r.Context()); err != nil {
		http.Error(w, "oidc discovery failed: "+err.Error(), 502)
		return
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", rp.Token)
	form.Set("client_id", o.clientID)
	if o.clientSecret != "" {
		form.Set("client_secret", o.clientSecret)
	}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, o.tokenEndpoint,
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := o.client.Do(req)
	if err != nil {
		http.Error(w, "token endpoint unreachable: "+err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	var tok struct {
		IDToken      string `json:"id_token"`
		RefreshToken string `json:"refresh_token"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		http.Error(w, "bad token response: "+err.Error(), 502)
		return
	}
	if tok.Error != "" {
		// IdP rejected the refresh — clear cookies, force re-login.
		o.session.Revoke(w)
		o.clearRefresh(w, r)
		http.Error(w, "refresh rejected: "+tok.Error+" — "+tok.ErrorDesc, 401)
		return
	}
	if tok.IDToken == "" {
		http.Error(w, "no id_token in refresh response", 502)
		return
	}
	user, ok := o.verifier.Verify(r.Context(), tok.IDToken)
	if !ok {
		http.Error(w, "id_token verification failed", 401)
		return
	}
	if o.firstLogin != nil {
		o.firstLogin(user)
	}
	o.session.Issue(w, r, user, "oidc")
	// Rotate: prefer the new refresh_token if the IdP returned one, otherwise
	// keep the existing token (some IdPs don't rotate).
	newToken := tok.RefreshToken
	if newToken == "" {
		newToken = rp.Token
	}
	o.issueRefreshCookie(w, r, user, newToken)
	w.WriteHeader(204)
}

func (o *OAuth) handleLogout(w http.ResponseWriter, r *http.Request) {
	if o.session != nil {
		o.session.Revoke(w)
	}
	o.clearRefresh(w, r)
	w.WriteHeader(204)
}

// issueRefreshCookie persists the IdP-issued refresh_token in an HttpOnly
// cookie, scoped to /api/auth/oidc so it never leaves the auth surface.
// Empty token is a no-op (IdP didn't return one — non-rotating or scope
// missing offline_access).
func (o *OAuth) issueRefreshCookie(w http.ResponseWriter, r *http.Request, user, token string) {
	if token == "" {
		return
	}
	rp := refreshPayload{Token: token, User: user, Exp: time.Now().Add(30 * 24 * time.Hour).Unix()}
	body, _ := json.Marshal(rp)
	enc := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, o.stateSecret)
	mac.Write([]byte(enc))
	value := enc + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    value,
		Path:     "/api/auth/oidc",
		MaxAge:   30 * 24 * 3600,
		HttpOnly: true,
		Secure:   isSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (o *OAuth) clearRefresh(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookieName, Value: "", Path: "/api/auth/oidc", MaxAge: -1,
		HttpOnly: true, Secure: isSecure(r), SameSite: http.SameSiteLaxMode,
	})
}

func (o *OAuth) verifyRefresh(s string) (refreshPayload, error) {
	parts := strings.SplitN(s, ".", 2)
	if len(parts) != 2 {
		return refreshPayload{}, errors.New("bad cookie shape")
	}
	mac := hmac.New(sha256.New, o.stateSecret)
	mac.Write([]byte(parts[0]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[1])) {
		return refreshPayload{}, errors.New("bad signature")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return refreshPayload{}, err
	}
	var rp refreshPayload
	if err := json.Unmarshal(body, &rp); err != nil {
		return refreshPayload{}, err
	}
	if time.Now().Unix() > rp.Exp {
		return refreshPayload{}, errors.New("refresh cookie expired")
	}
	return rp, nil
}

// --- discovery ---

func (o *OAuth) discover(ctx context.Context) error {
	if time.Since(o.discoveredAt) < 15*time.Minute && o.authEndpoint != "" {
		return nil
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		o.verifier.issuer+"/.well-known/openid-configuration", nil)
	resp, err := o.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New(resp.Status)
	}
	var disc struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&disc); err != nil {
		return err
	}
	if disc.AuthorizationEndpoint == "" || disc.TokenEndpoint == "" {
		return errors.New("discovery missing endpoints")
	}
	o.authEndpoint = disc.AuthorizationEndpoint
	o.tokenEndpoint = disc.TokenEndpoint
	o.discoveredAt = time.Now()
	return nil
}

// --- flow cookie (HMAC-signed JSON) ---

type flowPayload struct {
	Verifier string `json:"v"`
	State    string `json:"s"`
	ReturnTo string `json:"r"`
	Silent   bool   `json:"q,omitempty"` // prompt=none iframe flow
	Exp      int64  `json:"e"`
}

func (o *OAuth) signFlow(p flowPayload) string {
	body, _ := json.Marshal(p)
	enc := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, o.stateSecret)
	mac.Write([]byte(enc))
	return enc + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (o *OAuth) verifyFlow(s string) (flowPayload, error) {
	parts := strings.SplitN(s, ".", 2)
	if len(parts) != 2 {
		return flowPayload{}, errors.New("bad cookie shape")
	}
	mac := hmac.New(sha256.New, o.stateSecret)
	mac.Write([]byte(parts[0]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[1])) {
		return flowPayload{}, errors.New("bad signature")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return flowPayload{}, err
	}
	var p flowPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return flowPayload{}, err
	}
	if time.Now().Unix() > p.Exp {
		return flowPayload{}, errors.New("flow cookie expired")
	}
	return p, nil
}

// --- helpers ---

// markStateUsed registers the state as consumed and returns true if it had
// been seen before. The map is GC'd lazily on each call — entries older
// than 20 minutes (2× flow cookie TTL) are pruned. Memory bound:
// O(active-logins-per-20min); fine for any realistic deployment.
func (o *OAuth) markStateUsed(state string) (alreadyUsed bool) {
	o.usedStates.Lock()
	defer o.usedStates.Unlock()
	if o.usedStates.seen == nil {
		o.usedStates.seen = map[string]time.Time{}
	}
	now := time.Now()
	if _, hit := o.usedStates.seen[state]; hit {
		return true
	}
	// Opportunistic prune.
	if len(o.usedStates.seen) > 64 {
		cutoff := now.Add(-20 * time.Minute)
		for k, t := range o.usedStates.seen {
			if t.Before(cutoff) {
				delete(o.usedStates.seen, k)
			}
		}
	}
	o.usedStates.seen[state] = now
	return false
}

func randomURLSafe(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func subtleEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return hmac.Equal([]byte(a), []byte(b))
}

func sanitiseReturnTo(s string) string {
	// Only allow local paths. Reject anything that looks like an absolute URL
	// or starts with "//" (protocol-relative) to prevent open-redirect.
	if s == "" || !strings.HasPrefix(s, "/") || strings.HasPrefix(s, "//") {
		return "/"
	}
	return s
}

func isSecure(r *http.Request) bool {
	return r.TLS != nil || os.Getenv("ETCD_UI_TLS") == "on" || r.Header.Get("X-Forwarded-Proto") == "https"
}
