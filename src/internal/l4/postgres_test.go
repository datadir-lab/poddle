package l4

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"net"
	"strings"
	"sync"
	"testing"
)

func TestParseStartupParams(t *testing.T) {
	body := []byte("user\x00alice\x00database\x00shop\x00\x00")
	user, db := parseStartupParams(body)
	if user != "alice" || db != "shop" {
		t.Errorf("parsed user=%q db=%q", user, db)
	}
}

// fakePGUpstream speaks just enough Postgres to authenticate via cleartext and
// record the password it saw, then answers ReadyForQuery.
func fakePGUpstream(t *testing.T, gotPass *string, mu *sync.Mutex) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		if _, err := readStartup(r); err != nil { // the broker's StartupMessage
			return
		}
		_ = writeMessage(conn, 'R', authInt32(3)) // AuthenticationCleartextPassword
		typ, payload, err := readMessage(r)
		if err != nil || typ != 'p' {
			return
		}
		mu.Lock()
		*gotPass = strings.TrimRight(string(payload), "\x00")
		mu.Unlock()
		_ = writeMessage(conn, 'R', authInt32(0)) // AuthenticationOk
		_ = writeMessage(conn, 'Z', []byte{'I'})  // ReadyForQuery
		for {
			if _, _, err := readMessage(r); err != nil {
				return
			}
			_ = writeMessage(conn, 'Z', []byte{'I'})
		}
	}()
	return ln.Addr().String()
}

func TestServePostgres_TerminatesAndSplices(t *testing.T) {
	var mu sync.Mutex
	var gotPass string
	upAddr := fakePGUpstream(t, &gotPass, &mu)

	pl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pl.Close() })
	go func() {
		conn, err := pl.Accept()
		if err != nil {
			return
		}
		_ = ServePostgres(conn, fakeResolver{target: Target{Addr: upAddr, User: "realuser", Pass: "realpass", DB: "appdb"}}, nil)
	}()

	pod, err := net.Dial("tcp", pl.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer pod.Close()
	if err := writeStartup(pod, "poduser", "appdb"); err != nil {
		t.Fatal(err)
	}
	podR := bufio.NewReader(pod)

	// broker asks for a cleartext password
	typ, body, err := readMessage(podR)
	if err != nil || typ != 'R' || binary.BigEndian.Uint32(body[:4]) != 3 {
		t.Fatalf("expected cleartext request, got %q %v %v", typ, body, err)
	}
	// pod sends its handle
	if err := writeMessage(pod, 'p', append([]byte("poddle_handle"), 0)); err != nil {
		t.Fatal(err)
	}
	// broker completes auth against the real DB, then tells the pod OK
	typ, body, err = readMessage(podR)
	if err != nil || typ != 'R' || binary.BigEndian.Uint32(body[:4]) != 0 {
		t.Fatalf("expected AuthenticationOk, got %q %v %v", typ, body, err)
	}
	// upstream's ReadyForQuery is spliced through
	if typ, _, err = readMessage(podR); err != nil || typ != 'Z' {
		t.Errorf("expected ReadyForQuery, got %q %v", typ, err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotPass != "realpass" {
		t.Errorf("upstream saw password %q, want realpass (handle should be swapped)", gotPass)
	}
	if gotPass == "poddle_handle" {
		t.Error("the handle leaked to the upstream")
	}
}

// scramWireCapture is what fakePGSCRAMUpstream reports back: enough of the
// exchange (the client nonce, the server-first message it sent, and the raw
// client-final body) for a test to independently reassemble authMessage and
// recompute the expected proof for ANY candidate password — without the fake
// upstream itself verifying the proof (it can't; it isn't the real DB).
type scramWireCapture struct {
	clientNonce string
	serverFirst string
	finalBody   []byte
}

// fakePGSCRAMUpstream plays a fixed-salt/iteration Postgres SCRAM-SHA-256
// server: StartupMessage -> AuthenticationSASL(SCRAM-SHA-256) -> SASLContinue
// -> SASLFinal -> AuthenticationOk, then closes. It reports the captured
// exchange on capture so a test can verify which password produced the client's
// proof — the empirical check that ServePostgres's SCRAM step is driven by
// whatever authenticator it was given (the keeper), not a value read off
// Target.Pass.
func fakePGSCRAMUpstream(t *testing.T, capture chan<- scramWireCapture) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		if _, err := readStartup(r); err != nil { // the broker's StartupMessage
			return
		}
		// AuthenticationSASL: Int32(10) + NUL-terminated mechanism list + NUL.
		mechs := append(authInt32(10), []byte("SCRAM-SHA-256\x00\x00")...)
		if err := writeMessage(conn, 'R', mechs); err != nil {
			return
		}
		// SASLInitialResponse ('p'): "SCRAM-SHA-256\0" + int32(len) + client-first.
		typ, body, err := readMessage(r)
		if err != nil || typ != 'p' {
			return
		}
		nul := bytes.IndexByte(body, 0)
		if nul < 0 || len(body) < nul+5 {
			return
		}
		clientFirst := string(body[nul+1+4:])
		clientNonce := parseSCRAM(clientFirst)["r"]
		if clientNonce == "" {
			return
		}
		salt := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
		serverFirst := "r=" + clientNonce + "svr,s=" + salt + ",i=4096"
		if err := writeMessage(conn, 'R', append(authInt32(11), serverFirst...)); err != nil {
			return
		}
		_, finalBody, err := readMessage(r) // client-final ('p')
		if err != nil {
			return
		}
		capture <- scramWireCapture{clientNonce: clientNonce, serverFirst: serverFirst, finalBody: finalBody}
		_ = writeMessage(conn, 'R', authInt32(12)) // SASLFinal
		_ = writeMessage(conn, 'R', authInt32(0))  // AuthenticationOk
	}()
	return ln.Addr().String()
}

