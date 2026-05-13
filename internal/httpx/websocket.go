// Minimal RFC 6455 WebSocket server. Server→client only — we don't read
// frames from clients (the watch endpoint is unidirectional). No dependency
// on gorilla/coder/websocket: ~80 lines is enough for our use case.
//
// Frame format we send: text frame, single-fragment, FIN=1, no masking.
// We never accept fragmented frames or extensions.

package httpx

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// WSConn is a minimal WebSocket connection. Write-mostly: callers stream
// frames to clients via SendText; the read side just drains client→server
// frames so corporate proxies that look for bidirectional activity stay
// happy. Browsers can't send native Ping frames from JavaScript, so we
// accept a `{"type":"ping"}` text frame as a logical keep-alive and reply
// with a Pong.
type WSConn struct {
	conn   net.Conn
	bw     *bufio.Writer
	reader *bufio.Reader
	writeM sync.Mutex
}

// Upgrade promotes an HTTP connection to a WebSocket. Returns ErrNotWebsocket
// if the request doesn't carry the right Upgrade headers — the caller can
// then fall back to SSE.
//
// Subprotocol auth: clients that can't reliably send cookies on the WS
// handshake (Safari ITP, embedded webviews, cross-origin browser quirks)
// can pass an opaque bearer via Sec-WebSocket-Protocol. We accept the
// pattern `etcd-ui.bearer, <token>` and echo the first subprotocol on the
// 101 response (RFC 6455 requires the server to pick exactly one).
//
// The token itself is treated as the auth identity carrier — middleware
// upstream of this call should already have validated it and set
// `X-Etcd-UI-User`. We just unblock the handshake.
func Upgrade(w http.ResponseWriter, r *http.Request) (*WSConn, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") ||
		!strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
		return nil, ErrNotWebsocket
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, errors.New("missing Sec-WebSocket-Key")
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("response writer doesn't support hijacking")
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}

	sum := sha1.Sum([]byte(key + wsGUID))
	accept := base64.StdEncoding.EncodeToString(sum[:])

	headers := []string{
		"HTTP/1.1 101 Switching Protocols",
		"Upgrade: websocket",
		"Connection: Upgrade",
		"Sec-WebSocket-Accept: " + accept,
	}
	// Echo the first non-bearer subprotocol if the client offered any.
	// `etcd-ui.bearer` is a sentinel — its presence means the next protocol
	// in the list is the token; we don't echo the token back to the wire.
	if proto := pickSubprotocol(r); proto != "" {
		headers = append(headers, "Sec-WebSocket-Protocol: "+proto)
	}
	resp := strings.Join(headers, "\r\n") + "\r\n\r\n"
	if _, err := conn.Write([]byte(resp)); err != nil {
		conn.Close()
		return nil, err
	}
	c := &WSConn{conn: conn, bw: brw.Writer, reader: brw.Reader}
	// Background reader: handles client pings (data + control frames) and
	// auto-replies with pongs. Without this, the kernel buffer fills with
	// client→server bytes (browsers auto-pong our server-side Pings) and
	// the read side blocks forever, hiding closed connections.
	go c.readLoop()
	return c, nil
}

// readLoop drains client-sent frames and responds to pings. Exits silently
// on any framing error / close — the writer side will then fail on next
// SendText and the caller cleans up.
func (c *WSConn) readLoop() {
	for {
		op, payload, err := readFrame(c.reader)
		if err != nil {
			return
		}
		switch op {
		case 0x8: // Close
			return
		case 0x9: // Ping → reply Pong with same payload (≤ 125 bytes per spec)
			if len(payload) > 125 {
				payload = payload[:125]
			}
			c.writeFrame(0xA, payload)
		case 0xA: // Pong from peer — keep-alive proof, nothing to do
		case 0x1: // Text frame from client. Accept `{"type":"ping"}` as
			// a logical keep-alive (JS WS API can't send 0x9). Anything
			// else is silently dropped — this socket is server→client.
			if isClientPing(payload) {
				c.writeFrame(0xA, nil)
			}
		default:
			// Binary frames / continuations — discard.
		}
	}
}

func isClientPing(b []byte) bool {
	s := strings.TrimSpace(string(b))
	return s == `{"type":"ping"}` || s == `"ping"`
}

