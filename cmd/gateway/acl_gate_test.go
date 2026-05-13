package main

import (
	"testing"

	"github.com/yourorg/etcd-ui/internal/httpx"
)

func loadACL(t *testing.T, rules string) *httpx.ACL {
	t.Helper()
	a := httpx.NewACL()
	if err := a.LoadJSON([]byte(rules)); err != nil {
		t.Fatalf("loadACL: %v", err)
	}
	return a
}

func TestACLCanEdit_BootstrapWildcardAdminWorks(t *testing.T) {
	a := loadACL(t, `[
		{"user":"alice","cluster":"*","access":"admin"},
		{"user":"bob","cluster":"prod","access":"write"}
	]`)
	if !aclCanEdit(a, "alice") {
		t.Error("wildcard-admin should be allowed during bootstrap (no __acl__ rule)")
	}
	if aclCanEdit(a, "bob") {
		t.Error("write-only bob must not be allowed to edit ACL")
	}
}

func TestACLCanEdit_ExplicitACLRuleLocksDown(t *testing.T) {
	// Once an __acl__ rule exists, only it counts — wildcard-admin alone
	// is NOT sufficient any more.
	a := loadACL(t, `[
		{"user":"alice","cluster":"*","access":"admin"},
		{"user":"carol","cluster":"__acl__","access":"admin"}
	]`)
	if aclCanEdit(a, "alice") {
		t.Error("wildcard-admin alice must NOT be allowed once carol holds __acl__ admin")
	}
	if !aclCanEdit(a, "carol") {
		t.Error("explicit __acl__ admin must be allowed")
	}
}

func TestACLCanEdit_DenialOnReadOrWrite(t *testing.T) {
	a := loadACL(t, `[
		{"user":"alice","cluster":"__acl__","access":"write"},
		{"user":"bob","cluster":"__acl__","access":"read"}
	]`)
	if aclCanEdit(a, "alice") || aclCanEdit(a, "bob") {
		t.Error("only admin on __acl__ may edit; read/write must be rejected")
	}
}
