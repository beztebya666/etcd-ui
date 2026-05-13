//go:build !linux

package httpx

import "context"

// watchNative is a no-op on non-Linux. Polling Watch() handles everything.
func (a *ACL) watchNative(_ context.Context) bool { return false }