// readFrame parses one (unmasked-by-client-required) RFC 6455 frame. Client
// frames MUST be masked; we apply the mask before returning the payload.
func readFrame(r *bufio.Reader) (opcode byte, payload []byte, err error) {
	hdr := make([]byte, 2)
	if _, err = io.ReadFull(r, hdr); err != nil {
		return 0, nil, err
	}
	opcode = hdr[0] & 0x0f
	masked := hdr[1]&0x80 != 0
	plen := int64(hdr[1] & 0x7f)
	switch plen {
	case 126:
		ext := make([]byte, 2)
		if _, err = io.ReadFull(r, ext); err != nil {
			return 0, nil, err
		}
		plen = int64(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		if _, err = io.ReadFull(r, ext); err != nil {
			return 0, nil, err
		}
		plen = int64(binary.BigEndian.Uint64(ext))
	}
	var mask [4]byte
	if masked {
		if _, err = io.ReadFull(r, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	if plen > 1<<20 {
		return 0, nil, errors.New("ws frame too large")
	}
	payload = make([]byte, plen)
	if plen > 0 {
		if _, err = io.ReadFull(r, payload); err != nil {
			return 0, nil, err
		}
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i&3]
		}
	}
	return opcode, payload, nil
}

func (c *WSConn) writeFrame(opcode byte, payload []byte) error {
	c.writeM.Lock()
	defer c.writeM.Unlock()
	header := []byte{0x80 | opcode} // FIN=1
	n := len(payload)
	switch {
	case n < 126:
		header = append(header, byte(n))
	case n < 1<<16:
		header = append(header, 126, 0, 0)
		binary.BigEndian.PutUint16(header[len(header)-2:], uint16(n))
	default:
		header = append(header, 127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[len(header)-8:], uint64(n))
	}
	if _, err := c.bw.Write(header); err != nil {
		return err
	}
	if n > 0 {
		if _, err := c.bw.Write(payload); err != nil {
			return err
		}
	}
	return c.bw.Flush()
}

// SetReadDeadline lets the caller bound how long the read goroutine sits in
// io.ReadFull when the peer goes silent — typically called by the connection
// owner alongside the ticker that fires Ping().
func (c *WSConn) SetReadDeadline(d time.Duration) {
	if d <= 0 {
		_ = c.conn.SetReadDeadline(time.Time{})
		return
	}
	_ = c.conn.SetReadDeadline(time.Now().Add(d))
}

// BearerFromUpgrade extracts a token passed via the Sec-WebSocket-Protocol
// header. Returns "" if none. Auth middleware uses this so the WebSocket
// handshake doesn't depend on the browser sending cookies (Safari ITP / etc).
//
// Wire format (matches Kubernetes API server convention):
//
//	Sec-WebSocket-Protocol: etcd-ui.bearer, <opaque-token>, <another-subproto?>
//
// We accept any position pair (sentinel, token) so clients can negotiate
// other subprotocols alongside.
func BearerFromUpgrade(r *http.Request) string {
	for _, h := range r.Header.Values("Sec-WebSocket-Protocol") {
		parts := splitTrim(h)
		for i, p := range parts {
			if p == "etcd-ui.bearer" && i+1 < len(parts) {
				return parts[i+1]
			}
		}
	}
	return ""
}

// pickSubprotocol returns the first offered subprotocol that isn't the bearer
// sentinel or the token after it. Echoing one is mandatory in RFC 6455 when
// the client offered any — leaving it empty risks an Upgrade ping-pong.
func pickSubprotocol(r *http.Request) string {
	for _, h := range r.Header.Values("Sec-WebSocket-Protocol") {
		parts := splitTrim(h)
		skipNext := false
		for _, p := range parts {
			if skipNext {
				skipNext = false
				continue
			}
			if p == "etcd-ui.bearer" {
				skipNext = true
				continue
			}
			if p != "" {
				return p
			}
		}
	}
	return ""
}

func splitTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// SendText writes one text frame. Caller is expected to call Close when done.
func (c *WSConn) SendText(payload []byte) error {
	header := []byte{0x81} // FIN=1, opcode=text
	n := len(payload)
	switch {
	case n < 126:
		header = append(header, byte(n))
	case n < 1<<16:
		header = append(header, 126, 0, 0)
		binary.BigEndian.PutUint16(header[len(header)-2:], uint16(n))
	default:
		header = append(header, 127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[len(header)-8:], uint64(n))
	}
	if _, err := c.bw.Write(header); err != nil {
		return err
	}
	if _, err := c.bw.Write(payload); err != nil {
		return err
	}
	return c.bw.Flush()
}

// Ping sends a 0x9 control frame with empty payload.
func (c *WSConn) Ping() error {
	if _, err := c.bw.Write([]byte{0x89, 0x00}); err != nil {
		return err
	}
	return c.bw.Flush()
}

// Close sends a close frame and tears down the TCP connection.
func (c *WSConn) Close() error {
	_, _ = c.bw.Write([]byte{0x88, 0x00})
	_ = c.bw.Flush()
	return c.conn.Close()
}

// ErrNotWebsocket means the request didn't ask for an upgrade — handlers
// should fall through to a regular HTTP response (typically SSE).
var ErrNotWebsocket = errors.New("not a websocket upgrade request")
