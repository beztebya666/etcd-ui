//go:build linux

package httpx

import (
	"context"
	"sync"

	"golang.org/x/sys/unix"
)

// watchNative uses inotify to react to ACL-file changes within milliseconds
// instead of the polling window. K8s ConfigMap mounts replace the file via
// a symlink swap (atomic rename of the parent), so we watch the directory
// for MOVED_TO / CREATE / MODIFY events targeting our filename.
//
// Returns (ok, done):
//   - ok is true if inotify is up and running; false on any setup failure
//     (no inotify available, EPERM on AddWatch, etc.) — caller then leaves
//     polling on as the fallback.
//   - done is closed when the inotify goroutine has fully exited (fd
//     closed, watch removed). Callers MUST drain `done` before letting
//     the watched directory be unlinked — otherwise tests racing with
//     `t.TempDir()` cleanup see EBADF / "bad file descriptor" on the
//     RemoveAll. Nil when ok=false.
func (a *ACL) watchNative(ctx context.Context) (bool, <-chan struct{}) {
	a.mu.RLock()
	path := a.path
	a.mu.RUnlock()
	if path == "" {
		return false, nil
	}
	dir, file := splitDirBase(path)

	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC)
	if err != nil {
		return false, nil
	}
	mask := uint32(unix.IN_MODIFY | unix.IN_CLOSE_WRITE | unix.IN_MOVED_TO | unix.IN_CREATE)
	wd, err := unix.InotifyAddWatch(fd, dir, mask)
	if err != nil {
		unix.Close(fd)
		return false, nil
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Single-shot close so the interrupter goroutine and the
		// outer defer can both call without double-close noise.
		var closeOnce sync.Once
		closeFd := func() {
			closeOnce.Do(func() {
				_, _ = unix.InotifyRmWatch(fd, uint32(wd))
				_ = unix.Close(fd)
			})
		}
		defer closeFd()

		// Interrupter: closes the fd when ctx cancels, which makes
		// the blocking Read below return EBADF and the loop exit.
		go func() {
			<-ctx.Done()
			closeFd()
		}()

		buf := make([]byte, 4096)
		for {
			n, err := unix.Read(fd, buf)
			if err != nil || n <= 0 {
				return
			}
			// We don't bother parsing individual events — any event on the
			// watched directory means we should diff and possibly reload.
			// checkReload() short-circuits when mtime hasn't moved, so
			// re-running it for unrelated files in the same dir is cheap.
			for _, ev := range parseInotify(buf[:n]) {
				if ev.name == "" || ev.name == file ||
					ev.name == file+".swp" /* editor swaps */ {
					a.checkReload()
					break
				}
			}
		}
	}()
	return true, done
}

type inotifyEvent struct {
	mask uint32
	name string
}

func parseInotify(buf []byte) []inotifyEvent {
	var out []inotifyEvent
	for len(buf) >= unix.SizeofInotifyEvent {
		raw := (*unix.InotifyEvent)(unsafePointer(&buf[0]))
		name := ""
		if raw.Len > 0 {
			nameBytes := buf[unix.SizeofInotifyEvent : unix.SizeofInotifyEvent+int(raw.Len)]
			// Trim trailing NULs (inotify pads the name field).
			for i := 0; i < len(nameBytes); i++ {
				if nameBytes[i] == 0 {
					nameBytes = nameBytes[:i]
					break
				}
			}
			name = string(nameBytes)
		}
		out = append(out, inotifyEvent{mask: raw.Mask, name: name})
		buf = buf[unix.SizeofInotifyEvent+int(raw.Len):]
	}
	return out
}

func splitDirBase(path string) (string, string) {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i], path[i+1:]
		}
	}
	return ".", path
}
