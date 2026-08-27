package l4

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
)

// TestPgClientAuth_MD5 drives the MD5 branch: the upstream requests
// AuthenticationMD5Password and the broker answers with an md5-hashed response.
func TestPgClientAuth_MD5(t *testing.T) {
	up, srv := net.Pipe()
	defer up.Close()
	errc := make(chan error, 1)
	go func() {
		r := bufio.NewReader(srv)
		if _, err := readStartup(r); err != nil {
			errc <- err
			return
		}
		if err := writeMessage(srv, 'R', append(authInt32(5), []byte{1, 2, 3, 4}...)); err != nil {
			errc <- err
			return
		}
		typ, payload, err := readMessage(r)
		if err != nil || typ != 'p' {
			errc <- fmt.Errorf("want PasswordMessage 'p', got %q %v", typ, err)
			return
		}
		if !strings.HasPrefix(strings.TrimRight(string(payload), "\x00"), "md5") {
			errc <- fmt.Errorf("response is not md5-hashed: %q", payload)
			return
		}
		errc <- writeMessage(srv, 'R', authInt32(0)) // AuthenticationOk
	}()
	auth := localSCRAMAuthenticator{password: "pass"}
	if err := pgClientAuth(up, bufio.NewReader(up), "user", "pass", auth, "db"); err != nil {
		t.Fatalf("pgClientAuth: %v", err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("fake upstream: %v", err)
	}
}

func TestPgClientAuth_UpstreamError(t *testing.T) {
	up, srv := net.Pipe()
	defer up.Close()
	go func() {
		r := bufio.NewReader(srv)
		_, _ = readStartup(r)
		_ = writeMessage(srv, 'E', []byte("SFATAL\x00Mdenied\x00\x00")) // ErrorResponse
	}()
	if err := pgClientAuth(up, bufio.NewReader(up), "u", "p", localSCRAMAuthenticator{password: "p"}, "db"); err == nil {
		t.Error("expected an error when the upstream returns ErrorResponse")
	}
}

func TestPgClientAuth_UnsupportedAuthType(t *testing.T) {
	up, srv := net.Pipe()
	defer up.Close()
	go func() {
		r := bufio.NewReader(srv)
		_, _ = readStartup(r)
		_ = writeMessage(srv, 'R', authInt32(99)) // an auth type poddle does not implement
	}()
	if err := pgClientAuth(up, bufio.NewReader(up), "u", "p", localSCRAMAuthenticator{password: "p"}, "db"); err == nil {
		t.Error("expected an error for an unsupported upstream auth type")
	}
}

func TestPgClientAuth_ShortAuthMessage(t *testing.T) {
	up, srv := net.Pipe()
	defer up.Close()
	go func() {
		r := bufio.NewReader(srv)
		_, _ = readStartup(r)
		_ = writeMessage(srv, 'R', []byte{0, 1}) // fewer than 4 bytes: no valid auth code
	}()
	if err := pgClientAuth(up, bufio.NewReader(up), "u", "p", localSCRAMAuthenticator{password: "p"}, "db"); err == nil {
		t.Error("expected an error for a short authentication message")
	}
}
