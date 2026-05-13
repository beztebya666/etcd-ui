//go:build linux

package httpx

import "unsafe"

// unsafePointer wraps unsafe.Pointer in a single tiny helper so the inotify
// file above stays free of the `unsafe` import noise. We use it only to cast
// the byte buffer to the kernel's InotifyEvent layout.
func unsafePointer(p any) unsafe.Pointer {
	switch v := p.(type) {
	case *byte:
		return unsafe.Pointer(v)
	}
	return nil
}
