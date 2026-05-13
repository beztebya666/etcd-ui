package auth

import (
	"context"
	"net/http/httptest"
	"testing"
)

// When ETCD_UI_PEER_CLIENT_IDS contains the token's `azp`, the verifier must
// return a `system:peer:<id>` principal so ACL rules can target federation
// hops distinctly from human users.
func TestOIDCVerifier_FederationPeerTokenIdentifier(t *testing.T) {
	// Reuse the mock IdP from oauth_test.go.
	idp := newMockIdP(t, "etcd-ui")
	defer idp.Close()

	t.Setenv("ETCD_UI_OIDC_ISSUER", idp.URL())
	t.Setenv("ETCD_UI_OIDC_AUDIENCE", "etcd-ui")
	t.Setenv("ETCD_UI_PEER_CLIENT_IDS", "hub-eu,hub-us")

	v := NewOIDCVerifier()
	if !v.Enabled() {
		t.Fatal("verifier not enabled")
	}

	// 1) Token from a federation peer (azp matches). Expect peer principal.
	idp.kid = "test-kid-1"
	idToken := idp.mintIDTokenWithClaims(map[string]any{
		"iss":   idp.URL(),
		"sub":   "service-account",
		"email": "sa@hub-eu",
		"aud":   "etcd-ui",
		"azp":   "hub-eu",
		"exp":   nowPlus(3600),
	})
	got, ok := v.Verify(context.Background(), idToken)
	if !ok {
		t.Fatal("verify failed for valid federation token")
	}
	if got != "system:peer:hub-eu" {
		t.Errorf("federation principal = %q, want system:peer:hub-eu", got)
	}

	// 2) Same flow with `client_id` claim instead of azp.
	idToken2 := idp.mintIDTokenWithClaims(map[string]any{
		"iss":       idp.URL(),
		"sub":       "service-account",
		"email":     "sa@hub-us",
		"aud":       "etcd-ui",
		"client_id": "hub-us",
		"exp":       nowPlus(3600),
	})
	got2, ok := v.Verify(context.Background(), idToken2)
	if !ok || got2 != "system:peer:hub-us" {
		t.Errorf("client_id-based peer detection failed: %q ok=%v", got2, ok)
	}

	// 3) Regular human user — falls through to email.
	idToken3 := idp.mintIDTokenWithClaims(map[string]any{
		"iss":   idp.URL(),
		"sub":   "alice",
		"email": "alice@corp",
		"aud":   "etcd-ui",
		"azp":   "browser-app",
		"exp":   nowPlus(3600),
	})
	got3, _ := v.Verify(context.Background(), idToken3)
	if got3 != "alice@corp" {
		t.Errorf("non-peer token should resolve to email, got %q", got3)
	}

	_ = httptest.NewServer // keep import live for parity with oauth_test
}

// --- helpers ---

// mintIDTokenWithClaims is a more flexible cousin of mintIDToken used only by
// tests that need to craft non-standard claim shapes (azp/client_id, etc).
func (m *mockIdP) mintIDTokenWithClaims(claims map[string]any) string {
	saved := m.kid
	defer func() { m.kid = saved }()
	header := mustMarshal(map[string]any{"alg": "RS256", "kid": m.kid, "typ": "JWT"})
	body := mustMarshal(claims)
	signing := b64(header) + "." + b64(body)
	sum := sha256Sum(signing)
	sig, err := rsaSign(m.priv, sum[:])
	if err != nil {
		panic(err)
	}
	return signing + "." + b64URL(sig)
}

func nowPlus(secs int64) int64 { return testNow() + secs }
