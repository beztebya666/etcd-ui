package audit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendAndTail(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for i := 0; i < 6; i++ {
		s.Append(Event{Action: "test", Status: 200})
	}

	tail := s.Tail(0) // all
	if len(tail) != 4 {
		t.Fatalf("want 4 in ring, got %d", len(tail))
	}
	if tail[0].ID != 6 || tail[3].ID != 3 {
		t.Fatalf("wrong order, got first=%d last=%d", tail[0].ID, tail[3].ID)
	}

	since := s.Since(4, 0)
	if len(since) != 2 || since[0].ID != 6 || since[1].ID != 5 {
		t.Fatalf("since(4) want ids 6,5; got %+v", since)
	}
}

func TestPersistAcrossOpen(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	s.Append(Event{Action: "a"})
	s.Append(Event{Action: "b"})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, "audit.jsonl")); err != nil {
		t.Fatalf("audit file missing: %v", err)
	}

	s2, err := New(dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	tail := s2.Tail(0)
	if len(tail) != 2 {
		t.Fatalf("want 2 events after reopen, got %d", len(tail))
	}
	if tail[0].Action != "b" || tail[1].Action != "a" {
		t.Fatalf("order/contents wrong: %+v", tail)
	}
}
