package service

import (
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// Amount and rate bounds.
//
// The bounds are deliberately CONSISTENT with the ones in the cart flow; since
// the two sides do not import each other, the values are repeated here (the
// accepted cost of ADR 0001). They do not have to be the same, they have to be
// SUFFICIENT: had the ceiling here been smaller than the caller's, a line the
// cart counts as valid would be refused in the tax calculation and the customer
// could never close the cart.
const (
	// MaxTaxableAmount is the upper bound of a single line item's taxable base
	// (minor unit).
	//
	// The value is the same as the theoretical ceiling of a line subtotal in
	// the cart flow (unit price ceiling × quantity ceiling = 10^12 × 10^6). So
	// EVERY line that can arise in the cart can enter this calculation.
	MaxTaxableAmount int64 = 1_000_000_000_000_000_000
	// MaxItems is the number of line items accepted in a single calculation.
	//
	// The bound has to exist: an unbounded list of line items is the cheapest
	// way to exhaust memory and CPU with a single request. A thousand line
	// items is far above a real cart.
	MaxItems = 1000
	// BpsScale is the basis point scale: 10000 basis points = 100%.
	BpsScale int64 = 10_000
)

// addAmount adds two amounts WITHOUT OVERFLOWING.
//
// If the sum exceeds [MaxTaxableAmount] it returns an error. An overflowing
// addition silently produces a NEGATIVE amount; a negative tax total could
// shrink the cart total without limit in the customer's favor.
func addAmount(a, b int64) (int64, error) {
	if a < 0 || b < 0 {
		return 0, errors.Internal(CodeAmountOverflow,
			"amounts cannot be negative: %d + %d", a, b)
	}
	if a > MaxTaxableAmount-b {
		return 0, errors.Invalid(CodeAmountOverflow,
			"the sum of amounts exceeds the bound: %d + %d > %d", a, b, MaxTaxableAmount)
	}
	return a + b, nil
}

// TaxIncludedIn extracts the tax CONTAINED IN a gross amount.
//
// [TaxOf] adds ON TOP of the base; this is its inverse. In markets where the
// price already includes the tax — retail prices are written this way in
// Turkey — the figure the customer sees is the gross, and the line's net base
// is DERIVED from it:
//
//	tax = gross × rate / (10000 + rate),  base = gross − tax
//
// # Why the reverse calculation is REQUIRED and "net first, then TaxOf" is NOT ENOUGH
//
// Measured (9 September 2026): feeding the extracted net back into [TaxOf]
// produces one cent TOO MUCH, and the share of amounts where this happens has a
// CLOSED FORM — rate / (10000 + rate). At Turkey's 20% KDV it is one gross in
// every SIX, at 25% one in five. The shortcut gets worse as the rate rises,
// which is the opposite of what an implementer would guess. The two
// calculations are NOT inverses of each other, and one cannot stand in for the
// other.
//
// Measurement: docs/measurements/0086-tax-inclusive-rounding.md
//
// The extraction here hits the total BY DEFINITION: the base is found by
// subtracting the tax from the gross, so base + tax always equals the gross.
// The rounding direction is the same as [TaxOf]'s (DOWN) and the remainder is
// again in the customer's favor: a tax rounded down makes the base one cent
// LARGER, and the total collected does not change.
//
// # Why it divides first
//
// The same reasoning as [TaxOf]'s, with one difference: the divisor is not
// 10000 but 10000 + rate. The product gross × rate goes up to 10^22, and int64
// ends at 9.22 × 10^18. Writing gross = q × d + m (d = 10000 + rate), the result
// becomes q × rate + (m × rate) / d; q × rate is at most 5 × 10^17, and m × rate
// is below 2 × 10^8. Measured: on 215,015 value pairs the split calculation and
// the direct calculation are exactly the same, and [FuzzTaxIncludedIn] keeps
// running the same property against math/big.
func TaxIncludedIn(gross int64, rateBps int32) (int64, error) {
	if gross < 0 {
		return 0, errors.Internal(CodeAmountOverflow, "the gross amount cannot be negative: %d", gross)
	}
	if gross > MaxTaxableAmount {
		return 0, errors.Invalid(CodeAmountOverflow,
			"the gross amount exceeds the bound: %d > %d", gross, MaxTaxableAmount)
	}
	if rateBps < models.MinRateBps || rateBps > models.MaxRateBps {
		return 0, errors.Internal(CodeRateOutOfRange,
			"the tax rate has to be within [%d, %d] basis points, %d was reported",
			models.MinRateBps, models.MaxRateBps, rateBps)
	}
	if gross == 0 || rateBps == 0 {
		return 0, nil
	}

	rate := int64(rateBps)
	divisor := BpsScale + rate
	whole := (gross / divisor) * rate
	remainder := ((gross % divisor) * rate) / divisor

	return whole + remainder, nil
}

// TaxOf computes the tax on the given base at a basis point rate.
//
// # Rounding direction
//
// The result is rounded DOWN (integer division). The error is less than one
// minor unit per line item and is always IN THE CUSTOMER'S FAVOR. Rounding to
// nearest (round-half-up) was not chosen: it collects too much from the
// customer and leaves the question "where did the excess come from" to
// reconciliation. A floating-point rate is not considered at all, per plan
// Section 8.
//
// # Why it divides first
//
// The direct base × rate calculation would overflow: since the base is at most
// [MaxTaxableAmount] (10^18) and the rate at most 10^4, the product goes up to
// 10^22, while int64 ends at 9.22 × 10^18. Moving the division first does NOT
// CHANGE the result — writing base = q × 10000 + r, base × rate / 10000 =
// q × rate + (r × rate) / 10000, and since q × rate is already an integer the
// rounding down falls only on the second term. Both terms fit comfortably into
// int64 (q × rate ≤ 10^18, r × rate < 10^8).
//
// It is exported because both the local provider and the external provider
// adapters have to use the same arithmetic; two separate implementations would
// mean two different roundings.
func TaxOf(base int64, rateBps int32) (int64, error) {
	if base < 0 {
		return 0, errors.Internal(CodeAmountOverflow, "the tax base cannot be negative: %d", base)
	}
	if base > MaxTaxableAmount {
		return 0, errors.Invalid(CodeAmountOverflow,
			"the tax base exceeds the bound: %d > %d", base, MaxTaxableAmount)
	}
	if rateBps < models.MinRateBps || rateBps > models.MaxRateBps {
		return 0, errors.Internal(CodeRateOutOfRange,
			"the tax rate has to be within [%d, %d] basis points, %d was reported",
			models.MinRateBps, models.MaxRateBps, rateBps)
	}
	if base == 0 || rateBps == 0 {
		return 0, nil
	}

	rate := int64(rateBps)
	whole := (base / BpsScale) * rate
	remainder := ((base % BpsScale) * rate) / BpsScale
	return whole + remainder, nil
}

// checkTaxableAmount verifies that a taxable base is acceptable.
//
// A negative base is REFUSED, not pulled up to zero: a negative base is the
// clearest sign that the caller made a mistake in its discount calculation, and
// silently correcting it would hide that mistake.
func checkTaxableAmount(label string, value int64) error {
	if value < 0 {
		return errors.Invalid(CodeInvalidInput, "%s cannot be negative: %d", label, value)
	}
	if value > MaxTaxableAmount {
		return errors.Invalid(CodeAmountOverflow,
			"%s can be at most %d: %d", label, MaxTaxableAmount, value)
	}
	return nil
}
