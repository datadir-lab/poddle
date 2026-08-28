package broker

import (
	"crypto/tls"
	"sync"

	"github.com/datadir-lab/poddle/src/internal/tlsca"
)

// custodyLeafSource is the front-side LeafSource for TLS interception: it mints a
// per-host leaf via the keeper (Custody.SignLeaf) and reassembles a tls.Certificate
// the forward proxy presents to the pod. The CA private key that SIGNS the leaves
// lives keeper-side (a front RCE gets only per-host leaf keys, which it already
// controls for hosts it intercepts, never the CA that can forge any host).
// Reassembled leaves are cached so a repeat handshake to the same host doesn't
// re-RPC the keeper (the keeper caches the DER too; this caches the parsed form).
type custodyLeafSource struct {
	custody Custody

	mu    sync.Mutex
	cache map[string]*tls.Certificate
}

func newCustodyLeafSource(c Custody) *custodyLeafSource {
	return &custodyLeafSource{custody: c, cache: map[string]*tls.Certificate{}}
}

// LeafFor returns the cached leaf for host, or mints one via the keeper.
func (s *custodyLeafSource) LeafFor(host string) (*tls.Certificate, error) {
	s.mu.Lock()
	if lc, ok := s.cache[host]; ok {
		s.mu.Unlock()
		return lc, nil
	}
	s.mu.Unlock()

	certDER, keyDER, err := s.custody.SignLeaf(host)
	if err != nil {
		return nil, err
	}
	lc, err := tlsca.LeafFromDER(certDER, keyDER)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.cache[host] = lc
	s.mu.Unlock()
	return lc, nil
}

var _ LeafSource = (*custodyLeafSource)(nil)
