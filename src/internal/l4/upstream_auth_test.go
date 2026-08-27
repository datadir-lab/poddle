package l4

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"testing"
)

func TestTargetFromDSN(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want Target
	}{
		{
			name: "postgres with user, password and database",
			dsn:  "postgres://alice:s3cret@db.internal:5432/orders",
			want: Target{Addr: "db.internal:5432", User: "alice", Pass: "s3cret", DB: "orders"},
		},
		{
			name: "redis with password only (empty user)",
			dsn:  "redis://:hunter2@cache:6379",
			want: Target{Addr: "cache:6379", Pass: "hunter2"},
		},
		{
			name: "no userinfo and no database",
			dsn:  "redis://cache:6379",
			want: Target{Addr: "cache:6379"},
		},
		{
			name: "scheme is ignored; host and path are what matter",
			dsn:  "anything://u@h:1234/db",
			want: Target{Addr: "h:1234", User: "u", DB: "db"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := TargetFromDSN(tt.dsn)
			if err != nil {
				t.Fatalf("TargetFromDSN(%q) unexpected error: %v", tt.dsn, err)
			}
			if got != tt.want {
				t.Errorf("TargetFromDSN(%q) = %+v, want %+v", tt.dsn, got, tt.want)
			}
		})
	}
}

func TestTargetFromDSN_Errors(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
	}{
		{"no host", "postgres:///justapath"},
		{"unparseable url", "\x7f"}, // control byte -> url.Parse rejects
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := TargetFromDSN(tt.dsn); err == nil {
				t.Errorf("TargetFromDSN(%q) = %+v, nil error; want error", tt.dsn, got)
			}
		})
	}
}

