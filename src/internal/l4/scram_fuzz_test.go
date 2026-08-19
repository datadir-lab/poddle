package l4

import "testing"

// FuzzParseSCRAM feeds arbitrary strings to the SCRAM message parser. It parses
// server-sent (untrusted) messages, so it must never panic on malformed input.
func FuzzParseSCRAM(f *testing.F) {
	f.Add("n=user,r=nonce")
	f.Add("r=abc==,s=c2FsdA==,i=4096") // values contain '='
	f.Add("")
	f.Add(",=,=,")
	f.Add("k")      // no '=' -> skipped
	f.Add("=value") // empty key
	f.Fuzz(func(t *testing.T, msg string) {
		_ = parseSCRAM(msg) // must not panic
	})
}
