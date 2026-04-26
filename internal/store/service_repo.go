package store

import "github.com/jackc/pgx/v5/pgxpool"

// ServiceRepo handles ads_services CRUD. Per the plan, methods are added
// round-by-round; Round 1 only provides the constructor.
type ServiceRepo struct {
	pool *pgxpool.Pool
}

// NewServiceRepo returns a ServiceRepo bound to pool.
func NewServiceRepo(pool *pgxpool.Pool) *ServiceRepo {
	return &ServiceRepo{pool: pool}
}
