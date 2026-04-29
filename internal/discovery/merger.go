package discovery

import (
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/wso2/api-discovery-server/internal/models"
)

// MergeKey identifies one logical (service, method, normalized-path)
// signature. Multiple DeepFlow rows (one per observation_point or per
// agent) can share a key and must be folded together before upsert.
type MergeKey struct {
	ServiceIdentity string
	Method          string
	NormalizedPath  string
}

// MergedRow is the post-merge representation passed to the store. It carries
// everything the upsert needs in one struct, mirroring ads_discovered_apis.
type MergedRow struct {
	Key                  MergeKey
	EnvKind              string
	NormalizationVersion string
	LastWindowID         uuid.UUID

	FirstSeenAt    time.Time
	LastSeenAt     time.Time
	RowCount       int64 // sum of Count(row) across folded rows ≈ observations
	FlowCount      int64 // count of distinct (op, agent) source rows we folded
	StatusCodes    []int16
	RequestDomain  string
	SamplePod      string
	SampleWorkload string

	RawPathSamples        []string
	DistinctClientCount   int
	DistinctClientsSample []string
	AvgDurationUs         float64

	InternalFlows int64 // folded rowcount where direction == internal
	ExternalFlows int64 // folded rowcount where direction == external

	// TopClients is the per-cycle top-N callers ranked by Observations
	// desc. Capped at clientsCap entries before persisting.
	TopClients []models.ClientObservation
}

// clientsCap bounds the size of TopClients per row, controlling the
// JSONB column footprint. 20 fits comfortably under 2KB even with K8s
// workloads + namespaces and IP/port samples.
const clientsCap = 20

// MergeAndNormalize pipelines normalization + merging in one pass. For each
// classified signal:
//  1. Compute the normalized_path via norm.Normalize(endpoint).
//  2. Look up or create a MergedRow under (identity, method, normalized).
//  3. Fold the signal's counts, samples, identity into the existing row.
//
// The output is deterministic order (sort by service then path) so test
// assertions don't depend on map iteration order.
func MergeAndNormalize(
	signals []classified,
	norm *Normalizer,
	cycleID uuid.UUID,
) []MergedRow {
	bucket := make(map[MergeKey]*MergedRow, len(signals))
	// Per-bucket map of client identity → folding ClientObservation.
	// We build it during merge, then resolve to a sorted+capped slice
	// at the end before returning.
	clientsByBucket := make(map[MergeKey]map[string]*models.ClientObservation, len(signals))

	for _, s := range signals {
		key := MergeKey{
			ServiceIdentity: s.ServiceIdentity,
			Method:          s.Method,
			NormalizedPath:  norm.Normalize(s.Endpoint),
		}

		existing, ok := bucket[key]
		if !ok {
			existing = &MergedRow{
				Key:                  key,
				EnvKind:              envKindForService(s),
				NormalizationVersion: norm.Version,
				LastWindowID:         cycleID,
				FirstSeenAt:          unixToTime(s.FirstSeenUnix),
				LastSeenAt:           unixToTime(s.LastSeenUnix),
				SamplePod:            s.K8sPod,
				SampleWorkload:       s.K8sWorkload,
				RequestDomain:        s.RequestDomain,
			}
			bucket[key] = existing
		}

		existing.FlowCount++
		existing.RowCount += s.RowCount

		// Direction tally for the spec's internal_flows / external_flows
		// counters, used by Phase 3 to set the is_internal modifier.
		if s.TrafficDirection == "internal" {
			existing.InternalFlows += s.RowCount
		} else {
			existing.ExternalFlows += s.RowCount
		}

		first := unixToTime(s.FirstSeenUnix)
		last := unixToTime(s.LastSeenUnix)
		if first.Before(existing.FirstSeenAt) {
			existing.FirstSeenAt = first
		}
		if last.After(existing.LastSeenAt) {
			existing.LastSeenAt = last
		}

		// Weighted average duration. This is the spec §4.3 formula but
		// adapted: the per-row Avg arrives already-averaged from the
		// querier (Avg(response_duration)), weighted by RowCount.
		totalRows := existing.RowCount
		if totalRows > 0 {
			prevContribution := existing.AvgDurationUs * float64(totalRows-s.RowCount)
			newContribution := s.AvgDurationUs * float64(s.RowCount)
			existing.AvgDurationUs = (prevContribution + newContribution) / float64(totalRows)
		}

		// Bounded sample lists. Caps from spec §3 / §4: 20 paths, 300
		// clients, distinct status codes (no explicit cap; the values are
		// small).
		existing.RawPathSamples = appendUniqueCapped(existing.RawPathSamples, s.SampleURL, 20)
		existing.DistinctClientsSample = appendUniqueCapped(existing.DistinctClientsSample, s.ClientIP, 300)
		existing.DistinctClientCount = len(existing.DistinctClientsSample)
		if s.SampleStatus > 0 {
			existing.StatusCodes = appendUniqueStatus(existing.StatusCodes, int16(s.SampleStatus))
		}

		// Pod/workload preference: the spec's argMaxIf prefers s-p over s.
		// We approximate by overwriting only when we see an s-p signal
		// after an initial value from another tap.
		if (existing.SamplePod == "" || s.ObservationPoint == tapServerSideProcess) && s.K8sPod != "" {
			existing.SamplePod = s.K8sPod
		}
		if (existing.SampleWorkload == "" || s.ObservationPoint == tapServerSideProcess) && s.K8sWorkload != "" {
			existing.SampleWorkload = s.K8sWorkload
		}

		// Roll up top callers. Skip if classifier couldn't tag the
		// client (anonymous internet traffic, missing tags, etc.) —
		// those callers aren't actionable for triage.
		if s.ClientIdentity != "" {
			clients, ok := clientsByBucket[key]
			if !ok {
				clients = make(map[string]*models.ClientObservation)
				clientsByBucket[key] = clients
			}
			obs, seen := clients[s.ClientIdentity]
			if !seen {
				obs = &models.ClientObservation{
					Identity:  s.ClientIdentity,
					Kind:      s.ClientKind,
					Namespace: s.ClientNamespace,
					Workload:  s.ClientWorkload,
					IP:        s.ClientIP,
					Port:      s.ClientPortSample,
				}
				clients[s.ClientIdentity] = obs
			}
			obs.Observations += s.RowCount
			// Sample port only if not yet captured — first wins.
			if obs.Port == 0 && s.ClientPortSample > 0 {
				obs.Port = s.ClientPortSample
			}
		}
	}

	// Resolve per-bucket top_clients to sorted+capped slices.
	for key, clients := range clientsByBucket {
		row := bucket[key]
		list := make([]models.ClientObservation, 0, len(clients))
		for _, c := range clients {
			list = append(list, *c)
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].Observations != list[j].Observations {
				return list[i].Observations > list[j].Observations
			}
			return list[i].Identity < list[j].Identity
		})
		if len(list) > clientsCap {
			list = list[:clientsCap]
		}
		row.TopClients = list
	}

	out := make([]MergedRow, 0, len(bucket))
	for _, row := range bucket {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key.ServiceIdentity != out[j].Key.ServiceIdentity {
			return out[i].Key.ServiceIdentity < out[j].Key.ServiceIdentity
		}
		if out[i].Key.Method != out[j].Key.Method {
			return out[i].Key.Method < out[j].Key.Method
		}
		return out[i].Key.NormalizedPath < out[j].Key.NormalizedPath
	})
	return out
}

