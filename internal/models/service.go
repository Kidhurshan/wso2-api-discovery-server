// Package models holds the plain row structs shared by store and pipelines.
//
// These mirror the columns in schema/*.sql verbatim. They contain no business
// logic — keep computation in the discovery/managed/comparison packages.
package models

import (
	"time"

	"github.com/google/uuid"
)

// EnvKind identifies the runtime kind of a service.
const (
	EnvKindK8s     = "k8s"
	EnvKindLegacy  = "legacy"
	EnvKindUnknown = "unknown"
)

// Service is one row in ads_services. Maps schema/001_init.sql.
type Service struct {
	ID              uuid.UUID
	ServiceIdentity string // "k8s:<ns>/<svc>" or "host:<ip>:<port>"
	EnvKind         string // EnvKindK8s | EnvKindLegacy
	Metadata        map[string]any
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
