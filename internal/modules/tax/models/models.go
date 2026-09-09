// Package models defines the tax module's domain models.
//
// The types here are FREE OF database types: pgtype does not enter this
// package, the conversion is done in the repository wrapper. The service and
// API layers are therefore not bound to a storage detail.
//
// # Why the rate is an integer
//
// A tax rate is a BASIS POINT: 1 basis point = 0.01%, so 10000 = 100% and
// 2000 = 20%. Plan Section 8 forbids floats in money and its derivatives; the
// float equivalent of 20% (0.2), multiplied by an amount, would produce silent
// rounding at the cent level. Floating point is used nowhere in this package.
//
// Times are UTC and deletion is SOFT (DeletedAt).
package models

import "time"

// Tax rate bounds (basis points).
const (
	// MinRateBps is the smallest permitted tax rate.
	MinRateBps int32 = 0
	// MaxRateBps is the largest permitted tax rate: 100%.
	//
	// The upper bound is deliberate: a rate greater than 100% is a data entry
	// error and would silently double the cart total.
	MaxRateBps int32 = 10_000
	// BpsPerPercent is the number of basis points in ONE PERCENT: 100 basis
	// points = 1%.
	//
	// It is NOT the basis point SCALE (10000 = 100%); it is used only as the
	// divisor that converts a rate into a percentage (see
	// [TaxRate.RatePercent]). The name is deliberately kept apart from the
	// scale: having two "BpsScale" constants with different values in the same
	// module meant that a call computing tax with this constant would produce
	// 100 TIMES too much tax and the compiler would not catch it. The basis
	// point scale stands in a single place, under the name service.BpsScale.
	BpsPerPercent int32 = 100
)

// CountryCodeLength is the letter count of an ISO 3166-1 alpha-2 code.
const CountryCodeLength = 2

// MaxProvinceCodeLength is the maximum character count of a province/state
// code.
//
// The bound is the same as the CHECK in the database. The in-country part of
// ISO 3166-2 is at most three characters; ten characters is generous enough to
// leave room for non-standard but established usages (e.g. the license plate
// code in Turkey) and narrow enough not to turn into free text.
const MaxProvinceCodeLength = 10

// RuleReference says WHICH KIND of item a tax rule looks at.
//
// The values are exactly the same as the CHECK constraint in the database; a
// typo here comes back as a constraint violation at write time.
type RuleReference string

// Rule reference kinds.
const (
	// ReferenceProduct declares that the rule looks at a single product.
	ReferenceProduct RuleReference = "product"
	// ReferenceTaxClass declares that the rule looks at a TAX CLASS: the set of
	// products a merchant taxes the same way (books, food, electronics).
	//
	// The membership lives in this module (tax_class_member) because the rate
	// is chosen here and nothing else reads the classification.
	ReferenceTaxClass RuleReference = "tax_class"
	// ReferenceProductType declares that the rule looks at a product TYPE.
	ReferenceProductType RuleReference = "product_type"
	// ReferenceShippingOption declares that the rule looks at a shipping
	// option.
	ReferenceShippingOption RuleReference = "shipping_option"
)

// String returns the textual form of the reference.
func (r RuleReference) String() string { return string(r) }

// Valid reports whether the reference is a defined kind.
func (r RuleReference) Valid() bool {
	switch r {
	case ReferenceProduct, ReferenceTaxClass, ReferenceProductType, ReferenceShippingOption:
		return true
	default:
		return false
	}
}

// Specificity returns the reference's degree of SPECIFICITY; the greater one is
// the more specific.
//
// When more than one rule matches the same item, this order decides the winner:
// a rule written for a single product beats one written for its CLASS, and a
// class beats the product's type. Without the order, which rate got applied
// would be left to map iteration order and the same cart could produce two
// different taxes in two calls.
//
// The class sits between them because it is what the merchant said about a SET
// of products, while the product rule is what they said about this one: the
// narrower statement wins, and a class beats a type because gobit's type is the
// catalog's word and the class is the tax module's own.
//
// Shipping option rules do not compete WITH ITEMS — the shipping line is
// calculated separately — so their degree is taken to be the same as a product
// type's; the two never meet on the same item.
func (r RuleReference) Specificity() int {
	switch r {
	case ReferenceProduct:
		return 3
	case ReferenceTaxClass:
		return 2
	case ReferenceProductType, ReferenceShippingOption:
		return 1
	default:
		return 0
	}
}

