package l4

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

// FuzzParseStartupParams feeds arbitrary bytes to the Postgres startup-params
// parser (pod-controlled input). It must never panic, and any field it returns
// must be drawn from the input.
func FuzzParseStartupParams(f *testing.F) {
	f.Add([]byte("user\x00alice\x00database\x00app\x00"))
	f.Add([]byte("user\x00alice"))            // odd number of parts
	f.Add([]byte(""))                         // empty
	f.Add([]byte("\x00\x00\x00"))             // only separators
	f.Add([]byte("database\x00\x00user\x00")) // empty values
	f.Fuzz(func(t *testing.T, b []byte) {
		user, db := parseStartupParams(b) // must not panic
		if len(user) > len(b) || len(db) > len(b) {
			t.Fatalf("parsed field longer than input: user=%q db=%q", user, db)
		}
	})
}

// FuzzReadMessage feeds arbitrary bytes as a typed Postgres message frame
// (pod-controlled). The length field is attacker-controlled; readMessage must
// bound it (n <= 1<<24) and never panic or over-allocate.
func FuzzReadMessage(f *testing.F) {
	f.Add([]byte{'Q', 0, 0, 0, 5, 'x'})    // valid one-byte body
	f.Add([]byte{'X', 0, 0, 0, 4})         // empty body
	f.Add([]byte{'Z', 255, 255, 255, 255}) // huge length claim -> rejected
	f.Add([]byte{'E', 0, 0, 0})            // truncated header
	f.Fuzz(func(t *testing.T, b []byte) {
		r := bufio.NewReader(bytes.NewReader(b))
		typ, body, err := readMessage(r)
		if err != nil {
			return
		}
		if len(body) > 1<<24 {
			t.Fatalf("readMessage returned oversized body: type=%c len=%d", typ, len(body))
		}
	})
}

// FuzzReadStartup fuzzes the length-prefixed startup packet reader (pod's first
// packet; bounds 8 <= n <= 1<<20).
func FuzzReadStartup(f *testing.F) {
	f.Add([]byte{0, 0, 0, 8, 1, 2, 3, 4})
	f.Add([]byte{0, 0, 0, 4})         // length < 8 -> rejected
	f.Add([]byte{255, 255, 255, 255}) // length > 1MiB -> rejected
	f.Fuzz(func(t *testing.T, b []byte) {
		r := bufio.NewReader(bytes.NewReader(b))
		body, err := readStartup(r)
		if err != nil {
			return
		}
		if len(body) > 1<<20 {
			t.Fatalf("readStartup returned oversized body: len=%d", len(body))
		}
	})
}

// FuzzPgErrText fuzzes the upstream ErrorResponse message extractor.
func FuzzPgErrText(f *testing.F) {
	f.Add([]byte("SFATAL\x00Mpassword authentication failed\x00"))
	f.Add([]byte(""))
	f.Add([]byte("M"))
	f.Add([]byte("\x00\x00"))
	f.Fuzz(func(t *testing.T, b []byte) {
		_ = pgErrText(b) // must not panic
	})
}

// fuzzDiscardConn is a net.Conn whose writes are discarded and whose reads
// return EOF, so the upstream-auth state machine can be driven by a fixed byte
// slice (via upR) without a real socket.
type fuzzDiscardConn struct{}

func (fuzzDiscardConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (fuzzDiscardConn) Write(b []byte) (int, error)      { return len(b), nil }
func (fuzzDiscardConn) Close() error                     { return nil }
func (fuzzDiscardConn) LocalAddr() net.Addr              { return nil }
func (fuzzDiscardConn) RemoteAddr() net.Addr             { return nil }
func (fuzzDiscardConn) SetDeadline(time.Time) error      { return nil }
func (fuzzDiscardConn) SetReadDeadline(time.Time) error  { return nil }
func (fuzzDiscardConn) SetWriteDeadline(time.Time) error { return nil }

// FuzzPgClientAuth drives the upstream Postgres auth state machine with fuzzed
// server bytes. The upstream link is plaintext TCP, so a hostile server or a
// MITM controls these bytes; the reader must never panic or hang. The seeds
// include the short 'R' messages that used to slice-panic (body[:4], body[4:8]).
func FuzzPgClientAuth(f *testing.F) {
	f.Add([]byte{'R', 0, 0, 0, 4})             // empty body -> used to panic on body[:4]
	f.Add([]byte{'R', 0, 0, 0, 8, 0, 0, 0, 5}) // md5, no salt -> used to panic on body[4:8]
	f.Add([]byte{'R', 0, 0, 0, 8, 0, 0, 0, 0}) // AuthenticationOk
	f.Add([]byte{'E', 0, 0, 0, 4})             // ErrorResponse, empty body
	f.Fuzz(func(t *testing.T, upstream []byte) {
		upR := bufio.NewReader(bytes.NewReader(upstream))
		auth := localSCRAMAuthenticator{password: "pass"}
		_ = pgClientAuth(fuzzDiscardConn{}, upR, "user", "pass", auth, "db") // must not panic or hang
	})
}
