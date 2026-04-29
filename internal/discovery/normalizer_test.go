package discovery

import (
	"regexp"
	"testing"
)

// buildNormalizer constructs a Normalizer with the segment-based default
// pattern set lifted from config/config.toml.example.
func buildNormalizer(t *testing.T) *Normalizer {
	t.Helper()
	mustCompile := func(patterns ...string) []*regexp.Regexp {
		out := make([]*regexp.Regexp, len(patterns))
		for i, p := range patterns {
			out[i] = regexp.MustCompile(p)
		}
		return out
	}
	return &Normalizer{
		Version: "v2",
		builtin: mustCompile(
			`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`, // UUID
			`^[0-9a-fA-F]{24,}$`,           // MongoDB ObjectID / hex hash
			`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`, // ISO date
			`^[0-9]+$`,                     // numeric ID
			`^[A-Za-z0-9_-]{20,}={0,2}$`,   // base64-ish opaque
			`^[A-Z]{2,5}-[A-Z0-9-]{3,}$`,   // SKU pattern
		),
		exclude: mustCompile(
			`^v?[0-9]+\.[0-9]+(\.[0-9]+)?$`, // versions like 1.0.0, v2.1
			`^v[0-9]+$`,                     // short versions like v1, v2
			`^api$`,                         // literal "api"
		),
	}
}

func TestNormalize(t *testing.T) {
	n := buildNormalizer(t)
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain path passes through", "/products", "/products"},
		{"strips query string", "/products?sku=X", "/products"},
		{"strips trailing slash", "/products/", "/products"},
		{"keeps root slash", "/", "/"},
		{"numeric id mid path", "/users/123/posts", "/users/{id}/posts"},
		{"numeric id at end", "/users/123", "/users/{id}"},
		{"uuid mid path", "/orders/9ba518b8-e2ab-4a06-9eb6-ad60e2da5433/items", "/orders/{id}/items"},
		{"sku at end", "/items/SKU-IPHONE-15", "/items/{id}"},
		{"iso date at end", "/reports/2026-04-26", "/reports/{id}"},
		{"mongo objectid at end", "/blobs/507f1f77bcf86cd799439011", "/blobs/{id}"},
		{"multi-segment normalization", "/customers/SKU-ABC-X/orders/9ba518b8-e2ab-4a06-9eb6-ad60e2da5433", "/customers/{id}/orders/{id}"},
		{"version segment preserved", "/orders/1.0.0/items", "/orders/1.0.0/items"},
		{"short version preserved", "/api/v1/users", "/api/v1/users"},
		{"version preserved at end", "/products/v2", "/products/v2"},
		{"empty input", "", ""},
		{"already normalized stays put", "/users/{id}", "/users/{id}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := n.Normalize(tc.in)
			if got != tc.want {
				t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