// TaxRegion is a tax region: a country root or a province under that root.
//
// On a root region ParentID and ProvinceCode are nil; on a province region BOTH
// are filled. There is no state in between and a database constraint enforces
// this (tax_region_hierarchy_check): a province without a parent would never be
// found, while a root carrying a province code would be a record applying a
// rate to a single province instead of to the whole country.
type TaxRegion struct {
	// ID is the "taxreg_" prefixed, time-ordered id.
	ID string
	// CountryCode is the ISO 3166-1 alpha-2 code; always stored in UPPER case.
	CountryCode string
	// ProvinceCode is the province/state code; nil on a root region.
	ProvinceCode *string
	// ParentID is the id of the root region; nil on a root region.
	ParentID *string
	// ProviderID is the tax provider's id. When empty, the country root's
	// provider is INHERITED; when the root's is empty too, local calculation
	// applies. See the tax/service package comment, "The provider
	// abstraction".
	ProviderID string
	// PricesIncludeTax says whether the prices of this market are quoted with
	// the tax already inside them.
	//
	// NIL is INHERIT, the same way an empty ProviderID is: the resolution walks
	// from the most specific region to the country root and takes the first row
	// that says something. A chain that says nothing anywhere is tax-EXCLUSIVE,
	// which is what every installation did before this field existed.
	PricesIncludeTax *bool
	// Metadata is free-form metadata; this module does not interpret its
	// content.
	Metadata map[string]any
	// CreatedAt is the instant the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the instant the record was last updated (UTC).
	UpdatedAt time.Time
	// DeletedAt is the soft delete instant; when nil the record is live.
	DeletedAt *time.Time
}

// IsRoot reports whether the region is a country root.
func (r TaxRegion) IsRoot() bool { return r.ParentID == nil }

// Province returns the province code; the empty string on a root region.
//
// Not leaking the pointer outward is deliberate: most callers only compare, and
// repeating a nil check at every call site means a panic at the single check
// that gets forgotten.
func (r TaxRegion) Province() string {
	if r.ProvinceCode == nil {
		return ""
	}
	return *r.ProvinceCode
}

// Parent returns the id of the root region; the empty string on a root region.
func (r TaxRegion) Parent() string {
	if r.ParentID == nil {
		return ""
	}
	return *r.ParentID
}

// TaxRate is a rate in a tax region.
//
// IsDefault marks the region's DEFAULT rate and there can be at most one per
// region (partial unique index). A default rate has no rule; a rate that has a
// rule is applied only to the item its rule matches.
type TaxRate struct {
	// ID is the "taxrate_" prefixed, time-ordered id.
	ID string
	// TaxRegionID is the region the rate belongs to.
	TaxRegionID string
	// Name is the rate's display name (e.g. "VAT"); it cannot be empty.
	Name string
	// Code is the reconciliation code for external systems; nil when not given.
	Code *string
	// RateBps is the rate (basis points; 2000 = 20%).
	RateBps int32
	// IsDefault is whether this is the region's default rate.
	IsDefault bool
	// Metadata is free-form metadata; this module does not interpret its
	// content.
	Metadata map[string]any
	// CreatedAt is the instant the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the instant the record was last updated (UTC).
	UpdatedAt time.Time
	// DeletedAt is the soft delete instant; when nil the record is live.
	DeletedAt *time.Time
}

// RateCode returns the reconciliation code; the empty string when not given.
func (r TaxRate) RateCode() string {
	if r.Code == nil {
		return ""
	}
	return *r.Code
}

// RatePercent returns the whole percent part of the rate and the remaining
// basis points.
//
// The percentage is NOT RETURNED as a float: 2050 basis points comes back as
// "20% and 50 basis points", that is, as two integers. The presentation layer
// combines them however it likes; this package produces floating point nowhere
// (plan Section 8).
//
// The divisor is [BpsPerPercent] (100), NOT the basis point scale (10000):
// what is wanted here is "how many percent the rate makes", not a scale to be
// multiplied by an amount.
func (r TaxRate) RatePercent() (percent, remainder int32) {
	return r.RateBps / BpsPerPercent, r.RateBps % BpsPerPercent
}

