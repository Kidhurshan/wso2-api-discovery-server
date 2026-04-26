// Package comparison runs Phase 3: classify discovered rows as
// shadow/drift relative to the managed-API table, append to
// ads_classifications, and refresh the materialized view the BFF reads.
//
// The pipeline has no external dependencies beyond Postgres — it is
// composition over data Phases 1 and 2 produce. See
// claude/specs/phase3_comparison.md.
package comparison

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/wso2/api-discovery-server/internal/config"
	"github.com/wso2/api-discovery-server/internal/store"
)

// Pipeline runs one Phase 3 cycle when Run is called.
type Pipeline struct {
	cfg          *config.Config
	log          *zap.Logger
	classifyRepo *store.ClassificationRepo
	pipelineRepo *store.PipelineRepo
}

// NewPipeline wires the dependencies. cfg is needed for the freshness
// threshold and managed.poll_interval.
func NewPipeline(
	cfg *config.Config,
	log *zap.Logger,
	classifyRepo *store.ClassificationRepo,
	pipelineRepo *store.PipelineRepo,
) *Pipeline {
	return &Pipeline{
		cfg:          cfg,
		log:          log,
		classifyRepo: classifyRepo,
		pipelineRepo: pipelineRepo,
	}
}

// Run executes one Phase 3 cycle:
//  1. Freshness guard — skip if Phase 2 hasn't succeeded recently.
//  2. Classify (single SQL, append to ads_classifications).
//  3. Refresh v_current_classifications.
//  4. Update pipeline_state.
//
// Returns nil on either success or a deliberate skip. Returns error only
// on classifier or refresh failure (DB issues).
func (p *Pipeline) Run(ctx context.Context, cycleID uuid.UUID) error {
	cycleLog := p.log.With(zap.String("cycle_id", cycleID.String()))
	start := time.Now()

	state, err := p.pipelineRepo.Get(ctx)
	if err != nil {
		cycleLog.Error("phase 3 freshness guard: pipeline state read failed; skipping",
			zap.Error(err))
		return nil
	}
	threshold := freshnessThreshold(p.cfg)
	if reason, ok := freshnessReject(state.Phase2LastSuccess, threshold, time.Now()); !ok {
		cycleLog.Warn("phase 3 skipped — "+reason,
			zap.Time("phase2_last_success", state.Phase2LastSuccess),
			zap.Duration("threshold", threshold),
		)
		return nil
	}

	cycleLog.Info("phase 3 cycle starting")

	rowsInserted, err := p.classifyRepo.Classify(ctx, cycleID)
	if err != nil {
		return fmt.Errorf("classify: %w", err)
	}

	if err := p.classifyRepo.RefreshView(ctx); err != nil {
		return fmt.Errorf("refresh view: %w", err)
	}

	if err := p.pipelineRepo.UpdatePhase3Success(ctx); err != nil {
		return fmt.Errorf("update phase3 success: %w", err)
	}

	cycleLog.Info("phase 3 cycle complete",
		zap.Int("classifications_written", rowsInserted),
		zap.Duration("elapsed", time.Since(start)),
	)
	return nil
}

// freshnessThreshold returns freshness_threshold_multiplier *
// managed.poll_interval per spec phase3_comparison.md §7.
func freshnessThreshold(cfg *config.Config) time.Duration {
	return time.Duration(cfg.Comparison.FreshnessThresholdMultiplier) *
		cfg.Managed.PollInterval()
}

// freshnessReject is the pure predicate behind the freshness guard.
// Returns ("", true) when it's safe to proceed; otherwise (reason, false).
// Pulled out as a function so tests don't need a real PipelineRepo.
//
//	phase2LastSuccess zero or older than threshold → reject
//	otherwise                                      → accept
func freshnessReject(phase2LastSuccess time.Time, threshold time.Duration, now time.Time) (string, bool) {
	if phase2LastSuccess.IsZero() {
		return "phase 2 has never succeeded yet", false
	}
	age := now.Sub(phase2LastSuccess)
	if age > threshold {
		return "phase 2 data stale", false
	}
	return "", true
}
