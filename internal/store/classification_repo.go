package store

import "github.com/jackc/pgx/v5/pgxpool"

// ClassificationRepo handles ads_classifications CRUD. Round 4 adds Classify.
type ClassificationRepo struct {
	pool *pgxpool.Pool
}

// NewClassificationRepo returns a ClassificationRepo bound to pool.
func NewClassificationRepo(pool *pgxpool.Pool) *ClassificationRepo {
	return &ClassificationRepo{pool: pool}
}
