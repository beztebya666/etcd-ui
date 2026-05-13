package httpx

import (
	"net/http"
	"testing"
)

func TestBearerFromUpgrade_FindsTokenInSubprotocols(t *testing.T) {
	r, _ := http.NewRequest("GET", "/watch", nil)
	r.Header.Set("Sec-WebSocket-Protocol", "etcd-ui.bearer, eyJ.token.value, etcd-ui.v1")
	if got := BearerFromUpgrade(r); got != "eyJ.token.value" {
		t.Errorf("BearerFromUpgrade = %q, want eyJ.token.value", got)
	}
}

func TestBearerFromUpgrade_MultipleHeaders(t *testing.T) {
	r, _ := http.NewRequest("GET", "/watch", nil)
	r.Header.Add("Sec-WebSocket-Protocol", "etcd-ui.v1")
	r.Header.Add("Sec-WebSocket-Protocol", "etcd-ui.bearer, my.token")
	if got := BearerFromUpgrade(r); got != "my.token" {
		t.Errorf("BearerFromUpgrade with split headers = %q", got)
	}
}

func TestBearerFromUpgrade_NoSentinel(t *testing.T) {
	r, _ := http.NewRequest("GET", "/watch", nil)
	r.Header.Set("Sec-WebSocket-Protocol", "etcd-ui.v1, chat")
	if got := BearerFromUpgrade(r); got != "" {
		t.Errorf("BearerFromUpgrade should be empty without sentinel, got %q", got)
	}
}

func TestPickSubprotocol_EchoesNonBearer(t *testing.T) {
	r, _ := http.NewRequest("GET", "/watch", nil)
	r.Header.Set("Sec-WebSocket-Protocol", "etcd-ui.bearer, token123, etcd-ui.v1")
	if got := pickSubprotocol(r); got != "etcd-ui.v1" {
		t.Errorf("pickSubprotocol = %q, want etcd-ui.v1", got)
	}
}

func TestPickSubprotocol_EmptyWhenOnlyBearer(t *testing.T) {
	r, _ := http.NewRequest("GET", "/watch", nil)
	r.Header.Set("Sec-WebSocket-Protocol", "etcd-ui.bearer, token123")
	if got := pickSubprotocol(r); got != "" {
		t.Errorf("pickSubprotocol with only bearer = %q, want empty", got)
	}
}
