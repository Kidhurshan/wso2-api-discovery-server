package managed

import (
	"regexp"
	"testing"

	"github.com/wso2/api-discovery-server/internal/apim"
	"github.com/wso2/api-discovery-server/internal/config"
	"github.com/wso2/api-discovery-server/internal/discovery"
)

// shareNormalizer builds the same normalizer Phase 1 uses, with the
// production rule set.
func shareNormalizer(t *testing.T) *discovery.Normalizer {
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
	return &discovery.Normalizer{Version: "v1", Rules: rules}
}

func TestExpandTwoPassNormalization(t *testing.T) {
	exp := NewExpander(shareNormalizer(t))

	api := &apim.APIDetail{
		APISummary: apim.APISummary{
			ID: "u1", Name: "OrdersAPI", Version: "1.0.0", Context: "/orders/1.0.0",
			LifeCycleStatus: "PUBLISHED",
		},
		Operations: []apim.Operation{
			{Verb: "POST", Target: "/orders"},
			{Verb: "GET", Target: "/orders/{orderId}"},
			// APIM placeholder + path-style customer ID — Pass A collapses
			// both to {id}.
			{Verb: "GET", Target: "/customers/{customerId}/orders/{orderId}"},
		},
	}
	res := &ResolverResult{EnvKind: "k8s", ServiceIdentity: "k8s:techmart/orders"}

	out := exp.Expand(api, res)
	if len(out) != 3 {
		t.Fatalf("got %d ops, want 3", len(out))
	}

	wantPaths := map[string]string{
		"POST": "/orders/1.0.0/orders",
		"GET":  "", // first GET; we'll check below explicitly
	}
	_ = wantPaths

	if out[0].GatewayPath != "/orders/1.0.0/orders" {
		t.Errorf("op0 gateway_path = %q", out[0].GatewayPath)
	}
	if out[1].GatewayPath != "/orders/1.0.0/orders/{id}" {
		t.Errorf("op1 gateway_path = %q (want /orders/1.0.0/orders/{id})", out[1].GatewayPath)
	}
	if out[2].GatewayPath != "/orders/1.0.0/customers/{id}/orders/{id}" {
		t.Errorf("op2 gateway_path = %q", out[2].GatewayPath)
	}

	// Raw placeholders preserved per spec — Phase 4 detail view shows them.
	if len(out[2].RawPlaceholders) != 2 {
		t.Errorf("op2 raw_placeholders = %v (want 2 entries)", out[2].RawPlaceholders)
	}

	// Service identity & method propagated from resolver result.
	for i, op := range out {
		if op.ServiceIdentity != "k8s:techmart/orders" {
			t.Errorf("op%d service_identity = %q", i, op.ServiceIdentity)
		}
	}
}

func TestExpandUppercasesMethod(t *testing.T) {
	exp := NewExpander(shareNormalizer(t))
	api := &apim.APIDetail{
		APISummary: apim.APISummary{Context: "/x", Version: "1"},
		Operations: []apim.Operation{{Verb: "get", Target: "/y"}},
	}
	out := exp.Expand(api, &ResolverResult{})
	if out[0].Method != "GET" {
		t.Errorf("verb = %q, want GET", out[0].Method)
	}
}
