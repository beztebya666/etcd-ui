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

// atomicWrite mimics how kube-apiserver / K8s ConfigMap mounts replace
// files in production: write a temp file in the same directory, then
// rename onto the target. The rename is atomic at the filesystem level,
// so the watcher never observes a half-written file (which would
// produce a spurious -1 "invalid JSON" reload event mid-test under -race
// where goroutine scheduling magnifies the window). Tests that use this
// only fire ONE reload callback — the one that sees the final content —
// matching what production behavior actually is.
func atomicWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	tmp, err := os.CreateTemp(filepath.Dir(path), "acl-test-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmp.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		t.Fatal(err)
	}
}

// startWatch spawns a.Watch in a goroutine and registers a t.Cleanup
// that cancels the context AND waits for Watch to return before
// t.TempDir's deferred RemoveAll runs. Without this the watcher can be
// mid-read on a file that the test framework is concurrently unlinking,
// which on Linux surfaces as "unlinkat ...: bad file descriptor" — a
// real source of CI flakes that have nothing to do with the code under
// test. t.Cleanup callbacks run in LIFO order, and t.TempDir registers
// its own cleanup at the time of the call; so as long as we call
// t.TempDir() BEFORE startWatch(), our cleanup pops first and finishes
// the goroutine before the directory disappears.
func startWatch(t *testing.T, a *ACL, period time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		a.Watch(ctx, period)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Logf("ACL.Watch did not exit within 2s of cancel — TempDir cleanup may race")
		}
	})
}

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

	// Aggressive poll for tests — production is 5s.
	startWatch(t, a, 50*time.Millisecond)

	// Sleep enough that the next mtime tick definitely differs (some FS
	// have 1s resolution on ModTime).
	time.Sleep(1100 * time.Millisecond)

	atomicWrite(t, path, []byte(`[
		{"user":"alice","cluster":"prod","access":"admin"},
		{"user":"bob","cluster":"*","access":"write"}
	]`))

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
	startWatch(t, a, 50*time.Millisecond)

	time.Sleep(1100 * time.Millisecond)
	// Drop garbage on disk — atomic rename matches what an editor's
	// "save" actually does, and crucially means the watcher sees a
	// well-defined "before" and "after" rather than catching the file
	// mid-write (which would also trip the -1 callback we're testing,
	// muddying the assertion).
	atomicWrite(t, path, []byte(`not json at all`))

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

	startWatch(t, a, 20*time.Millisecond)

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
			// Atomic rename matches production K8s ConfigMap behaviour
			// and stops the watcher from observing a half-written file
			// (which would briefly emit invalid-JSON reload events,
			// muddy the race trace).
			tmp, err := os.CreateTemp(filepath.Dir(path), "acl-race-*")
			if err != nil {
				return
			}
			_, _ = tmp.Write([]byte(body))
			_ = tmp.Close()
			_ = os.Rename(tmp.Name(), path)
			time.Sleep(50 * time.Millisecond)
		}
	}()
	wg.Wait()
}
