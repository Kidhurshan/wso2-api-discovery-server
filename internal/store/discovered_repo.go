package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wso2/api-discovery-server/internal/models"
)

// DiscoveredRepo handles ads_discovered_apis CRUD.
type DiscoveredRepo struct {
	pool *pgxpool.Pool
}

// NewDiscoveredRepo returns a DiscoveredRepo bound to pool.
func NewDiscoveredRepo(pool *pgxpool.Pool) *DiscoveredRepo {
	return &DiscoveredRepo{pool: pool}
}

// DiscoveredUpsert is the input shape for BatchUpsert. It matches the
// columns the upsert SQL writes; the merger produces these one-for-one.
type DiscoveredUpsert struct {
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
	TopClients            []models.ClientObservation
}

// upsertSQL is the spec's history-preserving upsert (LEAST/GREATEST/SUM)
// from phase1_discovery.md §5.2, adapted to a single-row form so we can
// batch via pgx.Batch — each cycle's merged rows go in one round-trip
// without juggling array-of-array parameters.
const upsertSQL = `
INSERT INTO ads_discovered_apis (
    service_id, method, normalized_path, raw_path_samples,
    first_seen_at, last_seen_at,
    observation_count, flow_count, distinct_client_count,
    distinct_clients_sample, status_codes, avg_duration_us,
    request_domain, internal_flows, external_flows,
    sample_pod, sample_workload, normalization_version, last_window_id,
    top_clients
) VALUES (
    $1, $2, $3, $4,
    $5, $6,
    $7, $8, $9,
    $10, $11, $12,
    $13, $14, $15,
    $16, $17, $18, $19,
    $20::jsonb
)
ON CONFLICT (service_id, method, normalized_path) DO UPDATE SET
    last_seen_at        = GREATEST(ads_discovered_apis.last_seen_at, EXCLUDED.last_seen_at),
    first_seen_at       = LEAST(ads_discovered_apis.first_seen_at, EXCLUDED.first_seen_at),
    observation_count   = ads_discovered_apis.observation_count + EXCLUDED.observation_count,
    flow_count          = ads_discovered_apis.flow_count + EXCLUDED.flow_count,
    internal_flows      = ads_discovered_apis.internal_flows + EXCLUDED.internal_flows,
    external_flows      = ads_discovered_apis.external_flows + EXCLUDED.external_flows,
    distinct_client_count = GREATEST(ads_discovered_apis.distinct_client_count, EXCLUDED.distinct_client_count),
    raw_path_samples    = ads_capped_array_union(ads_discovered_apis.raw_path_samples,
                                                  EXCLUDED.raw_path_samples, 20),
    distinct_clients_sample = ads_capped_array_union(ads_discovered_apis.distinct_clients_sample,
                                                      EXCLUDED.distinct_clients_sample, 300),
    status_codes        = (
        SELECT array_agg(DISTINCT v ORDER BY v)
        FROM unnest(ads_discovered_apis.status_codes || EXCLUDED.status_codes) AS v
    )::smallint[],
    avg_duration_us     = (ads_discovered_apis.avg_duration_us * ads_discovered_apis.observation_count
                          + EXCLUDED.avg_duration_us * EXCLUDED.observation_count)
                         / NULLIF(ads_discovered_apis.observation_count + EXCLUDED.observation_count, 0),
    sample_pod          = COALESCE(NULLIF(EXCLUDED.sample_pod, ''), ads_discovered_apis.sample_pod),
    sample_workload     = COALESCE(NULLIF(EXCLUDED.sample_workload, ''), ads_discovered_apis.sample_workload),
    request_domain      = COALESCE(NULLIF(EXCLUDED.request_domain, ''), ads_discovered_apis.request_domain),
    last_window_id      = EXCLUDED.last_window_id,
    normalization_version = EXCLUDED.normalization_version,
    -- top_clients: replace with the latest cycle's snapshot (it's a
    -- per-window top-N, not a cumulative set).
    top_clients         = EXCLUDED.top_clients,
    is_active           = true,
    updated_at          = now()
`

// BatchUpsert applies the spec's upsert for each row in items. All rows are
// queued into a single pgx.Batch and sent in one network round-trip.
func (r *DiscoveredRepo) BatchUpsert(ctx context.Context, items []DiscoveredUpsert) error {
	if len(items) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, x := range items {
		// nil → empty for the array columns whose schema is NOT NULL
		// DEFAULT '{}'. See managed_repo.go for the same fix.
		raw := x.RawPathSamples
		if raw == nil {
			raw = []string{}
		}
		clients := x.DistinctClientsSample
		if clients == nil {
			clients = []string{}
		}
		statuses := x.StatusCodes
		if statuses == nil {
			statuses = []int16{}
		}
		topClients := x.TopClients
		if topClients == nil {
			topClients = []models.ClientObservation{}
		}
		topClientsJSON, err := json.Marshal(topClients)
		if err != nil {
			return fmt.Errorf("marshal top_clients for %s %s: %w",
				x.Method, x.NormalizedPath, err)
		}
		batch.Queue(upsertSQL,
			x.ServiceID, x.Method, x.NormalizedPath, raw,
			x.FirstSeenAt, x.LastSeenAt,
			x.ObservationCount, x.FlowCount, x.DistinctClientCount,
			clients, statuses, x.AvgDurationUs,
			x.RequestDomain, x.InternalFlows, x.ExternalFlows,
			x.SamplePod, x.SampleWorkload, x.NormalizationVersion, x.LastWindowID,
			topClientsJSON,
		)
	}

	br := r.pool.SendBatch(ctx, batch)
	defer br.Close()

	for i := range items {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("upsert row %d (%s %s %s): %w",
				i, items[i].Method, items[i].ServiceID, items[i].NormalizedPath, err)
		}
	}
	return nil
}
