package k8sdecode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// EncodeFromJSON is the inverse of Decode for `k8s-proto` values:
// takes the JSON the SPA edited (same shape Decode produced) and emits
// the bytes etcd should store — i.e. `k8s\0` + runtime.Unknown wrapper
// containing the proto-encoded object.
//
// We need this so operators can EDIT decoded K8s objects in the SPA's
// pretty JSON view and have those edits round-trip back into the
// cluster. Without it, the viewer would be read-only and changes would
// have to go through `kubectl edit`.
//
// Inputs:
//   - apiVersion + kind: typically taken from the original Decoded
//     wrapper so the user can't accidentally retype a Pod as a Service
//   - jsonBody: the edited JSON, as a string
//
// On unknown Kind we fail loudly — we WILL NOT silently fall through
// to "write the JSON bytes as etcd value" because kube-apiserver would
// then re-read the key and explode when it tried to proto-decode JSON.
func EncodeFromJSON(apiVersion, kind, jsonBody string) ([]byte, error) {
	if apiVersion == "" || kind == "" {
		return nil, errors.New("k8sdecode: apiVersion and kind required")
	}
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return nil, fmt.Errorf("bad apiVersion %q: %w", apiVersion, err)
	}
	gvk := gv.WithKind(kind)

	s := getScheme()
	obj, err := s.New(gvk)
	if err != nil {
		return nil, fmt.Errorf("no Go type for %s: %w", gvk.String(), err)
	}

	// Unmarshal the edited JSON into the typed struct. This validates
	// shape: typos in field names get caught here, not on the etcd
	// write. We DisallowUnknownFields to surface "spec.image" vs
	// "spec.images" mistakes immediately.
	dec := json.NewDecoder(bytes.NewReader([]byte(jsonBody)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(obj); err != nil {
		return nil, fmt.Errorf("invalid JSON for %s: %w", gvk.String(), err)
	}

	// Re-marshal to protobuf via the type's gen'd Marshal().
	m, ok := obj.(interface{ Marshal() ([]byte, error) })
	if !ok {
		return nil, fmt.Errorf("type %s missing Marshal method", gvk.String())
	}
	inner, err := m.Marshal()
	if err != nil {
		return nil, fmt.Errorf("proto marshal: %w", err)
	}

	// Wrap in runtime.Unknown. Wire format mirrors what kube-apiserver
	// emits: tag 0x0a (field 1, TypeMeta), 0x12 (field 2, raw bytes).
	var tm bytes.Buffer
	writeStringField(&tm, 1, apiVersion)
	writeStringField(&tm, 2, kind)

	var out bytes.Buffer
	out.WriteString("k8s\x00")
	writeBytesField(&out, 1, tm.Bytes())
	writeBytesField(&out, 2, inner)
	return out.Bytes(), nil
}

// EncodeJSON re-emits a CRD value (originally stored as JSON) after the
// user edited the structured view. Trivial passthrough that validates
// the input is still valid JSON. No schema enforcement — CRDs we don't
// have Go types for could be anything.
func EncodeJSON(jsonBody string) ([]byte, error) {
	var any json.RawMessage
	if err := json.Unmarshal([]byte(jsonBody), &any); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	// Re-emit minified to match the original wire format. The
	// apiserver doesn't care about whitespace but staying close to
	// the original shape keeps diff churn low for downstream watchers.
	var minified bytes.Buffer
	if err := json.Compact(&minified, []byte(jsonBody)); err != nil {
		return nil, fmt.Errorf("compact: %w", err)
	}
	return minified.Bytes(), nil
}

func writeVarint(w *bytes.Buffer, x uint64) {
	for x >= 0x80 {
		w.WriteByte(byte(x) | 0x80)
		x >>= 7
	}
	w.WriteByte(byte(x))
}

func writeBytesField(w *bytes.Buffer, fieldNum int, val []byte) {
	tag := uint64(fieldNum)<<3 | 2
	writeVarint(w, tag)
	writeVarint(w, uint64(len(val)))
	w.Write(val)
}

func writeStringField(w *bytes.Buffer, fieldNum int, val string) {
	writeBytesField(w, fieldNum, []byte(val))
}
