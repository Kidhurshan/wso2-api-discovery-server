// Package engine is the daemon's top-level orchestrator. It wires the
// config, logger, store, health, and (in later rounds) BFF + cycle loop.
//
// Round 1 ships only the bootstrap path: open DB, run migrations, start
// health server, block on ctx, drain on shutdown. Phase pipelines and BFF
// arrive in Rounds 2-5.
package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/wso2/api-discovery-server/internal/config"
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

	// 3. Boot the health-check state and start the health server.
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

	// Background DB-reachability poller flips state to false if Ping starts
	// failing. The full /readyz machinery (breakers, last-success times)
	// arrives in Round 6.
	wg.Add(1)
	go func() {
		defer wg.Done()
		pollDBReachability(ctx, pool, state, dbLog)
	}()

	// 4. Block until ctx cancels.
	engineLog.Info("ready")
	<-ctx.Done()
	engineLog.Info("shutdown requested")

	// 5. Drain. The health server stops when its ctx-derived listener
	// shuts down; we just wait for the goroutines to finish.
	wg.Wait()

	if err := <-healthErrCh; err != nil {
		return fmt.Errorf("health server: %w", err)
	}
	engineLog.Info("shutdown complete")
	return nil
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
