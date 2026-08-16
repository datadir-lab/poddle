// Package l4 is poddle's TCP (layer-4) broker: for datastores whose auth binds
// to the connection (Redis AUTH, later Postgres SCRAM), the pod authenticates
// to the broker with its handle and the broker re-authenticates to the real
// datastore with the real credential, then splices the two sockets. The real
// secret never reaches the pod — the same guarantee as the HTTP gateway, at L4.
package l4

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Target is the real datastore a handle resolves to.
type Target struct {
	Addr string // upstream host:port
	User string // real auth user (may be empty)
	Pass string // real auth password
	DB   string // database name (Postgres); empty for Redis
}

// TargetFromDSN parses a datastore DSN (e.g. postgres://user:pass@host:5432/db)
// into a Target. The scheme is ignored — host:port, userinfo, and path are used.
func TargetFromDSN(dsn string) (Target, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return Target{}, err
	}
	if u.Host == "" {
		return Target{}, fmt.Errorf("datastore DSN %q has no host", dsn)
	}
	t := Target{Addr: u.Host, DB: strings.TrimPrefix(u.Path, "/")}
	if u.User != nil {
		t.User = u.User.Username()
		t.Pass, _ = u.User.Password()
	}
	return t, nil
}

// Resolver turns a pod-presented handle into its real Target.
type Resolver interface {
	Resolve(handle string) (Target, error)
}

// ServeRedis terminates one pod Redis connection: it reads the pod's AUTH
// (whose password is the handle), resolves it, opens the real upstream and
// AUTHs with the real credential, tells the pod OK, then splices the session.
func ServeRedis(pod net.Conn, r Resolver) error {
	defer pod.Close()
	podR := bufio.NewReader(pod)

	cmd, err := readCommand(podR)
	if err != nil {
		return err
	}
	if len(cmd) < 2 || !strings.EqualFold(cmd[0], "AUTH") {
		writeErr(pod, "NOAUTH poddle requires AUTH <handle> first")
		return fmt.Errorf("expected AUTH first, got %v", cmd)
	}
	handle := cmd[len(cmd)-1]
	target, err := r.Resolve(handle)
	if err != nil {
		writeErr(pod, "WRONGPASS invalid or revoked handle")
		return err
	}

	up, err := net.Dial("tcp", target.Addr)
	if err != nil {
		writeErr(pod, "ERR poddle upstream unreachable")
		return err
	}
	defer up.Close()
	upR := bufio.NewReader(up)

	if err := redisAuth(up, upR, target.User, target.Pass); err != nil {
		writeErr(pod, "ERR poddle upstream auth failed")
		return err
	}
	if _, err := pod.Write([]byte("+OK\r\n")); err != nil {
		return err
	}

	// Splice the rest of the session. Buffered bytes in podR/upR are forwarded
	// because io.Copy drains the bufio.Reader before the underlying conn.
	return splice(pod, up, podR, upR)
}

// redisAuth authenticates to the upstream with the real credential and checks
// for +OK.
func redisAuth(up net.Conn, upR *bufio.Reader, user, pass string) error {
	var cmd string
	if user != "" {
		cmd = encodeCommand("AUTH", user, pass)
	} else {
		cmd = encodeCommand("AUTH", pass)
	}
	if _, err := up.Write([]byte(cmd)); err != nil {
		return err
	}
	line, err := upR.ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, "+OK") {
		return fmt.Errorf("upstream AUTH rejected: %s", strings.TrimSpace(line))
	}
	return nil
}

// splice copies bidirectionally until either side closes.
func splice(a, b net.Conn, aR, bR *bufio.Reader) error {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(b, aR); done <- struct{}{} }()
	go func() { _, _ = io.Copy(a, bR); done <- struct{}{} }()
	<-done
	return nil
}

// readCommand reads one RESP array-of-bulk-strings client command.
func readCommand(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if len(line) == 0 || line[0] != '*' {
		return nil, fmt.Errorf("expected a RESP array, got %q", line)
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil || n < 1 {
		return nil, fmt.Errorf("bad array length %q", line)
	}
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		hdr, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		hdr = strings.TrimRight(hdr, "\r\n")
		if len(hdr) == 0 || hdr[0] != '$' {
			return nil, fmt.Errorf("expected a bulk string, got %q", hdr)
		}
		blen, err := strconv.Atoi(hdr[1:])
		if err != nil || blen < 0 {
			return nil, fmt.Errorf("bad bulk length %q", hdr)
		}
		buf := make([]byte, blen+2) // include trailing CRLF
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:blen]))
	}
	return args, nil
}

// encodeCommand builds a RESP array-of-bulk-strings command.
func encodeCommand(parts ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(parts))
	for _, p := range parts {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(p), p)
	}
	return b.String()
}

func writeErr(c net.Conn, msg string) { _, _ = c.Write([]byte("-" + msg + "\r\n")) }
