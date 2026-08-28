//go:build linux

package privsep

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/datadir-lab/poddle/src/internal/l4"
)

const (
	// keeperEnvVar marks a re-exec'd process as the privsep keeper child. The
	// re-exec entrypoint checks it (IsKeeperMode) before normal startup and, if
	// set, runs the keeper serve loop instead of the front.
	keeperEnvVar = "PODDLE_PRIVSEP_KEEPER"

	// keeperFDNum is where ExtraFiles[0] lands in the child: fds 0/1/2 are
	// stdin/stdout/stderr, so the first inherited extra file is fd 3.
	keeperFDNum = 3

	// maxFrameLen bounds an untrusted length prefix so a hostile or corrupt peer
	// can't drive an unbounded allocation. A SCRAM proof request is tiny; 1 MiB
	// is generous headroom.
	maxFrameLen = 1 << 20
)

// Keeper is the minimal keeper contract this spike exercises: the one cleanly
// serializable broker.Keeper method, SCRAMProof (all bytes/strings/ints — no
// live http objects). The real *broker.localKeeper and the l4.SCRAMKeeper seam
// already have this exact signature, so Phase 2 swaps a socketpair-backed client
// for the in-process keeper with no change to l4's SCRAM state machine.
type Keeper interface {
	SCRAMProof(handle string, salt []byte, iter int, authMessage string) ([]byte, error)
}

// Spawn creates an AF_UNIX SOCK_STREAM socketpair, re-execs THIS binary in
// keeper mode with one end inherited at fd 3 (via ExtraFiles) and
// PODDLE_PRIVSEP_KEEPER=1 in its environment, and returns the front-side conn
// plus the already-started child *exec.Cmd. The caller supervises cmd (see
// Supervise) and talks to the keeper over the returned conn (see newClient).
//
// args are passed to the re-exec'd binary (e.g. a "privsep-keeper" subcommand
// marker for a production main); the spike relies only on the env var, so the
// test passes none.
//
// Topology note: whichever process calls Spawn becomes the PARENT. This spike's
// caller is the FRONT (the re-exec-self TEST harness makes the test process the
// parent), so here FRONT forks KEEPER. The mechanism is identical if inverted;
// production should invert it (KEEPER as PID 1 forks the FRONT worker) — see the
// spike report's supervision-topology section.
func Spawn(args ...string) (*net.UnixConn, *exec.Cmd, error) {
	// SOCK_CLOEXEC so neither end leaks into unrelated children on exec; the
	// keeper end is still delivered to the child because ExtraFiles dup2's it
	// into place (fd 3), and dup2 clears close-on-exec on the target.
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("privsep: socketpair: %w", err)
	}
	frontFile := os.NewFile(uintptr(fds[0]), "privsep-front")
	keeperFile := os.NewFile(uintptr(fds[1]), "privsep-keeper")

	frontConn, err := unixConnFromFile(frontFile)
	// net.FileConn dups the fd, so the original is no longer needed either way.
	_ = frontFile.Close()
	if err != nil {
		_ = keeperFile.Close()
		return nil, nil, err
	}

	self, err := os.Executable()
	if err != nil {
		_ = keeperFile.Close()
		_ = frontConn.Close()
		return nil, nil, fmt.Errorf("privsep: locate self: %w", err)
	}

	cmd := exec.Command(self, args...)
	cmd.Env = append(os.Environ(), keeperEnvVar+"=1")
	cmd.ExtraFiles = []*os.File{keeperFile} // -> child fd 3
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Pdeathsig is defense in depth: if the parent (front) dies, the kernel sends
	// SIGKILL to the keeper so no orphaned secret-holder survives even if it were
	// blocked somewhere other than the socket read. The primary death-detection
	// mechanism is still the socketpair EOF the serve loop observes.
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}

	if err := cmd.Start(); err != nil {
		_ = keeperFile.Close()
		_ = frontConn.Close()
		return nil, nil, fmt.Errorf("privsep: start keeper: %w", err)
	}
	// The child inherited its own dup of the keeper end; close the parent's copy
	// so that when the child exits the front's read sees a clean EOF (and vice
	// versa). Keeping it open here would defeat EOF-based death detection.
	_ = keeperFile.Close()

	return frontConn, cmd, nil
}

