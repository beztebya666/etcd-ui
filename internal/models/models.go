package models

import "time"

// ClusterSummary is the public-facing shape returned by /api/clusters.
type ClusterSummary struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Source       string    `json:"source"`
	Endpoints    []string  `json:"endpoints"`
	Healthy      bool      `json:"healthy"`
	Leader       string    `json:"leader,omitempty"`
	// LeaderShort is `Leader` with the longest dot-prefixed suffix common
	// to all members stripped (`node-aa-1.internal.example` →
	// `node-aa-1` if every member shares `.internal.example`).
	// Optional — empty when there's no common suffix to strip.
	LeaderShort  string    `json:"leaderShort,omitempty"`
	LeaderID     uint64    `json:"leaderId,omitempty"`
	MemberCount  int       `json:"memberCount"`
	// Live count of keys in the keyspace right now. Cheap to fetch
	// (etcd returns it from MVCC without scanning). Zero on v2 or when
	// the count probe failed.
	KeyCount     int64     `json:"keyCount,omitempty"`
	DBSizeBytes  int64     `json:"dbSizeBytes"`
	DBSizeInUse  int64     `json:"dbSizeInUse"`
	Revision     int64     `json:"revision"`
	RaftTerm     uint64    `json:"raftTerm"`
	LastChecked  time.Time `json:"lastChecked"`
	Alarms       []string  `json:"alarms,omitempty"`
	Error        string    `json:"error,omitempty"`
	// API surface this cluster speaks. "v3" → gRPC over HTTP/2 (etcd 3.x),
	// "v2" → REST/JSON over HTTP/1.1 (etcd 2.x). Auto-detected from
	// /version on first contact. Empty until detection completes.
	APIVersion    string    `json:"apiVersion,omitempty"`
	// Cluster-reported version string (e.g. "3.5.15"). Optional; informational.
	ServerVersion string    `json:"serverVersion,omitempty"`
}

type Member struct {
	ID uint64 `json:"id"`
	// IDStr is the decimal-string representation of ID, sent alongside
	// because etcd member IDs are uint64 and routinely exceed JS's
	// 2^53 Number-precision ceiling (we hit this in the move-leader
	// flow — the SPA receives `7682169262620220000` from JSON, JS turns
	// it into a float64, the bottom 2-3 digits silently shift, and the
	// resulting move-leader call lands on the wrong member or fails).
	IDStr      string   `json:"idStr"`
	Name       string   `json:"name"`
	PeerURLs   []string `json:"peerUrls"`
	ClientURLs []string `json:"clientUrls"`
	IsLeader   bool     `json:"isLeader"`
	IsLearner  bool     `json:"isLearner"`
}

type KV struct {
	Key            string `json:"key"`
	Value          string `json:"value"`
	CreateRevision int64  `json:"createRevision"`
	ModRevision    int64  `json:"modRevision"`
	Version        int64  `json:"version"`
	Lease          int64  `json:"lease,omitempty"`
	// Server-decoded structured preview for binary formats we recognise
	// (currently: K8s protobuf wrapper, plain-JSON K8s objects). When
	// set, the SPA can render `kind / namespace/name` instead of a hex
	// dump. Empty for plain text / unknown binary.
	Preview *KVPreview `json:"preview,omitempty"`
}

