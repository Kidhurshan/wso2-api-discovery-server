// Package bff is the daemon's REST surface — the Backend-for-Frontend that
// the carbon-apimgt admin v1 module proxies to in Phase 4.
//
// Endpoints, shapes, and auth are defined in claude/specs/phase4_admin_portal.md.
package bff

import (
	"container/list"
	"sync"
	"time"

	"github.com/wso2/api-discovery-server/internal/apim"
)

// tokenCache is a fixed-capacity LRU of (token → TokenInfo + cachedAt).
// Per spec phase4_admin_portal.md §7.2 the daemon caches positive
// introspection results for 30 seconds (configurable) so a busy admin
// portal doesn't hammer APIM's /oauth2/introspect endpoint.
//
// We only cache active tokens — invalid/inactive tokens always re-introspect
// so a permission revocation isn't masked by a stale cache hit. (The spec
// is silent on this; failing closed seems like the safer default.)
type tokenCache struct {
	ttl   time.Duration
	max   int
	mu    sync.Mutex
	items map[string]*list.Element
	lru   *list.List
}

type tokenEntry struct {
	token  string
	info   *apim.TokenInfo
	cached time.Time
}

// newTokenCache builds an empty cache. ttl is the per-entry validity
// window; max is the LRU capacity (entries beyond it get evicted).
func newTokenCache(ttl time.Duration, max int) *tokenCache {
	if max <= 0 {
		max = 1000
	}
	return &tokenCache{
		ttl:   ttl,
		max:   max,
		items: make(map[string]*list.Element, max),
		lru:   list.New(),
	}
}

// get returns the cached TokenInfo for token, or nil if absent / expired.
// On hit, the entry is moved to the front of the LRU.
func (c *tokenCache) get(token string) *apim.TokenInfo {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[token]
	if !ok {
		return nil
	}
	entry := elem.Value.(*tokenEntry)
	if time.Since(entry.cached) > c.ttl {
		c.lru.Remove(elem)
		delete(c.items, token)
		return nil
	}
	c.lru.MoveToFront(elem)
	return entry.info
}

// put stores info for token. Evicts the LRU entry if at capacity.
func (c *tokenCache) put(token string, info *apim.TokenInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[token]; ok {
		entry := elem.Value.(*tokenEntry)
		entry.info = info
		entry.cached = time.Now()
		c.lru.MoveToFront(elem)
		return
	}

	if c.lru.Len() >= c.max {
		oldest := c.lru.Back()
		if oldest != nil {
			c.lru.Remove(oldest)
			delete(c.items, oldest.Value.(*tokenEntry).token)
		}
	}

	entry := &tokenEntry{token: token, info: info, cached: time.Now()}
	elem := c.lru.PushFront(entry)
	c.items[token] = elem
}

// size is exported via accessor for tests; not meant for production callers.
func (c *tokenCache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}
