package discovery

import (
	"regexp"
	"testing"

	"github.com/wso2/api-discovery-server/internal/config"
)

// buildNormalizer constructs a Normalizer with the spec's default rule set
// (RE2-adapted via \b boundaries). Lifts the patterns from
// config/config.toml.example so the test is the source of truth alongside
// the example config.
func buildNormalizer(t *testing.T) *Normalizer {
	t.Helper()
	rules := []config.NormalizationRule{
		{Name: "uuid_v4", Pattern: `/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}(/|$)`, Placeholder: "/{id}$1"},
		{Name: "iso_date", Pattern: `/\d{4}-\d{2}-\d{2}(/|$)`, Placeholder: "/{id}$1"},
		{Name: "mongo_objectid", Pattern: `/[0-9a-f]{24}(/|$)`, Placeholder: "/{id}$1"},
		{Name: "customer_id", Pattern: `/CUST-[A-Z0-9]+(/|$)`, Placeholder: "/{id}$1"},
		{Name: "sku_pattern", Pattern: `/[A-Z]{3,}(?:-[A-Z0-9]+)+(/|$)`, Placeholder: "/{id}$1"},
		{Name: "numeric_id", Pattern: `/[0-9]+(/|$)`, Placeholder: "/{id}$1"},
	}
	for i := range rules {
		rules[i].Compiled = regexp.MustCompile(rules[i].Pattern)
	}
	return &Normalizer{Version: "v1", Rules: rules}
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
		{"customer id at end", "/customers/CUST-ABC123", "/customers/{id}"},
		{"iso date at end", "/reports/2026-04-26", "/reports/{id}"},
		{"mongo objectid at end", "/blobs/507f1f77bcf86cd799439011", "/blobs/{id}"},
		{"multi-segment normalization", "/customers/CUST-ABC/orders/9ba518b8-e2ab-4a06-9eb6-ad60e2da5433", "/customers/{id}/orders/{id}"},
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
