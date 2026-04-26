// Package engine is the daemon's top-level orchestrator. It wires the
// config, logger, store, health, deepflow client, APIM auth/publisher, and
// the cycle loop that runs each phase on its configured cadence.
package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/wso2/api-discovery-server/internal/apim"
	"github.com/wso2/api-discovery-server/internal/comparison"
	"github.com/wso2/api-discovery-server/internal/config"
	"github.com/wso2/api-discovery-server/internal/deepflow"
	"github.com/wso2/api-discovery-server/internal/discovery"
	"github.com/wso2/api-discovery-server/internal/health"
	"github.com/wso2/api-discovery-server/internal/logging"
	"github.com/wso2/api-discovery-server/internal/managed"
	"github.com/wso2/api-discovery-server/internal/store"
)

// dcrCredsFile is the relative path under the config directory where DCR
// credentials are persisted. Resolved against the config file's parent dir.
const dcrCredsFile = "dcr_creds.json"

// Run is the daemon entry point. Returns nil on graceful shutdown via ctx,
// or a wrapped error for any startup failure.
func Run(ctx context.Context, cfg *config.Config, configPath string) error {
	log, err := logging.New(cfg.ADS.LogLevel)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer func() { _ = log.Sync() }()

	engineLog := logging.WithComponent(log, "engine")
	engineLog.Info("starting",
		zap.String("name", cfg.ADS.Name),
		zap.String("version", cfg.ADS.Version),
	)

	// 1. Open Postgres with retry.
	dbLog := logging.WithComponent(log, "store")
	pool, err := store.ConnectWithRetry(ctx, dbLog, cfg)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	defer pool.Close()
	dbLog.Info("db pool ready")

	// 2. Run migrations.
	if err := store.RunMigrations(ctx, dbLog, pool); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	// 3. Build repos.
	serviceRepo := store.NewServiceRepo(pool)
	discoveredRepo := store.NewDiscoveredRepo(pool)
	managedRepo := store.NewManagedRepo(pool)
	classificationRepo := store.NewClassificationRepo(pool)
	pipelineRepo := store.NewPipelineRepo(pool)

	// 4. DeepFlow client (optional — non-fatal at startup).
	var dfClient deepflow.Client
	if cfg.DeepFlow.Enabled {
		dfLog := logging.WithComponent(log, "deepflow")
		dfClient, err = deepflow.New(&cfg.DeepFlow)
		if err != nil {
			return fmt.Errorf("init deepflow client: %w", err)
		}
		pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := dfClient.Ping(pingCtx); err != nil {
			dfLog.Warn("deepflow ping failed at startup; will retry per-cycle", zap.Error(err))
		} else {
			dfLog.Info("deepflow client ready")
		}
		cancel()
	} else {
		engineLog.Info("deepflow disabled in config; phase 1 will not run")
	}

	// 5. APIM auth + publisher client (Phase 2). Non-fatal at startup —
	//    DCR/token failures are retried per cycle. The credsPath is the
	//    config directory's sibling file, so deployments that share a
	//    config dir reuse the same DCR registration.
	var auth *apim.Auth
	var publisher *apim.PublisherClient
	apimLog := logging.WithComponent(log, "apim")
	credsPath := filepath.Join(filepath.Dir(configPath), dcrCredsFile)
	auth = apim.NewAuth(&cfg.APIM, apimLog, credsPath)
	if err := auth.Start(ctx); err != nil {
		apimLog.Warn("apim auth not ready at startup; phase 2 will retry per cycle", zap.Error(err))
	} else {
		publisher = apim.NewPublisherClient(&cfg.APIM, auth, cfg.Managed.FetchConcurrency, apimLog)
	}

	// 6. Topology + DNS cache + resolver + shared normalizer for Phase 2.
	managedLog := logging.WithComponent(log, "managed")
	topology, err := managed.NewTopology(&cfg.Deployment.Topology)
	if err != nil {
		return fmt.Errorf("topology: %w", err)
	}
	dns := managed.NewDNSCache(time.Duration(cfg.Managed.DNSCacheTTLMinutes) * time.Minute)
	resolver := managed.NewResolver(topology, dns)
	sharedNormalizer := discovery.NewFromConfig(&cfg.Discovery)

	// 7. Boot the health-check state and start the health server.
	state := health.NewStaticState(true)
	healthLog := logging.WithComponent(log, "health")
	healthSrv := health.New(cfg.Health.ListenAddr, state, healthLog)

	wg := &sync.WaitGroup{}
	wg.Add(1)
	healthErrCh := make(chan error, 1)
	go func() {
		defer wg.Done()
		healthErrCh <- healthSrv.Run(ctx)
	}()

	// 8. DB-reachability poller.
	wg.Add(1)
	go func() {
		defer wg.Done()
		pollDBReachability(ctx, pool, state, dbLog)
	}()

	// 9. Cycle loop.
	comparisonLog := logging.WithComponent(log, "comparison")
	wg.Add(1)
	go func() {
		defer wg.Done()
		runCycleLoop(ctx, cfg, log,
			dfClient, sharedNormalizer,
			publisher, resolver,
			serviceRepo, discoveredRepo, managedRepo, classificationRepo, pipelineRepo,
			managedLog, comparisonLog,
		)
	}()

	engineLog.Info("ready")
	<-ctx.Done()
	engineLog.Info("shutdown requested")

	wg.Wait()
	if dfClient != nil {
		dfClient.Close()
	}
	if err := <-healthErrCh; err != nil {
		return fmt.Errorf("health server: %w", err)
	}
	engineLog.Info("shutdown complete")
	return nil
}

