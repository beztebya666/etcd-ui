package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"time"
)

// Shared helpers across oauth_test.go and oidc_peer_test.go to keep the JWT
// minting paths reusable without copy-pasting.

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func b64(b []byte) string       { return base64.RawURLEncoding.EncodeToString(b) }
func b64URL(b []byte) string    { return base64.RawURLEncoding.EncodeToString(b) }
func sha256Sum(s string) [32]byte { return sha256.Sum256([]byte(s)) }

func rsaSign(priv *rsa.PrivateKey, hash []byte) ([]byte, error) {
	return rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, hash)
}

func testNow() int64 { return time.Now().Unix() }
