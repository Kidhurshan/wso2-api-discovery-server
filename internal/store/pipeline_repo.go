package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wso2/api-discovery-server/internal/models"
)

// PipelineRepo reads and writes the single ads_pipeline_state row.
//
// Round 1 implements Get so health probes (Round 6) and the freshness guard
// (Round 4) have a stable read path. Per-phase update methods are added in
// the rounds that need them.
type PipelineRepo struct {
	pool *pgxpool.Pool
}

// NewPipelineRepo returns a PipelineRepo bound to pool.
func NewPipelineRepo(pool *pgxpool.Pool) *PipelineRepo {
	return &PipelineRepo{pool: pool}
}

// UpdatePhase3Success records a successful Phase 3 cycle and the
// just-completed materialized-view refresh time.
func (r *PipelineRepo) UpdatePhase3Success(ctx context.Context) error {
	const q = `
        UPDATE ads_pipeline_state
           SET phase3_last_success      = now(),
               phase3_last_view_refresh = now()
    `
	if _, err := r.pool.Exec(ctx, q); err != nil {
		return fmt.Errorf("update phase3 success: %w", err)
	}
	return nil
}

// UpdatePhase2Success records a successful Phase 2 cycle.
func (r *PipelineRepo) UpdatePhase2Success(ctx context.Context) error {
	const q = `UPDATE ads_pipeline_state SET phase2_last_success = now()`
	if _, err := r.pool.Exec(ctx, q); err != nil {
		return fmt.Errorf("update phase2 success: %w", err)
	}
	return nil
}

// UpdatePhase1Success records a successful Phase 1 cycle: bumps the
// last-success timestamp and stores the just-queried window bounds.
func (r *PipelineRepo) UpdatePhase1Success(ctx context.Context, windowStart, windowEnd time.Time) error {
	const q = `
        UPDATE ads_pipeline_state SET
            phase1_last_success      = now(),
            phase1_last_window_start = $1,
            phase1_last_window_end   = $2
    `
	if _, err := r.pool.Exec(ctx, q, windowStart, windowEnd); err != nil {
		return fmt.Errorf("update phase1 success: %w", err)
	}
	return nil
}

// Get returns the seeded pipeline-state row. Phase 1 and Phase 2 timestamps
// are zero until those rounds populate them.
func (r *PipelineRepo) Get(ctx context.Context) (*models.PipelineState, error) {
	const q = `
        SELECT id::text,
               COALESCE(phase1_last_success, 'epoch'),
               COALESCE(phase1_last_window_start, 'epoch'),
               COALESCE(phase1_last_window_end, 'epoch'),
               COALESCE(phase2_last_success, 'epoch'),
               COALESCE(phase3_last_success, 'epoch'),
               COALESCE(phase3_last_view_refresh, 'epoch'),
               COALESCE(last_retention_run, 'epoch'),
               discovery_breaker_state,
               managed_breaker_state
          FROM ads_pipeline_state
         LIMIT 1
    `
	var s models.PipelineState
	err := r.pool.QueryRow(ctx, q).Scan(
		&s.ID,
		&s.Phase1LastSuccess,
		&s.Phase1LastWindowStart,
		&s.Phase1LastWindowEnd,
		&s.Phase2LastSuccess,
		&s.Phase3LastSuccess,
		&s.Phase3LastViewRefresh,
		&s.LastRetentionRun,
		&s.DiscoveryBreakerState,
		&s.ManagedBreakerState,
	)
	if err != nil {
		return nil, fmt.Errorf("get pipeline state: %w", err)
	}
	return &s, nil
}
