package l4

import (
	"strings"
	"testing"
)

// spyAuthenticator wraps a real authenticator and records everything that crosses
// the delegation boundary — exactly the bytes that would travel to the vault
// process over the socketpair under Tier 2. It stands in for "what a compromised,
// byte-parsing worker can observe about the password."
type spyAuthenticator struct {
	inner      scramAuthenticator
	gotSalt    []byte
	gotIter    int
	gotAuthMsg string
	calls      int
}

func (s *spyAuthenticator) Proof(salt []byte, iter int, authMessage string) ([]byte, error) {
	s.calls++
	s.gotSalt, s.gotIter, s.gotAuthMsg = salt, iter, authMessage
	return s.inner.Proof(salt, iter, authMessage)
}

// TestSCRAM_PrivsepBoundary_ProofDelegatedWithoutPassword is the Tier 2 spike's
// feasibility proof: the SCRAM state machine can complete the exchange while the
// password crosses ONLY the scramAuthenticator boundary — never the state machine
// or the channel a worker would see. It asserts two things at once:
//
//  1. Correctness — delegating the proof step still yields the exact RFC 7677
//     known-answer final message (byte-identical to the in-process path).
//  2. Confinement — the password never appears in ANY value handed to the
//     authenticator (salt, iter, authMessage). Those are the only things that
//     would travel to the vault; the password stays on the vault side.
func TestSCRAM_PrivsepBoundary_ProofDelegatedWithoutPassword(t *testing.T) {
	const (
		password    = "pencil"
		clientNonce = "rOprNGfwEbeRWgbNEkqO"
		serverFirst = "r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096"
		wantFinal   = "c=biws,r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,p=dHzbZapWIk4jUhN+Ute9ytag9zjfMHgsqmmiz7AndVQ="
	)
	spy := &spyAuthenticator{inner: localSCRAMAuthenticator{password: password}}
	sc := newSCRAMWithAuth(spy, "user", clientNonce)

	final, err := sc.finalMessage(serverFirst)
	if err != nil {
		t.Fatal(err)
	}
	if final != wantFinal {
		t.Errorf("delegated final message =\n %q\nwant\n %q", final, wantFinal)
	}

	// The proof step is delegated exactly once, and the password is confined to
	// the authenticator: none of the delegated inputs carry it.
	if spy.calls != 1 {
		t.Errorf("expected exactly one delegated Proof call, got %d", spy.calls)
	}
	if strings.Contains(string(spy.gotSalt), password) {
		t.Error("password leaked into the delegated salt")
	}
	if strings.Contains(spy.gotAuthMsg, password) {
		t.Error("password leaked into the delegated auth message")
	}
	// And the state machine itself holds no password field — only the authenticator
	// does. (Compile-time: scramClient has no password; this documents the intent.)
	if sc.clientNonce != clientNonce {
		t.Errorf("clientNonce = %q, want %q", sc.clientNonce, clientNonce)
	}
}

// TestSCRAM_PrivsepBoundary_DefensiveIterationBound proves the password-holding
// side is self-protecting: even if a (compromised) caller delegates an
// out-of-range iteration count, the authenticator refuses rather than spinning
// PBKDF2 — the worker's own bound is not the only line of defense.
func TestSCRAM_PrivsepBoundary_DefensiveIterationBound(t *testing.T) {
	auth := localSCRAMAuthenticator{password: "pencil"}
	if _, err := auth.Proof([]byte("salt"), maxSCRAMIterations+1, "authmsg"); err == nil {
		t.Error("authenticator must reject an out-of-range iteration count")
	}
	if _, err := auth.Proof([]byte("salt"), 0, "authmsg"); err == nil {
		t.Error("authenticator must reject a zero iteration count")
	}
	if _, err := auth.Proof([]byte("salt"), 4096, "authmsg"); err != nil {
		t.Errorf("a normal iteration count must be accepted: %v", err)
	}
}
