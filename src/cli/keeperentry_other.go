//go:build !linux && !docgen

package main

// maybeRunKeeper is a no-op off Linux: the broker's keeper subprocess (privsep fork
// + socketpair) is Linux-only, so no process is ever re-exec'd as a keeper here and
// the broker always runs in-process.
func maybeRunKeeper() {}
