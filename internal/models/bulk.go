package models

// BulkPutRequest applies many puts atomically when Transactional == true,
// otherwise sequentially.
type BulkPutRequest struct {
	Items         []PutRequest `json:"items"`
	Transactional bool         `json:"transactional"`
}

type BulkDeleteRequest struct {
	Keys     []string `json:"keys"`
	Prefixes []string `json:"prefixes"`
}

type BulkResult struct {
	Applied  int    `json:"applied"`
	Failed   int    `json:"failed"`
	Revision int64  `json:"revision,omitempty"`
	Error    string `json:"error,omitempty"`
}

// TxnCondition describes a comparison in an etcd transaction.
//
//	field:  value | createRevision | modRevision | version
//	op:     equal | not-equal | greater | less
//	target: string for value, int64 for revisions/version
type TxnCondition struct {
	Key    string `json:"key"`
	Field  string `json:"field"` // value | createRevision | modRevision | version
	Op     string `json:"op"`    // equal | not-equal | greater | less
	Target string `json:"target"`
}

type TxnOp struct {
	Type   string `json:"type"` // put | delete | get
	Key    string `json:"key"`
	Value  string `json:"value,omitempty"`
	Prefix bool   `json:"prefix,omitempty"`
}

type TxnRequest struct {
	Conditions []TxnCondition `json:"conditions"`
	OnSuccess  []TxnOp        `json:"onSuccess"`
	OnFailure  []TxnOp        `json:"onFailure"`
}

type TxnResponse struct {
	Succeeded bool   `json:"succeeded"`
	Revision  int64  `json:"revision"`
	Error     string `json:"error,omitempty"`
}

// RestoreSnapshot is the JSON shape exported by /api/clusters/{id}/export and
// accepted by /api/clusters/{id}/restore. Compatible with .json from etcdctl
// get --prefix '' --print-value-only=false …; see exporter for details.
type RestoreSnapshot struct {
	Cluster    string `json:"cluster"`
	ExportedAt string `json:"exportedAt"`
	Revision   int64  `json:"revision"`
	KVs        []KV   `json:"kvs"`
}

type RestoreOptions struct {
	ClearTargetPrefix string `json:"clearTargetPrefix"` // delete-prefix before importing
	DryRun            bool   `json:"dryRun"`
}
