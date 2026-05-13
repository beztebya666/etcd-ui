// Package auth ships a minimal HTTP Basic auth middleware. Users are loaded
// from the AUTH_USERS env var: "alice:plain:secret,bob:bcrypt:$2a$10$...".
//
// If AUTH_USERS is unset, auth is disabled entirely — useful for dev and for
// users that already gate the UI behind an OIDC reverse-proxy upstream.
//
// On successful login the username is forwarded to downstream services via
// the X-Etcd-UI-User header so the audit log can attribute writes.
package auth

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
)

type entry struct {
	user, kind, secret string
}

type Auth struct {
	enabled bool
	realm   string
	users   []entry
	session *SessionCookie
	oidc    *OIDCVerifier
	oauth   *OAuth

	// First-login hook — fires once per process the first time any user
	// completes a successful OIDC verification. Used by the gateway to
	// grant `__acl__` admin to the first SSO user on a fresh deployment.
	firstLogin     func(user string)
	firstLoginOnce sync.Once
}

func New() *Auth {
	a := &Auth{realm: "etcd-ui", session: NewSessionCookie(), oidc: NewOIDCVerifier()}
	a.oauth = NewOAuth(a.oidc, a.session)
	raw := os.Getenv("AUTH_USERS")
	if raw != "" {
		a.enabled = true
		for _, item := range strings.Split(raw, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			parts := strings.SplitN(item, ":", 3)
			if len(parts) != 3 {
				continue
			}
			a.users = append(a.users, entry{user: parts[0], kind: parts[1], secret: parts[2]})
		}
	}
	if a.oidc.Enabled() {
		a.enabled = true
	}
	return a
}

func (a *Auth) Enabled() bool           { return a.enabled }
func (a *Auth) Session() *SessionCookie { return a.session }
func (a *Auth) OAuth() *OAuth           { return a.oauth }
func (a *Auth) OIDCLoginConfigured() bool {
	return a.oauth != nil && a.oauth.Enabled()
}

// OnFirstLogin registers a callback that fires at most once per process,
// the first time any user successfully completes OIDC verification. The
// gateway uses this for ETCD_UI_ACL_BOOTSTRAP_FIRST_LOGIN: grant the
// arriving sub `__acl__` admin so the deployment has an editor without
// pre-configuring a name.
func (a *Auth) OnFirstLogin(fn func(user string)) {
	a.firstLogin = fn
	if a.oauth != nil {
		// OAuth handleCallback fires the same hook directly — wire it now.
		a.oauth.firstLogin = a.fireFirstLogin
	}
}

func (a *Auth) fireFirstLogin(user string) {
	if a.firstLogin == nil || user == "" {
		return
	}
	a.firstLoginOnce.Do(func() { a.firstLogin(user) })
}

// Middleware checks (in order) session cookie → Bearer (OIDC) → Basic.
// On success it sets X-Etcd-UI-User and forwards. /healthz, /readyz, /metrics
// and static SPA assets are always public.
//
// WebSocket-friendly: requests with `Upgrade: websocket` may carry the
// bearer token via Sec-WebSocket-Protocol instead of Authorization, since
// browser WS APIs can't set custom headers. We accept both.
func (a *Auth) Middleware(next http.Handler) http.Handler {
	if !a.enabled {
		return next
	}
	publicPath := func(p string) bool {
		return p == "/healthz" || p == "/readyz" || p == "/metrics" ||
			!strings.HasPrefix(p, "/api") ||
			strings.HasPrefix(p, "/api/auth/")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		// 1) session cookie
		if user, _, err := a.session.Read(r); err == nil {
			r.Header.Set("X-Etcd-UI-User", user)
			next.ServeHTTP(w, r)
			return
		}
		// 2) OIDC bearer — accept either Authorization header or, for WS
		//    upgrades, the Sec-WebSocket-Protocol subprotocol carrier.
		if a.oidc.Enabled() {
			token := ""
			if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
				token = strings.TrimPrefix(h, "Bearer ")
			} else if isWebSocketUpgrade(r) {
				token = bearerFromSubprotocol(r)
			}
			if token != "" {
				if u, ok := a.oidc.Verify(r.Context(), token); ok {
					a.fireFirstLogin(u)
					r.Header.Set("X-Etcd-UI-User", applyOnBehalf(u, r))
					if isWebSocketUpgrade(r) {
						// The reverse proxy's handleUpgradeResponse rewrites
						// the 101 directly to the hijacked socket, bypassing
						// rw.Header(). Stash the cookie value in a header the
						// proxy's ModifyResponse hook will pick up and re-add
						// on the way out.
						val := a.session.IssueValue(r, u, "oidc")
						r.Header.Set(WSDeferredCookieHeader, val)
					} else {
						a.session.Issue(w, r, u, "oidc")
					}
					next.ServeHTTP(w, r)
					return
				}
			}
		}
		// 3) Basic
		if u, p, ok := r.BasicAuth(); ok && a.checkBasic(u, p) {
			r.Header.Set("X-Etcd-UI-User", u)
			a.session.Issue(w, r, u, "basic")
			next.ServeHTTP(w, r)
			return
		}
		if !a.oidc.Enabled() {
			w.Header().Set("WWW-Authenticate", `Basic realm="`+a.realm+`"`)
		}
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	})
}

