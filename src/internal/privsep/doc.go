// Package privsep is a de-risking SPIKE for Phase 2 of poddle's broker Tier-2
// privilege separation (see docs/design/broker-privilege-separation.md). It is
// NOT wired into the production daemon; it proves ONE mechanism in running code:
// that a privileged KEEPER process and an unprivileged FRONT process, forked
// from a single static Go binary via re-exec, can carry a broker.Keeper call
// across an inherited AF_UNIX socketpair while running under the broker's locked
// container model (--read-only --cap-drop=all --no-new-privileges, distroless
// static — none of which block fork/exec/socketpair).
//
// The spike resolves three open questions in code:
//
//   - A parent creates a socketpair, re-execs itself in "keeper mode" (detected
//     via the PODDLE_PRIVSEP_KEEPER env var) with one end inherited at fd 3
//     through exec.Cmd.ExtraFiles, and both sides talk over net.FileConn'd ends.
//   - A Keeper RPC round-trip crosses the two processes: the FRONT frames a
//     request, the KEEPER runs the REAL l4 SCRAM arithmetic and frames the
//     result back, and the returned proof is byte-identical to an in-process
//     l4.ComputeSCRAMProof for the same inputs — the real crypto crosses the
//     boundary, not a stub.
//   - Death propagation is mutual and fail-closed: the parent supervises the
//     child (Wait); when the child dies the front's next call errors on socket
//     EOF instead of hanging; when the parent dies the keeper's serve loop hits
//     EOF and exits (belt-and-suspenders: Pdeathsig SIGKILL). Neither a
//     vaultless front nor an orphaned secret-holder survives.
//
// Everything with OS specifics lives in the //go:build linux files; this file
// carries only the package documentation so the package still builds (as an
// empty package) on non-Linux hosts.
package privsep