func TestMD5Hex(t *testing.T) {
	// Well-known MD5 vectors.
	for in, want := range map[string]string{
		"":    "d41d8cd98f00b204e9800998ecf8427e",
		"abc": "900150983cd24fb0d6963f7d28e17f72",
	} {
		if got := md5hex(in); got != want {
			t.Errorf("md5hex(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestMD5Auth_PostgresWireFormat(t *testing.T) {
	user, pass := "alice", "s3cret"
	salt := []byte{0xde, 0xad, 0xbe, 0xef}

	// Postgres MD5 auth: "md5" + md5hex(md5hex(password+user) + salt).
	inner := fmt.Sprintf("%x", md5.Sum([]byte(pass+user)))
	want := "md5" + fmt.Sprintf("%x", md5.Sum([]byte(inner+string(salt))))

	got := md5Auth(user, pass, salt)
	if got != want {
		t.Errorf("md5Auth = %s, want %s", got, want)
	}
	if len(got) != 35 { // "md5" + 32 hex digits
		t.Errorf("md5Auth length = %d, want 35", len(got))
	}
}

func TestRandNonce(t *testing.T) {
	n1, err := randNonce()
	if err != nil {
		t.Fatalf("randNonce: %v", err)
	}
	raw, err := base64.RawStdEncoding.DecodeString(n1)
	if err != nil {
		t.Fatalf("nonce %q is not raw-std base64: %v", n1, err)
	}
	if len(raw) != 18 {
		t.Errorf("nonce decodes to %d bytes, want 18", len(raw))
	}
	if n2, _ := randNonce(); n1 == n2 {
		t.Error("two nonces should not be equal")
	}
}

func TestWriteErrPG(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	go func() {
		writeErrPG(server, "28000", "handle rejected")
		server.Close()
	}()

	typ, body, err := readMessage(bufio.NewReader(client))
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if typ != 'E' {
		t.Errorf("message type = %q, want 'E' (ErrorResponse)", typ)
	}
	if got := pgErrText(body); got != "handle rejected" {
		t.Errorf("error message = %q, want %q", got, "handle rejected")
	}
	if !strings.Contains(string(body), "28000") {
		t.Errorf("body missing SQLSTATE code 28000: %q", body)
	}
}

func TestPgSCRAM_RejectsWhenMechanismAbsent(t *testing.T) {
	// The upstream advertises only MD5, so SCRAM must fail before any I/O
	// (up/upR and the authenticator are never touched on this path).
	if err := pgSCRAM(nil, nil, localSCRAMAuthenticator{password: "pw"}, []byte("MD5\x00")); err == nil {
		t.Fatal("expected error when SCRAM-SHA-256 is not offered")
	}
}

func TestPgSCRAM_HappyPath(t *testing.T) {
	up, srv := net.Pipe()
	defer up.Close()

	errc := make(chan error, 1)
	go func() { errc <- fakeSCRAMUpstream(srv) }()

	auth := localSCRAMAuthenticator{password: "s3cret"}
	if err := pgSCRAM(up, bufio.NewReader(up), auth, []byte("SCRAM-SHA-256\x00")); err != nil {
		t.Fatalf("pgSCRAM: %v", err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("fake upstream: %v", err)
	}
}

// fakeSCRAMKeeper implements SCRAMKeeper for tests: it computes the proof via
// ComputeSCRAMProof (the same math localSCRAMAuthenticator uses) and records
// the handle it was asked to resolve, so tests can assert the delegation
// actually crossed the SCRAMKeeper boundary rather than silently falling back
// to a locally-held password.
type fakeSCRAMKeeper struct {
	password  string
	calls     int
	gotHandle string
}

func (f *fakeSCRAMKeeper) SCRAMProof(handle string, salt []byte, iter int, authMessage string) ([]byte, error) {
	f.calls++
	f.gotHandle = handle
	return ComputeSCRAMProof(f.password, salt, iter, authMessage)
}

// TestPgSCRAM_HappyPath_ViaKeeper is the keeper-delegated twin of
// TestPgSCRAM_HappyPath: pgSCRAM is driven with a keeperSCRAMAuthenticator
// instead of a localSCRAMAuthenticator, proving the production wiring
// (ServePostgres constructs exactly this when a keeper is available) drives
// the SAME pgSCRAM/scramClient code, unmodified, with the proof crossing to
// the keeper rather than being computed from a password pgSCRAM itself holds.
func TestPgSCRAM_HappyPath_ViaKeeper(t *testing.T) {
	up, srv := net.Pipe()
	defer up.Close()

	errc := make(chan error, 1)
	go func() { errc <- fakeSCRAMUpstream(srv) }()

	fk := &fakeSCRAMKeeper{password: "s3cret"}
	auth := keeperSCRAMAuthenticator{keeper: fk, handle: "poddle_handle"}
	if err := pgSCRAM(up, bufio.NewReader(up), auth, []byte("SCRAM-SHA-256\x00")); err != nil {
		t.Fatalf("pgSCRAM: %v", err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("fake upstream: %v", err)
	}
	if fk.calls != 1 {
		t.Errorf("keeper.SCRAMProof calls = %d, want 1", fk.calls)
	}
	if fk.gotHandle != "poddle_handle" {
		t.Errorf("keeper.SCRAMProof handle = %q, want %q", fk.gotHandle, "poddle_handle")
	}
}

// fakeSCRAMUpstream plays a minimal SCRAM-SHA-256 server: it echoes the client
// nonce (so finalMessage's prefix check passes) and walks the client through
// SASLContinue -> SASLFinal -> AuthenticationOk. It does not verify the client
// proof — the function under test is pgSCRAM's client-side exchange, not mutual
// authentication.
func fakeSCRAMUpstream(c net.Conn) error {
	defer c.Close()
	r := bufio.NewReader(c)

	// 1. SASLInitialResponse ('p'): "SCRAM-SHA-256\0" + int32(len) + client-first.
	typ, body, err := readMessage(r)
	if err != nil {
		return err
	}
	if typ != 'p' {
		return fmt.Errorf("want SASLInitialResponse 'p', got %q", typ)
	}
	nul := bytes.IndexByte(body, 0)
	if nul < 0 || len(body) < nul+5 {
		return fmt.Errorf("malformed SASLInitialResponse: %q", body)
	}
	clientFirst := string(body[nul+1+4:]) // skip mechanism name, NUL, and int32 length
	clientNonce := parseSCRAM(clientFirst)["r"]
	if clientNonce == "" {
		return fmt.Errorf("no client nonce in %q", clientFirst)
	}

	// 2. server-first ('R', code 11 = SASLContinue): echo nonce, add salt + iterations.
	salt := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	serverFirst := "r=" + clientNonce + "svr,s=" + salt + ",i=4096"
	if err := writeMessage(c, 'R', append(int32be(11), serverFirst...)); err != nil {
		return err
	}

	// 3. client-final ('p') — accepted without verifying the proof.
	if _, _, err := readMessage(r); err != nil {
		return err
	}

	// 4. SASLFinal ('R', code 12) then AuthenticationOk ('R', code 0).
	if err := writeMessage(c, 'R', int32be(12)); err != nil {
		return err
	}
	return writeMessage(c, 'R', int32be(0))
}
