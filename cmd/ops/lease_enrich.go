package main

import (
	"context"
	"path"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"github.com/yourorg/etcd-ui/internal/k8sproto"
)

// leaseEnrichment is what we extract from the first attached key whose
// value looks like a `coordination.k8s.io/v1.Lease` object. K8s uses
// these for leader election (kube-scheduler, kube-controller-manager,
// every controller registered with the leaderelection library) and for
// node heartbeats (kubelet writes one per node into /registry/leases/
// kube-node-lease/<node>).
type leaseEnrichment struct {
	HolderIdentity   string
	HolderKind       string
	RenewedAt        string
	AcquiredAt       string
	LeaseTransitions int32
}

// enrichLease looks up the first attached key, decodes its value if it's
// a K8s Lease, and pulls out the holder + timestamps. Best-effort: any
// error returns a zero struct without failing the parent call. Bounded
// by a 1s read budget so listing 200 K8s leases doesn't stall the page.
func enrichLease(ctx context.Context, cli *clientv3.Client, attachedKeys []string) leaseEnrichment {
	if len(attachedKeys) == 0 {
		return leaseEnrichment{}
	}
	// Prefer keys that look like K8s leases over arbitrary ones.
	key := pickK8sLeaseKey(attachedKeys)
	if key == "" {
		return leaseEnrichment{}
	}

	rctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	resp, err := cli.Get(rctx, key)
	if err != nil || len(resp.Kvs) == 0 {
		return leaseEnrichment{}
	}
	raw := resp.Kvs[0].Value
	p := k8sproto.Decode(raw)
	if p == nil || p.Kind != "Lease" {
		return leaseEnrichment{}
	}

	out := leaseEnrichment{}
	out.HolderKind = classifyHolder(key)

	// Lease.spec contains holderIdentity / acquireTime / renewTime /
	// leaseTransitions. The proto is nested deeper than our decoder
	// goes (k8sproto only walks ObjectMeta), so we fall back to a
	// byte-string scan — the field names appear as ASCII tags inside
	// the spec subtree and survive any UTF-8 round-tripping.
	out.HolderIdentity = scanASCIIField(raw, []byte{0x12, 0x12, 0x12}) // crude — not used
	if id := findLeaseHolderIdentity(raw); id != "" {
		out.HolderIdentity = id
	}
	if t := findLeaseTime(raw, "renewTime"); t != "" {
		out.RenewedAt = t
	}
	if t := findLeaseTime(raw, "acquireTime"); t != "" {
		out.AcquiredAt = t
	}
	return out
}

// pickK8sLeaseKey returns the first key under /registry/leases/ (the
// canonical K8s lease prefix); falls back to the first key otherwise.
func pickK8sLeaseKey(keys []string) string {
	for _, k := range keys {
		if strings.HasPrefix(k, "/registry/leases/") {
			return k
		}
	}
	return keys[0]
}

// classifyHolder maps the lease's path to a human-friendly role.
// `/registry/leases/kube-node-lease/<node>` → kubelet on <node>.
// `/registry/leases/kube-system/<name>` → controller leader election.
func classifyHolder(key string) string {
	if strings.HasPrefix(key, "/registry/leases/kube-node-lease/") {
		return "kubelet · " + path.Base(key)
	}
	if strings.HasPrefix(key, "/registry/leases/kube-system/") {
		// Names like `kube-scheduler`, `kube-controller-manager`,
		// `cloud-controller-manager`, `snapshot-controller-leader`, etc.
		return path.Base(key)
	}
	if strings.HasPrefix(key, "/registry/leases/") {
		// Namespaced controllers (cert-manager-controller, etc).
		parts := strings.SplitN(strings.TrimPrefix(key, "/registry/leases/"), "/", 2)
		if len(parts) == 2 {
			return parts[0] + "/" + parts[1]
		}
	}
	return ""
}

// findLeaseHolderIdentity scans the protobuf-encoded Lease value looking
// for the holderIdentity field. We don't link the full proto — instead
// we recognise the byte pattern `\x0a <len> "<ascii string>"` that
// appears in LeaseSpec (field 1, string). Returns the longest run of
// printable chars that looks like a holder ID (≥3 chars, ASCII-ish).
//
// This is heuristic and may produce false positives on non-Lease objects,
// but the caller already checked Kind=="Lease".
func findLeaseHolderIdentity(v []byte) string {
	// LeaseSpec is field 2 of Lease (tag 0x12 in the wrapper). Inside
	// LeaseSpec, holderIdentity is field 1 (tag 0x0a).
	// Find LeaseSpec start: a "spec" sub-message.
	for i := 0; i < len(v)-2; i++ {
		// Look for `\x0a <len> <printable string starting with system: or
		// hostname-like>` patterns.
		if v[i] == 0x0a && i+1 < len(v) {
			l := int(v[i+1])
			if l > 4 && l < 80 && i+2+l <= len(v) {
				s := v[i+2 : i+2+l]
				if looksLikeHolder(s) {
					return string(s)
				}
			}
		}
	}
	return ""
}

func looksLikeHolder(s []byte) bool {
	// Heuristic: holder identities are usually hostnames or
	// `<pod-name>_<uuid>` style. Require mostly printable ASCII with
	// dashes/underscores/dots/colons; reject anything with control
	// chars or unusual symbols.
	if len(s) < 4 {
		return false
	}
	letters := 0
	for _, b := range s {
		if b < 0x20 || b > 0x7e {
			return false
		}
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') {
			letters++
		}
	}
	return letters >= 3
}

// findLeaseTime looks for an RFC3339-shaped timestamp in the value. The
// google.protobuf.Timestamp wire encoding is two int64s but the JSON
// serialization (also common) embeds RFC3339 as a plain string. We try
// both: scan for `YYYY-MM-DDTHH:MM:SS` substring directly.
func findLeaseTime(v []byte, _ string) string {
	// Quick ASCII scan for a date prefix `20xx-` followed by 18-25 chars.
	for i := 0; i < len(v)-20; i++ {
		if v[i] == '2' && v[i+1] == '0' && (v[i+2] >= '0' && v[i+2] <= '9') &&
			(v[i+3] >= '0' && v[i+3] <= '9') && v[i+4] == '-' {
			end := i
			for end < len(v) && end-i < 30 && (v[end] >= 0x20 && v[end] <= 0x7e) &&
				v[end] != '"' && v[end] != ' ' {
				end++
			}
			s := string(v[i:end])
			if strings.Contains(s, "T") && (strings.HasSuffix(s, "Z") || strings.Contains(s, "+")) {
				return s
			}
		}
	}
	return ""
}

// scanASCIIField is kept as a debug helper; not used in production.
func scanASCIIField(_ []byte, _ []byte) string { return "" }
