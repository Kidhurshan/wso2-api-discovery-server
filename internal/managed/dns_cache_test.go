package managed

import (
	"net"
	"testing"
	"time"
)

func TestDNSCacheCachesWithinTTL(t *testing.T) {
	c := NewDNSCache(50 * time.Millisecond)
	host := "localhost"

	first, err := c.Lookup(host)
	if err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("expected at least one IP for localhost")
	}

	// Mutate the cache entry to a sentinel; if the cache is honored, we
	// should see the sentinel. If it does a fresh lookup, we get real IPs.
	sentinel := []net.IP{net.ParseIP("203.0.113.1")}
	c.mu.Lock()
	c.entries[host] = dnsEntry{ips: sentinel, expires: time.Now().Add(time.Hour)}
	c.mu.Unlock()

	got, err := c.Lookup(host)
	if err != nil {
		t.Fatalf("second lookup: %v", err)
	}
	if len(got) != 1 || !got[0].Equal(sentinel[0]) {
		t.Errorf("cache miss within TTL: got %v want %v", got, sentinel)
	}
}

func TestDNSCacheRefreshesAfterTTL(t *testing.T) {
	c := NewDNSCache(20 * time.Millisecond)
	host := "localhost"

	if _, err := c.Lookup(host); err != nil {
		t.Fatal(err)
	}
	// Plant a sentinel that's already expired.
	c.mu.Lock()
	c.entries[host] = dnsEntry{ips: []net.IP{net.ParseIP("203.0.113.2")}, expires: time.Now().Add(-time.Second)}
	c.mu.Unlock()

	got, err := c.Lookup(host)
	if err != nil {
		t.Fatal(err)
	}
	for _, ip := range got {
		if ip.Equal(net.ParseIP("203.0.113.2")) {
			t.Error("returned expired sentinel; expected fresh lookup")
		}
	}
}

func TestDNSCacheZeroTTLDisables(t *testing.T) {
	c := NewDNSCache(0)
	if _, err := c.Lookup("localhost"); err != nil {
		t.Fatal(err)
	}
	if len(c.entries) != 0 {
		t.Errorf("zero TTL should not cache: %d entries", len(c.entries))
	}
}
