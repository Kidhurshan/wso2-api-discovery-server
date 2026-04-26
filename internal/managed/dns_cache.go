// Package managed implements Phase 2: pull APIM publisher state, resolve
// each API's backend URL into the same service_identity Phase 1 produces,
// and write to ads_managed_apis.
package managed

import (
	"net"
	"sync"
	"time"
)

// DNSCache wraps net.LookupIP with a short TTL so the resolver doesn't
// re-query DNS for every operation in the same APIM sync cycle.
//
// Per spec phase2_managed_sync.md §5.3: 5-min TTL by default — backends
// rarely change IPs and the 5-min window matches Phase 1's discovery
// cadence. net.LookupIP automatically consults /etc/hosts on Linux so
// TechMart's *.techmart.internal entries resolve without extra plumbing.
type DNSCache struct {
	ttl     time.Duration
	mu      sync.RWMutex
	entries map[string]dnsEntry
}

type dnsEntry struct {
	ips     []net.IP
	expires time.Time
}

// NewDNSCache returns an empty cache with the given TTL. A non-positive ttl
// disables caching entirely (every Lookup hits net.LookupIP directly).
func NewDNSCache(ttl time.Duration) *DNSCache {
	return &DNSCache{
		ttl:     ttl,
		entries: make(map[string]dnsEntry),
	}
}

// Lookup returns the cached or freshly resolved IPs for host.
func (d *DNSCache) Lookup(host string) ([]net.IP, error) {
	if d.ttl > 0 {
		d.mu.RLock()
		if e, ok := d.entries[host]; ok && time.Now().Before(e.expires) {
			d.mu.RUnlock()
			return e.ips, nil
		}
		d.mu.RUnlock()
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, err
	}

	if d.ttl > 0 {
		d.mu.Lock()
		d.entries[host] = dnsEntry{ips: ips, expires: time.Now().Add(d.ttl)}
		d.mu.Unlock()
	}
	return ips, nil
}
