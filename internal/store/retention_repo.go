package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wso2/api-discovery-server/internal/config"
)

// RetentionRepo bundles the daily cleanup SQL per spec
// claude/specs/operations_guide.md §8. Each method runs one of the three
// cleanup statements; RunRetention runs all three in order and finishes
// by stamping pipeline_state.last_retention_run.
type RetentionRepo struct {
	pool *pgxpool.Pool
}

// NewRetentionRepo binds a RetentionRepo to pool.
func NewRetentionRepo(pool *pgxpool.Pool) *RetentionRepo {
	return &RetentionRepo{pool: pool}
}

// RunRetention runs all three retention statements + the marker update.
//
// Errors from individual statements are joined and returned together so a
// single bad row doesn't mask cleanup of the other tables.
func (r *RetentionRepo) RunRetention(ctx context.Context, cfg *config.RetentionConfig) error {
	if err := r.cleanupClassifications(ctx, cfg.ClassificationsRetentionDays); err != nil {
		return fmt.Errorf("retention classifications: %w", err)
	}
	if err := r.cleanupDiscoveredAPIs(ctx, cfg.DiscoveredAPIsRetentionDays); err != nil {
		return fmt.Errorf("retention discovered_apis: %w", err)
	}
	if err := r.cleanupManagedAPIs(ctx); err != nil {
		return fmt.Errorf("retention managed_apis: %w", err)
	}
	const markQ = `UPDATE ads_pipeline_state SET last_retention_run = now()`
	if _, err := r.pool.Exec(ctx, markQ); err != nil {
		return fmt.Errorf("mark retention run: %w", err)
	}
	return nil
}

// cleanupClassifications deletes classifications older than retentionDays,
// preserving the latest classification per discovered_api per spec §8.1.
//
// The subquery's "DISTINCT ON (discovered_api_id) ... ORDER BY classified_at
// DESC" guarantees we never lose the most recent record even if it's
// already older than the retention window.
func (r *RetentionRepo) cleanupClassifications(ctx context.Context, retentionDays int) error {
	q := fmt.Sprintf(`
        DELETE FROM ads_classifications
         WHERE classified_at < now() - INTERVAL '%d days'
           AND id NOT IN (
               SELECT DISTINCT ON (discovered_api_id) id
                 FROM ads_classifications
                ORDER BY discovered_api_id, classified_at DESC
           )
    `, retentionDays)
	_, err := r.pool.Exec(ctx, q)
	return err
}

// cleanupDiscoveredAPIs soft-deletes rows that haven't been seen for 7
// days, then hard-deletes the soft-deleted ones older than retentionDays.
//
// Anchor protection: rows with is_anchor = true are preserved regardless of
// last_seen_at. Anchors are set by Phase 3 when a discovered API matches a
// managed entry — keeping them alive ensures that "service is partially
// managed" inference doesn't decay to false-shadow when the managed paths
// go idle for longer than the retention window.
func (r *RetentionRepo) cleanupDiscoveredAPIs(ctx context.Context, retentionDays int) error {
	const softQ = `
        UPDATE ads_discovered_apis
           SET is_active = false
         WHERE last_seen_at < now() - INTERVAL '7 days'
           AND is_active = true
           AND is_anchor = false
    `
	if _, err := r.pool.Exec(ctx, softQ); err != nil {
		return fmt.Errorf("soft-delete: %w", err)
	}
	hardQ := fmt.Sprintf(`
        DELETE FROM ads_discovered_apis
         WHERE is_active = false
           AND last_seen_at < now() - INTERVAL '%d days'
           AND is_anchor = false
    `, retentionDays)
	_, err := r.pool.Exec(ctx, hardQ)
	return err
}

// cleanupManagedAPIs hard-deletes rows soft-deleted by Phase 2 more than
// 90 days ago. Spec §8.3 (constant 90 — not configurable in v1 because
// nothing currently distinguishes it from the discovered hard-delete cap).
func (r *RetentionRepo) cleanupManagedAPIs(ctx context.Context) error {
	const q = `
        DELETE FROM ads_managed_apis
         WHERE is_active = false
           AND last_synced_at < now() - INTERVAL '90 days'
    `
	_, err := r.pool.Exec(ctx, q)
	return err
}
