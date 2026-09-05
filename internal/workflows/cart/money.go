package cart

import (
	"math"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
)

// Amount, quantity and rate limits.
//
// The limits are deliberately THE SAME as the ones in the cart and pricing
// modules; because the two sides do not import each other, the values are
// repeated here (the accepted price of ADR 0001). They do not have to be the
// same, they have to be SUFFICIENT: were the ceiling here larger than the
// module's, an amount this package let through would be rejected in the module
// and the error would surface at the end of the computation.
//
// The limits are not arbitrary: a line's subtotal is the unit price x the
// quantity and that product MUST FIT in an int64. Because MaxAmount x
// MaxQuantity = 10^12 x 10^6 = 10^18 < 9.22 x 10^18, overflow is structurally
// impossible.
const (
	// MinQuantity is the smallest quantity of a line.
	MinQuantity int64 = 1
	// MaxQuantity is the largest quantity of a line.
	MaxQuantity int64 = 1_000_000
	// MaxAmount is the largest permitted unit amount (minor unit).
	MaxAmount int64 = 1_000_000_000_000
	// MaxTotal is the largest value of a total field (minor unit).
	MaxTotal = MaxAmount * MaxQuantity
	// BpsScale is the basis point scale: 10000 basis points = 100%.
	BpsScale int64 = 10_000
	// MaxTaxRateBps is the largest permitted tax rate (100%).
	MaxTaxRateBps int32 = 10_000
	// maxIDLen is the upper bound for ids arriving from outside; core/link and
	// the cart module apply the same limit.
	maxIDLen = 255
)

// addAmount adds two amounts WITHOUT OVERFLOW.
//
// It returns an error when the sum exceeds [MaxTotal]. An overflowing addition
// silently produces a NEGATIVE amount, and a negative total could accidentally
// pass the cart's consistency check.
func addAmount(a, b int64) (int64, error) {
	if a < 0 || b < 0 {
		return 0, errors.Internal(CodeAmountOverflow,
			"the amounts cannot be negative: %d + %d", a, b)
	}
	if a > MaxTotal-b {
		return 0, errors.Invalid(CodeAmountOverflow,
			"the amount total exceeds the limit: %d + %d > %d", a, b, MaxTotal)
	}
	return a + b, nil
}

// mulAmount multiplies a unit price by a quantity WITHOUT OVERFLOW.
func mulAmount(unitPrice, quantity int64) (int64, error) {
	if unitPrice < 0 || quantity < 0 {
		return 0, errors.Internal(CodeAmountOverflow,
			"the unit price and the quantity cannot be negative: %d x %d", unitPrice, quantity)
	}
	if unitPrice == 0 || quantity == 0 {
		return 0, nil
	}
	if quantity > MaxTotal/unitPrice {
		return 0, errors.Invalid(CodeAmountOverflow,
			"the line subtotal exceeds the limit: %d x %d > %d", unitPrice, quantity, MaxTotal)
	}
	return unitPrice * quantity, nil
}

// taxOf computes the tax over the given base with a basis point rate.
//
// The result is rounded DOWN; the reasoning, and what the base of the tax is,
// live under the "Tax contract" heading of the package comment.
//
// # Why the division comes first
//
// Computing base x rate directly would overflow: because the base is at most
// [MaxTotal] (10^18) and the rate at most 10^4, the product climbs up to 10^22,
// while an int64 ends at 9.22 x 10^18. Moving the division first DOES NOT
// CHANGE the result — writing base = q x 10000 + r gives base x rate / 10000 =
// q x rate + (r x rate) / 10000, and because q x rate is already a whole
// number, the rounding down falls on the second term only. Both terms fit
// comfortably in an int64 (q x rate <= 10^18, r x rate < 10^8).
func taxOf(base int64, rateBps int32) (int64, error) {
	if base < 0 {
		return 0, errors.Internal(CodeAmountOverflow, "the tax base cannot be negative: %d", base)
	}
	if base > MaxTotal {
		return 0, errors.Invalid(CodeAmountOverflow,
			"the tax base exceeds the limit: %d > %d", base, MaxTotal)
	}
	if rateBps < 0 || rateBps > MaxTaxRateBps {
		return 0, errors.Internal(CodeTaxRateInvalid,
			"the tax rate must be in the range [0, %d] basis points, %d was reported", MaxTaxRateBps, rateBps)
	}
	if base == 0 || rateBps == 0 {
		return 0, nil
	}

	rate := int64(rateBps)
	whole := (base / BpsScale) * rate
	remainder := ((base % BpsScale) * rate) / BpsScale
	return whole + remainder, nil
}

// quantity32 converts a cart quantity to the int32 that pricing expects.
//
// The conversion happens only AFTER the bounds check: [MaxQuantity] (10^6) is
// far below the ceiling of an int32, so every value that passes the check fits
// without loss. An unchecked conversion would silently cut a quantity in the
// billions down to a small (or even negative) number and would pick the wrong
// price tier.
func quantity32(quantity int64) (int32, error) {
	if quantity < MinQuantity || quantity > MaxQuantity {
		return 0, errors.Invalid(CodeInvalidInput,
			"the quantity must be in the range [%d, %d], %d was given", MinQuantity, MaxQuantity, quantity)
	}
	if quantity > math.MaxInt32 {
		// Unreachable: MaxQuantity is already far smaller. The check makes sure
		// the conversion does not break silently should the constant be raised
		// later.
		return 0, errors.Internal(CodeAmountOverflow,
			"the quantity does not fit in an int32: %d", quantity)
	}
	return int32(quantity), nil
}

// checkAmount verifies that an amount is within the permitted range.
func checkAmount(label string, value, upper int64) error {
	if value < 0 {
		return errors.Invalid(CodeAmountOverflow, "%s cannot be negative: %d", label, value)
	}
	if value > upper {
		return errors.Invalid(CodeAmountOverflow,
			"%s can be at most %d: %d", label, upper, value)
	}
	return nil
}

// requireID verifies that an id arriving from outside is usable.
//
// The id is NOT TRIMMED, it is rejected: trimming separates the id the caller
// sent from the id that gets stored, and the difference only becomes visible
// after the data is corrupted. The same contract holds in core/link and in the
// cart module.
func requireID(label, value string) error {
	if value == "" {
		return errors.Invalid(CodeInvalidInput, "%s cannot be empty", label)
	}
	if strings.TrimSpace(value) != value {
		return errors.Invalid(CodeInvalidInput, "%s cannot contain leading/trailing whitespace: %q", label, value)
	}
	if len(value) > maxIDLen {
		return errors.Invalid(CodeInvalidInput,
			"%s can be at most %d bytes: %d", label, maxIDLen, len(value))
	}
	return nil
}