// WSDeferredCookieHeader is the inbound request header that auth.Middleware
// uses to ferry a freshly-issued session cookie to the reverse-proxy's
// ModifyResponse hook. The proxy strips it from the upstream request and
// re-emits it as a Set-Cookie on the 101 Switching Protocols response, so
// browsers that authenticated via Sec-WebSocket-Protocol get a normal
// HttpOnly session cookie for the next regular HTTP request.
//
// Plain hop-by-hop header name (X-* prefix) to dodge upstream filtering.
const WSDeferredCookieHeader = "X-Etcd-UI-WS-Set-Cookie"

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// applyOnBehalf rewrites the verified principal when the request carries
// X-Etcd-UI-On-Behalf AND the principal is recognised as a federation peer
// (`system:peer:*`). The downstream ACL middleware then sees a composite
// identity like `peer:hub-eu/alice@corp` and can apply real per-user rules.
//
// Trust model: we honour the header only when the carrier *itself* is a
// trusted peer. A normal user can't forge "I'm on behalf of admin@corp"
// because their token's verified identity isn't a `system:peer:*` and the
// header is silently ignored.
func applyOnBehalf(verified string, r *http.Request) string {
	if !strings.HasPrefix(verified, "system:peer:") {
		return verified
	}
	on := strings.TrimSpace(r.Header.Get("X-Etcd-UI-On-Behalf"))
	if on == "" {
		return verified
	}
	// Strip the system:peer: prefix from the carrier so the composite stays
	// short and readable in audit / ACL.
	peer := strings.TrimPrefix(verified, "system:peer:")
	return "peer:" + peer + "/" + on
}

// bearerFromSubprotocol mirrors httpx.BearerFromUpgrade — kept in this
// package to avoid a circular import (httpx → auth would create a loop).
func bearerFromSubprotocol(r *http.Request) string {
	for _, h := range r.Header.Values("Sec-WebSocket-Protocol") {
		parts := strings.Split(h, ",")
		for i, p := range parts {
			if strings.TrimSpace(p) == "etcd-ui.bearer" && i+1 < len(parts) {
				return strings.TrimSpace(parts[i+1])
			}
		}
	}
	return ""
}

// LoginHandler accepts POST /api/auth/login with {"username":"...","password":"..."}.
// On success it sets a session cookie; on failure 401. Useful for SPA login forms.
func (a *Auth) LoginHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.enabled {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var body struct{ Username, Password string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if a.checkBasic(body.Username, body.Password) {
			a.session.Issue(w, r, body.Username, "basic")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
	}
}

// LogoutHandler clears the session cookie. Always returns 204.
func (a *Auth) LogoutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		a.session.Revoke(w)
		w.WriteHeader(http.StatusNoContent)
	}
}

// MeHandler returns the currently-authenticated user (or 401 if no session).
// Includes the session's absolute expiry (unix seconds) so the SPA can
// schedule a proactive refresh before it hits.
func (a *Auth) MeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.enabled {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user":"anonymous","authDisabled":true}`))
			return
		}
		user, src, exp, err := a.session.ReadFull(r)
		if err != nil {
			http.Error(w, "no session", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		body, _ := json.Marshal(map[string]any{
			"user": user,
			"src":  src,
			"exp":  exp,
		})
		_, _ = w.Write(body)
	}
}

func (a *Auth) checkBasic(user, pass string) bool {
	for _, e := range a.users {
		if subtle.ConstantTimeCompare([]byte(e.user), []byte(user)) != 1 {
			continue
		}
		switch e.kind {
		case "plain":
			return subtle.ConstantTimeCompare([]byte(e.secret), []byte(pass)) == 1
		case "bcrypt":
			return bcryptCompare(e.secret, pass)
		}
	}
	return false
}