// runCycleLoop drives Phase 1, Phase 2, and Phase 3 on independent timers.
// Per spec phase3_comparison.md §4, Phase 3 is meant to run "at the end of
// every Phase 1 cycle, after both Phase 1 and Phase 2 have committed". We
// implement that by running Phase 3 inline after each successful Phase 1.
//
// Cadence implementation: per-phase mutex guards prevent overlapping cycles
// of the same phase from racing on writes.
func runCycleLoop(
	ctx context.Context,
	cfg *config.Config,
	log *zap.Logger,
	df deepflow.Client,
	norm *discovery.Normalizer,
	publisher *apim.PublisherClient,
	resolver *managed.Resolver,
	serviceRepo *store.ServiceRepo,
	discoveredRepo *store.DiscoveredRepo,
	managedRepo *store.ManagedRepo,
	classificationRepo *store.ClassificationRepo,
	pipelineRepo *store.PipelineRepo,
	managedLog *zap.Logger,
	comparisonLog *zap.Logger,
) {
	cycleLog := logging.WithComponent(log, "cycle")

	var phase1 *discovery.Pipeline
	if cfg.DeepFlow.Enabled && df != nil {
		phase1 = discovery.NewPipeline(cfg, log, df, serviceRepo, discoveredRepo, pipelineRepo)
	} else {
		cycleLog.Info("phase 1 disabled (deepflow not enabled)")
	}

	var phase2 *managed.Pipeline
	if publisher != nil {
		phase2 = managed.NewPipeline(managedLog, publisher, resolver, norm, managedRepo, pipelineRepo)
	} else {
		cycleLog.Info("phase 2 disabled (apim auth not initialized)")
	}

	// Phase 3 always runs (it's purely DB-side). Even when Phase 1 is
	// disabled we want comparison to refresh — operators may have seeded
	// ads_discovered_apis manually for testing.
	phase3 := comparison.NewPipeline(cfg, comparisonLog, classificationRepo, pipelineRepo)

	if phase1 == nil && phase2 == nil {
		// Nothing to discover — don't spin tickers. But still allow Phase
		// 3 to run on a slow ticker for housekeeping.
		runPhase3Only(ctx, cycleLog, phase3, cfg)
		return
	}

	p1Interval := time.Duration(cfg.Discovery.PollIntervalMinutes) * time.Minute
	p2Interval := time.Duration(cfg.Managed.PollIntervalMinutes) * time.Minute

	p1Timer := newImmediateTimer(phase1 != nil)
	p2Timer := newImmediateTimer(phase2 != nil)

	var p1Mu, p2Mu, p3Mu sync.Mutex // serialize cycles per-phase

	// runPhase3 is called after each successful Phase 1 cycle. The mutex
	// guards against the rare case of overlapping invocations (only
	// possible if the operator sets a very short Phase 1 interval).
	runPhase3 := func(parent uuid.UUID) {
		if !p3Mu.TryLock() {
			cycleLog.Warn("phase 3 cycle skipped — previous still running")
			return
		}
		defer p3Mu.Unlock()
		cycleID := uuid.New()
		if err := phase3.Run(ctx, cycleID); err != nil {
			cycleLog.Error("phase 3 cycle failed",
				zap.String("cycle_id", cycleID.String()),
				zap.String("triggering_phase1_cycle", parent.String()),
				zap.Error(err))
		}
	}

	for {
		select {
		case <-ctx.Done():
			cycleLog.Info("cycle loop exiting")
			return

		case <-tick(p1Timer):
			if phase1 == nil {
				continue
			}
			go func() {
				if !p1Mu.TryLock() {
					cycleLog.Warn("phase 1 cycle skipped — previous still running")
					return
				}
				defer p1Mu.Unlock()
				cycleID := uuid.New()
				if err := phase1.Run(ctx, cycleID); err != nil {
					cycleLog.Error("phase 1 cycle failed",
						zap.String("cycle_id", cycleID.String()), zap.Error(err))
					return
				}
				// Phase 3 piggy-backs on Phase 1's success — that's
				// when fresh discovered_apis exist to classify.
				runPhase3(cycleID)
			}()
			p1Timer.Reset(p1Interval)

		case <-tick(p2Timer):
			if phase2 == nil {
				continue
			}
			go func() {
				if !p2Mu.TryLock() {
					cycleLog.Warn("phase 2 cycle skipped — previous still running")
					return
				}
				defer p2Mu.Unlock()
				cycleID := uuid.New()
				if err := phase2.Run(ctx, cycleID); err != nil {
					cycleLog.Error("phase 2 cycle failed",
						zap.String("cycle_id", cycleID.String()), zap.Error(err))
				}
			}()
			p2Timer.Reset(p2Interval)
		}
	}
}

