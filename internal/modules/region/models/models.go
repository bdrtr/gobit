// Package models defines the region module's domain models.
//
// The types here are FREE OF database types: pgtype does not enter this
// package, the conversion is done in the repository wrapper. The service and
// API layers are therefore not bound to a storage detail.
//
// Money passes through this module not as an AMOUNT but as a DEFINITION: a
// region carries a currency CODE, it carries no amount. That an amount is a
// minor unit integer (plan Section 8) concerns this module at this one point:
// [Currency.DecimalDigits] says with which factor that integer becomes readable
// by a human.
// Times are UTC.
package models

import "time"

// Tax rate bounds.
//
// The rate is in BASIS POINTS: 1 basis point = 0.01%, so 10000 = 100%. Being an
// integer is deliberate — plan Section 8 forbids floats in money and its
// derivatives; the float equivalent of 20% (0.2), multiplied by an amount,
// would produce silent rounding at the cent level.
const (
	// MinTaxRate is the smallest permitted tax rate (basis points).
	MinTaxRate int32 = 0
	// MaxTaxRate is the largest permitted tax rate (basis points): 100%.
	MaxTaxRate int32 = 10_000
	// TaxRateScale is the basis point scale; the rate is divided by it to turn
	// it into a percentage.
	TaxRateScale int32 = 100
)

// Decimal digit bounds.
//
// The upper bound is four: the highest minor unit exponent in use in ISO 4217
// is 4 (e.g. UYW). The existence of the bound also guarantees that the 10^n
// factor of [Currency.MinorUnitFactor] stays in a reasonable range.
const (
	// MinDecimalDigits is the smallest permitted number of decimal digits.
	MinDecimalDigits int32 = 0
	// MaxDecimalDigits is the largest permitted number of decimal digits.
	MaxDecimalDigits int32 = 4
)

// CurrencyCodeLength is the letter count of an ISO 4217 alphabetic code.
const CurrencyCodeLength = 3

// CountryCodeLength is the letter count of an ISO 3166-1 alpha-2 code.
const CountryCodeLength = 2

// Currency is an ISO 4217 currency and it is REFERENCE DATA.
//
// The records are seeded by a migration (see 000002_region_seed); the module
// has no write surface. The reason is simple: ISO 4217 is a standard that comes
// from outside, and every installation entering it by hand would mean that a
// code entered incompletely makes it impossible to write a price in that
// currency.
type Currency struct {
	// Code is the ISO 4217 alphabetic code; it is always stored in UPPER case
	// (e.g. "TRY").
	Code string
	// Symbol is the currency's display symbol (e.g. "₺").
	Symbol string
	// Name is the currency's English name in ISO; localization is the
	// storefront's job.
	Name string
	// DecimalDigits is how many decimal digits the unit has (TRY/USD 2, JPY 0,
	// KWD 3). Since money is stored as a minor unit integer, the presentation
	// layer learns the division factor FROM HERE; see
	// [Currency.MinorUnitFactor].
	DecimalDigits int32
	// CreatedAt is the moment the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the moment the record was last updated (UTC).
	UpdatedAt time.Time
	// DeletedAt is the soft delete moment; if nil the record is live.
	DeletedAt *time.Time
}

// MinorUnitFactor returns how many minor units one major unit makes
// (10^digits).
//
// This is the factor that makes the stored integer amount showable to a human:
// 1999 TRY minor units -> 1999 / 100 = 19.99 ₺; 1999 JPY minor units -> 1999 /
// 1 = 1999 ¥. A presentation layer assuming a fixed 100 would show a yen amount
// a hundred times too small and a dinar amount ten times too large.
//
// The division is done by the CALLER and has to be done with integers; this
// package produces a float nowhere (plan Section 8). For an undefined (out of
// range) digit count it returns 1: a factor of zero meant division by zero in
// the caller.
func (c Currency) MinorUnitFactor() int64 {
	if c.DecimalDigits < MinDecimalDigits || c.DecimalDigits > MaxDecimalDigits {
		return 1
	}
	factor := int64(1)
	for i := int32(0); i < c.DecimalDigits; i++ {
		factor *= 10
	}
	return factor
}

