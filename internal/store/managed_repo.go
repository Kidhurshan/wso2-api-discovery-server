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
type ManagedSync struct {
	APIMAPIID           string
	APIMAPIName         string
	APIMAPIVersion      string
	APIMAPIContext      string
	APIMAPIProvider     string
	APIMLifecycleStatus string

	EnvKind         string // 'k8s' | 'legacy' | 'unknown'
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
	Warnings        []string
}

// Sync executes the spec's two-step transaction (claude/specs/
// phase2_managed_sync.md §8):
//  1. Upsert every operation in items, marking is_active = true and
//     updating last_synced_at = syncStartedAt.
//  2. Soft-delete any row whose last_synced_at is older than syncStartedAt
//     (i.e., not in the current sync), preserving history per spec §8.
//
// Both steps run in one Postgres transaction so a partial failure leaves
// the table consistent.
func (r *ManagedRepo) Sync(ctx context.Context, items []ManagedSync, syncStartedAt time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op if Commit succeeded

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
    env_kind, service_identity,
    method, gateway_path, operation_target, raw_operation_target,
    raw_placeholders, auth_type, throttling_policy,
    backend_url, backend_resolved_ip, backend_resolved_port,
    apim_updated_time, last_synced_at, is_active, warnings
) VALUES (
    $1, $2, $3, $4,
    $5, $6,
    $7, $8,
    $9, $10, $11, $12,
    $13, $14, $15,
    $16, $17, $18,
    $19, $20, true, $21
)
ON CONFLICT (apim_api_id, method, gateway_path) DO UPDATE SET
    apim_api_name           = EXCLUDED.apim_api_name,
    apim_api_version        = EXCLUDED.apim_api_version,
    apim_api_context        = EXCLUDED.apim_api_context,
    apim_api_provider       = EXCLUDED.apim_api_provider,
    apim_lifecycle_status   = EXCLUDED.apim_lifecycle_status,
    env_kind                = EXCLUDED.env_kind,
    service_identity        = EXCLUDED.service_identity,
    operation_target        = EXCLUDED.operation_target,
    raw_operation_target    = EXCLUDED.raw_operation_target,
    raw_placeholders        = EXCLUDED.raw_placeholders,
    auth_type               = EXCLUDED.auth_type,
    throttling_policy       = EXCLUDED.throttling_policy,
    backend_url             = EXCLUDED.backend_url,
    backend_resolved_ip     = EXCLUDED.backend_resolved_ip,
    backend_resolved_port   = EXCLUDED.backend_resolved_port,
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
		// Postgres columns raw_placeholders + warnings are NOT NULL with
		// DEFAULT '{}'. pgx encodes a nil Go slice as NULL, which would
		// violate the constraint, so coerce to empty slice here.
		batch.Queue(managedUpsertSQL,
			x.APIMAPIID, x.APIMAPIName, x.APIMAPIVersion, x.APIMAPIContext,
			x.APIMAPIProvider, x.APIMLifecycleStatus,
			x.EnvKind, x.ServiceIdentity,
			x.Method, x.GatewayPath, x.OperationTarget, x.RawOperationTarget,
			nilToEmpty(x.RawPlaceholders), x.AuthType, x.ThrottlingPolicy,
			x.BackendURL, x.BackendResolvedIP, x.BackendResolvedPort,
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
