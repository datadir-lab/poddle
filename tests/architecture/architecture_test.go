package architecture

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

const module = "git.dev.datadir.co/datadir/poddle"

type pkg struct {
	ImportPath string
	Imports    []string
}

// loadPackages lists every package under src/ with its direct imports, via the
// go toolchain (no external deps, works offline).
func loadPackages(t *testing.T) []pkg {
	t.Helper()
	out, err := exec.Command("go", "list", "-json", module+"/src/...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	var pkgs []pkg
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var p pkg
		if err := dec.Decode(&p); err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		pkgs = append(pkgs, p)
	}
	if len(pkgs) == 0 {
		t.Fatal("go list returned no packages under src/")
	}
	return pkgs
}

// featureSlice returns the slice name for a package under src/cli/<name>/...,
// or "" for the cli root or any non-slice package.
func featureSlice(importPath string) string {
	const prefix = module + "/src/cli/"
	if !strings.HasPrefix(importPath, prefix) {
		return ""
	}
	return strings.SplitN(strings.TrimPrefix(importPath, prefix), "/", 2)[0]
}

// TestFeatureSlicesAreIndependent: a package in one feature slice must not
// import another feature slice. Only the root composes slices together.
func TestFeatureSlicesAreIndependent(t *testing.T) {
	for _, p := range loadPackages(t) {
		from := featureSlice(p.ImportPath)
		if from == "" {
			continue
		}
		for _, imp := range p.Imports {
			if to := featureSlice(imp); to != "" && to != from {
				t.Errorf("feature slice %q imports feature slice %q\n  %s -> %s",
					from, to, p.ImportPath, imp)
			}
		}
	}
}

// TestKernelHasNoUpwardDeps: src/internal (the shared kernel) must not import
// anything under src/cli.
func TestKernelHasNoUpwardDeps(t *testing.T) {
	const internalPrefix = module + "/src/internal/"
	const cliPrefix = module + "/src/cli"
	for _, p := range loadPackages(t) {
		if !strings.HasPrefix(p.ImportPath, internalPrefix) {
			continue
		}
		for _, imp := range p.Imports {
			if strings.HasPrefix(imp, cliPrefix) {
				t.Errorf("kernel package %q imports cli %q", p.ImportPath, imp)
			}
		}
	}
}
