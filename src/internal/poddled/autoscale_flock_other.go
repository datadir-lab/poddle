//go:build !unix

package poddled

import "os"

// tryLockFile is a best-effort single-instance lock for platforms without flock
// (Windows). It creates the lock file exclusively and removes it on release. A
// clean exit frees the lock; a crash leaves the file behind (Windows has no
// flock auto-release), which must then be removed manually. This is acceptable:
// rootless podman's primary target is Linux, which uses the race-free flock path
// in autoscale_flock_unix.go, and the autoscaler is grow-only with a per-pod
// cooldown so any transient double-run is bounded.
func tryLockFile(path string) (release func(), held bool, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, false, nil // another instance (or a stale lock after a crash)
		}
		return nil, false, err
	}
	_ = f.Close()
	return func() { _ = os.Remove(path) }, true, nil
}
