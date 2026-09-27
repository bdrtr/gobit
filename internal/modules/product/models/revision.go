package models

import (
	"encoding/json"
	"time"
)

// Revision is a product as it stood after one write to it (ADR 0221).
type Revision struct {
	ID        string `json:"id"`
	ProductID string `json:"product_id"`
	// Version numbers the product's revisions from 1, one per write that
	// changed its admin view.
	Version    int64     `json:"version"`
	RecordedAt time.Time `json:"recorded_at"`
	// Changed names the top-level fields of the snapshot that differ from the
	// revision before; the first revision names none.
	Changed []string `json:"changed"`
	// RequestID is the HTTP request that made the revision, the key the admin
	// audit log records its caller under; absent for a job's.
	RequestID *string `json:"request_id,omitempty"`
	// Snapshot is the product's admin view without its timestamps: its own
	// fields, variants, options, images, tags, categories and attribute values.
	// A listing leaves it out.
	Snapshot json.RawMessage `json:"snapshot,omitempty"`
}
