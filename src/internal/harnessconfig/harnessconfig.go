// Package harnessconfig resolves the per-harness directory where a user places
// custom agent config (settings, plugins, MCP declarations) to be seeded into a
// pod and persisted. Mirrors identity.DefaultBase / policy.DefaultDir.
package harnessconfig

import (
	"os"
	"path/filepath"
)

// DefaultBase is <UserConfigDir>/poddle/harness (XDG_CONFIG_HOME honored).
func DefaultBase() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = "."
	}
	return filepath.Join(d, "poddle", "harness")
}

// Dir is the host directory for harness name's custom config.
func Dir(name string) string { return filepath.Join(DefaultBase(), name) }
