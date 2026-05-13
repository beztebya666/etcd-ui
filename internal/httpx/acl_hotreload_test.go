package httpx

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Reads/writes to the same file from multiple goroutines — the reloader has
// to see the new mtime and swap atomically without corrupting the live rule
// set.
func TestACL_HotReload_SwapsRulesAfterWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "acl.json")
	if err := os.WriteFile(path, []byte(`[{"user":"alice","cluster":"prod","access":"read"}]`), 0o644); err != nil {
		t.Fatal(err)
	}

	a := NewACL()
	if err := a.LoadFile(path); err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if got := a.Resolve("alice", "prod", "/x"); got != AccessRead {
		t.Fatalf("initial Resolve = %q, want read", got)
	}

	// Reload events come through OnReload; collect them so the test waits
	// for the actual swap, not a wall-clock sleep.
	reloads := make(chan int, 4)
	a.OnReload(func(n int) { reloads <- n })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Aggressive poll for tests — production is 5s.
	go a.Watch(ctx, 50*time.Millisecond)

	// Sleep enough that the next mtime tick definitely differs (some FS
	// have 1s resolution on ModTime).
	time.Sleep(1100 * time.Millisecond)

	if err := os.WriteFile(path, []byte(`[
		{"user":"alice","cluster":"prod","access":"admin"},
		{"user":"bob","cluster":"*","access":"write"}
	]`), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case n := <-reloads:
		if n != 2 {
			t.Fatalf("OnReload reported %d rules, want 2", n)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ACL never hot-reloaded within 3s after file rewrite")
	}

	if got := a.Resolve("alice", "prod", "/x"); got != AccessAdmin {
		t.Fatalf("after reload alice → %q, want admin", got)
	}
	if got := a.Resolve("bob", "anything", "/"); got != AccessWrite {
		t.Fatalf("after reload bob → %q, want write", got)
	}
}

// Bad JSON written to the file MUST NOT replace the live rule set. We keep
// serving traffic against the last good snapshot and signal -1 via OnReload.
func TestACL_HotReload_BadJSONPreservesPrevious(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "acl.json")
	if err := os.WriteFile(path, []byte(`[{"user":"alice","cluster":"prod","access":"write"}]`), 0o644); err != nil {
		t.Fatal(err)
	}

	a := NewACL()
	if err := a.LoadFile(path); err != nil {
		t.Fatal(err)
	}

	var failures atomic.Int32
	a.OnReload(func(n int) {
		if n < 0 {
			failures.Add(1)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Watch(ctx, 50*time.Millisecond)

	time.Sleep(1100 * time.Millisecond)
	// Drop garbage on disk.
	if err := os.WriteFile(path, []byte(`not json at all`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait for the failure signal.
	deadline := time.After(3 * time.Second)
	for {
		if failures.Load() > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("OnReload never fired with -1 after bad JSON")
		case <-time.After(50 * time.Millisecond):
		}
	}

	// Live set must still resolve correctly.
	if got := a.Resolve("alice", "prod", "/x"); got != AccessWrite {
		t.Fatalf("after bad JSON alice → %q, want write (kept previous)", got)
	}
}

// Concurrent Resolve while reloads happen — race detector must not fire.
func TestACL_HotReload_ConcurrentReaders(t *testing.T) {
	if testing.Short() {
		t.Skip("skip race-heavy test under -short")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "acl.json")
	_ = os.WriteFile(path, []byte(`[{"user":"u","cluster":"c","access":"read"}]`), 0o644)
	a := NewACL()
	_ = a.LoadFile(path)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Watch(ctx, 20*time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5000; j++ {
				_ = a.Resolve("u", "c", "/x")
			}
		}()
	}
	// Concurrent writer (every 50ms for 1s).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			body := `[{"user":"u","cluster":"c","access":"read"}]`
			if i%2 == 1 {
				body = `[{"user":"u","cluster":"c","access":"write"}]`
			}
			_ = os.WriteFile(path, []byte(body), 0o644)
			time.Sleep(50 * time.Millisecond)
		}
	}()
	wg.Wait()
}
