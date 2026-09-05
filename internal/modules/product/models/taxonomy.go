package models

import "time"

// Category is the category that groups products in a tree.
//
// ParentID is a reference to the same table: since the category tree lives
// inside a single module, a foreign key is free here (what is forbidden is the
// CROSS-MODULE foreign key, Principle 2.2).
type Category struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Handle      string     `json:"handle"`
	Description *string    `json:"description,omitempty"`
	ParentID    *string    `json:"parent_id,omitempty"`
	IsActive    bool       `json:"is_active"`
	IsInternal  bool       `json:"is_internal"`
	Rank        int32      `json:"rank"`
	CreatedAt   time.Time  `json:"created_at,omitzero"`
	UpdatedAt   time.Time  `json:"updated_at,omitzero"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

// Collection is the collection that gathers products into a flat set (e.g.
// "Summer 2026").
type Collection struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	Handle    string         `json:"handle"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt *time.Time     `json:"deleted_at,omitempty"`
}

// Tag is the free-form label attached to a product.
type Tag struct {
	ID        string     `json:"id"`
	Value     string     `json:"value"`
	CreatedAt time.Time  `json:"created_at,omitzero"`
	UpdatedAt time.Time  `json:"updated_at,omitzero"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// Image is an image of a product. The file itself is not kept in this module;
// only a reachable link (URL) and, when there is one, the id of the upload it
// was made from.
type Image struct {
	ID        string `json:"id"`
	ProductID string `json:"product_id"`
	URL       string `json:"url"`
	// UploadID is the id of the file module's upload record; nil when the image
	// points at an address this installation never uploaded (an imported
	// catalog, a hand-typed CDN address).
	//
	// It is what makes "which upload is this image" answerable: the URL shows
	// the file and says nothing else about it, and the record behind it —
	// detected content type, size, checksum, storing provider — is reachable
	// only through this id. It is NOT a foreign key (Principle 2.2); the record
	// lives in another module and is read through that module's cross-module
	// surface.
	UploadID  *string        `json:"upload_id,omitempty"`
	Rank      int32          `json:"rank"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at,omitzero"`
	UpdatedAt time.Time      `json:"updated_at,omitzero"`
	DeletedAt *time.Time     `json:"deleted_at,omitempty"`
}
