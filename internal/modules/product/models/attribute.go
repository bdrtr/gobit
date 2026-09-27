package models

import (
	"regexp"
	"time"
)

// The kinds of a product attribute (ADR 0219).
const (
	// AttributeNumber holds one number per product: a width, a capacity.
	AttributeNumber = "number"
	// AttributeBoolean holds yes or no per product: waterproof or not.
	AttributeBoolean = "boolean"
	// AttributeSelect holds one or more of the attribute's options per product.
	AttributeSelect = "select"
)

// The bounds of the attributes.
const (
	// MaxAttributes is how many live attributes a catalog defines.
	MaxAttributes = 100
	// MaxAttributeOptions is how many live options a select attribute has.
	MaxAttributeOptions = 200
	// MaxAttributeFilters is how many attributes one listing filters on.
	MaxAttributeFilters = 10
	// MaxAttributeTitleLen is the longest title or option value, in bytes.
	MaxAttributeTitleLen = 200
	// MaxAttributeHandleLen is the longest handle, in bytes.
	MaxAttributeHandleLen = 64
)

// AttributeHandlePattern is what an attribute's or an option's handle looks
// like: lowercase words joined by single hyphens, the migration's CHECK.
var AttributeHandlePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Attribute is a store-wide definition a product takes a value of.
type Attribute struct {
	ID string `json:"id"`
	// Handle is how the storefront names it in a filter; it does not change.
	Handle string `json:"handle"`
	Title  string `json:"title"`
	// Kind is AttributeNumber, AttributeBoolean or AttributeSelect; it does not
	// change, since every value was written for it.
	Kind string `json:"kind"`
	// Rank is the operator's order; the smaller comes first.
	Rank int32 `json:"rank"`
	// Options are a select attribute's choices, in their order.
	Options   []AttributeOption `json:"options"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// AttributeOption is one choice of a select attribute.
type AttributeOption struct {
	ID          string `json:"id"`
	AttributeID string `json:"attribute_id"`
	// Handle is how the storefront names it in a filter.
	Handle string `json:"handle"`
	// Value is what the shopper reads.
	Value string `json:"value"`
	Rank  int32  `json:"rank"`
}

// ProductAttributeValue is a product's value of one attribute: the options it
// chose, or its number, or its boolean.
type ProductAttributeValue struct {
	AttributeID string            `json:"attribute_id"`
	Handle      string            `json:"handle"`
	Title       string            `json:"title"`
	Kind        string            `json:"kind"`
	Options     []AttributeOption `json:"options,omitempty"`
	Number      *float64          `json:"number,omitempty"`
	Boolean     *bool             `json:"boolean,omitempty"`
}
