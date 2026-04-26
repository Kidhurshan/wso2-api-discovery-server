package managed

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/wso2/api-discovery-server/internal/apim"
	"github.com/wso2/api-discovery-server/internal/discovery"
	"github.com/wso2/api-discovery-server/internal/store"
)

// Pipeline runs one Phase 2 cycle when Run is called.
type Pipeline struct {
	log         *zap.Logger
	publisher   *apim.PublisherClient
	resolver    *Resolver
	expander    *Expander
	managedRepo *store.ManagedRepo
	pipeRepo    *store.PipelineRepo
}

// NewPipeline wires the dependencies. The expander reuses the same
// *discovery.Normalizer instance Phase 1 uses so both phases produce
// identical normalized paths for the same logical operation.
func NewPipeline(
	log *zap.Logger,
	publisher *apim.PublisherClient,
	resolver *Resolver,
	norm *discovery.Normalizer,
	managedRepo *store.ManagedRepo,
	pipeRepo *store.PipelineRepo,
) *Pipeline {
	return &Pipeline{
		log:         log,
		publisher:   publisher,
		resolver:    resolver,
		expander:    NewExpander(norm),
		managedRepo: managedRepo,
		pipeRepo:    pipeRepo,
	}
}

// Run executes one cycle: list PUBLISHED APIs, fetch each detail, resolve
// each backend URL, expand operations, sync into Postgres.
//
// Returns nil when the cycle completed (even if some individual API fetches
// failed — the partial result still updates the table). Returns an error
// only on unrecoverable failure (auth, db).
func (p *Pipeline) Run(ctx context.Context, cycleID uuid.UUID) error {
	cycleLog := p.log.With(zap.String("cycle_id", cycleID.String()))
	syncStartedAt := time.Now()
	cycleLog.Info("phase 2 cycle starting")

	// 1. List PUBLISHED APIs.
	summaries, err := p.publisher.ListPublishedAPIs(ctx)
	if err != nil {
		return fmt.Errorf("list publisher apis: %w", err)
	}
	cycleLog.Info("publisher list complete", zap.Int("published_apis", len(summaries)))

	// 2. Fetch detail for each API (concurrent, capped).
	ids := make([]string, 0, len(summaries))
	for _, s := range summaries {
		ids = append(ids, s.ID)
	}
	details, err := p.publisher.FetchDetails(ctx, ids)
	if err != nil {
		// Partial failures are allowed — we proceed with what we got.
		cycleLog.Warn("some api detail fetches failed", zap.Error(err))
	}
	cycleLog.Info("api details fetched", zap.Int("count", len(details)))

	// 3. Resolve + expand per API.
	var ops []store.ManagedSync
	resolveErrors := 0
	for i := range details {
		api := &details[i]
		if len(api.Operations) == 0 {
			cycleLog.Warn("api has no operations — skipping",
				zap.String("api_id", api.ID),
				zap.String("api_name", api.Name),
			)
			continue
		}
		res, err := p.resolver.Resolve(api)
		if err != nil {
			cycleLog.Warn("resolver dropped api",
				zap.String("api_id", api.ID),
				zap.String("api_name", api.Name),
				zap.Error(err),
			)
			resolveErrors++
			continue
		}
		expanded := p.expander.Expand(api, res)
		updated, _ := time.Parse(time.RFC3339Nano, api.UpdatedTime)
		for _, op := range expanded {
			ops = append(ops, store.ManagedSync{
				APIMAPIID:           op.APIID,
				APIMAPIName:         op.APIName,
				APIMAPIVersion:      op.APIVersion,
				APIMAPIContext:      op.APIContext,
				APIMAPIProvider:     op.APIProvider,
				APIMLifecycleStatus: op.APILifecycleStatus,

				EnvKind:         op.EnvKind,
				ServiceIdentity: op.ServiceIdentity,

				Method:             op.Method,
				GatewayPath:        op.GatewayPath,
				OperationTarget:    op.OperationTarget,
				RawOperationTarget: op.RawOperationTarget,
				RawPlaceholders:    op.RawPlaceholders,
				AuthType:           op.AuthType,
				ThrottlingPolicy:   op.ThrottlingPolicy,

				BackendURL:          op.BackendURL,
				BackendResolvedIP:   op.BackendResolvedIP,
				BackendResolvedPort: op.BackendResolvedPort,

				APIMUpdatedTime: updated,
				Warnings:        op.Warnings,
			})
		}
	}
	cycleLog.Info("operations expanded",
		zap.Int("operation_count", len(ops)),
		zap.Int("resolve_errors", resolveErrors),
	)

	// 4. Sync into Postgres (upsert + soft-delete).
	if err := p.managedRepo.Sync(ctx, ops, syncStartedAt); err != nil {
		return fmt.Errorf("managed repo sync: %w", err)
	}

	if err := p.pipeRepo.UpdatePhase2Success(ctx); err != nil {
		return fmt.Errorf("update phase2 success: %w", err)
	}

	cycleLog.Info("phase 2 cycle complete",
		zap.Int("operations_synced", len(ops)),
		zap.Duration("elapsed", time.Since(syncStartedAt)),
	)
	return nil
}

// SafeAuthClose ensures we don't leak the auth goroutine if the engine
// shuts down before the first Run completes. Engine calls this on
// shutdown.
type SafeAuthClose interface {
	Close()
}

var _ = SafeAuthClose(nil) // keep type alive for future engine wiring
