package models

import (
	"time"

	"github.com/google/uuid"
)

// Classification labels for ads_classifications.classification.
//
// Per claude/specs/phase3_comparison.md §2 only shadow and drift are stored;
// "managed" rows are computed but excluded by the WHERE clause in the
// classification SQL.
const (
	ClassificationShadow = "shadow"
	ClassificationDrift  = "drift"
)

// Classification is one row in ads_classifications.
type Classification struct {
	ID                uuid.UUID
	DiscoveredAPIID   uuid.UUID
	CycleID           uuid.UUID
	Classification    string // ClassificationShadow | ClassificationDrift
	IsInternal        bool
	MatchedManagedIDs []uuid.UUID
	MatchedAPIMAPIIDs []string
	ClassifiedAt      time.Time
}
