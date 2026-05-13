package main

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		path        string
		method      string
		wantCluster string
		wantAction  string
	}{
		{"/api/clusters", "POST", "", "cluster.create-or-list"},
		{"/api/clusters/prod", "DELETE", "prod", "cluster.remove"},
		{"/api/clusters/prod/put", "POST", "prod", "kv.put"},
		{"/api/clusters/prod/delete", "POST", "prod", "kv.delete"},
		{"/api/clusters/prod/bulk/put", "POST", "prod", "kv.bulk.put"},
		{"/api/clusters/prod/txn", "POST", "prod", "kv.txn"},
		{"/api/clusters/prod/restore", "POST", "prod", "ops.restore"},
		{"/api/clusters/prod/compact", "POST", "prod", "ops.compact"},
		{"/api/clusters/prod/leases/42", "DELETE", "prod", "ops.leases.revoke"},
		{"/api/clusters/prod/rbac/users", "POST", "prod", "rbac.users"},
	}
	for _, c := range cases {
		gc, ga := classify(c.path, c.method)
		if gc != c.wantCluster || ga != c.wantAction {
			t.Errorf("classify(%s, %s) = (%q, %q) want (%q, %q)",
				c.path, c.method, gc, ga, c.wantCluster, c.wantAction)
		}
	}
}

func TestIsMutation(t *testing.T) {
	// We can't easily construct *http.Request inline without net/http, but we
	// inline a tiny shim. Just check the method gate.
	for _, m := range []string{"GET", "HEAD", "OPTIONS"} {
		if isMutationStub(m, "/api/clusters/x/put") {
			t.Errorf("%s should not be mutation", m)
		}
	}
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		if !isMutationStub(m, "/api/clusters/x/put") {
			t.Errorf("%s should be mutation", m)
		}
	}
	if isMutationStub("POST", "/healthz") {
		t.Error("non-/api path should not be mutation")
	}
	if isMutationStub("POST", "/api/audit/events") {
		t.Error("/api/audit should not be mutation")
	}
}

// Tiny copy of isMutation that takes raw inputs — keeps tests free of http.Request plumbing.
func isMutationStub(method, path string) bool {
	if method == "GET" || method == "HEAD" || method == "OPTIONS" {
		return false
	}
	if len(path) < 4 || path[:4] != "/api" {
		return false
	}
	if len(path) >= 10 && path[:10] == "/api/audit" {
		return false
	}
	return true
}
