package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wso2/api-discovery-server/internal/models"
)

// BFFRepo serves the read paths backing the daemon's REST endpoints.
// All queries hit v_current_classifications + ads_managed_apis;
// Phase 1/2/3 writers use the other repos.
type BFFRepo struct {
	pool *pgxpool.Pool
}

// NewBFFRepo binds a BFFRepo to pool.
func NewBFFRepo(pool *pgxpool.Pool) *BFFRepo {
	return &BFFRepo{pool: pool}
}

// ErrNotFound is returned when a single-row lookup misses.
var ErrNotFound = errors.New("not found")

// SummaryRow is one entry in the per-service breakdown of the summary
// endpoint. Per spec phase4_admin_portal.md §2.1.
type SummaryRow struct {
	ServiceIdentity string `json:"service_identity"`
	FullyGoverned   bool   `json:"fully_governed"`
	Shadow          int    `json:"shadow"`
	Drift           int    `json:"drift"`
}

// Summary is the aggregate-counts payload for GET /summary.
type Summary struct {
	Total          int          `json:"total"`
	Managed        int          `json:"managed"`
	Unmanaged      int          `json:"unmanaged"`
	SkipInternal   bool         `json:"skip_internal"`
	ByType         ByType       `json:"by_type"`
	ByReachability ByReach      `json:"by_reachability"`
	ByService      []SummaryRow `json:"by_service"`
}

// ByType counts unmanaged classifications by classification.
type ByType struct {
	Shadow int `json:"shadow"`
	Drift  int `json:"drift"`
}

// ByReach counts unmanaged classifications by traffic direction.
type ByReach struct {
	External int `json:"external"`
	Internal int `json:"internal"`
}

