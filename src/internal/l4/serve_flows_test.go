package l4

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
)

func TestServeRedis_ResolveError(t *testing.T) {
	broker, pod := net.Pipe()
	defer pod.Close()
	done := make(chan error, 1)
	go func() { done <- ServeRedis(broker, fakeResolver{err: errors.New("revoked")}) }()

	if _, err := pod.Write([]byte(resp("AUTH", "badhandle"))); err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(pod).ReadString('\n')
	if !strings.Contains(line, "WRONGPASS") {
		t.Errorf("reply = %q, want a WRONGPASS error", line)
	}
	if err := <-done; err == nil {
		t.Error("expected ServeRedis to return the resolve error")
	}
}

func TestServeRedis_UpstreamUnreachable(t *testing.T) {
	broker, pod := net.Pipe()
	defer pod.Close()
	done := make(chan error, 1)
	// A valid handle resolves, but the upstream address refuses connection.
	go func() { done <- ServeRedis(broker, fakeResolver{target: Target{Addr: "127.0.0.1:1"}}) }()

	if _, err := pod.Write([]byte(resp("AUTH", "handle"))); err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(pod).ReadString('\n')
	if !strings.Contains(line, "unreachable") {
		t.Errorf("reply = %q, want an upstream-unreachable error", line)
	}
	if err := <-done; err == nil {
		t.Error("expected ServeRedis to return the dial error")
	}
}

func TestRedisAuth_WithUser(t *testing.T) {
	up, srv := net.Pipe()
	defer up.Close()
	errc := make(chan error, 1)
	go func() {
		cmd, err := readCommand(bufio.NewReader(srv))
		if err != nil {
			errc <- err
			return
		}
		if len(cmd) != 3 || cmd[0] != "AUTH" || cmd[1] != "u" || cmd[2] != "p" {
			errc <- fmt.Errorf("upstream saw %v, want [AUTH u p]", cmd)
			return
		}
		_, _ = srv.Write([]byte("+OK\r\n"))
		errc <- nil
	}()
	if err := redisAuth(up, bufio.NewReader(up), "u", "p"); err != nil {
		t.Fatalf("redisAuth: %v", err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("fake upstream: %v", err)
	}
}

func TestRedisAuth_Rejected(t *testing.T) {
	up, srv := net.Pipe()
	defer up.Close()
	go func() {
		_, _ = readCommand(bufio.NewReader(srv)) // drain the AUTH...
		_, _ = srv.Write([]byte("-WRONGPASS nope\r\n"))
	}()
	if err := redisAuth(up, bufio.NewReader(up), "", "pw"); err == nil {
		t.Error("expected an error when the upstream rejects AUTH")
	}
}

// writeSSLRequest sends a Postgres SSLRequest packet (length-prefixed, no type).
func writeSSLRequest(t *testing.T, w io.Writer) {
	t.Helper()
	if _, err := w.Write(append(int32be(8), int32be(pgSSLRequest)...)); err != nil {
		t.Fatal(err)
	}
}

func TestServePostgres_SSLThenResolveError(t *testing.T) {
	broker, pod := net.Pipe()
	defer pod.Close()
	done := make(chan error, 1)
	go func() { done <- ServePostgres(broker, fakeResolver{err: errors.New("revoked")}) }()

	pr := bufio.NewReader(pod)

	// 1. SSLRequest -> the broker declines TLS with 'N'.
	writeSSLRequest(t, pod)
	if b, err := pr.ReadByte(); err != nil || b != 'N' {
		t.Fatalf("SSL reply = %q, %v; want 'N'", b, err)
	}
	// 2. Real startup -> the broker asks for a cleartext password ('R', code 3).
	if err := writeStartup(pod, "u", "app"); err != nil {
		t.Fatal(err)
	}
	typ, body, err := readMessage(pr)
	if err != nil || typ != 'R' {
		t.Fatalf("auth request typ=%q err=%v; want 'R'", typ, err)
	}
	if code, ok := pgAuthCode(body); !ok || code != 3 {
		t.Fatalf("auth code = %d (ok=%v), want 3 (cleartext)", code, ok)
	}
	// 3. Send the handle as the password -> Resolve fails -> ErrorResponse.
	if err := writeMessage(pod, 'p', []byte("badhandle\x00")); err != nil {
		t.Fatal(err)
	}
	etyp, ebody, err := readMessage(pr)
	if err != nil || etyp != 'E' {
		t.Fatalf("error reply typ=%q err=%v; want 'E' (ErrorResponse)", etyp, err)
	}
	if !strings.Contains(string(ebody), "28P01") {
		t.Errorf("error missing SQLSTATE 28P01: %q", ebody)
	}
	if err := <-done; err == nil {
		t.Error("expected ServePostgres to return the resolve error")
	}
}

func TestServePostgres_UpstreamUnreachable(t *testing.T) {
	broker, pod := net.Pipe()
	defer pod.Close()
	done := make(chan error, 1)
	go func() { done <- ServePostgres(broker, fakeResolver{target: Target{Addr: "127.0.0.1:1"}}) }()

	pr := bufio.NewReader(pod)
	if err := writeStartup(pod, "u", ""); err != nil { // direct startup, no SSL probe
		t.Fatal(err)
	}
	if _, _, err := readMessage(pr); err != nil { // 'R' cleartext-password request
		t.Fatal(err)
	}
	if err := writeMessage(pod, 'p', []byte("h\x00")); err != nil {
		t.Fatal(err)
	}
	etyp, ebody, err := readMessage(pr)
	if err != nil || etyp != 'E' {
		t.Fatalf("error reply typ=%q err=%v; want 'E'", etyp, err)
	}
	if !strings.Contains(string(ebody), "08006") {
		t.Errorf("error missing SQLSTATE 08006 (unreachable): %q", ebody)
	}
	if err := <-done; err == nil {
		t.Error("expected ServePostgres to return the dial error")
	}
}
