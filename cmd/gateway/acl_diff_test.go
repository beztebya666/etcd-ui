package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/yourorg/etcd-ui/internal/httpx"
)

func itoa(i int) string { return strconv.Itoa(i) }

func TestAclDiff_Added(t *testing.T) {
	before := []httpx.ACLRule{
		{User: "alice", Cluster: "prod", Access: "read"},
	}
	after := append(before,
		httpx.ACLRule{User: "bob", Cluster: "*", Access: "write"},
	)
	d := aclDiff(before, after)
	if d.Added != 1 || d.Removed != 0 || d.Changed != 0 {
		t.Fatalf("expected 1 add, got %+v", d)
	}
	if d.Diff[0].Op != "add" || d.Diff[0].Rule.User != "bob" {
		t.Fatalf("wrong diff entry: %+v", d.Diff[0])
	}
}

func TestAclDiff_Removed(t *testing.T) {
	before := []httpx.ACLRule{
		{User: "alice", Cluster: "prod", Access: "read"},
		{User: "bob", Cluster: "*", Access: "write"},
	}
	after := before[:1]
	d := aclDiff(before, after)
	if d.Removed != 1 {
		t.Fatalf("expected 1 removal, got %+v", d)
	}
}

func TestAclDiff_Changed(t *testing.T) {
	before := []httpx.ACLRule{
		{User: "alice", Cluster: "prod", Prefix: "/svc/", Access: "read"},
	}
	after := []httpx.ACLRule{
		{User: "alice", Cluster: "prod", Prefix: "/svc/", Access: "admin"},
	}
	d := aclDiff(before, after)
	if d.Changed != 1 || d.Added != 0 || d.Removed != 0 {
		t.Fatalf("expected 1 change, got %+v", d)
	}
	if d.Diff[0].Op != "chg" {
		t.Fatalf("expected chg op, got %s", d.Diff[0].Op)
	}
	if d.Diff[0].Prior == nil || d.Diff[0].Prior.Access != "read" {
		t.Fatal("change diff must carry prior rule")
	}
}

func TestAclDiff_OverBudgetStripsBody(t *testing.T) {
	// Synthesise enough additions that the JSON body blows past 3.5 KB.
	// Users have to be unique or they collapse to one entry in the diff map.
	var after []httpx.ACLRule
	for i := 0; i < 200; i++ {
		after = append(after, httpx.ACLRule{
			User:    "user-" + strings.Repeat("x", 20) + "-" + itoa(i),
			Cluster: "cluster-" + strings.Repeat("y", 10),
			Access:  "read",
		})
	}
	d := aclDiff(nil, after)
	body := d.JSON()
	if len(body) > 3700 {
		t.Fatalf("body too big: %d bytes", len(body))
	}
	// Parsed body should keep counters but the per-row Diff field is empty.
	var parsed aclDiffResult
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Added != 200 {
		t.Fatalf("counters lost: %+v", parsed)
	}
	if len(parsed.Diff) != 0 {
		t.Fatalf("over-budget should drop Diff body, got %d entries", len(parsed.Diff))
	}
}
