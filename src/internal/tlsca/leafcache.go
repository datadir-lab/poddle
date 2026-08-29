package tlsca

import (
	"container/list"
	"crypto/tls"
	"time"
)

// maxLeafCache bounds the per-host leaf cache. A legitimate intercepting pod hits
// a handful of distinct hosts; the cap exists so a compromised broker FRONT can't
// drive unbounded memory growth (and repeated ECDSA keygen) in the vault-holding
// KEEPER by asking it to sign leaves for a flood of unique hostnames. 1024 is far
// above any real working set and still trivially small in memory.
const maxLeafCache = 1024

// leafRenewBefore re-mints a cached leaf this long before it expires, so a leaf
// handed to a just-starting handshake can't expire mid-connection. Well under the
// 7-day leafTTL.
const leafRenewBefore = time.Hour

// leafEntry is one LRU node: the host it was minted for and the leaf itself.
type leafEntry struct {
	host string
	cert *tls.Certificate
}

// leafLRU is a bounded, least-recently-used cache of per-host leaves. It is NOT
// safe for concurrent use on its own — Authority always calls it under Authority.mu
// (which also serializes leaf minting), so no internal lock is needed.
type leafLRU struct {
	capacity int
	ll       *list.List               // front = most recently used
	m        map[string]*list.Element // host -> element holding a *leafEntry
}

func newLeafLRU(capacity int) *leafLRU {
	return &leafLRU{capacity: capacity, ll: list.New(), m: map[string]*list.Element{}}
}

// get returns the cached leaf for host if present and not within leafRenewBefore of
// expiry (a near-expired entry is dropped and reported as a miss so the caller
// re-mints). A hit is promoted to most-recently-used.
func (c *leafLRU) get(host string, now time.Time) (*tls.Certificate, bool) {
	el, ok := c.m[host]
	if !ok {
		return nil, false
	}
	ent := el.Value.(*leafEntry)
	if ent.cert.Leaf == nil || now.Add(leafRenewBefore).After(ent.cert.Leaf.NotAfter) {
		c.ll.Remove(el)
		delete(c.m, host)
		return nil, false
	}
	c.ll.MoveToFront(el)
	return ent.cert, true
}

// put inserts (or refreshes) the leaf for host as most-recently-used, evicting the
// least-recently-used entry when over capacity.
func (c *leafLRU) put(host string, cert *tls.Certificate) {
	if el, ok := c.m[host]; ok {
		el.Value.(*leafEntry).cert = cert
		c.ll.MoveToFront(el)
		return
	}
	c.m[host] = c.ll.PushFront(&leafEntry{host: host, cert: cert})
	for c.ll.Len() > c.capacity {
		oldest := c.ll.Back()
		if oldest == nil {
			break
		}
		c.ll.Remove(oldest)
		delete(c.m, oldest.Value.(*leafEntry).host)
	}
}

// len reports the number of cached leaves (for tests/observability).
func (c *leafLRU) len() int { return c.ll.Len() }
