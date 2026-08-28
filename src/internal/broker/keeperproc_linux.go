//go:build linux

package broker

import (
	"os"

	"github.com/datadir-lab/poddle/src/internal/privsep"
)

// envKeeperMirrorDir carries the OAuth write-back mirror directory to the keeper
// subprocess (via the environment it inherits at spawn). It is the ONE piece of
// keeper config that can't ride the RPC: the persister writes to disk keeper-side,
// so the front can't hand it an OAuthPersister value — only the dir to build one
// from. Egress mode, by contrast, is set over the RPC (custody.SetEgressMode).
const envKeeperMirrorDir = "PODDLE_KEEPER_OAUTH_MIRROR_DIR"

// keeperSubcommandMarker is passed as the re-exec'd child's argv marker. The env
// var (PODDLE_PRIVSEP_KEEPER, set by privsep.Spawn) is what the entrypoint actually
// keys on; the marker just makes the process legible in a process listing.
const keeperSubcommandMarker = "__poddle_keeper__"

// RunKeeperProcess is the KEEPER subprocess entrypoint: it attaches to the
// inherited socketpair (privsep.KeeperConn), builds a fresh vault-backed localKeeper
// — the ONLY copy of the vault in the two-process broker — configures the parts of
// it that can't ride the RPC, and serves the custody RPC until the front closes the
// socket (a clean EOF -> the keeper exits, fail closed; no vaultless front and no
// orphaned secret-holder survive). The re-exec entrypoint calls this when
// privsep.IsKeeperMode() is true, then exits with its result.
func RunKeeperProcess() error {
	conn, err := privsep.KeeperConn()
	if err != nil {
		return err
	}
	defer conn.Close()

	k := newLocalKeeper(NewHandles(NewVault()))
	if dir := os.Getenv(envKeeperMirrorDir); dir != "" {
		k.SetOAuthPersister(NewStateDirPersister(dir))
	}
	// Egress mode is not read here: the front sets it over the RPC after spawn
	// (custody.SetEgressMode). Until then the keeper defaults to "redact" (the safe
	// default), and no pod traffic flows before the front finishes startup.
	return serveKeeper(conn, k)
}

// spawnKeeperBroker forks the keeper subprocess and returns a Broker whose custody
// is the socketKeeperClient over it — the front then holds NO vault, only the
// socket — plus a channel that delivers the keeper's exit so the caller can fail
// closed (or restart) on keeper death. mirrorDir (if non-empty) is handed to the
// keeper via the environment it inherits, so its RunKeeperProcess can build the
// write-back persister keeper-side.
func spawnKeeperBroker(mirrorDir string) (*Broker, <-chan error, error) {
	if mirrorDir != "" {
		// Set on the front's environment so the forked child inherits it; the front
		// never reads it, so this is harmless. Spawn happens once at startup, before
		// any concurrency, so the process-global Setenv is safe here.
		_ = os.Setenv(envKeeperMirrorDir, mirrorDir)
	}
	conn, cmd, err := privsep.Spawn(keeperSubcommandMarker)
	if err != nil {
		return nil, nil, err
	}
	client := newSocketKeeperClient(conn)
	return newBrokerOverKeeper(client), privsep.Supervise(cmd), nil
}

// closeCustody shuts down a two-process Broker's socket client (closing the conn,
// which the keeper observes as EOF and exits). A no-op for an in-process Broker.
func (b *Broker) closeCustody() {
	if c, ok := b.custody.(*socketKeeperClient); ok {
		_ = c.Close()
	}
}
