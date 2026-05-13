package main

import (
	"github.com/yourorg/etcd-ui/internal/k8sdecode"
	"github.com/yourorg/etcd-ui/internal/models"
)

// kvPreview returns a non-nil preview pointer when the raw bytes match
// a format we recognise:
//
//   - K8s protobuf wrapper (`k8s\0` prefix). Decoded against the linked
//     k8s.io/api scheme — 519 Kinds across every built-in group. When
//     the Kind is registered we emit a full JSON representation; when
//     it isn't (custom CRD proto, future apiserver release) we still
//     populate apiVersion/kind/namespace/name from the wrapper.
//   - JSON-encoded CRD (`{` prefix with kind+apiVersion fields). We
//     pretty-print it and surface the same metadata fields.
//
// IMPORTANT: must be called with the RAW []byte from etcd, not a
// `string(v)` round-trip — JSON marshalling replaces invalid UTF-8 with
// U+FFFD so the protobuf wire format would be corrupted if you let the
// SPA do the decode.
func kvPreview(raw []byte) *models.KVPreview {
	d := k8sdecode.Decode(raw)
	if d == nil {
		return nil
	}
	return &models.KVPreview{
		Format:      d.Format,
		APIVersion:  d.APIVersion,
		Kind:        d.Kind,
		Namespace:   d.Namespace,
		Name:        d.Name,
		JSON:        d.JSON,
		DecodeError: d.DecodeError,
	}
}
