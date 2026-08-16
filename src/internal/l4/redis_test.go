package l4

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
)

// resp builds a RESP array of bulk strings (a client command).
func resp(parts ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(parts))
	for _, p := range parts {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(p), p)
	}
	return b.String()
}

type fakeResolver struct {
	target Target
	err    error
}

func (f fakeResolver) Resolve(string) (Target, error) { return f.target, f.err }

// fakeRedis is a minimal upstream: it expects an AUTH, records the password it
// saw, replies +OK, then answers PING with +PONG.
func fakeRedis(t *testing.T, gotPass *string, mu *sync.Mutex) string {
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
		br := bufio.NewReader(conn)
		cmd, err := readCommand(br)
		if err != nil || len(cmd) < 2 || !strings.EqualFold(cmd[0], "AUTH") {
			_, _ = conn.Write([]byte("-ERR expected AUTH\r\n"))
			return
		}
		mu.Lock()
		*gotPass = cmd[len(cmd)-1]
		mu.Unlock()
		_, _ = conn.Write([]byte("+OK\r\n"))
		for {
			c, err := readCommand(br)
			if err != nil {
				return
			}
			if strings.EqualFold(c[0], "PING") {
				_, _ = conn.Write([]byte("+PONG\r\n"))
			} else {
				_, _ = conn.Write([]byte("+OK\r\n"))
			}
		}
	}()
	return ln.Addr().String()
}

func TestReadCommand(t *testing.T) {
	br := bufio.NewReader(strings.NewReader(resp("AUTH", "user", "secret")))
	cmd, err := readCommand(br)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmd) != 3 || cmd[0] != "AUTH" || cmd[1] != "user" || cmd[2] != "secret" {
		t.Errorf("parsed %v", cmd)
	}
}

func TestServeRedis_RewritesAuthAndSplices(t *testing.T) {
	var mu sync.Mutex
	var gotPass string
	upAddr := fakeRedis(t, &gotPass, &mu)

	// proxy listener
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
		_ = ServeRedis(conn, fakeResolver{target: Target{Addr: upAddr, Pass: "realpass"}})
	}()

	// a "pod" connects to the proxy, authenticates with its handle, then PINGs.
	pod, err := net.Dial("tcp", pl.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer pod.Close()
	if _, err := pod.Write([]byte(resp("AUTH", "poddle_handle"))); err != nil {
		t.Fatal(err)
	}
	rd := bufio.NewReader(pod)
	line, _ := rd.ReadString('\n')
	if strings.TrimSpace(line) != "+OK" {
		t.Fatalf("auth reply = %q, want +OK", line)
	}
	if _, err := pod.Write([]byte(resp("PING"))); err != nil {
		t.Fatal(err)
	}
	pong, _ := rd.ReadString('\n')
	if strings.TrimSpace(pong) != "+PONG" {
		t.Errorf("ping reply = %q, want +PONG", pong)
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

func TestServeRedis_RejectsWithoutAuth(t *testing.T) {
	pl, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { _ = pl.Close() })
	go func() {
		conn, err := pl.Accept()
		if err != nil {
			return
		}
		_ = ServeRedis(conn, fakeResolver{target: Target{Addr: "127.0.0.1:1"}})
	}()
	pod, _ := net.Dial("tcp", pl.Addr().String())
	defer pod.Close()
	_, _ = pod.Write([]byte(resp("PING"))) // no AUTH first
	line, _ := bufio.NewReader(pod).ReadString('\n')
	if !strings.HasPrefix(line, "-") {
		t.Errorf("expected an error reply for missing AUTH, got %q", line)
	}
}
