package discovery

import (
	"fmt"
	"strings"

	"github.com/wso2/api-discovery-server/internal/deepflow"
	"github.com/wso2/api-discovery-server/internal/models"
)

// DeepFlow auto_instance_type enum values, per spec phase1_discovery.md §3.3.
const (
	instanceTypeChost          = 1   // legacy VM (cloud-host)
	instanceTypeK8sPod         = 10  // K8s pod
	instanceTypeK8sNodeOrNIC   = 14  // K8s node-or-NIC (defensive include)
	instanceTypeInternetUnknwn = 255 // public internet / unknown peer
)

// Observation point taps that DeepFlow exposes; used as priority signals
// when deciding which row's K8s identity to trust.
const (
	tapServerSide        = "s"
	tapServerSideProcess = "s-p"
	tapClientSide        = "c"
	tapClientSideProcess = "c-p"
)

// rawSignal collects the columns the per-flow query returns for one row in
// the DeepFlow result set. We pull them out of the loose Row map once at
// ingestion so downstream code never reaches into the map again.
type rawSignal struct {
	Method           string
	Endpoint         string
	ObservationPoint string
	ServerPort       int
	AgentID          int
	RowCount         int64
	SampleURL        string
	RequestDomain    string
	SampleStatus     int
	K8sService       string
	K8sNamespace     string
	K8sWorkload      string
	K8sPod           string
	ServerIP         string
	InstanceTypeS    int
	InstanceTypeC    int
	ClientIP         string
	FirstSeenUnix    int64
	LastSeenUnix     int64
	AvgDurationUs    float64
}

// fromRow extracts a rawSignal from one DeepFlow row.
func fromRow(r deepflow.Row) rawSignal {
	return rawSignal{
		Method:           r.String("request_type"),
		Endpoint:         r.String("endpoint"),
		ObservationPoint: r.String("observation_point"),
		ServerPort:       r.Int("server_port"),
		AgentID:          r.Int("agent_id"),
		RowCount:         r.Int64("row_count"),
		SampleURL:        r.String("sample_url"),
		RequestDomain:    r.String("request_domain"),
		SampleStatus:     r.Int("sample_status"),
		K8sService:       r.String("k8s_service"),
		K8sNamespace:     r.String("k8s_namespace"),
		K8sWorkload:      r.String("k8s_workload"),
		K8sPod:           r.String("k8s_pod"),
		ServerIP:         r.String("server_ip"),
		InstanceTypeS:    r.Int("instance_type_server"),
		InstanceTypeC:    r.Int("instance_type_client"),
		ClientIP:         r.String("client_ip"),
		FirstSeenUnix:    r.Int64("first_seen_unix"),
		LastSeenUnix:     r.Int64("last_seen_unix"),
		AvgDurationUs:    r.Float64("avg_duration_us"),
	}
}

// classified holds the spec-§3.2 derivations for one rawSignal. classify()
// fills these from the auto-tag columns according to the truth table in
// spec §3.3.
type classified struct {
	rawSignal
	EnvKind          string // "k8s" | "legacy" | "skip"
	ServiceIdentity  string // "k8s:<ns>/<svc>" | "host:<ip>:<port>" | ""
	TrafficDirection string // "internal" | "external"
}

// classify applies the spec's truth tables to a single raw signal.
//
// EnvKind branches (matching spec §3 multiIf):
//
//	K8s    if instance_type_server in {pod, node-or-NIC} AND k8s_service != "" AND k8s_namespace != ""
//	Legacy if instance_type_server == chost AND server_ip != "" AND server_port > 0
//	Skip   otherwise
//
// Direction branches (spec §3.3):
//
//	If client tap exists (c or c-p):
//	    client tracked (1, 10, 14)  → internal
//	    client untracked            → external
//	Else (only s/s-p tap):
//	    instance_type_client == 255 → external
//	    else                        → internal
//
// The current per-flow query groups by observation_point, so each rawSignal
// represents ONE tap side. The pipeline merger (merger.go) consolidates
// multiple tap-side rows for the same (method, endpoint).
func classify(r rawSignal) classified {
	c := classified{rawSignal: r}

	switch {
	case (r.InstanceTypeS == instanceTypeK8sPod || r.InstanceTypeS == instanceTypeK8sNodeOrNIC) &&
		r.K8sService != "" && r.K8sNamespace != "":
		c.EnvKind = "k8s"
		c.ServiceIdentity = "k8s:" + r.K8sNamespace + "/" + r.K8sService
	case r.InstanceTypeS == instanceTypeChost && r.ServerIP != "" && r.ServerPort > 0:
		c.EnvKind = "legacy"
		c.ServiceIdentity = fmt.Sprintf("host:%s:%d", r.ServerIP, r.ServerPort)
	default:
		c.EnvKind = "skip"
	}

	c.TrafficDirection = directionFor(r)
	return c
}

// directionFor implements the spec §3.3 direction precedence.
func directionFor(r rawSignal) string {
	hasClientTap := r.ObservationPoint == tapClientSide || r.ObservationPoint == tapClientSideProcess
	if hasClientTap {
		switch r.InstanceTypeC {
		case instanceTypeChost, instanceTypeK8sPod, instanceTypeK8sNodeOrNIC:
			return "internal"
		default:
			return "external"
		}
	}
	if r.InstanceTypeC == instanceTypeInternetUnknwn {
		return "external"
	}
	return "internal"
}

// classifyAndDrop runs classify() over signals and drops any whose env_kind
// became "skip". Returns only meaningfully classified rows.
func classifyAndDrop(signals []rawSignal) []classified {
	out := make([]classified, 0, len(signals))
	for _, s := range signals {
		c := classify(s)
		if c.EnvKind == "skip" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// envKindForService is a tiny helper for the merger so we can produce the
// service-row enum without repeating the switch.
func envKindForService(c classified) string {
	if strings.HasPrefix(c.ServiceIdentity, "k8s:") {
		return models.EnvKindK8s
	}
	return models.EnvKindLegacy
}
