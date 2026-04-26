package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ServiceRepo handles ads_services CRUD.
type ServiceRepo struct {
	pool *pgxpool.Pool
}

// NewServiceRepo returns a ServiceRepo bound to pool.
func NewServiceRepo(pool *pgxpool.Pool) *ServiceRepo {
	return &ServiceRepo{pool: pool}
}

// ServiceUpsert is the input shape for EnsureServices. Mirrors the columns
// the upsert needs without taking a dependency on the discovery package.
type ServiceUpsert struct {
	ServiceIdentity string
	EnvKind         string // 'k8s' | 'legacy'
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
}

// EnsureServices upserts each (service_identity, env_kind) and returns a map
// from identity to the row's UUID. Caller can then build foreign keys for
// ads_discovered_apis without a second SELECT.
//
// The query uses a CTE-based unnest pattern so all rows are inserted in a
// single round-trip; for typical Phase 1 cycles (tens of services) this is
// well under 1ms even on a slow Postgres.
func (r *ServiceRepo) EnsureServices(ctx context.Context, items []ServiceUpsert) (map[string]uuid.UUID, error) {
	if len(items) == 0 {
		return map[string]uuid.UUID{}, nil
	}

	identities := make([]string, len(items))
	envKinds := make([]string, len(items))
	firstSeen := make([]time.Time, len(items))
	lastSeen := make([]time.Time, len(items))
	for i, x := range items {
		identities[i] = x.ServiceIdentity
		envKinds[i] = x.EnvKind
		firstSeen[i] = x.FirstSeenAt
		lastSeen[i] = x.LastSeenAt
	}

	const q = `
        INSERT INTO ads_services (service_identity, env_kind, first_seen_at, last_seen_at)
        SELECT * FROM unnest($1::text[], $2::text[], $3::timestamptz[], $4::timestamptz[])
        ON CONFLICT (service_identity) DO UPDATE SET
            env_kind      = EXCLUDED.env_kind,
            last_seen_at  = GREATEST(ads_services.last_seen_at, EXCLUDED.last_seen_at),
            first_seen_at = LEAST(ads_services.first_seen_at, EXCLUDED.first_seen_at),
            updated_at    = now()
        RETURNING id, service_identity
    `

	rows, err := r.pool.Query(ctx, q, identities, envKinds, firstSeen, lastSeen)
	if err != nil {
		return nil, fmt.Errorf("upsert services: %w", err)
	}
	defer rows.Close()

	out := make(map[string]uuid.UUID, len(items))
	for rows.Next() {
		var id uuid.UUID
		var identity string
		if err := rows.Scan(&id, &identity); err != nil {
			return nil, fmt.Errorf("scan service row: %w", err)
		}
		out[identity] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate services: %w", err)
	}
	return out, nil
}
