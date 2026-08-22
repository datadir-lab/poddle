//go:build unix

package poddled

import (
	"os"
	"syscall"
)

// tryLockFile takes an exclusive advisory (flock) lock on path, creating the
// file if needed. Returns (release, true, nil) on success; (nil, false, nil)
// when another process already holds it; (nil, false, err) on a real error.
//
// The kernel releases the flock when the holding process exits — including on a
// crash — so a dead holder never leaves a lock behind and there is nothing to
// "reclaim". Two concurrent starts therefore can never both win: exactly one
// gets LOCK_EX, the other gets EWOULDBLOCK.
func tryLockFile(path string) (release func(), held bool, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, false, nil // held by a live instance
		}
		return nil, false, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close() // leave the file; the lock lives on the open fd, not the path
	}, true, nil
}