// TestServePostgres_SCRAM_ProofComesFromKeeperNotTargetPass wires ServePostgres
// with a keeper holding the REAL password and a Target.Pass holding an unused
// decoy, then independently recomputes the expected SCRAM proof for both
// candidates from the captured wire exchange. It asserts the wire proof
// matches the keeper's password and NOT the decoy — the concrete evidence
// that the L4 front's SCRAM step is delegated, not driven by a locally-held
// password (the property this task's wiring exists for).
func TestServePostgres_SCRAM_ProofComesFromKeeperNotTargetPass(t *testing.T) {
	const realPassword = "s3cret"
	const decoyPass = "DECOY-should-never-drive-the-wire-proof"

	capture := make(chan scramWireCapture, 1)
	upAddr := fakePGSCRAMUpstream(t, capture)

	pl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pl.Close() })

	fk := &fakeSCRAMKeeper{password: realPassword}
	go func() {
		conn, err := pl.Accept()
		if err != nil {
			return
		}
		_ = ServePostgres(conn, fakeResolver{target: Target{Addr: upAddr, User: "realuser", Pass: decoyPass, DB: "appdb"}}, fk)
	}()

	pod, err := net.Dial("tcp", pl.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer pod.Close()
	if err := writeStartup(pod, "poduser", "appdb"); err != nil {
		t.Fatal(err)
	}
	podR := bufio.NewReader(pod)

	// broker asks for a cleartext password (the handle)
	typ, body, err := readMessage(podR)
	if err != nil || typ != 'R' || binary.BigEndian.Uint32(body[:4]) != 3 {
		t.Fatalf("expected cleartext request, got %q %v %v", typ, body, err)
	}
	if err := writeMessage(pod, 'p', append([]byte("poddle_handle"), 0)); err != nil {
		t.Fatal(err)
	}
	// broker completes SCRAM against the real DB, then tells the pod OK
	typ, body, err = readMessage(podR)
	if err != nil || typ != 'R' || binary.BigEndian.Uint32(body[:4]) != 0 {
		t.Fatalf("expected AuthenticationOk, got %q %v %v", typ, body, err)
	}

	var wire scramWireCapture
	select {
	case wire = <-capture:
	default:
		t.Fatal("upstream never captured the client-final SCRAM message")
	}

	if fk.calls != 1 {
		t.Errorf("keeper.SCRAMProof calls = %d, want 1", fk.calls)
	}
	if fk.gotHandle != "poddle_handle" {
		t.Errorf("keeper.SCRAMProof handle = %q, want %q", fk.gotHandle, "poddle_handle")
	}

	// Reassemble authMessage exactly as scramClient.finalMessage does, and the
	// salt exactly as fakePGSCRAMUpstream sent it, to recompute the expected
	// proof for each candidate password independently of production code.
	salt := []byte("0123456789abcdef")      // the raw salt fakePGSCRAMUpstream base64-encoded into s=
	firstBare := "n=,r=" + wire.clientNonce // Postgres always uses an empty SCRAM username
	combinedNonce := parseSCRAM(wire.serverFirst)["r"]
	finalNoProof := "c=biws,r=" + combinedNonce
	authMessage := firstBare + "," + wire.serverFirst + "," + finalNoProof

	gotProofB64 := parseSCRAM(string(wire.finalBody))["p"]
	if gotProofB64 == "" {
		t.Fatalf("no p= proof field in client-final %q", wire.finalBody)
	}

	wantProof, err := ComputeSCRAMProof(realPassword, salt, 4096, authMessage)
	if err != nil {
		t.Fatalf("ComputeSCRAMProof(real): %v", err)
	}
	decoyProof, err := ComputeSCRAMProof(decoyPass, salt, 4096, authMessage)
	if err != nil {
		t.Fatalf("ComputeSCRAMProof(decoy): %v", err)
	}
	wantProofB64 := base64.StdEncoding.EncodeToString(wantProof)
	decoyProofB64 := base64.StdEncoding.EncodeToString(decoyProof)

	if gotProofB64 != wantProofB64 {
		t.Errorf("wire proof = %q, want %q (computed from the keeper's password)", gotProofB64, wantProofB64)
	}
	if gotProofB64 == decoyProofB64 {
		t.Error("wire proof matches the decoy Target.Pass — the SCRAM step used the local password, not the keeper")
	}
}
