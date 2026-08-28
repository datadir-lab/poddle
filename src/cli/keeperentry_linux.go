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
