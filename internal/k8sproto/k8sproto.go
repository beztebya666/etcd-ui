// Package k8sproto extracts a small, structured preview from raw K8s
// objects stored in etcd.
//
// Every K8s object goes into etcd as:
//
//	"k8s\x00" + runtime.Unknown{ TypeMeta, raw: <ObjectMeta + spec + status> }
//
// runtime.Unknown is protobuf with two stable fields we care about:
//
//	field 1 (TypeMeta):  { apiVersion: string, kind: string }
//	field 2 (raw bytes): the actual encoded object — starts with ObjectMeta
//	                     which itself has name (field 1) and namespace (field 3).
//
// We don't link the full K8s proto schema (megabytes of generated code)
// because we only need four fields. The wire format is simple enough to
// decode by hand and survives across every K8s version since 1.6.
//
// Doing this server-side instead of in the SPA solves a real problem:
// JSON-marshalling a Go string holding arbitrary bytes replaces every
// invalid-UTF-8 byte with the Unicode replacement char (U+FFFD), so the
// SPA never receives the raw protobuf intact. Parsing here means the
// SPA gets a clean { kind, apiVersion, name, namespace } regardless.

package k8sproto

import "strings"

// Preview is the lightweight summary attached to KV responses for K8s
// objects. Format = "" means "no preview" (caller should fall back to
// raw hex / text).
type Preview struct {
	Format     string `json:"format"`               // "k8s-proto" | "json" | ""
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name,omitempty"`
}

// Decode returns a non-nil Preview when the value looks like a K8s
// protobuf object. nil means "not recognised, caller decides what to do".
func Decode(value []byte) *Preview {
	if p := decodeK8sProto(value); p != nil {
		return p
	}
	if p := decodeJSON(value); p != nil {
		return p
	}
	return nil
}

// --- K8s protobuf ---------------------------------------------------------

var k8sMagic = []byte{'k', '8', 's', 0}

func decodeK8sProto(v []byte) *Preview {
	if len(v) < 6 || !startsWith(v, k8sMagic) {
		return nil
	}
	v = v[4:]

	// runtime.Unknown { typeMeta = field 1 (tag 0x0a, length-delimited) }
	apiVersion, kind, raw, ok := readTypeMetaAndRaw(v)
	if !ok {
		return nil
	}
	p := &Preview{Format: "k8s-proto", APIVersion: apiVersion, Kind: kind}

	// Inner object starts with ObjectMeta as field 1 (tag 0x0a).
	if len(raw) >= 2 && raw[0] == 0x0a {
		metaLen, n := readVarint(raw[1:])
		if n > 0 {
			start := 1 + n
			end := start + int(metaLen)
			if end <= len(raw) {
				p.Name, p.Namespace = readObjectMeta(raw[start:end])
			}
		}
	}
	return p
}

// readTypeMetaAndRaw expects the body of a runtime.Unknown message
// (without the outer length prefix — the magic-stripped value is the
// message body directly, since runtime.Unknown is the root).
//
// Layout:
//
//	0x0a <varint len> <TypeMeta bytes>
//	0x12 <varint len> <raw bytes>
//	... (optional content_encoding / content_type — ignored)
func readTypeMetaAndRaw(v []byte) (apiVersion, kind string, raw []byte, ok bool) {
	if len(v) == 0 || v[0] != 0x0a {
		return "", "", nil, false
	}
	tlen, n := readVarint(v[1:])
	if n == 0 {
		return "", "", nil, false
	}
	tmStart := 1 + n
	tmEnd := tmStart + int(tlen)
	if tmEnd > len(v) {
		return "", "", nil, false
	}
	apiVersion, kind = readTypeMeta(v[tmStart:tmEnd])

	// Optional next field: raw bytes (tag 0x12).
	rest := v[tmEnd:]
	if len(rest) >= 2 && rest[0] == 0x12 {
		rl, m := readVarint(rest[1:])
		if m > 0 {
			rStart := 1 + m
			rEnd := rStart + int(rl)
			if rEnd <= len(rest) {
				raw = rest[rStart:rEnd]
			}
		}
	}
	return apiVersion, kind, raw, true
}

// TypeMeta { apiVersion = field 1 (0x0a), kind = field 2 (0x12) }
func readTypeMeta(v []byte) (apiVersion, kind string) {
	for len(v) > 0 {
		tag := v[0]
		v = v[1:]
		// Only length-delimited fields (wire type 2) — bail on anything else.
		if tag&0x07 != 2 {
			return
		}
		l, n := readVarint(v)
		if n == 0 || int(l) > len(v)-n {
			return
		}
		s := string(v[n : n+int(l)])
		v = v[n+int(l):]
		switch tag >> 3 {
		case 1:
			apiVersion = s
		case 2:
			kind = s
		}
	}
	return
}

// ObjectMeta { name = field 1, namespace = field 3 }. Tolerant: stops at
// the first non-length-delimited field rather than parsing every type.
func readObjectMeta(v []byte) (name, namespace string) {
	for len(v) > 0 {
		tag := v[0]
		v = v[1:]
		if tag&0x07 != 2 {
			return
		}
		l, n := readVarint(v)
		if n == 0 || int(l) > len(v)-n {
			return
		}
		s := string(v[n : n+int(l)])
		v = v[n+int(l):]
		switch tag >> 3 {
		case 1:
			name = s
		case 3:
			namespace = s
		}
		if name != "" && namespace != "" {
			return
		}
	}
	return
}

func readVarint(v []byte) (uint64, int) {
	var x uint64
	var s uint
	for i, b := range v {
		if i >= 10 {
			return 0, 0
		}
		if b < 0x80 {
			return x | uint64(b)<<s, i + 1
		}
		x |= uint64(b&0x7f) << s
		s += 7
	}
	return 0, 0
}

func startsWith(b, prefix []byte) bool {
	if len(b) < len(prefix) {
		return false
	}
	for i := range prefix {
		if b[i] != prefix[i] {
			return false
		}
	}
	return true
}

// --- JSON peek ------------------------------------------------------------

// decodeJSON catches plain-JSON K8s objects (kube-apiserver can be
// configured to store JSON instead of protobuf). Cheap: looks for the
// distinctive `{"kind":"X","apiVersion":"Y"` pattern in the first 256
// bytes, no full JSON parse.
func decodeJSON(v []byte) *Preview {
	if len(v) < 16 || v[0] != '{' {
		return nil
	}
	head := v
	if len(head) > 1024 {
		head = head[:1024]
	}
	s := string(head)
	if !strings.Contains(s, `"kind":"`) || !strings.Contains(s, `"apiVersion":"`) {
		return nil
	}
	return &Preview{
		Format:     "json",
		Kind:       extractString(s, `"kind":"`),
		APIVersion: extractString(s, `"apiVersion":"`),
		Name:       extractString(s, `"name":"`),
		Namespace:  extractString(s, `"namespace":"`),
	}
}

func extractString(haystack, needle string) string {
	i := strings.Index(haystack, needle)
	if i < 0 {
		return ""
	}
	rest := haystack[i+len(needle):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}
