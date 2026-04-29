package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ManagedRepo handles ads_managed_apis CRUD.
type ManagedRepo struct {
	pool *pgxpool.Pool
}

// NewManagedRepo returns a ManagedRepo bound to pool.
func NewManagedRepo(pool *pgxpool.Pool) *ManagedRepo {
	return &ManagedRepo{pool: pool}
}

// ManagedSync is the input shape for Sync. One per (api_id, method,
// gateway_path) — the expander already performs the per-operation
// expansion before reaching the store.
//
// Per the redesign: no env_kind, no service_identity, no backend resolved
// IP/port. The match key is (method, gateway_path | backend_path) only.
type ManagedSync struct {
	APIMAPIID           string
	APIMAPIName         string
	APIMAPIVersion      string
	APIMAPIContext      string
	APIMAPIProvider     string
	APIMLifecycleStatus string

	Method      string
	GatewayPath string // /prod/1.0.0/item/{id}    — what the client sees
	BackendPath string // /products/v1/item/{id}   — what the backend sees
	BackendURL  string // raw URL for debug / display

	AuthType         string
	ThrottlingPolicy string

	APIMUpdatedTime time.Time
	Warnings        []string
}

// Sync executes the spec's two-step transaction:
//  1. Upsert every operation in items, marking is_active = true and
//     updating last_synced_at = syncStartedAt.
//  2. Soft-delete any row whose last_synced_at is older than syncStartedAt.
func (r *ManagedRepo) Sync(ctx context.Context, items []ManagedSync, syncStartedAt time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := r.upsertAll(ctx, tx, items, syncStartedAt); err != nil {
		return err
	}
	if err := r.softDeleteStale(ctx, tx, syncStartedAt); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit sync tx: %w", err)
	}
	return nil
}

const managedUpsertSQL = `
INSERT INTO ads_managed_apis (
    apim_api_id, apim_api_name, apim_api_version, apim_api_context,
    apim_api_provider, apim_lifecycle_status,
    method, gateway_path, backend_path,
    auth_type, throttling_policy,
    backend_url,
    apim_updated_time, last_synced_at, is_active, warnings
) VALUES (
    $1, $2, $3, $4,
    $5, $6,
    $7, $8, $9,
    $10, $11,
    $12,
    $13, $14, true, $15
)
ON CONFLICT (apim_api_id, method, gateway_path) DO UPDATE SET
    apim_api_name           = EXCLUDED.apim_api_name,
    apim_api_version        = EXCLUDED.apim_api_version,
    apim_api_context        = EXCLUDED.apim_api_context,
    apim_api_provider       = EXCLUDED.apim_api_provider,
    apim_lifecycle_status   = EXCLUDED.apim_lifecycle_status,
    backend_path            = EXCLUDED.backend_path,
    auth_type               = EXCLUDED.auth_type,
    throttling_policy       = EXCLUDED.throttling_policy,
    backend_url             = EXCLUDED.backend_url,
    apim_updated_time       = EXCLUDED.apim_updated_time,
    last_synced_at          = EXCLUDED.last_synced_at,
    is_active               = true,
    warnings                = EXCLUDED.warnings,
    updated_at              = now()
`

func (r *ManagedRepo) upsertAll(ctx context.Context, tx pgx.Tx, items []ManagedSync, syncStartedAt time.Time) error {
	if len(items) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, x := range items {
		batch.Queue(managedUpsertSQL,
			x.APIMAPIID, x.APIMAPIName, x.APIMAPIVersion, x.APIMAPIContext,
			x.APIMAPIProvider, x.APIMLifecycleStatus,
			x.Method, x.GatewayPath, x.BackendPath,
			x.AuthType, x.ThrottlingPolicy,
			x.BackendURL,
			x.APIMUpdatedTime, syncStartedAt, nilToEmpty(x.Warnings),
		)
	}
	br := tx.SendBatch(ctx, batch)
	defer br.Close()
	for i := range items {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("upsert managed row %d (%s %s %s): %w",
				i, items[i].APIMAPIName, items[i].Method, items[i].GatewayPath, err)
		}
	}
	return nil
}

// nilToEmpty returns an empty []string when xs is nil; otherwise xs unchanged.
// Needed because pgx encodes a nil slice as SQL NULL, violating columns
// declared NOT NULL DEFAULT '{}'.
func nilToEmpty(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

func (r *ManagedRepo) softDeleteStale(ctx context.Context, tx pgx.Tx, syncStartedAt time.Time) error {
	const q = `
        UPDATE ads_managed_apis
           SET is_active  = false,
               updated_at = now()
         WHERE last_synced_at < $1
           AND is_active = true
    `
	if _, err := tx.Exec(ctx, q, syncStartedAt); err != nil {
		return fmt.Errorf("soft-delete stale managed apis: %w", err)
	}
	return nil
}
