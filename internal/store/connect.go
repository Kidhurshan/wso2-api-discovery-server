// Package store wraps pgxpool with retry, migrations, and per-table repos.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/wso2/api-discovery-server/internal/config"
)

// connectAttempts is the maximum number of connection attempts before giving
// up. Per claude/specs/operations_guide.md §2.4 the daemon retries 30 times
// with exponential backoff capped at 30s — roughly 5 minutes of patience.
const connectAttempts = 30

// connectMaxBackoff is the cap on the per-attempt sleep.
const connectMaxBackoff = 30 * time.Second

// ConnectWithRetry opens a pgxpool against cfg.Database with bounded
// exponential backoff. Returns the pool on first successful Ping or the last
// error after exhausting retries.
//
// The caller is responsible for closing the pool.
func ConnectWithRetry(ctx context.Context, log *zap.Logger, cfg *config.Config) (*pgxpool.Pool, error) {
	dsn := cfg.Database.DSN()

	var (
		pool    *pgxpool.Pool
		lastErr error
	)
	backoff := 1 * time.Second

	for attempt := 1; attempt <= connectAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		pool, lastErr = pgxpool.New(ctx, dsn)
		if lastErr == nil {
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			lastErr = pool.Ping(pingCtx)
			cancel()
			if lastErr == nil {
				if attempt > 1 {
					log.Info("db connection established",
						zap.Int("attempt", attempt),
					)
				}
				return pool, nil
			}
			pool.Close()
			pool = nil
		}

		log.Warn("db connection failed, retrying",
			zap.Int("attempt", attempt),
			zap.Int("max_attempts", connectAttempts),
			zap.Duration("backoff", backoff),
			zap.Error(lastErr),
		)

		// Wait, but cancel cleanly if ctx is done.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}

		if backoff < connectMaxBackoff {
			backoff *= 2
			if backoff > connectMaxBackoff {
				backoff = connectMaxBackoff
			}
		}
	}

	return nil, fmt.Errorf("db connection failed after %d attempts: %w", connectAttempts, lastErr)
}
