//go:build linux

package httpx

import (
	"context"

	"golang.org/x/sys/unix"
)

// watchNative uses inotify to react to ACL-file changes within milliseconds
// instead of the polling window. K8s ConfigMap mounts replace the file via
// a symlink swap (atomic rename of the parent), so we watch the directory
// for MOVED_TO / CREATE / MODIFY events targeting our filename.
//
// Returns true if inotify is up and running; false on any setup failure
// (no inotify available, EPERM on AddWatch, etc.) — caller then leaves
// polling on as the fallback.
func (a *ACL) watchNative(ctx context.Context) bool {
	a.mu.RLock()
	path := a.path
	a.mu.RUnlock()
	if path == "" {
		return false
	}
	dir, file := splitDirBase(path)

	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC)
	if err != nil {
		return false
	}
	mask := uint32(unix.IN_MODIFY | unix.IN_CLOSE_WRITE | unix.IN_MOVED_TO | unix.IN_CREATE)
	wd, err := unix.InotifyAddWatch(fd, dir, mask)
	if err != nil {
		unix.Close(fd)
		return false
	}

	go func() {
		defer unix.Close(fd)
		defer unix.InotifyRmWatch(fd, uint32(wd))

		// Goroutine to interrupt the blocking Read when ctx cancels.
		go func() {
			<-ctx.Done()
			// Closing fd interrupts the Read with EBADF; the goroutine below
			// then exits cleanly.
			_ = unix.Close(fd)
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
	return true
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
