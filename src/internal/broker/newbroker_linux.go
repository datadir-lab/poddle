//go:build linux

package broker

import "os"

// envBrokerPrivsep opts into the Phase-2 two-process broker (default off). Set to
// "1", NewBrokerFromEnv forks a keeper subprocess that holds the vault; unset, the
// broker runs in-process (the shipped default). Opt-in + default-off is poddle's
// cautious-rollout posture (as with TLS interception): the proven single-process
// broker stays the default until the two-process path is e2e-hardened.
const envBrokerPrivsep = "PODDLE_BROKER_PRIVSEP"

// NewBrokerFromEnv builds the broker for poddled: two-process (a keeper subprocess
// holding the vault) when PODDLE_BROKER_PRIVSEP=1, else in-process. It returns the
// Broker, a channel that delivers the keeper's exit so the caller can fail closed
// (nil in-process — nothing to supervise), and any spawn error. mirrorDir enables
// OAuth write-back: in-process it's set directly; two-process the keeper builds its
// own disk persister from it (the persister can't cross the wire).
func NewBrokerFromEnv(mirrorDir string) (*Broker, <-chan error, error) {
	if os.Getenv(envBrokerPrivsep) == "1" {
		return spawnKeeperBroker(mirrorDir)
	}
	b := NewBroker()
	b.EnableOAuthWriteBack(mirrorDir)
	return b, nil, nil
}
