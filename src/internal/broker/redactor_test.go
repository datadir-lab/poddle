package broker

import (
	"bytes"
	"strings"
	"testing"
)

func TestRedactor_ManagedSecretScrubbed(t *testing.T) {
	r := NewRedactor("redact")
	body := []byte(`{"note":"the key is sk-SECRET-123 ok"}`)
	out, hits, block := r.Scan(body, "sk-SECRET-123")
	if block || hits != 1 {
		t.Fatalf("hits=%d block=%v, want 1/false", hits, block)
	}
	if bytes.Contains(out, []byte("sk-SECRET-123")) {
		t.Errorf("managed secret survived: %s", out)
	}
	if !bytes.Contains(out, []byte(RedactPlaceholder)) {
		t.Errorf("expected placeholder in %s", out)
	}
}

func TestRedactor_BasicTokenPartScrubbed(t *testing.T) {
	r := NewRedactor("redact")
	// gateway passes both "user:token" and the token part for basic creds
	out, hits, _ := r.Scan([]byte("leaked deadbeeftoken here"), "me:deadbeeftoken", "deadbeeftoken")
	if hits == 0 || bytes.Contains(out, []byte("deadbeeftoken")) {
		t.Errorf("token part should be scrubbed, hits=%d out=%s", hits, out)
	}
}

func TestRedactor_PatternsScrubbed(t *testing.T) {
	r := NewRedactor("redact")
	body := []byte("aws=AKIAIOSFODNN7EXAMPLE gh=ghp_" + strings.Repeat("a", 36))
	out, hits, _ := r.Scan(body, "")
	if hits < 2 {
		t.Errorf("want >=2 pattern hits, got %d", hits)
	}
	if bytes.Contains(out, []byte("AKIAIOSFODNN7EXAMPLE")) || bytes.Contains(out, []byte("ghp_")) {
		t.Errorf("patterns survived: %s", out)
	}
}

func TestRedactor_BlockReturnsOriginal(t *testing.T) {
	r := NewRedactor("block")
	body := []byte("token AKIAIOSFODNN7EXAMPLE")
	out, hits, block := r.Scan(body, "")
	if !block || hits != 1 {
		t.Fatalf("hits=%d block=%v, want 1/true", hits, block)
	}
	if !bytes.Equal(out, body) {
		t.Errorf("block should return the original body unchanged")
	}
}

func TestRedactor_OffAndClean(t *testing.T) {
	if out, hits, _ := NewRedactor("off").Scan([]byte("AKIAIOSFODNN7EXAMPLE"), ""); hits != 0 || !bytes.Contains(out, []byte("AKIA")) {
		t.Errorf("off mode must not touch the body")
	}
	if _, hits, _ := NewRedactor("redact").Scan([]byte(`{"ok":true}`), "unused"); hits != 0 {
		t.Errorf("clean body should have 0 hits, got %d", hits)
	}
}

func TestNewRedactor_DefaultsToRedact(t *testing.T) {
	if _, hits, block := NewRedactor("").Scan([]byte("AKIAIOSFODNN7EXAMPLE"), ""); hits != 1 || block {
		t.Errorf("empty mode should default to redact (hits=%d block=%v)", hits, block)
	}
}
