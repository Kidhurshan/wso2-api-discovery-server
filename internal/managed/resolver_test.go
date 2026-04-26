package managed

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/wso2/api-discovery-server/internal/apim"
	"github.com/wso2/api-discovery-server/internal/config"
)

// presetTopology builds a topology with one K8s node and one legacy chost,
// matching TechMart's layout.
func presetTopology(t *testing.T) *Topology {
	t.Helper()
	topo, err := NewTopology(&config.TopologyConfig{
		K8sNodes:     []config.K8sNode{{IP: "10.50.1.10", DefaultNamespace: "techmart"}},
		LegacyChosts: []string{"10.50.1.11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return topo
}

// presetDNS returns a DNS cache pre-seeded with hostname → IP mappings so
// tests don't depend on real DNS.
func presetDNS(t *testing.T, mappings map[string][]net.IP) *DNSCache {
	t.Helper()
	c := NewDNSCache(time.Hour)
	c.mu.Lock()
	for host, ips := range mappings {
		c.entries[host] = dnsEntry{ips: ips, expires: time.Now().Add(time.Hour)}
	}
	c.mu.Unlock()
	return c
}

func TestResolverK8s(t *testing.T) {
	r := NewResolver(presetTopology(t),
		presetDNS(t, map[string][]net.IP{
			"products.techmart.internal": {net.ParseIP("10.50.1.10")},
		}))
	api := &apim.APIDetail{
		APISummary: apim.APISummary{ID: "u1", Name: "ProductsAPI", Version: "1.0.0", Context: "/products/1.0.0"},
		EndpointConfig: apim.EndpointConfig{
			EndpointType:        "http",
			ProductionEndpoints: &apim.ProductionEndpoint{URL: "http://products.techmart.internal:8080"},
		},
	}
	res, err := r.Resolve(api)
	if err != nil {
		t.Fatal(err)
	}
	if res.EnvKind != "k8s" || res.ServiceIdentity != "k8s:techmart/products" {
		t.Errorf("got %+v", res)
	}
	if res.BackendIP != "10.50.1.10" || res.BackendPort != 8080 {
		t.Errorf("backend resolved wrong: %+v", res)
	}
}

func TestResolverLegacy(t *testing.T) {
	r := NewResolver(presetTopology(t),
		presetDNS(t, map[string][]net.IP{
			"payments.techmart.internal": {net.ParseIP("10.50.1.11")},
		}))
	api := &apim.APIDetail{
		APISummary: apim.APISummary{ID: "u2", Name: "PaymentsAPI", Version: "1.0.0", Context: "/payments/1.0.0"},
		EndpointConfig: apim.EndpointConfig{
			EndpointType:        "http",
			ProductionEndpoints: &apim.ProductionEndpoint{URL: "http://payments.techmart.internal:8083"},
		},
	}
	res, err := r.Resolve(api)
	if err != nil {
		t.Fatal(err)
	}
	if res.EnvKind != "legacy" || res.ServiceIdentity != "host:10.50.1.11:8083" {
		t.Errorf("got %+v", res)
	}
}

func TestResolverHTTPSDefaultPort(t *testing.T) {
	r := NewResolver(presetTopology(t),
		presetDNS(t, map[string][]net.IP{"x.example.com": {net.ParseIP("10.50.1.11")}}))
	api := &apim.APIDetail{
		APISummary: apim.APISummary{Context: "/foo"},
		EndpointConfig: apim.EndpointConfig{
			EndpointType:        "http",
			ProductionEndpoints: &apim.ProductionEndpoint{URL: "https://x.example.com"},
		},
	}
	res, err := r.Resolve(api)
	if err != nil {
		t.Fatal(err)
	}
	if res.BackendPort != 443 {
		t.Errorf("expected default 443, got %d", res.BackendPort)
	}
}

func TestResolverNamespaceOverride(t *testing.T) {
	topo, _ := NewTopology(&config.TopologyConfig{
		K8sNodes:           []config.K8sNode{{IP: "10.50.1.10", DefaultNamespace: "techmart"}},
		NamespaceOverrides: []config.NamespaceOverride{{APIID: "u-alt", Namespace: "techmart-alt"}},
	})
	r := NewResolver(topo, presetDNS(t, map[string][]net.IP{"alt": {net.ParseIP("10.50.1.10")}}))
	api := &apim.APIDetail{
		APISummary: apim.APISummary{ID: "u-alt", Context: "/alt"},
		EndpointConfig: apim.EndpointConfig{
			EndpointType:        "http",
			ProductionEndpoints: &apim.ProductionEndpoint{URL: "http://alt:8080"},
		},
	}
	res, _ := r.Resolve(api)
	if res.ServiceIdentity != "k8s:techmart-alt/alt" {
		t.Errorf("override not applied: %s", res.ServiceIdentity)
	}
}

func TestResolverDNSFailureNonFatal(t *testing.T) {
	r := NewResolver(presetTopology(t), NewDNSCache(time.Hour))
	api := &apim.APIDetail{
		APISummary: apim.APISummary{Context: "/x"},
		EndpointConfig: apim.EndpointConfig{
			EndpointType:        "http",
			ProductionEndpoints: &apim.ProductionEndpoint{URL: "http://does-not-resolve.invalid:8080"},
		},
	}
	res, err := r.Resolve(api)
	if err != nil {
		t.Fatalf("DNS failure should be non-fatal, got error: %v", err)
	}
	if res.EnvKind != "unknown" {
		t.Errorf("expected unknown env_kind on DNS fail, got %q", res.EnvKind)
	}
	if !strings.Contains(res.Warnings[0], "DNS resolution failed") {
		t.Errorf("expected DNS warning, got %v", res.Warnings)
	}
}

func TestResolverNonHTTPEndpointType(t *testing.T) {
	r := NewResolver(presetTopology(t), NewDNSCache(time.Hour))
	api := &apim.APIDetail{EndpointConfig: apim.EndpointConfig{EndpointType: "graphql"}}
	res, err := r.Resolve(api)
	if err != nil {
		t.Fatal(err)
	}
	if res.EnvKind != "unknown" {
		t.Errorf("expected unknown for graphql, got %q", res.EnvKind)
	}
}

func TestResolverMissingURL(t *testing.T) {
	r := NewResolver(presetTopology(t), NewDNSCache(time.Hour))
	api := &apim.APIDetail{EndpointConfig: apim.EndpointConfig{EndpointType: "http"}}
	if _, err := r.Resolve(api); err == nil {
		t.Error("expected error for nil ProductionEndpoints")
	}
}

func TestResolverIPNotInTopology(t *testing.T) {
	r := NewResolver(presetTopology(t),
		presetDNS(t, map[string][]net.IP{"unknown-host": {net.ParseIP("203.0.113.99")}}))
	api := &apim.APIDetail{
		APISummary: apim.APISummary{Context: "/foo"},
		EndpointConfig: apim.EndpointConfig{
			EndpointType:        "http",
			ProductionEndpoints: &apim.ProductionEndpoint{URL: "http://unknown-host:9000"},
		},
	}
	res, err := r.Resolve(api)
	if err != nil {
		t.Fatal(err)
	}
	if res.EnvKind != "unknown" || res.ServiceIdentity != "host:203.0.113.99:9000" {
		t.Errorf("got %+v", res)
	}
}
