package models

import "time"

// PipelineState is the single-row tracker in ads_pipeline_state. Phases write
// last-success timestamps here; comparison's freshness guard reads them.
type PipelineState struct {
	ID                    string
	Phase1LastSuccess     time.Time
	Phase1LastWindowStart time.Time
	Phase1LastWindowEnd   time.Time
	Phase2LastSuccess     time.Time
	Phase3LastSuccess     time.Time
	Phase3LastViewRefresh time.Time
	LastRetentionRun      time.Time
	DiscoveryBreakerState string
	ManagedBreakerState   string
}
