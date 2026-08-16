package l4

import "testing"

// TestSCRAM_RFC7677 checks the SCRAM-SHA-256 client computation against the
// worked example in RFC 7677 §3 (username "user", password "pencil").
func TestSCRAM_RFC7677(t *testing.T) {
	const clientNonce = "rOprNGfwEbeRWgbNEkqO"
	const serverFirst = "r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096"
	const wantFinal = "c=biws,r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,p=dHzbZapWIk4jUhN+Ute9ytag9zjfMHgsqmmiz7AndVQ="

	s := newSCRAM("user", "pencil", clientNonce)
	if got := s.firstMessage(); got != "n,,n=user,r="+clientNonce {
		t.Errorf("firstMessage = %q", got)
	}
	final, err := s.finalMessage(serverFirst)
	if err != nil {
		t.Fatal(err)
	}
	if final != wantFinal {
		t.Errorf("finalMessage =\n %q\nwant\n %q", final, wantFinal)
	}
}

func TestSCRAM_RejectsBadServerNonce(t *testing.T) {
	s := newSCRAM("", "pencil", "myNonce")
	// server-first whose nonce does not start with the client nonce
	if _, err := s.finalMessage("r=WRONG,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096"); err == nil {
		t.Error("expected an error when the server nonce does not extend the client nonce")
	}
}
