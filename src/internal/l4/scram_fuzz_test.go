package l4

import "testing"

// FuzzSCRAMFinal fuzzes the SCRAM client-final computation with an
// attacker-controlled server-first message. The upstream Postgres link is
// plaintext TCP, so a hostile server or a MITM controls these bytes; parsing
// them (nonce prefix, base64 salt, iteration count) must never panic and must
// not hang — the high-iteration seed regresses the maxSCRAMIterations cap that
// stops a multi-billion-round PBKDF2. The trivial parseSCRAM splitter is
// exercised transitively.
func FuzzSCRAMFinal(f *testing.F) {
	f.Add("r=clientnonceSERVER,s=c2FsdA==,i=4096")       // well-formed
	f.Add("r=clientnonceSERVER,s=c2FsdA==,i=2000000000") // huge iter -> rejected, no hang
	f.Add("r=clientnonceSERVER,s=!!notbase64,i=4096")    // bad salt
	f.Add("r=wrongprefix,s=c2FsdA==,i=4096")             // nonce does not extend client
	f.Add("")
	f.Add(",=,=,")
	f.Fuzz(func(t *testing.T, serverFirst string) {
		sc := newSCRAM("", "password", "clientnonce")
		_, _ = sc.finalMessage(serverFirst) // must not panic or hang
	})
}
