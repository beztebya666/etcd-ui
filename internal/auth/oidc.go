package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yourorg/etcd-ui/internal/tracing"
)

// OIDCVerifier validates `Authorization: Bearer <jwt>` headers against an
// OIDC issuer's JWKS. Deliberately small (no full oauth2 login flow) — assumes
// you already have a token (from an IdP or auth-proxy) and just want to pull
// the subject claim for identity + audit attribution.
//
// Configure via:
//
//	ETCD_UI_OIDC_ISSUER=https://accounts.example.com
//	ETCD_UI_OIDC_AUDIENCE=etcd-ui            (optional, recommended)
//	ETCD_UI_OIDC_USERNAME_CLAIM=email        (default: email; falls back to sub)
//
// Service-account tokens (federation peer→peer) are tagged automatically
// when their `azp` / `client_id` claim is in ETCD_UI_PEER_CLIENT_IDS — the
// returned username becomes `system:peer:<client_id>` so ACL rules can grant
// them explicit, narrow access without exposing human-user privileges.
type OIDCVerifier struct {
	enabled       bool
	issuer        string
	audience      string
	usernameClaim string
	peerClients   map[string]struct{} // client_ids recognised as federation SAs
	client        *http.Client

	mu       sync.RWMutex
	jwks     map[string]*rsa.PublicKey
	expireAt time.Time
}

func NewOIDCVerifier() *OIDCVerifier {
	iss := strings.TrimRight(os.Getenv("ETCD_UI_OIDC_ISSUER"), "/")
	claim := os.Getenv("ETCD_UI_OIDC_USERNAME_CLAIM")
	if claim == "" {
		claim = "email"
	}
	peers := map[string]struct{}{}
	if raw := os.Getenv("ETCD_UI_PEER_CLIENT_IDS"); raw != "" {
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				peers[p] = struct{}{}
			}
		}
	}
	return &OIDCVerifier{
		enabled:       iss != "",
		issuer:        iss,
		audience:      os.Getenv("ETCD_UI_OIDC_AUDIENCE"),
		usernameClaim: claim,
		peerClients:   peers,
		client: &http.Client{
			Timeout:   5 * time.Second,
			Transport: tracing.WrapTransport(http.DefaultTransport),
		},
		jwks: map[string]*rsa.PublicKey{},
	}
}

func (v *OIDCVerifier) Enabled() bool { return v.enabled }

// Verify returns (username, ok). When OIDC is disabled, returns ("", false).
func (v *OIDCVerifier) Verify(ctx context.Context, bearer string) (string, bool) {
	if !v.enabled {
		return "", false
	}
	parts := strings.Split(bearer, ".")
	if len(parts) != 3 {
		return "", false
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	var hdr struct {
		Alg, Kid string
	}
	if err := json.Unmarshal(headerJSON, &hdr); err != nil || hdr.Alg != "RS256" {
		return "", false
	}
	if err := v.refreshKeys(ctx); err != nil {
		return "", false
	}
	v.mu.RLock()
	key, ok := v.jwks[hdr.Kid]
	v.mu.RUnlock()
	if !ok {
		return "", false
	}

	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sigBytes); err != nil {
		return "", false
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	var claims map[string]any
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return "", false
	}
	if iss, _ := claims["iss"].(string); strings.TrimRight(iss, "/") != v.issuer {
		return "", false
	}
	if exp, _ := claims["exp"].(float64); int64(exp) < time.Now().Unix() {
		return "", false
	}
	if v.audience != "" && !audienceMatches(claims["aud"], v.audience) {
		return "", false
	}

	// Federation peer token? client_credentials grants typically carry
	// `azp` (preferred) or `client_id`. If either matches a configured peer
	// client_id, identify the principal as `system:peer:<id>` so ACL can
	// gate federation distinctly from human users.
	if len(v.peerClients) > 0 {
		clientID, _ := claims["azp"].(string)
		if clientID == "" {
			clientID, _ = claims["client_id"].(string)
		}
		if _, ok := v.peerClients[clientID]; ok && clientID != "" {
			return "system:peer:" + clientID, true
		}
	}

	user, _ := claims[v.usernameClaim].(string)
	if user == "" {
		user, _ = claims["sub"].(string)
	}
	return user, user != ""
}

func (v *OIDCVerifier) refreshKeys(ctx context.Context) error {
	v.mu.RLock()
	fresh := time.Now().Before(v.expireAt) && len(v.jwks) > 0
	v.mu.RUnlock()
	if fresh {
		return nil
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, v.issuer+"/.well-known/openid-configuration", nil)
	resp, err := v.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("oidc discovery: %s", resp.Status)
	}
	var disc struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&disc); err != nil {
		return err
	}
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, disc.JWKSURI, nil)
	resp, err = v.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var jwks struct {
		Keys []struct {
			Kid, Kty, N, E string
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" {
			continue
		}
		nB, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eB, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		e := 0
		for _, b := range eB {
			e = e<<8 | int(b)
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(nB), E: e}
	}
	v.mu.Lock()
	v.jwks = keys
	v.expireAt = time.Now().Add(15 * time.Minute)
	v.mu.Unlock()
	return nil
}

func audienceMatches(aud any, want string) bool {
	switch a := aud.(type) {
	case string:
		return a == want
	case []any:
		for _, v := range a {
			if s, _ := v.(string); s == want {
				return true
			}
		}
	}
	return false
}
