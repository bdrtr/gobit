// Package models defines the domain models of the product module.
//
// These types are the shape the module shows to the OUTSIDE: the service
// returns them and the API layer writes them straight to JSON. They are
// deliberately kept free of database types (pgtype.*) — pgtype stays inside the
// repository boundary; otherwise the choice of driver would leak into every
// layer of the module.
//
// Time fields are UTC and come from the database clock. Deletion is SOFT: a
// record whose DeletedAt is set is returned by no read query.
package models

import "time"

// Status is the publication status of a product.
//
// The value set is exactly the same as the product_status_check constraint in
// the database; if either of the two changes, the other has to change as well.
type Status string

// Product publication statuses.
const (
	// StatusDraft is a draft; it is not visible in the store API.
	StatusDraft Status = "draft"
	// StatusPublished is live; it is the default filter of the store API.
	StatusPublished Status = "published"
	// StatusArchived is archived; not visible in the store API but not deleted.
	StatusArchived Status = "archived"
)

// Valid reports whether the status is one of the defined values.
func (s Status) Valid() bool {
	switch s {
	case StatusDraft, StatusPublished, StatusArchived:
		return true
	default:
		return false
	}
}

// String returns the textual form of the status.
func (s Status) String() string { return string(s) }

// ProductOrder is the order a product listing comes back in.
//
// It is a CLOSED set and not a column name the caller supplies, and the reason
// is the keyset cursor rather than taste: every order a listing offers needs an
// index its seek can ride, and an order named by the client would either find
// no index or fall back to a sort of the whole table without saying so.
//
// The two values here cost no index at all. `product_created_at_idx` is
// declared `(created_at DESC, id DESC)` and PostgreSQL reads a b-tree in either
// direction, so the older-first order is the same index walked the other way.
// The orders that are NOT here are absent for a reason and not by omission:
// price and availability are not columns of this table at all — they belong to
// the pricing and inventory modules — and what they would even mean is the open
// question docs/gaps.md records as A16 and A17.
type ProductOrder string

// Product listing orders.
const (
	// ProductOrderNewest is newest first. It is the default and the order every
	// listing had before this parameter existed.
	ProductOrderNewest ProductOrder = "newest"
	// ProductOrderOldest is oldest first: the same key read the other way.
	ProductOrderOldest ProductOrder = "oldest"
)

// Valid reports whether the order is one of the defined values.
func (o ProductOrder) Valid() bool {
	switch o {
	case ProductOrderNewest, ProductOrderOldest:
		return true
	default:
		return false
	}
}

// String returns the textual form of the order.
func (o ProductOrder) String() string { return string(o) }

// Product is a product in the catalog.
//
// The PRICE and the STOCK of the product are NOT here: both are the data of
// separate modules (Principle 2.3) and are attached with links over the
// variant. The product carries catalog information only.
type Product struct {
	ID            string         `json:"id"`
	Handle        string         `json:"handle"`
	Title         string         `json:"title"`
	Subtitle      *string        `json:"subtitle,omitempty"`
	Description   *string        `json:"description,omitempty"`
	Thumbnail     *string        `json:"thumbnail,omitempty"`
	Status        Status         `json:"status"`
	IsGiftcard    bool           `json:"is_giftcard"`
	Discountable  bool           `json:"discountable"`
	Weight        *int32         `json:"weight,omitempty"`
	Length        *int32         `json:"length,omitempty"`
	Height        *int32         `json:"height,omitempty"`
	Width         *int32         `json:"width,omitempty"`
	Material      *string        `json:"material,omitempty"`
	OriginCountry *string        `json:"origin_country,omitempty"`
	CollectionID  *string        `json:"collection_id,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     *time.Time     `json:"deleted_at,omitempty"`

	// The fields below are related records; they are filled only when the caller
	// asks for them and are never written to JSON while empty. They are filled
	// with BULK queries (see repository ...ByProductIDs), not with one query per
	// record.
	Variants   []Variant  `json:"variants,omitempty"`
	Options    []Option   `json:"options,omitempty"`
	Images     []Image    `json:"images,omitempty"`
	Tags       []Tag      `json:"tags,omitempty"`
	Categories []Category `json:"categories,omitempty"`
}
