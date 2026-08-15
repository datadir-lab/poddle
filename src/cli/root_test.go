package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootCmd_ShowsUsage(t *testing.T) {
	c := NewRootCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetArgs([]string{"--help"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "poddle") {
		t.Fatalf("usage missing 'poddle': %q", out.String())
	}
}
