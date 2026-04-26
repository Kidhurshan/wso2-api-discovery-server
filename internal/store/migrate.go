package store

import (
	"context"
	"embed"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// migrationsFS embeds the schema/*.sql files at compile time. Each file is
// applied in lexicographic order on startup; idempotency is the file's own
// responsibility (CREATE IF NOT EXISTS, CREATE OR REPLACE, INSERT ON CONFLICT
// DO NOTHING).
//
//go:embed all:migrations
var migrationsFS embed.FS

// RunMigrations executes every embedded *.sql file against the pool, each in
// its own transaction. The function is safe to call repeatedly.
//
// We use a separate `migrations/` directory under store/ for the embed because
// go:embed requires the embedded files to live under the embedding package's
// directory. The actual canonical location is `schema/`; the `migrations/`
// dir is a build-time copy populated by the Makefile.
func RunMigrations(ctx context.Context, log *zap.Logger, pool *pgxpool.Pool) error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin tx for %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}

		log.Info("migration applied", zap.String("file", name))
	}

	return nil
}
