package managed

import (
	"strings"
	"testing"

	"github.com/wso2/api-discovery-server/internal/config"
)

func TestNewTopologyHappyPath(t *testing.T) {
	cfg := &config.TopologyConfig{
		K8sNodes: []config.K8sNode{
			{IP: "10.50.1.10", DefaultNamespace: "techmart"},
		},
		LegacyChosts: []string{"10.50.1.11"},
		NamespaceOverrides: []config.NamespaceOverride{
			{APIID: "uuid-1", Namespace: "techmart-experimental"},
		},
	}
	topo, err := NewTopology(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if topo.K8sNodeIPs["10.50.1.10"].DefaultNamespace != "techmart" {
		t.Errorf("k8s node entry wrong: %+v", topo.K8sNodeIPs["10.50.1.10"])
	}
	if !topo.LegacyChostIPs["10.50.1.11"] {
		t.Error("legacy chost not indexed")
	}
	if topo.NamespaceOverrides["uuid-1"] != "techmart-experimental" {
		t.Error("namespace override not indexed")
	}
}

func TestNewTopologyDuplicateK8sIPRejected(t *testing.T) {
	cfg := &config.TopologyConfig{K8sNodes: []config.K8sNode{
		{IP: "10.0.0.1", DefaultNamespace: "a"},
		{IP: "10.0.0.1", DefaultNamespace: "b"},
	}}
	if _, err := NewTopology(cfg); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected duplicate IP error, got %v", err)
	}
}

func TestNewTopologyConflictBetweenLists(t *testing.T) {
	cfg := &config.TopologyConfig{
		K8sNodes:     []config.K8sNode{{IP: "10.0.0.1", DefaultNamespace: "a"}},
		LegacyChosts: []string{"10.0.0.1"},
	}
	if _, err := NewTopology(cfg); err == nil || !strings.Contains(err.Error(), "both") {
		t.Errorf("expected k8s/legacy conflict error, got %v", err)
	}
}
