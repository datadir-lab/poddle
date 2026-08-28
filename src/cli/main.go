//go:build !docgen

// Command poddle is poddle's command-line interface.
package main

import (
	"fmt"
	"os"
)

func main() {
	// If this process was re-exec'd as the broker keeper subprocess (Phase-2
	// privsep, Linux only), serve the keeper and exit before cobra parses argv.
	// A no-op in a normal front invocation.
	maybeRunKeeper()
	if err := Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