// CollectServices returns one ServiceCandidate per distinct (identity,
// env_kind) seen in rows. Ready to feed to ServiceRepo.EnsureServices.
func CollectServices(rows []MergedRow) []ServiceCandidate {
	seen := make(map[string]ServiceCandidate, len(rows))
	for _, r := range rows {
		c, ok := seen[r.Key.ServiceIdentity]
		if !ok {
			c = ServiceCandidate{
				ServiceIdentity: r.Key.ServiceIdentity,
				EnvKind:         r.EnvKind,
				FirstSeenAt:     r.FirstSeenAt,
				LastSeenAt:      r.LastSeenAt,
			}
		}
		if r.FirstSeenAt.Before(c.FirstSeenAt) {
			c.FirstSeenAt = r.FirstSeenAt
		}
		if r.LastSeenAt.After(c.LastSeenAt) {
			c.LastSeenAt = r.LastSeenAt
		}
		seen[r.Key.ServiceIdentity] = c
	}
	out := make([]ServiceCandidate, 0, len(seen))
	for _, c := range seen {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ServiceIdentity < out[j].ServiceIdentity })
	return out
}

// ServiceCandidate is the payload for ads_services upsert. Lives here (rather
// than in the store package) to keep the data model close to discovery.
type ServiceCandidate struct {
	ServiceIdentity string
	EnvKind         string
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
}

// EnvKindIsK8s lets store code distinguish without re-importing the discovery
// helper. Returns the spec-required string.
func (c ServiceCandidate) EnvKindString() string {
	if c.EnvKind == "" {
		return models.EnvKindUnknown
	}
	return c.EnvKind
}

// unixToTime converts a Unix-second timestamp to a UTC time.Time. Zero stays
// zero so downstream code can guard on IsZero.
func unixToTime(unix int64) time.Time {
	if unix <= 0 {
		return time.Time{}
	}
	return time.Unix(unix, 0).UTC()
}

// appendUniqueCapped appends v to xs if not already present, capping the
// result at limit entries. Order is insertion order; first wins on dedup.
func appendUniqueCapped(xs []string, v string, limit int) []string {
	if v == "" || len(xs) >= limit {
		return xs
	}
	for _, existing := range xs {
		if existing == v {
			return xs
		}
	}
	return append(xs, v)
}

// appendUniqueStatus is the int16 specialisation of appendUniqueCapped.
func appendUniqueStatus(xs []int16, v int16) []int16 {
	for _, existing := range xs {
		if existing == v {
			return xs
		}
	}
	return append(xs, v)
}
