//go:build !windows

package poddled

import "syscall"

// detachAttrs starts poddled in its own session so it survives the CLI exiting.
func detachAttrs() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
