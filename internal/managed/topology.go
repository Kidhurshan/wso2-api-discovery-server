package managed

import (
	"fmt"

	"github.com/wso2/api-discovery-server/internal/config"
)

// K8sNodeInfo is the per-node metadata the resolver looks up by IP.
type K8sNodeInfo struct {
	IP               string
	DefaultNamespace string
}

// Topology is the resolver's lookup table: which IPs are K8s nodes (and
// their default namespace) vs legacy chosts. Built once at startup from
// config.DeploymentConfig.
type Topology struct {
	K8sNodeIPs         map[string]K8sNodeInfo // ip → node info
	LegacyChostIPs     map[string]bool        // ip → presence
	NamespaceOverrides map[string]string      // apim_api_id → namespace
}

// NewTopology validates and indexes cfg into a Topology. Errors on
// duplicate IPs across K8s and legacy lists — that would be ambiguous.
func NewTopology(cfg *config.TopologyConfig) (*Topology, error) {
	t := &Topology{
		K8sNodeIPs:         make(map[string]K8sNodeInfo, len(cfg.K8sNodes)),
		LegacyChostIPs:     make(map[string]bool, len(cfg.LegacyChosts)),
		NamespaceOverrides: make(map[string]string, len(cfg.NamespaceOverrides)),
	}

	for _, n := range cfg.K8sNodes {
		if _, dup := t.K8sNodeIPs[n.IP]; dup {
			return nil, fmt.Errorf("topology: k8s_nodes contains duplicate IP %q", n.IP)
		}
		t.K8sNodeIPs[n.IP] = K8sNodeInfo{IP: n.IP, DefaultNamespace: n.DefaultNamespace}
	}

	for _, ip := range cfg.LegacyChosts {
		if _, conflict := t.K8sNodeIPs[ip]; conflict {
			return nil, fmt.Errorf("topology: %q listed as both k8s_node and legacy_chost", ip)
		}
		t.LegacyChostIPs[ip] = true
	}

	for _, o := range cfg.NamespaceOverrides {
		t.NamespaceOverrides[o.APIID] = o.Namespace
	}

	return t, nil
}