// Supervise waits on the keeper child in a goroutine and returns a channel that
// delivers the child's exit result exactly once. If the keeper dies, the front's
// next SCRAMProof call sees a socketpair read error (fail closed) — the front
// never proceeds without a live vault. The caller must not also call cmd.Wait.
func Supervise(cmd *exec.Cmd) <-chan error {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return done
}

// IsKeeperMode reports whether this process was re-exec'd as the privsep keeper
// (PODDLE_PRIVSEP_KEEPER=1). The re-exec entrypoint (TestMain in the spike; a
// keeper subcommand in production) checks this before normal startup and, if
// true, runs RunKeeper instead of the front.
func IsKeeperMode() bool { return os.Getenv(keeperEnvVar) == "1" }

// RunKeeper is the keeper child entrypoint: it attaches to the inherited
// socketpair (fd 3) and serves k until the front closes the socket (EOF), then
// returns. Callers exit the process with a non-zero status on a non-nil error.
func RunKeeper(k Keeper) error {
	conn, err := keeperConn()
	if err != nil {
		return err
	}
	defer conn.Close()
	return ServeKeeper(conn, k)
}

// keeperConn returns the inherited socketpair end (fd 3) as a *net.UnixConn.
// Valid only in a process for which IsKeeperMode() is true.
func keeperConn() (*net.UnixConn, error) {
	f := os.NewFile(uintptr(keeperFDNum), "privsep-keeper")
	if f == nil {
		return nil, errors.New("privsep: keeper fd 3 was not inherited")
	}
	conn, err := unixConnFromFile(f)
	_ = f.Close() // net.FileConn dups
	return conn, err
}

// unixConnFromFile wraps an fd-backed *os.File as a *net.UnixConn, asserting the
// concrete type net.FileConn returns for an AF_UNIX socket.
func unixConnFromFile(f *os.File) (*net.UnixConn, error) {
	c, err := net.FileConn(f)
	if err != nil {
		return nil, fmt.Errorf("privsep: FileConn: %w", err)
	}
	uc, ok := c.(*net.UnixConn)
	if !ok {
		_ = c.Close()
		return nil, fmt.Errorf("privsep: fd is %T, not *net.UnixConn", c)
	}
	return uc, nil
}

// ServeKeeper runs the keeper-side request loop against conn, dispatching each
// framed request to k until the front closes the socket (a clean EOF, which
// returns nil so the keeper exits — fail closed, no vaultless front and no
// orphaned secret-holder) or a framing/decode error occurs (returned).
func ServeKeeper(conn *net.UnixConn, k Keeper) error {
	for {
		frame, err := readFrame(conn)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil // front gone between requests — exit cleanly
			}
			return err
		}
		var req scramProofReq
		if err := decode(frame, &req); err != nil {
			return fmt.Errorf("privsep keeper: decode request: %w", err)
		}
		proof, perr := k.SCRAMProof(req.Handle, req.Salt, req.Iter, req.AuthMessage)
		resp := scramProofResp{Proof: proof}
		if perr != nil {
			resp.Err = perr.Error()
		}
		out, err := encode(resp)
		if err != nil {
			return fmt.Errorf("privsep keeper: encode response: %w", err)
		}
		if err := writeFrame(conn, out); err != nil {
			return fmt.Errorf("privsep keeper: write response: %w", err)
		}
	}
}

// scramProofReq is the framed request for the one method this spike carries.
type scramProofReq struct {
	Handle      string
	Salt        []byte
	Iter        int
	AuthMessage string
}

