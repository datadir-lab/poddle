//go:build linux && !docgen

package main

import (
	"fmt"
	"os"

	"github.com/datadir-lab/poddle/src/internal/broker"
	"github.com/datadir-lab/poddle/src/internal/privsep"
)

// maybeRunKeeper serves the broker's keeper subprocess and exits, when THIS process
// was re-exec'd as the keeper (privsep.IsKeeperMode, set by broker's spawn). It MUST
// run before cobra parses args — the keeper child carries a marker argv cobra would
// reject — so main() calls it first. In a normal front invocation it returns
// immediately and the CLI runs as usual.
//
// PODDLE_PRIVSEP_KEEPER is a RESERVED, internal env var: only broker's Spawn sets it
// (on the child's env), never the front on itself. Do not set it in poddled's ambient
// environment — an invocation that inherits it routes into keeper mode and exits 1
// when fd 3 isn't the inherited socketpair (fail-closed, but a misconfiguration).
func maybeRunKeeper() {
	if !privsep.IsKeeperMode() {
		return
	}
	if err := broker.RunKeeperProcess(); err != nil {
		fmt.Fprintln(os.Stderr, "poddle keeper:", err)
		os.Exit(1)
	}
	os.Exit(0)
}
