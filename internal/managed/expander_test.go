package managed

import (
	"regexp"
	"testing"

	"github.com/wso2/api-discovery-server/internal/apim"
	"github.com/wso2/api-discovery-server/internal/discovery"
)

// shareNormalizer builds the same normalizer Phase 1 uses, with the
// segment-based default pattern set.
func shareNormalizer(t *testing.T) *discovery.Normalizer {
	t.Helper()
	mustCompile := func(patterns ...string) []*regexp.Regexp {
		out := make([]*regexp.Regexp, len(patterns))
		for i, p := range patterns {
			out[i] = regexp.MustCompile(p)
		}
		return out
	}
	return discovery.NewNormalizerFromRegexes(
		mustCompile(
			`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
			`^[0-9a-fA-F]{24,}$`,
			`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`,
			`^[0-9]+$`,
			`^[A-Za-z0-9_-]{20,}={0,2}$`,
			`^[A-Z]{2,5}-[A-Z0-9-]{3,}$`,
		),
		nil, // user_patterns
		mustCompile(
			`^v?[0-9]+\.[0-9]+(\.[0-9]+)?$`,
			`^v[0-9]+$`,
			`^api$`,
		),
	)
}

func TestExpandComposesGatewayAndBackendPaths(t *testing.T) {
	exp := NewExpander(shareNormalizer(t))

	api := &apim.APIDetail{
		APISummary: apim.APISummary{
			ID: "u1", Name: "ProductsAPI", Version: "1.0.0", Context: "/prod/1.0.0",
			LifeCycleStatus: "PUBLISHED",
		},
		EndpointConfig: apim.EndpointConfig{
			ProductionEndpoints: &apim.ProductionEndpoint{URL: "http://products-service:8080/products/v1"},
			EndpointType:        "http",
		},
		Operations: []apim.Operation{
			{Verb: "POST", Target: "/items"},
			{Verb: "GET", Target: "/items/{itemId}"},
			{Verb: "GET", Target: "/customers/{customerId}/items/{itemId}"},
		},
	}

	out := exp.Expand(api)
	if len(out) != 3 {
		t.Fatalf("got %d ops, want 3", len(out))
	}

	// gateway_path = context + version + target (with version-dedup)
	if got, want := out[0].GatewayPath, "/prod/1.0.0/items"; got != want {
		t.Errorf("op0 gateway_path = %q, want %q", got, want)
	}
	if got, want := out[1].GatewayPath, "/prod/1.0.0/items/{id}"; got != want {
		t.Errorf("op1 gateway_path = %q, want %q", got, want)
	}
	if got, want := out[2].GatewayPath, "/prod/1.0.0/customers/{id}/items/{id}"; got != want {
		t.Errorf("op2 gateway_path = %q, want %q", got, want)
	}

	// backend_path = backend URL.path + target
	if got, want := out[0].BackendPath, "/products/v1/items"; got != want {
		t.Errorf("op0 backend_path = %q, want %q", got, want)
	}
	if got, want := out[1].BackendPath, "/products/v1/items/{id}"; got != want {
		t.Errorf("op1 backend_path = %q, want %q", got, want)
	}
}

func TestExpandWithNoBackendBasePath(t *testing.T) {
	exp := NewExpander(shareNormalizer(t))
	api := &apim.APIDetail{
		APISummary: apim.APISummary{Context: "/orders", Version: "1.0.0"},
		EndpointConfig: apim.EndpointConfig{
			ProductionEndpoints: &apim.ProductionEndpoint{URL: "http://orders:8080"},
			EndpointType:        "http",
		},
		Operations: []apim.Operation{{Verb: "GET", Target: "/items/{id}"}},
	}
	out := exp.Expand(api)
	if got, want := out[0].BackendPath, "/items/{id}"; got != want {
		t.Errorf("backend_path = %q, want %q (no base path → just target)", got, want)
	}
}

func TestExpandWithTrailingSlashInBackendURL(t *testing.T) {
	exp := NewExpander(shareNormalizer(t))
	api := &apim.APIDetail{
		APISummary: apim.APISummary{Context: "/x", Version: "1.0.0"},
		EndpointConfig: apim.EndpointConfig{
			ProductionEndpoints: &apim.ProductionEndpoint{URL: "https://api.acme.com/v2/"},
			EndpointType:        "http",
		},
		Operations: []apim.Operation{{Verb: "GET", Target: "/users"}},
	}
	out := exp.Expand(api)
	if got, want := out[0].BackendPath, "/v2/users"; got != want {
		t.Errorf("backend_path = %q, want %q (trailing slash trimmed)", got, want)
	}
}

func TestExpandUppercasesMethod(t *testing.T) {
	exp := NewExpander(shareNormalizer(t))
	api := &apim.APIDetail{
		APISummary: apim.APISummary{Context: "/x", Version: "1"},
		Operations: []apim.Operation{{Verb: "get", Target: "/y"}},
	}
	out := exp.Expand(api)
	if out[0].Method != "GET" {
		t.Errorf("verb = %q, want GET", out[0].Method)
	}
}
