package l4

import (
	"bufio"
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
		_ = ServePostgres(conn, fakeResolver{target: Target{Addr: upAddr, User: "realuser", Pass: "realpass", DB: "appdb"}})
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
