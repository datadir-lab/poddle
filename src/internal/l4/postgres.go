package l4

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strings"
)

// Postgres startup request codes (sent where a protocol version would be).
const (
	pgSSLRequest    = 80877103
	pgGSSENCRequest = 80877104
	pgProtocol30    = 196608
)

// ServePostgres terminates one pod Postgres connection: it answers the pod's
// startup, asks it for a cleartext password (the handle), resolves it, opens the
// real database and performs the REAL auth (trust / cleartext / md5 / SCRAM)
// with the real password, tells the pod AuthenticationOk, then splices — the
// upstream's ParameterStatus/BackendKeyData/ReadyForQuery flow straight through.
func ServePostgres(pod net.Conn, r Resolver) error {
	defer pod.Close()
	podR := bufio.NewReader(pod)

	var database string
	for {
		body, err := readStartup(podR)
		if err != nil {
			return err
		}
		code := binary.BigEndian.Uint32(body[:4])
		if code == pgSSLRequest || code == pgGSSENCRequest {
			if _, err := pod.Write([]byte{'N'}); err != nil { // no SSL on pod↔broker
				return err
			}
			continue
		}
		_, database = parseStartupParams(body[4:]) // pod's user is ignored; broker uses the real one
		break
	}

	// Ask the pod for a cleartext password — its handle.
	if err := writeMessage(pod, 'R', authInt32(3)); err != nil {
		return err
	}
	typ, payload, err := readMessage(podR)
	if err != nil {
		return err
	}
	if typ != 'p' {
		return fmt.Errorf("expected PasswordMessage, got %q", typ)
	}
	handle := strings.TrimRight(string(payload), "\x00")

	target, err := r.Resolve(handle)
	if err != nil {
		writeErrPG(pod, "28P01", "invalid or revoked poddle handle")
		return err
	}
	db := target.DB
	if database != "" {
		db = database
	}

	up, err := net.Dial("tcp", target.Addr)
	if err != nil {
		writeErrPG(pod, "08006", "poddle upstream unreachable")
		return err
	}
	defer up.Close()
	upR := bufio.NewReader(up)
	if err := pgClientAuth(up, upR, target.User, target.Pass, db); err != nil {
		writeErrPG(pod, "28P01", "poddle upstream auth failed")
		return err
	}

	if err := writeMessage(pod, 'R', authInt32(0)); err != nil { // AuthenticationOk
		return err
	}
	return splice(pod, up, podR, upR)
}

// pgAuthCode reads the Int32 auth-request code from an 'R' (Authentication)
// message body. readMessage only guarantees a body length >= 0, so a malformed
// or hostile upstream can send a short 'R' message; the upstream link is
// plaintext TCP, so this includes a MITM. Return ok=false rather than letting
// body[:4] slice-panic and crash the daemon for every pod.
func pgAuthCode(body []byte) (code uint32, ok bool) {
	if len(body) < 4 {
		return 0, false
	}
	return binary.BigEndian.Uint32(body[:4]), true
}

// pgClientAuth performs the real startup + authentication to the upstream.
func pgClientAuth(up net.Conn, upR *bufio.Reader, user, pass, db string) error {
	if err := writeStartup(up, user, db); err != nil {
		return err
	}
	for {
		typ, body, err := readMessage(upR)
		if err != nil {
			return err
		}
		switch typ {
		case 'R':
			code, ok := pgAuthCode(body)
			if !ok {
				return fmt.Errorf("short authentication message (%d bytes)", len(body))
			}
			switch code {
			case 0: // AuthenticationOk
				return nil
			case 3: // cleartext
				if err := writeMessage(up, 'p', append([]byte(pass), 0)); err != nil {
					return err
				}
			case 5: // md5
				if len(body) < 8 {
					return fmt.Errorf("short md5 salt in upstream auth message")
				}
				resp := md5Auth(user, pass, body[4:8])
				if err := writeMessage(up, 'p', append([]byte(resp), 0)); err != nil {
					return err
				}
			case 10: // SASL
				return pgSCRAM(up, upR, pass, body[4:])
			default:
				return fmt.Errorf("unsupported upstream auth type %d", code)
			}
		case 'E':
			return fmt.Errorf("upstream refused: %s", pgErrText(body))
		default:
			return fmt.Errorf("unexpected message %q during auth", typ)
		}
	}
}