// runPhase3Only is the fallback when Phase 1 + 2 are both disabled. Runs
// Phase 3 every Phase 1 interval anyway so classification stays current
// against any externally-seeded data.
func runPhase3Only(ctx context.Context, log *zap.Logger, phase3 *comparison.Pipeline, cfg *config.Config) {
	interval := time.Duration(cfg.Discovery.PollIntervalMinutes) * time.Minute
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		cycleID := uuid.New()
		if err := phase3.Run(ctx, cycleID); err != nil {
			log.Error("phase 3 cycle failed",
				zap.String("cycle_id", cycleID.String()), zap.Error(err))
		}
		timer.Reset(interval)
	}
}

// newImmediateTimer returns a timer set to fire immediately. enabled=false
// returns nil so the select branch can be quietly dead.
func newImmediateTimer(enabled bool) *time.Timer {
	if !enabled {
		return nil
	}
	return time.NewTimer(0)
}

// tick returns the timer's channel, or a nil channel if t is nil — a select
// branch on a nil channel is forever-blocking, which is what we want.
func tick(t *time.Timer) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// pollDBReachability pings the pool every 10s and updates state.
func pollDBReachability(ctx context.Context, pool *pgxpool.Pool, state interface {
	SetDBReachable(bool)
}, log *zap.Logger) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err := pool.Ping(pingCtx)
			cancel()
			reachable := err == nil
			state.SetDBReachable(reachable)
			if !reachable {
				log.Warn("db ping failed", zap.Error(err))
			}
		}
	}
}