// GetSummary computes the summary in a single round-trip via two queries
// against v_current_classifications + ads_managed_apis.
//
// Per the redesign: managed table no longer stores service_identity.
// "managed" count is just COUNT(*) of active managed rows. Per-service
// breakdown uses the discovered side's service_identity (from the view).
//
// skipInternal is the daemon's [discovery].skip_internal config — passed in
// rather than re-read from DB because the daemon cache is the truth.
func (r *BFFRepo) GetSummary(ctx context.Context, skipInternal bool) (*Summary, error) {
	const q = `
        WITH unmanaged AS (
            SELECT classification, is_internal, service_identity
              FROM v_current_classifications
        )
        SELECT
            (SELECT COUNT(*) FROM unmanaged) +
              (SELECT COUNT(*) FROM ads_managed_apis WHERE is_active = true) AS total,
            (SELECT COUNT(*) FROM ads_managed_apis WHERE is_active = true)::int AS managed,
            (SELECT COUNT(*) FROM unmanaged)::int                              AS unmanaged,
            (SELECT COUNT(*) FROM unmanaged WHERE classification = 'shadow')::int AS shadow,
            (SELECT COUNT(*) FROM unmanaged WHERE classification = 'drift')::int  AS drift,
            (SELECT COUNT(*) FROM unmanaged WHERE is_internal = false)::int AS external,
            (SELECT COUNT(*) FROM unmanaged WHERE is_internal = true)::int  AS internal
    `
	var s Summary
	err := r.pool.QueryRow(ctx, q).Scan(
		&s.Total, &s.Managed, &s.Unmanaged,
		&s.ByType.Shadow, &s.ByType.Drift,
		&s.ByReachability.External, &s.ByReachability.Internal,
	)
	if err != nil {
		return nil, fmt.Errorf("summary aggregate: %w", err)
	}
	s.SkipInternal = skipInternal

	// Per-service breakdown — uses discovered.service_identity (from the
	// materialized view). A service is "fully_governed" iff it has at
	// least one anchored discovered row AND no shadow/drift findings.
	// (We can't tell from the discovered side alone whether managed APIs
	//  exist for a service that has zero traffic — that's the documented
	//  limitation.)
	const perServiceQ = `
        WITH per AS (
            SELECT service_identity,
                   COUNT(*) FILTER (WHERE classification = 'shadow') AS shadow,
                   COUNT(*) FILTER (WHERE classification = 'drift')  AS drift
              FROM v_current_classifications
             GROUP BY service_identity
        )
        SELECT p.service_identity,
               (p.shadow = 0 AND p.drift = 0) AS fully_governed,
               p.shadow,
               p.drift
          FROM per p
         ORDER BY p.service_identity
    `
	rows, err := r.pool.Query(ctx, perServiceQ)
	if err != nil {
		return nil, fmt.Errorf("summary per-service: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row SummaryRow
		if err := rows.Scan(&row.ServiceIdentity, &row.FullyGoverned, &row.Shadow, &row.Drift); err != nil {
			return nil, fmt.Errorf("scan per-service: %w", err)
		}
		s.ByService = append(s.ByService, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &s, nil
}

// ListItem is one row of the GET /apis list endpoint. Trimmed shape per
// spec phase4_admin_portal.md §2.2.
type ListItem struct {
	ID                  uuid.UUID `json:"id"`
	ServiceIdentity     string    `json:"service_identity"`
	EnvKind             string    `json:"env_kind"`
	Method              string    `json:"method"`
	NormalizedPath      string    `json:"normalized_path"`
	Classification      string    `json:"classification"`
	IsInternal          bool      `json:"is_internal"`
	ObservationCount    int64     `json:"observation_count"`
	DistinctClientCount int       `json:"distinct_client_count"`
	LastSeenAt          time.Time `json:"last_seen_at"`
	MatchedAPIMAPIIDs   []string  `json:"matched_apim_api_ids"`
}

// ListFilter constrains the GET /apis result set. All fields optional —
// zero values mean "no filter".
type ListFilter struct {
	Classification string // "" | "shadow" | "drift"
	Service        string // service_identity exact match
	Internal       string // "" | "true" | "false" | "only"
	Limit          int    // clamped to [1, 100]; default 25
	Offset         int    // default 0
}

// ListResult bundles paginated list output with the total count for the
// pagination block.
type ListResult struct {
	List  []ListItem
	Total int
}

// ListDiscovered runs the filtered/paginated list query.
func (r *BFFRepo) ListDiscovered(ctx context.Context, f ListFilter) (*ListResult, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	// Build WHERE dynamically. Always parameterized — never concat user input.
	var conds []string
	var args []any
	idx := 1
	add := func(cond string, arg any) {
		conds = append(conds, fmt.Sprintf(cond, idx))
		args = append(args, arg)
		idx++
	}
	if f.Classification != "" {
		add("classification = $%d", f.Classification)
	}
	if f.Service != "" {
		add("service_identity = $%d", f.Service)
	}
	switch f.Internal {
	case "false":
		conds = append(conds, "is_internal = false")
	case "only":
		conds = append(conds, "is_internal = true")
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	// Total count for pagination.
	totalQ := "SELECT COUNT(*) FROM v_current_classifications " + where
	var total int
	if err := r.pool.QueryRow(ctx, totalQ, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count discovered: %w", err)
	}

	// Page query.
	pageQ := fmt.Sprintf(`
        SELECT discovered_api_id, service_identity, env_kind, method, normalized_path,
               classification, is_internal, observation_count, distinct_client_count,
               last_seen_at, matched_apim_api_ids
          FROM v_current_classifications
        %s
         ORDER BY last_seen_at DESC, discovered_api_id
         LIMIT %d OFFSET %d
    `, where, limit, offset)

	rows, err := r.pool.Query(ctx, pageQ, args...)
	if err != nil {
		return nil, fmt.Errorf("list discovered: %w", err)
	}
	defer rows.Close()

	var items []ListItem
	for rows.Next() {
		var it ListItem
		if err := rows.Scan(
			&it.ID, &it.ServiceIdentity, &it.EnvKind, &it.Method, &it.NormalizedPath,
			&it.Classification, &it.IsInternal, &it.ObservationCount, &it.DistinctClientCount,
			&it.LastSeenAt, &it.MatchedAPIMAPIIDs,
		); err != nil {
			return nil, fmt.Errorf("scan list item: %w", err)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if items == nil {
		items = []ListItem{}
	}
	return &ListResult{List: items, Total: total}, nil
}

// Detail is the full payload for GET /apis/{id}, per spec §2.3.
type Detail struct {
	ID                    uuid.UUID      `json:"id"`
	ServiceIdentity       string         `json:"service_identity"`
	EnvKind               string         `json:"env_kind"`
	Namespace             string         `json:"namespace,omitempty"`
	ServiceName           string         `json:"service_name,omitempty"`
	SamplePod             string         `json:"sample_pod,omitempty"`
	SampleWorkload        string         `json:"sample_workload,omitempty"`
	Method                string         `json:"method"`
	NormalizedPath        string         `json:"normalized_path"`
	RawPathSamples        []string       `json:"raw_path_samples"`
	Classification        string         `json:"classification"`
	IsInternal            bool           `json:"is_internal"`
	FirstSeenAt           time.Time      `json:"first_seen_at"`
	LastSeenAt            time.Time      `json:"last_seen_at"`
	ObservationCount      int64          `json:"observation_count"`
	DistinctClientCount   int            `json:"distinct_client_count"`
	DistinctClientsSample []string       `json:"distinct_clients_sample"`
	StatusCodes           []int          `json:"status_codes"`
	AvgDurationUs         float64        `json:"avg_duration_us"`
	MatchedAPIMAPIIDs     []string       `json:"matched_apim_api_ids"`
	MatchedAPIMAPIs       []DetailAPIRef `json:"matched_apim_apis"`
	ServiceManagedAPIs    []DetailAPIRef `json:"service_managed_apis"`

	// TopClients is the per-cycle top callers for this finding,
	// pre-sorted by Observations desc and capped at 20.
	TopClients []models.ClientObservation `json:"top_clients"`
}

// DetailAPIRef is the trimmed APIM-API reference embedded in Detail.
type DetailAPIRef struct {
	APIMAPIID      string `json:"apim_api_id"`
	APIMAPIName    string `json:"apim_api_name"`
	APIMAPIVersion string `json:"apim_api_version"`
}

// GetDiscoveredByID returns the detail row plus the cross-references
// (matched_apim_apis derived from matched_apim_api_ids; service_managed_apis
// from ads_managed_apis on the same service_identity).
func (r *BFFRepo) GetDiscoveredByID(ctx context.Context, id uuid.UUID) (*Detail, error) {
	const q = `
        SELECT discovered_api_id, service_identity, env_kind,
               method, normalized_path, raw_path_samples,
               classification, is_internal,
               first_seen_at, last_seen_at,
               observation_count, distinct_client_count, distinct_clients_sample,
               status_codes, avg_duration_us,
               sample_pod, sample_workload,
               matched_apim_api_ids,
               top_clients
          FROM v_current_classifications
         WHERE discovered_api_id = $1
    `
	var d Detail
	var statusCodes []int16
	var topClientsJSON []byte
	err := r.pool.QueryRow(ctx, q, id).Scan(
		&d.ID, &d.ServiceIdentity, &d.EnvKind,
		&d.Method, &d.NormalizedPath, &d.RawPathSamples,
		&d.Classification, &d.IsInternal,
		&d.FirstSeenAt, &d.LastSeenAt,
		&d.ObservationCount, &d.DistinctClientCount, &d.DistinctClientsSample,
		&statusCodes, &d.AvgDurationUs,
		&d.SamplePod, &d.SampleWorkload,
		&d.MatchedAPIMAPIIDs,
		&topClientsJSON,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("detail row: %w", err)
	}
	for _, s := range statusCodes {
		d.StatusCodes = append(d.StatusCodes, int(s))
	}
	if len(topClientsJSON) > 0 {
		if err := json.Unmarshal(topClientsJSON, &d.TopClients); err != nil {
			return nil, fmt.Errorf("unmarshal top_clients: %w", err)
		}
	}
	if d.TopClients == nil {
		d.TopClients = []models.ClientObservation{}
	}

	// Derive namespace + service from k8s:<ns>/<svc> identity.
	if strings.HasPrefix(d.ServiceIdentity, "k8s:") {
		rest := strings.TrimPrefix(d.ServiceIdentity, "k8s:")
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			d.Namespace = rest[:i]
			d.ServiceName = rest[i+1:]
		}
	}

	// Sister-set: APIM-managed APIs sharing this discovered path's APIM
	// context prefix. Post-redesign the managed table no longer carries
	// service_identity, so we match the discovered path's normalized form
	// against the managed apim_api_context (e.g. "/customers/1.0.0"):
	// any managed API whose context is a prefix of the discovered path
	// is part of the same APIM API "family" the operator can compare to.
	const sisterQ = `
        SELECT DISTINCT apim_api_id, apim_api_name, apim_api_version
          FROM ads_managed_apis
         WHERE is_active = true
           AND ($1 = apim_api_context
                OR $1 LIKE apim_api_context || '/%')
         ORDER BY apim_api_name
    `
	rows, err := r.pool.Query(ctx, sisterQ, d.NormalizedPath)
	if err != nil {
		return nil, fmt.Errorf("service managed apis: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ref DetailAPIRef
		if err := rows.Scan(&ref.APIMAPIID, &ref.APIMAPIName, &ref.APIMAPIVersion); err != nil {
			return nil, err
		}
		d.ServiceManagedAPIs = append(d.ServiceManagedAPIs, ref)
	}
	if d.ServiceManagedAPIs == nil {
		d.ServiceManagedAPIs = []DetailAPIRef{}
	}

	// matched_apim_apis: same as ServiceManagedAPIs but filtered to
	// matched_apim_api_ids.
	if len(d.MatchedAPIMAPIIDs) > 0 {
		const matchedQ = `
            SELECT DISTINCT apim_api_id, apim_api_name, apim_api_version
              FROM ads_managed_apis
             WHERE apim_api_id = ANY($1) AND is_active = true
        `
		mrows, err := r.pool.Query(ctx, matchedQ, d.MatchedAPIMAPIIDs)
		if err != nil {
			return nil, fmt.Errorf("matched apim apis: %w", err)
		}
		defer mrows.Close()
		for mrows.Next() {
			var ref DetailAPIRef
			if err := mrows.Scan(&ref.APIMAPIID, &ref.APIMAPIName, &ref.APIMAPIVersion); err != nil {
				return nil, err
			}
			d.MatchedAPIMAPIs = append(d.MatchedAPIMAPIs, ref)
		}
	}
	if d.MatchedAPIMAPIs == nil {
		d.MatchedAPIMAPIs = []DetailAPIRef{}
	}

	if d.RawPathSamples == nil {
		d.RawPathSamples = []string{}
	}
	if d.DistinctClientsSample == nil {
		d.DistinctClientsSample = []string{}
	}
	if d.MatchedAPIMAPIIDs == nil {
		d.MatchedAPIMAPIIDs = []string{}
	}
	return &d, nil
}

// UntraffickedItem is one row of GET /untrafficked. Per spec §2.4: managed
// APIs APIM thinks exist but no Phase 1 row has matched.
type UntraffickedItem struct {
	APIMAPIID       string    `json:"apim_api_id"`
	APIMAPIName     string    `json:"apim_api_name"`
	APIMAPIVersion  string    `json:"apim_api_version"`
	Method          string    `json:"method"`
	GatewayPath     string    `json:"gateway_path"`
	ServiceIdentity string    `json:"service_identity"`
	LastSyncedAt    time.Time `json:"last_synced_at"`
}

// ListUntrafficked returns active managed operations with no matching
// discovered row. Post-redesign the match key is (method, path) only —
// the managed table no longer stores service_identity. The reported
// "service" identity is derived from the APIM API context, which is the
// most stable identity we still have on the managed side.
func (r *BFFRepo) ListUntrafficked(ctx context.Context) ([]UntraffickedItem, error) {
	const q = `
        SELECT m.apim_api_id, m.apim_api_name, m.apim_api_version,
               m.method, m.gateway_path, m.apim_api_context, m.last_synced_at
          FROM ads_managed_apis m
         WHERE m.is_active = true
           AND NOT EXISTS (
               SELECT 1 FROM ads_discovered_apis d
                WHERE d.method          = m.method
                  AND d.is_active       = true
                  AND (d.normalized_path = m.gateway_path
                       OR d.normalized_path = m.backend_path)
           )
         ORDER BY m.apim_api_name, m.gateway_path
    `
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("untrafficked: %w", err)
	}
	defer rows.Close()
	var out []UntraffickedItem
	for rows.Next() {
		var u UntraffickedItem
		if err := rows.Scan(
			&u.APIMAPIID, &u.APIMAPIName, &u.APIMAPIVersion,
			&u.Method, &u.GatewayPath, &u.ServiceIdentity, &u.LastSyncedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []UntraffickedItem{}
	}
	return out, nil
}