// pgSCRAM runs the SCRAM-SHA-256 exchange to the upstream, consuming through
// AuthenticationOk.
func pgSCRAM(up net.Conn, upR *bufio.Reader, pass string, mechs []byte) error {
	if !bytes.Contains(mechs, []byte("SCRAM-SHA-256")) {
		return fmt.Errorf("upstream does not offer SCRAM-SHA-256")
	}
	nonce, err := randNonce()
	if err != nil {
		return err
	}
	sc := newSCRAM("", pass, nonce)

	first := sc.firstMessage()
	var init bytes.Buffer
	init.WriteString("SCRAM-SHA-256")
	init.WriteByte(0)
	init.Write(int32be(uint32(len(first))))
	init.WriteString(first)
	if err := writeMessage(up, 'p', init.Bytes()); err != nil {
		return err
	}

	typ, body, err := readMessage(upR)
	if err != nil {
		return err
	}
	if code, ok := pgAuthCode(body); typ != 'R' || !ok || code != 11 { // SASLContinue
		return fmt.Errorf("expected SASLContinue")
	}
	final, err := sc.finalMessage(string(body[4:]))
	if err != nil {
		return err
	}
	if err := writeMessage(up, 'p', []byte(final)); err != nil {
		return err
	}

	if typ, body, err = readMessage(upR); err != nil { // SASLFinal
		return err
	}
	if code, ok := pgAuthCode(body); typ != 'R' || !ok || code != 12 {
		return fmt.Errorf("expected SASLFinal")
	}
	if typ, body, err = readMessage(upR); err != nil { // AuthenticationOk
		return err
	}
	if code, ok := pgAuthCode(body); typ != 'R' || !ok || code != 0 {
		return fmt.Errorf("expected AuthenticationOk after SASL")
	}
	return nil
}

// --- wire helpers ---

// readStartup reads a length-prefixed startup/SSLRequest packet (no type byte).
func readStartup(r *bufio.Reader) ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n < 8 || n > 1<<20 {
		return nil, fmt.Errorf("bad startup length %d", n)
	}
	body := make([]byte, n-4)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

// readMessage reads a typed message: type byte + Int32 length + body.
func readMessage(r *bufio.Reader) (byte, []byte, error) {
	typ, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n < 4 || n > 1<<24 {
		return 0, nil, fmt.Errorf("bad message length %d", n)
	}
	body := make([]byte, n-4)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}
	return typ, body, nil
}

// writeMessage writes a typed message.
func writeMessage(w io.Writer, typ byte, body []byte) error {
	hdr := []byte{typ, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(body)+4))
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}

// writeStartup sends a protocol-3.0 StartupMessage with user (+ database).
func writeStartup(w io.Writer, user, db string) error {
	var b bytes.Buffer
	b.Write(int32be(pgProtocol30))
	b.WriteString("user\x00")
	b.WriteString(user)
	b.WriteByte(0)
	if db != "" {
		b.WriteString("database\x00")
		b.WriteString(db)
		b.WriteByte(0)
	}
	b.WriteByte(0) // params terminator
	full := append(int32be(uint32(b.Len()+4)), b.Bytes()...)
	_, err := w.Write(full)
	return err
}

func parseStartupParams(b []byte) (user, db string) {
	parts := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
	for i := 0; i+1 < len(parts); i += 2 {
		switch parts[i] {
		case "user":
			user = parts[i+1]
		case "database":
			db = parts[i+1]
		}
	}
	return
}

func md5Auth(user, pass string, salt []byte) string {
	inner := md5hex(pass + user)
	return "md5" + md5hex(inner+string(salt))
}

func md5hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func randNonce() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(b), nil
}

func int32be(n uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, n)
	return b
}

func authInt32(n uint32) []byte { return int32be(n) }

func pgErrText(body []byte) string {
	for _, f := range strings.Split(string(body), "\x00") {
		if strings.HasPrefix(f, "M") {
			return strings.TrimPrefix(f, "M")
		}
	}
	return "error"
}

// writeErrPG sends a minimal ErrorResponse so the pod's client fails cleanly.
func writeErrPG(c net.Conn, code, msg string) {
	var b bytes.Buffer
	b.WriteString("SFATAL\x00")
	b.WriteString("C" + code + "\x00")
	b.WriteString("M" + msg + "\x00")
	b.WriteByte(0)
	_ = writeMessage(c, 'E', b.Bytes())
}
