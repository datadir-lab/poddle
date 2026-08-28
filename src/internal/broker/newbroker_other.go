//go:build !linux

package broker

// NewBrokerFromEnv builds an in-process broker off Linux: the two-process keeper
// (privsep fork + socketpair) is Linux-only. The signature matches the Linux
// variant so poddled.Serve stays platform-agnostic; the keeper-death channel is
// always nil here (nothing to supervise).
func NewBrokerFromEnv(mirrorDir string) (*Broker, <-chan error, error) {
	b := NewBroker()
	b.EnableOAuthWriteBack(mirrorDir)
	return b, nil, nil
}
