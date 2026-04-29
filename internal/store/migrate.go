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
// its own transaction. Applied filenames are recorded in schema_migrations so
// each migration runs at most once per database.
//
// We use a separate `migrations/` directory under store/ for the embed because
// go:embed requires the embedded files to live under the embedding package's
// directory. The actual canonical location is `schema/`; the `migrations/`
// dir is a build-time copy populated by the Makefile.
func RunMigrations(ctx context.Context, log *zap.Logger, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	// Backfill: if the legacy tables already exist (deployed before the
	// tracker was introduced) but schema_migrations is empty, treat all
	// shipped migrations up to and including 007 as already-applied so we
	// don't re-run them against an existing schema.
	var trackerCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&trackerCount); err != nil {
		return fmt.Errorf("count schema_migrations: %w", err)
	}
	if trackerCount == 0 {
		var managedExists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_schema='public' AND table_name='ads_managed_apis'
			)`).Scan(&managedExists); err != nil {
			return fmt.Errorf("probe ads_managed_apis: %w", err)
		}
		if managedExists {
			var hasServiceIdentity bool
			if err := pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM information_schema.columns
					WHERE table_schema='public'
					  AND table_name='ads_managed_apis'
					  AND column_name='service_identity'
				)`).Scan(&hasServiceIdentity); err != nil {
				return fmt.Errorf("probe service_identity: %w", err)
			}
			backfill := []string{
				"001_init.sql",
				"002_phase1_normalization_version.sql",
				"003_managed_apis.sql",
				"004_classifications.sql",
				"005_view.sql",
				"006_capped_array_union.sql",
			}
			if !hasServiceIdentity {
				backfill = append(backfill, "007_redesign_managed_and_anchors.sql")
			}
			for _, n := range backfill {
				if _, err := pool.Exec(ctx,
					`INSERT INTO schema_migrations(version) VALUES ($1) ON CONFLICT DO NOTHING`, n); err != nil {
					return fmt.Errorf("backfill %s: %w", n, err)
				}
			}
			log.Info("schema_migrations backfilled from existing schema",
				zap.Int("entries", len(backfill)))
		}
	}

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
		var alreadyApplied bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`,
			name).Scan(&alreadyApplied); err != nil {
			return fmt.Errorf("check %s: %w", name, err)
		}
		if alreadyApplied {
			continue
		}

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
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations(version) VALUES ($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}

		log.Info("migration applied", zap.String("file", name))
	}

	return nil
}
