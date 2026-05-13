package threewaymerge

import (
	"strings"
	"testing"
)

func TestMerge_NoChange(t *testing.T) {
	r := Merge("a\nb\nc", "a\nb\nc", "a\nb\nc")
	if r.Status != StatusNoChange {
		t.Errorf("want StatusNoChange, got %v", r.Status)
	}
}

func TestMerge_OnlyOurs(t *testing.T) {
	r := Merge("a\nb\nc\n", "a\nX\nc\n", "a\nb\nc\n")
	if r.Status != StatusClean {
		t.Fatalf("want clean, got %v", r.Status)
	}
	if r.Merged != "a\nX\nc\n" {
		t.Errorf("got %q", r.Merged)
	}
}

func TestMerge_OnlyTheirs(t *testing.T) {
	r := Merge("a\nb\nc\n", "a\nb\nc\n", "a\nY\nc\n")
	if r.Status != StatusClean {
		t.Fatalf("want clean, got %v", r.Status)
	}
	if r.Merged != "a\nY\nc\n" {
		t.Errorf("got %q", r.Merged)
	}
}

func TestMerge_NonOverlapping(t *testing.T) {
	// We change line 2, they change line 4 — should weave cleanly.
	base := "a\nb\nc\nd\ne"
	ours := "a\nX\nc\nd\ne"
	theirs := "a\nb\nc\nY\ne"
	r := Merge(base, ours, theirs)
	if r.Status != StatusClean {
		t.Fatalf("expected clean weave, got status=%v merged=%q", r.Status, r.Merged)
	}
	if !strings.Contains(r.Merged, "X") || !strings.Contains(r.Merged, "Y") {
		t.Fatalf("merged should carry both changes: %q", r.Merged)
	}
	if strings.Contains(r.Merged, "<<<<<<<") {
		t.Fatalf("should be conflict-free: %q", r.Merged)
	}
}

func TestMerge_Conflict(t *testing.T) {
	base := "a\nb\nc"
	ours := "a\nX\nc"
	theirs := "a\nY\nc"
	r := Merge(base, ours, theirs)
	if r.Status != StatusConflict {
		t.Fatalf("expected conflict, got %v", r.Status)
	}
	for _, marker := range []string{"<<<<<<< ours", "=======", ">>>>>>> theirs", "X", "Y"} {
		if !strings.Contains(r.Merged, marker) {
			t.Errorf("missing marker %q in:\n%s", marker, r.Merged)
		}
	}
	if r.Conflicts != 1 {
		t.Errorf("conflict count: got %d, want 1", r.Conflicts)
	}
}

func TestMerge_IdenticalReplacement(t *testing.T) {
	// Both sides made the same edit. Should apply once, no conflict.
	base := "a\nb\nc"
	ours := "a\nX\nc"
	theirs := "a\nX\nc"
	r := Merge(base, ours, theirs)
	if r.Status != StatusNoChange {
		t.Fatalf("want NoChange (ours==theirs), got %v", r.Status)
	}
}

func TestMerge_EmptyInputs(t *testing.T) {
	r := Merge("", "", "")
	if r.Status != StatusNoChange {
		t.Errorf("empty merge: got %v", r.Status)
	}
}

func TestMerge_PreservesTrailingNewline(t *testing.T) {
	r := Merge("a\n", "a\nb\n", "a\n")
	if !strings.HasSuffix(r.Merged, "\n") {
		t.Errorf("trailing newline lost: %q", r.Merged)
	}
}

func TestMerge_LargeFile(t *testing.T) {
	// 200 lines, change one in each — ours line 5, theirs line 195.
	mk := func(line int, val string) string {
		var sb strings.Builder
		for i := 0; i < 200; i++ {
			if i == line {
				sb.WriteString(val)
			} else {
				sb.WriteString("line-")
				sb.WriteString(strings.Repeat("x", i%5))
			}
			sb.WriteString("\n")
		}
		return sb.String()
	}
	base := mk(-1, "")
	ours := mk(5, "OURS")
	theirs := mk(195, "THEIRS")
	r := Merge(base, ours, theirs)
	if r.Status != StatusClean {
		t.Fatalf("status: %v", r.Status)
	}
	if !strings.Contains(r.Merged, "OURS") || !strings.Contains(r.Merged, "THEIRS") {
		t.Fatal("both changes should be present")
	}
}
