//go:build !windows

package poddled

import "syscall"

// detachAttrs starts a spawned process in its own session so it survives the
// CLI exiting (used to launch the host autoscaler detached).
func detachAttrs() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
