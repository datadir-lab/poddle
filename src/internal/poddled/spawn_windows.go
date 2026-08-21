//go:build windows

package poddled

import "syscall"

// detachAttrs is a no-op on Windows; poddled targets Linux hosts in practice.
func detachAttrs() *syscall.SysProcAttr { return &syscall.SysProcAttr{} }
