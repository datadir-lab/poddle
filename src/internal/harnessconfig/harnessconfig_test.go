package harnessconfig

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultBase_UnderPoddleConfig(t *testing.T) {
	got := DefaultBase()
	if !strings.HasSuffix(got, filepath.FromSlash("poddle/harness")) {
		t.Errorf("DefaultBase = %q, want .../poddle/harness", got)
	}
}

func TestDir_PerHarness(t *testing.T) {
	got := Dir("codex")
	if filepath.Base(got) != "codex" || filepath.Dir(got) != DefaultBase() {
		t.Errorf("Dir(codex) = %q, want <base>/codex", got)
	}
}
