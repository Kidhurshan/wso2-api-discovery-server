// Package engine is the daemon's top-level orchestrator. It wires the
// config, logger, store, health, deepflow client, and the cycle loop that
// runs each phase on its configured cadence.
package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/wso2/api-discovery-server/internal/config"
	"github.com/wso2/api-discovery-server/internal/deepflow"
	"github.com/wso2/api-discovery-server/internal/discovery"
	"github.com/wso2/api-discovery-server/internal/health"
	"github.com/wso2/api-discovery-server/internal/logging"
	"github.com/wso2/api-discovery-server/internal/store"
)

// Run is the daemon entry point. Returns nil on graceful shutdown via ctx,
// or a wrapped error for any startup failure.
func Run(ctx context.Context, cfg *config.Config) error {
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
	pipelineRepo := store.NewPipelineRepo(pool)

	// 4. Optional: build the DeepFlow client and ping it. A dead DeepFlow at
	//    startup is non-fatal — the daemon stays up and serves health probes
	//    while the cycle loop will keep retrying. Per operations_guide.md §2.1.
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

	// 5. Boot the health-check state and start the health server.
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

	// 6. DB-reachability poller.
	wg.Add(1)
	go func() {
		defer wg.Done()
		pollDBReachability(ctx, pool, state, dbLog)
	}()

	// 7. Cycle loop. Round 2 only runs Phase 1; Round 3 will add Phase 2,
	//    Round 4 Phase 3.
	wg.Add(1)
	go func() {
		defer wg.Done()
		runCycleLoop(ctx, cfg, log, dfClient, serviceRepo, discoveredRepo, pipelineRepo)
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

// runCycleLoop runs Phase 1 every cfg.Discovery.PollIntervalMinutes.
//
// Single tick semantics: an immediate first tick (don't wait the full
// interval before discovering anything), then steady cadence. The ticker is
// drained on ctx cancel so a long-running phase doesn't outlive shutdown.
func runCycleLoop(
	ctx context.Context,
	cfg *config.Config,
	log *zap.Logger,
	df deepflow.Client,
	serviceRepo *store.ServiceRepo,
	discoveredRepo *store.DiscoveredRepo,
	pipelineRepo *store.PipelineRepo,
) {
	cycleLog := logging.WithComponent(log, "cycle")

	if !cfg.DeepFlow.Enabled || df == nil {
		cycleLog.Info("cycle loop idle: deepflow disabled")
		<-ctx.Done()
		return
	}

	pipeline := discovery.NewPipeline(cfg, log, df, serviceRepo, discoveredRepo, pipelineRepo)
	interval := time.Duration(cfg.Discovery.PollIntervalMinutes) * time.Minute

	// First tick fires immediately; subsequent ticks at interval.
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			cycleLog.Info("cycle loop exiting")
			return
		case <-timer.C:
		}

		cycleID := uuid.New()
		if err := pipeline.Run(ctx, cycleID); err != nil {
			// Round 6 will route this through a circuit breaker; for now
			// log and continue. A persistently failing DeepFlow is visible
			// via /readyz once the breaker lands.
			cycleLog.Error("phase 1 cycle failed",
				zap.String("cycle_id", cycleID.String()),
				zap.Error(err),
			)
		}

		timer.Reset(interval)
	}
}

// pollDBReachability pings the pool every 10s and updates state. Cheap, but
// catches the case where Postgres goes away mid-run so /readyz can flip.
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
