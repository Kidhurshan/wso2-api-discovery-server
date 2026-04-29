package models

import (
	"time"

	"github.com/google/uuid"
)

// ClientObservation is one entry in the per-finding top_clients list,
// stored as JSONB on ads_discovered_apis. Recomputed each Phase 1 cycle;
// the merger sorts by Observations desc and caps at top 20.
type ClientObservation struct {
	Identity     string `json:"identity"`            // "k8s:<ns>/<workload>" | "host:<ip>"
	Kind         string `json:"kind"`                // "k8s" | "legacy"
	Namespace    string `json:"namespace,omitempty"` // K8s only
	Workload     string `json:"workload,omitempty"`  // K8s only
	IP           string `json:"ip"`                  // sample IP (always populated)
	Port         int    `json:"port,omitempty"`      // sample source port; rotates per connection
	Observations int64  `json:"observations"`
}

// DiscoveredAPI is one row in ads_discovered_apis. Maps the upsert in
// claude/specs/phase1_discovery.md §5.2.
type DiscoveredAPI struct {
	ID                    uuid.UUID
	ServiceID             uuid.UUID
	Method                string
	NormalizedPath        string
	RawPathSamples        []string
	FirstSeenAt           time.Time
	LastSeenAt            time.Time
	ObservationCount      int64
	FlowCount             int64
	DistinctClientCount   int
	DistinctClientsSample []string
	StatusCodes           []int16
	AvgDurationUs         float64
	RequestDomain         string
	InternalFlows         int64
	ExternalFlows         int64
	SamplePod             string
	SampleWorkload        string
	NormalizationVersion  string
	LastWindowID          uuid.UUID
	IsActive              bool
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// ManagedAPI is one row in ads_managed_apis. Maps schema in
// claude/specs/phase2_managed_sync.md §7.
type ManagedAPI struct {
	ID                  uuid.UUID
	APIMAPIID           string
	APIMAPIName         string
	APIMAPIVersion      string
	APIMAPIContext      string
	APIMAPIProvider     string
	APIMLifecycleStatus string

	EnvKind         string // EnvKindK8s | EnvKindLegacy | EnvKindUnknown
	ServiceIdentity string

	Method             string
	GatewayPath        string
	OperationTarget    string
	RawOperationTarget string
	RawPlaceholders    []string
	AuthType           string
	ThrottlingPolicy   string

	BackendURL          string
	BackendResolvedIP   string
	BackendResolvedPort int

	APIMUpdatedTime time.Time
	LastSyncedAt    time.Time
	IsActive        bool
	Warnings        []string

	CreatedAt time.Time
	UpdatedAt time.Time
}
