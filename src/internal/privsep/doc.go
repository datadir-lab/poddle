// Package privsep is the process-separation MECHANISM for Phase 2 of poddle's
// broker Tier-2 privilege separation (see docs/design/broker-privilege-separation.md):
// it lets a single static Go binary fork a privileged KEEPER process (holding the
// vault + secret custody) from an unprivileged FRONT process (parsing untrusted
// pod traffic), so a front RCE cannot read the keeper's address space.
//
// It is deliberately protocol-free — it carries no broker types and imports
// nothing of the broker package (so broker can import privsep for Spawn with no
// import cycle). The broker owns the keeper RPC (broker.serveKeeper /
// socketKeeperClient); privsep only provides the transport it runs over:
//
//   - Spawn creates an AF_UNIX socketpair, re-execs THIS binary in "keeper mode"
//     (detected via the PODDLE_PRIVSEP_KEEPER env var) with one end inherited at
//     fd 3 through exec.Cmd.ExtraFiles, and returns the front-side *net.UnixConn
//     plus the started child. The keeper entrypoint gets its end via KeeperConn.
//   - Supervise waits on the child; when the keeper dies the front's next keeper
//     call errors on socket EOF (fail closed) instead of hanging, and when the
//     front dies the keeper's serve loop hits EOF and exits (belt-and-suspenders:
//     a thread-scoped Pdeathsig SIGKILL backstop). Neither a vaultless front nor
//     an orphaned secret-holder survives.
//
// The feasibility of all this under the broker's locked container model
// (--read-only --cap-drop=all --no-new-privileges, distroless static — none of
// which block fork/exec/socketpair) was proven by the original spike (PR #152)
// and is re-verified on every CI run by the linux socketpair round-trip test.
//
// Everything with OS specifics lives in the //go:build linux files; this file
// carries only the package documentation so the package still builds (as an empty
// package) on non-Linux hosts.
package privsep
