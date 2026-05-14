//go:build !linux

package httpx

import "context"

// watchNative is a no-op on non-Linux. Polling Watch() handles everything.
// Second return is nil — there's no goroutine to wait for on shutdown.
func (a *ACL) watchNative(_ context.Context) (bool, <-chan struct{}) { return false, nil }
