package tlsca

import (
	"crypto/tls"
	"crypto/x509"
	"strconv"
	"testing"
	"time"
)

// leafValidUntil builds a cache entry whose leaf expires at exp — enough for the
// cache's expiry/eviction logic (which only reads Leaf.NotAfter).
func leafValidUntil(exp time.Time) *tls.Certificate {
	return &tls.Certificate{Leaf: &x509.Certificate{NotAfter: exp}}
}

func TestLeafLRU_BoundsSize(t *testing.T) {
	c := newLeafLRU(4)
	now := time.Now()
	exp := now.Add(24 * time.Hour)
	for i := 0; i < 100; i++ {
		c.put("host"+strconv.Itoa(i), leafValidUntil(exp))
	}
	if c.len() != 4 {
		t.Fatalf("cache grew past its cap: len=%d, want 4", c.len())
	}
	// The 4 most-recently-put survive; older ones were evicted.
	if _, ok := c.get("host99", now); !ok {
		t.Error("most-recent entry should be present")
	}
	if _, ok := c.get("host0", now); ok {
		t.Error("oldest entry should have been evicted")
	}
}

func TestLeafLRU_EvictsLeastRecentlyUsed(t *testing.T) {
	c := newLeafLRU(3)
	now := time.Now()
	exp := now.Add(24 * time.Hour)
	c.put("a", leafValidUntil(exp))
	c.put("b", leafValidUntil(exp))
	c.put("c", leafValidUntil(exp))
	// Touch "a" so "b" becomes the LRU, then insert "d" -> "b" evicted.
	if _, ok := c.get("a", now); !ok {
		t.Fatal("a should be present")
	}
	c.put("d", leafValidUntil(exp))
	if _, ok := c.get("b", now); ok {
		t.Error("b (least-recently-used) should have been evicted")
	}
	for _, h := range []string{"a", "c", "d"} {
		if _, ok := c.get(h, now); !ok {
			t.Errorf("%s should still be cached", h)
		}
	}
}

func TestLeafLRU_RenewsNearExpiry(t *testing.T) {
	c := newLeafLRU(4)
	now := time.Now()
	// A leaf expiring within leafRenewBefore is treated as a miss (so LeafFor
	// re-mints) and dropped from the cache.
	c.put("soon", leafValidUntil(now.Add(leafRenewBefore/2)))
	if _, ok := c.get("soon", now); ok {
		t.Error("a near-expiry leaf should be a cache miss")
	}
	if c.len() != 0 {
		t.Errorf("near-expiry entry should be evicted on access; len=%d", c.len())
	}
	// A comfortably-valid leaf is a hit.
	c.put("fresh", leafValidUntil(now.Add(24*time.Hour)))
	if _, ok := c.get("fresh", now); !ok {
		t.Error("a fresh leaf should be a cache hit")
	}
}

// TestAuthority_LeafCacheBounded is the end-to-end guard: minting leaves for far
// more hosts than the cap keeps the Authority's cache bounded (a compromised front
// can't grow keeper memory without limit via SignLeaf).
func TestAuthority_LeafCacheBounded(t *testing.T) {
	a, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for i := 0; i < maxLeafCache+50; i++ {
		if _, err := a.LeafFor("h" + strconv.Itoa(i) + ".example"); err != nil {
			t.Fatalf("LeafFor: %v", err)
		}
	}
	a.mu.Lock()
	n := a.leaves.len()
	a.mu.Unlock()
	if n > maxLeafCache {
		t.Errorf("Authority leaf cache unbounded: %d entries, cap %d", n, maxLeafCache)
	}
}
