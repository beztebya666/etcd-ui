package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// SessionCookie is signed by an HMAC secret. Cookie value: base64(payload).base64(sig).
// Payload is a small JSON: { "sub":"alice", "exp":<unix>, "src":"basic"|"oidc" }.
//
// Idle timeout: ETCD_UI_SESSION_TTL_SECONDS (default 1h).
type SessionCookie struct {
	secret []byte
	ttl    time.Duration
	name   string
}

func NewSessionCookie() *SessionCookie {
	sec := os.Getenv("AUTH_SESSION_SECRET")
	if sec == "" {
		// random per-process secret — sessions invalidated on restart
		buf := make([]byte, 32)
		_, _ = rand.Read(buf)
		sec = base64.RawURLEncoding.EncodeToString(buf)
	}
	ttl := 60 * time.Minute
	if v := os.Getenv("ETCD_UI_SESSION_TTL_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ttl = time.Duration(n) * time.Second
		}
	}
	return &SessionCookie{secret: []byte(sec), ttl: ttl, name: "etcd-ui-session"}
}

type sessionPayload struct {
	Sub string `json:"sub"`
	Exp int64  `json:"exp"`
	Src string `json:"src"`
}

func (sc *SessionCookie) Issue(w http.ResponseWriter, r *http.Request, user, src string) {
	value := sc.IssueValue(r, user, src)
	tls := r.TLS != nil || os.Getenv("ETCD_UI_TLS") == "on" || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name:     sc.name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(sc.ttl.Seconds()),
		HttpOnly: true,
		Secure:   tls,
		SameSite: http.SameSiteLaxMode,
	})
}

// IssueValue returns the signed cookie value without writing it to the
// response. Used by the WS-upgrade path: the cookie has to ride a header
// the reverse proxy's ModifyResponse picks up, because the upgrade response
// is written directly to the hijacked socket — bypassing rw.Header().
func (sc *SessionCookie) IssueValue(r *http.Request, user, src string) string {
	payload := sessionPayload{Sub: user, Exp: time.Now().Add(sc.ttl).Unix(), Src: src}
	return sc.sign(payload)
}

// Name + TTL accessors used by the proxy hook when materialising the
// deferred Set-Cookie header.
func (sc *SessionCookie) CookieName() string  { return sc.name }
func (sc *SessionCookie) TTLSeconds() int     { return int(sc.ttl.Seconds()) }

func (sc *SessionCookie) Revoke(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sc.name, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func (sc *SessionCookie) Read(r *http.Request) (string, string, error) {
	user, src, _, err := sc.ReadFull(r)
	return user, src, err
}

// ReadFull is like Read but also returns the cookie's absolute expiry (unix
// seconds). Used by /api/auth/me so the SPA can schedule a proactive refresh
// just before TTL hits.
func (sc *SessionCookie) ReadFull(r *http.Request) (user, src string, exp int64, err error) {
	c, err := r.Cookie(sc.name)
	if err != nil {
		return "", "", 0, err
	}
	p, err := sc.verify(c.Value)
	if err != nil {
		return "", "", 0, err
	}
	if time.Now().Unix() > p.Exp {
		return "", "", 0, errors.New("session expired")
	}
	return p.Sub, p.Src, p.Exp, nil
}

func (sc *SessionCookie) sign(p sessionPayload) string {
	body, _ := json.Marshal(p)
	enc := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, sc.secret)
	mac.Write([]byte(enc))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return enc + "." + sig
}

func (sc *SessionCookie) verify(s string) (sessionPayload, error) {
	parts := strings.SplitN(s, ".", 2)
	if len(parts) != 2 {
		return sessionPayload{}, errors.New("bad cookie")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return sessionPayload{}, err
	}
	mac := hmac.New(sha256.New, sc.secret)
	mac.Write([]byte(parts[0]))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return sessionPayload{}, errors.New("bad signature")
	}
	var p sessionPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return sessionPayload{}, err
	}
	return p, nil
}

// Name is exported so the gateway can use it in a NetworkPolicy or audit.
func (sc *SessionCookie) Name() string { return sc.name }