// scramProofResp is the framed response. Err carries a stringified keeper-side
// error (gob can't transport a Go error value); an empty Err means success.
type scramProofResp struct {
	Proof []byte
	Err   string
}

// socketKeeperClient is the front-side stub. It frames a SCRAMProof request over
// the socketpair and reads the framed response, satisfying the same SCRAMProof
// shape the in-process keeper does — so Phase 2 hands l4's state machine one of
// these instead of a *localKeeper with no other change. A mutex serializes
// callers over the single stream (one request/response in flight at a time).
type socketKeeperClient struct {
	mu   sync.Mutex
	conn *net.UnixConn
}

// newClient builds a front-side client over the socketpair conn Spawn returned.
func newClient(conn *net.UnixConn) *socketKeeperClient {
	return &socketKeeperClient{conn: conn}
}

// SCRAMProof frames the request to the keeper process and returns its proof, or
// an error. A read error (EOF/broken pipe/deadline) means the keeper is gone —
// the front fails closed with a clear error rather than hanging.
func (c *socketKeeperClient) SCRAMProof(handle string, salt []byte, iter int, authMessage string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	frame, err := encode(scramProofReq{Handle: handle, Salt: salt, Iter: iter, AuthMessage: authMessage})
	if err != nil {
		return nil, fmt.Errorf("privsep: encode request: %w", err)
	}
	if err := writeFrame(c.conn, frame); err != nil {
		return nil, fmt.Errorf("privsep: keeper unreachable (send): %w", err)
	}
	respFrame, err := readFrame(c.conn)
	if err != nil {
		return nil, fmt.Errorf("privsep: keeper unreachable (recv): %w", err)
	}
	var resp scramProofResp
	if err := decode(respFrame, &resp); err != nil {
		return nil, fmt.Errorf("privsep: decode response: %w", err)
	}
	if resp.Err != "" {
		return nil, errors.New(resp.Err)
	}
	return resp.Proof, nil
}

// --- minimal length-prefixed gob codec: 4-byte big-endian length + gob body ---

func encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decode(data []byte, v any) error {
	return gob.NewDecoder(bytes.NewReader(data)).Decode(v)
}

func writeFrame(w io.Writer, payload []byte) error {
	if len(payload) > maxFrameLen {
		return fmt.Errorf("privsep: frame of %d bytes exceeds max %d", len(payload), maxFrameLen)
	}
	var hdr [4]byte
	//nolint:gosec // G115: len(payload) is bounded by maxFrameLen (<=1 MiB) in the check above, so the int->uint32 conversion cannot overflow.
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > maxFrameLen {
		return nil, fmt.Errorf("privsep: frame length %d exceeds max %d", n, maxFrameLen)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// fixedPasswordKeeper is the spike's stand-in for *broker.localKeeper: it
// computes the SCRAM proof with the REAL l4 RFC 7677 arithmetic
// (l4.ComputeSCRAMProof) — the same code broker.localKeeper.SCRAMProof calls
// after it resolves a handle to a password — over a fixed test password. Wiring
// a full localKeeper needs a vault plus a stored credential, which is out of
// scope for a mechanism spike; swapping this for *broker.localKeeper in Phase 2
// is a one-line change because the SCRAMProof signature is identical. The
// password is a test fixture and is never logged.
type fixedPasswordKeeper struct{ password string }

// SCRAMProof ignores handle (the real keeper resolves it to a credential; the
// spike uses a fixed password) and runs the real proof arithmetic.
func (f fixedPasswordKeeper) SCRAMProof(handle string, salt []byte, iter int, authMessage string) ([]byte, error) {
	return l4.ComputeSCRAMProof(f.password, salt, iter, authMessage)
}

// NewFixedPasswordKeeper builds the spike keeper over a fixed test password.
func NewFixedPasswordKeeper(password string) Keeper { return fixedPasswordKeeper{password: password} }
