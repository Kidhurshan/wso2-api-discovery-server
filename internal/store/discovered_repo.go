package store

import "github.com/jackc/pgx/v5/pgxpool"

// DiscoveredRepo handles ads_discovered_apis CRUD. Round 2 adds BatchUpsert.
type DiscoveredRepo struct {
	pool *pgxpool.Pool
}

// NewDiscoveredRepo returns a DiscoveredRepo bound to pool.
func NewDiscoveredRepo(pool *pgxpool.Pool) *DiscoveredRepo {
	return &DiscoveredRepo{pool: pool}
}
