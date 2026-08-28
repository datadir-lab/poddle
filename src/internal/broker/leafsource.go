package broker

import (
	"crypto/tls"
	"sync"

	"github.com/datadir-lab/poddle/src/internal/tlsca"
)

// custodyLeafSource is the front-side LeafSource for TLS interception: it mints a
// per-host leaf via the keeper (Custody.SignLeaf) and reassembles a tls.Certificate
// the forward proxy presents to the pod. The security win: the CA PRIVATE KEY that
// signs the leaves never leaves the keeper. A compromised front can still ASK the
// keeper to sign a leaf for any host (SignLeaf is an online, per-call signing oracle
// with no keeper-side host allow-list — worth hardening later, e.g. an allow-list /
// rate-limit, which would also bound the keeper's per-host leaf cache against a
// unique-host flood), but it can never EXFILTRATE the CA key to mint leaves offline
// or after it's evicted — killing the front ends its access. This is strictly
// better than the old model, where the front held the CA key outright.
//
// Reassembled leaves are cached so a repeat handshake to the same host doesn't
// re-RPC the keeper (the keeper caches the DER too; this caches the parsed form).
// NOTE: this cache is not invalidated on a CA rotation (a second EnsureCA) — fine
// today because nothing rotates the CA at runtime; revisit when rotation is wired.
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
