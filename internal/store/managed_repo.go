package store

import "github.com/jackc/pgx/v5/pgxpool"

// ManagedRepo handles ads_managed_apis CRUD. Round 3 adds Sync.
type ManagedRepo struct {
	pool *pgxpool.Pool
}

// NewManagedRepo returns a ManagedRepo bound to pool.
func NewManagedRepo(pool *pgxpool.Pool) *ManagedRepo {
	return &ManagedRepo{pool: pool}
}
