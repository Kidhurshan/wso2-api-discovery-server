package bff

import (
	"testing"
	"time"

	"github.com/wso2/api-discovery-server/internal/apim"
)

func TestTokenCacheGetMissReturnsNil(t *testing.T) {
	c := newTokenCache(time.Minute, 10)
	if got := c.get("nope"); got != nil {
		t.Errorf("expected nil on miss, got %+v", got)
	}
}

func TestTokenCachePutGet(t *testing.T) {
	c := newTokenCache(time.Minute, 10)
	info := &apim.TokenInfo{Active: true, Scope: "apim:admin", Username: "alice"}
	c.put("tok1", info)

	got := c.get("tok1")
	if got == nil || got.Username != "alice" {
		t.Errorf("expected info back, got %+v", got)
	}
}

func TestTokenCacheExpiry(t *testing.T) {
	c := newTokenCache(20*time.Millisecond, 10)
	c.put("tok", &apim.TokenInfo{Active: true})

	time.Sleep(40 * time.Millisecond)

	if got := c.get("tok"); got != nil {
		t.Errorf("expected expired entry to miss, got %+v", got)
	}
	if c.size() != 0 {
		t.Errorf("expired entry should be evicted on miss, size=%d", c.size())
	}
}

func TestTokenCacheLRUEviction(t *testing.T) {
	c := newTokenCache(time.Minute, 2)

	c.put("a", &apim.TokenInfo{Active: true, Username: "a"})
	c.put("b", &apim.TokenInfo{Active: true, Username: "b"})
	// Touch a so b becomes the LRU.
	_ = c.get("a")
	c.put("c", &apim.TokenInfo{Active: true, Username: "c"})

	if c.get("a") == nil {
		t.Error("a should still be present (most recently used)")
	}
	if c.get("b") != nil {
		t.Error("b should have been evicted as LRU")
	}
	if c.get("c") == nil {
		t.Error("c should be present (most recent put)")
	}
	if c.size() != 2 {
		t.Errorf("cache should hold cap=2, got %d", c.size())
	}
}

func TestHasAnyScope(t *testing.T) {
	cases := []struct {
		name string
		have string
		want []string
		ok   bool
	}{
		{"single match", "apim:admin foo", []string{"apim:admin"}, true},
		{"multiple required, one matches", "apim:admin", []string{"apim:other", "apim:admin"}, true},
		{"none match", "foo bar", []string{"apim:admin"}, false},
		{"empty granted", "", []string{"apim:admin"}, false},
		{"empty required (defensive)", "apim:admin", []string{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := &apim.TokenInfo{Scope: tc.have}
			if got := hasAnyScope(info, tc.want); got != tc.ok {
				t.Errorf("hasAnyScope(have=%q want=%v) = %v, want %v",
					tc.have, tc.want, got, tc.ok)
			}
		})
	}
}