// KVPreview mirrors k8sproto.Preview but lives here so HTTP responses
// don't drag in the decoder package across module boundaries. When
// `JSON` is populated, the SPA renders the full structured object via
// the syntax-highlighted viewer; when only metadata fields are set,
// the binary hex viewer is shown with the preview chip on top.
type KVPreview struct {
	Format      string `json:"format"`
	APIVersion  string `json:"apiVersion,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	Name        string `json:"name,omitempty"`
	JSON        string `json:"json,omitempty"`
	DecodeError string `json:"decodeError,omitempty"`
}

type RangeRequest struct {
	Prefix     string `json:"prefix,omitempty"`
	From       string `json:"from,omitempty"`
	End        string `json:"end,omitempty"`
	Limit      int64  `json:"limit,omitempty"`
	KeysOnly   bool   `json:"keysOnly,omitempty"`
	CountOnly  bool   `json:"countOnly,omitempty"`
	Revision   int64  `json:"revision,omitempty"`
	// Optional server-side regex filters. KeyRegex matches against the key
	// (re-uses Go's regexp/syntax). ValueRegex matches against the value;
	// keys whose value doesn't match are dropped.
	KeyRegex   string `json:"keyRegex,omitempty"`
	ValueRegex string `json:"valueRegex,omitempty"`
}

type RangeResponse struct {
	KVs   []KV  `json:"kvs"`
	More  bool  `json:"more"`
	Count int64 `json:"count"`
}

type PutRequest struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	LeaseID int64  `json:"leaseId,omitempty"`
}

// PutCASRequest is a compare-and-set put with 3-way merge fallback.
// `baseRev` is the modRevision the client read before editing; the server
// uses it as the merge base when the live revision has moved forward.
// PutK8sRequest writes a K8s object the SPA has been editing as
// structured JSON. The server re-encodes (proto or pretty-JSON) before
// writing so kube-apiserver continues to read the value as it expects.
type PutK8sRequest struct {
	Key        string `json:"key"`
	// Format must match what the original Decode returned for the key:
	//   "k8s-proto" — wrap edits in runtime.Unknown + protobuf
	//   "json"     — write minified JSON (CRDs without typed shim)
	Format     string `json:"format"`
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	JSON       string `json:"json"`
	BaseRev    int64  `json:"baseRev"`
}

type PutK8sResponse struct {
	Status   string `json:"status"`
	Revision int64  `json:"revision,omitempty"`
}

type PutCASRequest struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	BaseRev int64  `json:"baseRev"`
	// When true, merge conflicts are committed verbatim (with <<<<<<<
	// markers) — useful for power users who want to resolve in their text
	// editor of choice. Default is to return 409 and let the SPA prompt.
	AcceptConflicts bool `json:"acceptConflicts,omitempty"`
}

// PutCASResponse summarises what happened.  Status:
//   "ok"        — straight put, no contention
//   "merged"    — automatic 3-way merge succeeded, value updated
//   "conflict"  — merge has conflicts; `merged` carries <<<<<<< markers,
//                 nothing written. Client should pop the resolver UI.
type PutCASResponse struct {
	Status    string `json:"status"`
	Revision  int64  `json:"revision,omitempty"`
	Merged    string `json:"merged,omitempty"`    // present for merged + conflict
	Base      string `json:"base,omitempty"`      // present for conflict
	Theirs    string `json:"theirs,omitempty"`    // present for conflict
	Conflicts int    `json:"conflicts,omitempty"` // count of <<<<<<< blocks
}

type DeleteRequest struct {
	Key    string `json:"key"`
	Prefix bool   `json:"prefix,omitempty"`
}

type WatchEvent struct {
	Type      string     `json:"type"` // PUT | DELETE
	Key       string     `json:"key"`
	Value     string     `json:"value,omitempty"`
	PrevValue string     `json:"prevValue,omitempty"`
	Revision  int64      `json:"revision"`
	Preview   *KVPreview `json:"preview,omitempty"`
}

type Lease struct {
	ID           int64    `json:"id"`
	TTL          int64    `json:"ttl"`
	GrantedTTL   int64    `json:"grantedTtl"`
	AttachedKeys []string `json:"attachedKeys,omitempty"`
	// Best-effort enrichment fields derived from the attached keys'
	// values when they look like Kubernetes Lease objects (most leases
	// in a K8s cluster are: every kubelet, every controller-manager
	// leader, every scheduler leader, etc).
	HolderIdentity     string `json:"holderIdentity,omitempty"`
	HolderKind         string `json:"holderKind,omitempty"`         // "kube-scheduler" | "kube-controller-manager" | "kubelet" | …
	RenewedAt          string `json:"renewedAt,omitempty"`          // RFC3339 from K8s Lease.spec.renewTime
	AcquiredAt         string `json:"acquiredAt,omitempty"`         // RFC3339 from K8s Lease.spec.acquireTime
	LeaseTransitions   int32  `json:"leaseTransitions,omitempty"`   // K8s Lease.spec.leaseTransitions
	// AttachedCount lets the SPA show "12 keys" even when AttachedKeys
	// is trimmed (we cap to a small sample to avoid sending megabytes
	// for things like Vault dynamic-secret leases).
	AttachedCount int `json:"attachedCount"`
}

type HistoryResponse struct {
	Key             string `json:"key"`
	CurrentRevision int64  `json:"currentRevision"`
	Versions        []KV   `json:"versions"`
	TruncatedAt     string `json:"truncatedAt,omitempty"`
}

type DiffEntry struct {
	Key    string `json:"key"`
	Kind   string `json:"kind"` // only-left | only-right | different
	Left   string `json:"left,omitempty"`
	Right  string `json:"right,omitempty"`
}

type DiffResponse struct {
	Left  string      `json:"left"`
	Right string      `json:"right"`
	Total int         `json:"total"`
	Diffs []DiffEntry `json:"diffs"`
}