// TaxRatePatch is a PARTIAL update of a rate.
//
// A nil field means "do not touch"; a filled field is the new value. Had a full
// body been required, a client that forgot to send rate_bps in its body would
// silently zero the rate.
type TaxRatePatch struct {
	// Name is the new name; when nil the name does not change.
	Name *string
	// Code is the new reconciliation code; when nil the code does not change.
	//
	// To REMOVE the code the pointer must be filled and the value it points to
	// must be the empty string; the service turns the empty string into SQL
	// NULL. Not using a two-level pointer is deliberate: that is the only shape
	// able to carry the difference between "code": null in JSON and the field
	// not being sent at all, and its price is a doubled nil check at every call
	// site.
	Code *string
	// RateBps is the new rate (basis points); when nil the rate does not change.
	RateBps *int32
	// IsDefault is the default flag; when nil it does not change.
	IsDefault *bool
	// Metadata is the new metadata; when nil the metadata does not change.
	Metadata map[string]any
}

// Empty reports that the patch carries no field at all.
func (p TaxRatePatch) Empty() bool {
	return p.Name == nil && p.Code == nil && p.RateBps == nil &&
		p.IsDefault == nil && p.Metadata == nil
}

// Patched returns a NEW rate with the patch applied; the receiver is not
// modified.
//
// Taking a value and returning a value is deliberate: the update is applied on
// top of the row read under lock, and being a pure transformation makes it
// testable without a database.
func (r TaxRate) Patched(p TaxRatePatch) TaxRate {
	if p.Name != nil {
		r.Name = *p.Name
	}
	if p.Code != nil {
		if *p.Code == "" {
			r.Code = nil
		} else {
			code := *p.Code
			r.Code = &code
		}
	}
	if p.RateBps != nil {
		r.RateBps = *p.RateBps
	}
	if p.IsDefault != nil {
		r.IsDefault = *p.IsDefault
	}
	if p.Metadata != nil {
		r.Metadata = p.Metadata
	}
	return r
}

// TaxRateRule says WHICH item a rate is applied to.
//
// ReferenceID is an id belonging to other modules (product, fulfillment) and is
// NOT a foreign key in this module (Principle 2.2); its existence is not
// verified here.
type TaxRateRule struct {
	// ID is the "taxrule_" prefixed, time-ordered id.
	ID string
	// TaxRateID is the rate the rule is attached to.
	TaxRateID string
	// Reference is the kind of the item.
	Reference RuleReference
	// ReferenceID is the id within that kind; it cannot be empty.
	ReferenceID string
	// CreatedAt is the instant the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the instant the record was last updated (UTC).
	UpdatedAt time.Time
	// DeletedAt is the soft delete instant; when nil the record is live.
	DeletedAt *time.Time
}

// TaxClass is a set of products a merchant taxes the same way.
//
// # Why the classification lives in the tax module
//
// The rate is chosen here and nothing else reads the classification. Putting it
// on the product would put a tax word in the catalog's model — and in the
// storefront body that model is embedded in — and would make the cart's tax leg
// read the catalog a second time to answer a question this module can answer
// from its own tables.
//
// A class is named, not coded: an operator picks it by name, and two live
// classes may not share one (tax_class_name_uniq).
type TaxClass struct {
	// ID is the "taxcls_" prefixed, time-ordered id.
	ID string
	// Name is what the operator picks the class by; it cannot be empty.
	Name string
	// Metadata is the caller's free extra data.
	Metadata map[string]any
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt is the soft delete instant; when nil the record is live.
	DeletedAt *time.Time
}

// TaxClassMember binds one product to one class.
//
// The product id belongs to the catalog and IS NOT A FOREIGN KEY here
// (Principle 2.2), exactly as [TaxRateRule.ReferenceID] is not: this module
// looks at id equality and knows no catalog record. A membership left behind by
// a deleted product is harmless, because no line carrying that id enters a
// calculation any more.
type TaxClassMember struct {
	// ID is the "taxclsm_" prefixed, time-ordered id.
	ID string
	// TaxClassID is the class the product belongs to.
	TaxClassID string
	// ProductID is the catalog's product id.
	//
	// A product is in AT MOST ONE class (tax_class_member_product_uniq), and
	// that is what keeps the selection decidable: two classes would give a line
	// two match keys of equal specificity and which rate applied would fall to
	// row order.
	ProductID string
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt is the soft delete instant; when nil the record is live.
	DeletedAt *time.Time
}
