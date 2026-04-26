package managed

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/wso2/api-discovery-server/internal/apim"
)

// ResolverResult is the outcome of mapping one APIM API's backend URL onto
// the Phase 1 service-identity space.
type ResolverResult struct {
	EnvKind         string // "k8s" | "legacy" | "unknown"
	ServiceIdentity string
	BackendIP       string
	BackendPort     int
	Warnings        []string
}

// Resolver converts an APIM API detail into a service identity using the
// configured deployment topology and DNS lookups.
//
// Construct via NewResolver and reuse. Safe for concurrent use because the
// only mutable state is inside DNSCache (its own RWMutex).
type Resolver struct {
	topology *Topology
	dns      *DNSCache
}

// NewResolver wires topology + DNS cache.
func NewResolver(topo *Topology, dns *DNSCache) *Resolver {
	return &Resolver{topology: topo, dns: dns}
}

// Resolve produces a ResolverResult per claude/specs/phase2_managed_sync.md
// §5.2. Does NOT error on degraded outcomes (DNS fail, IP not in topology,
// non-http endpoint type) — those return env_kind="unknown" with a Warnings
// entry so the caller can record without losing the row entirely.
//
// True errors (nil endpoint, malformed URL) return error so the caller can
// drop the API from this sync cycle.
func (r *Resolver) Resolve(api *apim.APIDetail) (*ResolverResult, error) {
	ec := api.EndpointConfig
	if ec.EndpointType != "" && ec.EndpointType != "http" {
		return &ResolverResult{
			EnvKind:  "unknown",
			Warnings: []string{fmt.Sprintf("unsupported endpoint_type=%q", ec.EndpointType)},
		}, nil
	}

	if ec.ProductionEndpoints == nil || ec.ProductionEndpoints.URL == "" {
		return nil, fmt.Errorf("api %s (%s): no production endpoint URL", api.ID, api.Name)
	}

	parsed, err := url.Parse(ec.ProductionEndpoints.URL)
	if err != nil {
		return nil, fmt.Errorf("parse backend URL %q: %w", ec.ProductionEndpoints.URL, err)
	}
	host := parsed.Hostname()
	if host == "" {
		return nil, fmt.Errorf("backend URL %q has no host", ec.ProductionEndpoints.URL)
	}

	port, _ := strconv.Atoi(parsed.Port())
	if port == 0 {
		switch parsed.Scheme {
		case "https":
			port = 443
		default:
			port = 80
		}
	}

	ips, err := r.dns.Lookup(host)
	if err != nil {
		// DNS failure is non-fatal — fall back to the host literal so
		// Phase 3 can still attempt path-only matching.
		return &ResolverResult{
			EnvKind:         "unknown",
			ServiceIdentity: fmt.Sprintf("host:%s:%d", host, port),
			BackendPort:     port,
			Warnings:        []string{fmt.Sprintf("DNS resolution failed for %s: %v", host, err)},
		}, nil
	}
	primary := firstIPv4String(ips)
	if primary == "" {
		return &ResolverResult{
			EnvKind:         "unknown",
			ServiceIdentity: fmt.Sprintf("host:%s:%d", host, port),
			BackendPort:     port,
			Warnings:        []string{fmt.Sprintf("no IPv4 address for %s", host)},
		}, nil
	}

	// K8s match: backend IP equals one of the K8s node IPs in topology.
	if k8sInfo, ok := r.topology.K8sNodeIPs[primary]; ok {
		ns := k8sInfo.DefaultNamespace
		if override, ok := r.topology.NamespaceOverrides[api.ID]; ok && override != "" {
			ns = override
		}
		// Per spec §5.2: derive the service name from api.Context (strip
		// leading slash). For the spec's TechMart layout this gives
		// products → "products", orders → "orders", etc.
		svcName := strings.TrimPrefix(api.Context, "/")
		// Some APIM contexts include a version suffix like /products/1.0.0.
		// Take only the first path segment.
		if i := strings.IndexByte(svcName, '/'); i >= 0 {
			svcName = svcName[:i]
		}
		return &ResolverResult{
			EnvKind:         "k8s",
			ServiceIdentity: fmt.Sprintf("k8s:%s/%s", ns, svcName),
			BackendIP:       primary,
			BackendPort:     port,
		}, nil
	}

	// Legacy match: backend IP is a known chost.
	if r.topology.LegacyChostIPs[primary] {
		return &ResolverResult{
			EnvKind:         "legacy",
			ServiceIdentity: fmt.Sprintf("host:%s:%d", primary, port),
			BackendIP:       primary,
			BackendPort:     port,
		}, nil
	}

	// IP not in topology — record as unknown but keep the host:port form
	// so a Phase 1 row that happens to match the same identity (also
	// host:ip:port) can still be paired by Phase 3.
	return &ResolverResult{
		EnvKind:         "unknown",
		ServiceIdentity: fmt.Sprintf("host:%s:%d", primary, port),
		BackendIP:       primary,
		BackendPort:     port,
		Warnings:        []string{fmt.Sprintf("IP %s not in known topology", primary)},
	}, nil
}

// firstIPv4String returns the dotted form of the first IPv4 address in ips,
// or "" if ips is empty/IPv6-only.
func firstIPv4String(ips []net.IP) string {
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			return v4.String()
		}
	}
	return ""
}