// Country is an ISO 3166-1 alpha-2 country and it is REFERENCE DATA.
//
// The records are seeded by a migration; the module's write surface changes
// only which region the country belongs to.
type Country struct {
	// Code is the ISO 3166-1 alpha-2 code; it is always stored in UPPER case
	// (e.g. "TR").
	Code string
	// Name is the country's English short name in ISO.
	Name string
	// RegionID is the region the country is attached to; if nil the country
	// belongs to no region. Because the field is SINGLE, the rule "a country
	// belongs to at most one region" is structural — there is no place for
	// belonging to a second region.
	RegionID *string
	// CreatedAt is the moment the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the moment the record was last updated (UTC).
	UpdatedAt time.Time
	// DeletedAt is the soft delete moment; if nil the record is live.
	DeletedAt *time.Time
}

// Region is a sales region: currency and tax behavior.
//
// The cart takes its currency and its tax region from here; the region is the
// foundation of the cart flow for that reason. The region does NOT KNOW carts
// or orders — the bond is made over Module Links and region never sees that
// bond (Principle 2.1/2.2).
type Region struct {
	// ID is the time-ordered id prefixed with "reg_".
	ID string
	// Name is the region's display name; it cannot be empty.
	Name string
	// CurrencyCode is the region's currency (ISO 4217, UPPER case). It has to
	// point at a defined currency; the validation is done a second time by the
	// foreign key in the database.
	CurrencyCode string
	// AutomaticTaxes states whether the tax will be applied to the cart total
	// automatically.
	AutomaticTaxes bool
	// TaxRate is the region's FALLBACK tax rate (basis points; 2000 = 20%).
	//
	// In Phase 7 the tax module TOOK OVER the tax calculation; this field
	// REMAINED as the FALLBACK path of the cart flow and must not be removed:
	// when the tax module is not registered, or when a country cannot be
	// resolved from the region, the tax is computed from here. So that it can
	// work, a single and simple rate is carried on the region; when the rule
	// grows complicated (a rate by product kind, exemptions, a registered tax
	// number) this field WILL BE REMOVED and no new rule should be added here.
	TaxRate int32
	// CreatedAt is the moment the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the moment the record was last updated (UTC).
	UpdatedAt time.Time
	// DeletedAt is the soft delete moment; if nil the record is live.
	DeletedAt *time.Time
}

// TaxRatePercent returns the whole percent part of the rate and the remaining
// basis points.
//
// The percentage is NOT RETURNED as a float: 2050 basis points comes back as
// "20% and 50 basis points", that is, as two integers. The presentation layer
// combines them in whichever form it wants; this package produces floating
// point nowhere (plan Section 8).
func (r Region) TaxRatePercent() (percent, remainder int32) {
	return r.TaxRate / TaxRateScale, r.TaxRate % TaxRateScale
}

// RegionPatch is the PARTIAL update of a region.
//
// A nil field means "do not touch"; a filled field is the new value. The
// alternative to a partial update would be demanding the whole body, and in
// that case a client that forgets to send tax_rate in its body would silently
// zero the rate.
type RegionPatch struct {
	// Name is the new name; if nil the name does not change.
	Name *string
	// CurrencyCode is the new currency code; if nil the currency does not change.
	CurrencyCode *string
	// AutomaticTaxes states whether the tax is applied automatically; if nil it
	// does not change.
	AutomaticTaxes *bool
	// TaxRate is the new tax rate (basis points); if nil the rate does not change.
	TaxRate *int32
}

// Empty reports that the patch carries no field at all.
func (p RegionPatch) Empty() bool {
	return p.Name == nil && p.CurrencyCode == nil && p.AutomaticTaxes == nil && p.TaxRate == nil
}

// Patched returns a NEW region with the patch applied; the receiver is not
// modified.
//
// Taking a value and returning a value is deliberate: the update is applied on
// top of the row read under a lock, and being a pure transformation makes it
// testable without a database.
func (r Region) Patched(p RegionPatch) Region {
	if p.Name != nil {
		r.Name = *p.Name
	}
	if p.CurrencyCode != nil {
		r.CurrencyCode = *p.CurrencyCode
	}
	if p.AutomaticTaxes != nil {
		r.AutomaticTaxes = *p.AutomaticTaxes
	}
	if p.TaxRate != nil {
		r.TaxRate = *p.TaxRate
	}
	return r
}
