package checkout

import (
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// Amount and quantity limits.
//
// The limits are deliberately CONSISTENT with the ones in the cart, order and
// payment modules; because the sides do not import each other the values are
// repeated here (the accepted price of ADR 0001). They do not have to be
// identical, they have to be SUFFICIENT: were the ceiling here larger than the
// module's, an amount this package let through would be rejected by the module and
// the error would only surface after the stock had been reserved.
//
// The limits are not arbitrary: a line subtotal is the unit price x the quantity
// and that product MUST FIT into an int64. Because MaxAmount x MaxQuantity =
// 10^12 x 10^6 = 10^18 < 9.22 x 10^18, an overflow is structurally impossible.
const (
	// MinQuantity is the smallest quantity of a line.
	MinQuantity int64 = 1
	// MaxQuantity is the largest quantity of a line.
	MaxQuantity int64 = 1_000_000
	// MaxAmount is the largest unit amount allowed (minor unit).
	MaxAmount int64 = 1_000_000_000_000
	// MaxTotal is the largest value of a total field (minor unit).
	MaxTotal = MaxAmount * MaxQuantity
	// maxIDLen is the upper bound for identifiers that come from the outside; the
	// core/link, cart and order modules apply the same limit.
	maxIDLen = 255
)

// MaxCartIDLen is the longest cart identifier this workflow accepts.
//
// The limit comes from the idempotency key: the key is the concatenation of
// [IdempotencyKeyPrefix] and the cart identifier, and the engine bounds it at
// MaxIdempotencyKeyLen bytes. Rejecting the identifier here makes it possible to
// return with no side effect applied and with an understandable message; letting
// the limit be caught by the engine would instead produce an error such as "the
// idempotency key is too long", one that cannot be related to the field the caller
// sent.
const MaxCartIDLen = workflow.MaxIdempotencyKeyLen - len(IdempotencyKeyPrefix)

// requireID verifies that an identifier coming from the outside is usable.
//
// The identifier is NOT TRIMMED, it is rejected: trimming separates the identifier
// the caller sent from the one that is stored, and the difference only becomes
// visible after the data has been corrupted. The same contract holds in the
// core/link, cart and order modules.
func requireID(label, value string, upper int) error {
	if value == "" {
		return errors.Invalid(CodeInvalidInput, "%s cannot be empty", label)
	}
	if strings.TrimSpace(value) != value {
		return errors.Invalid(CodeInvalidInput, "%s cannot contain leading/trailing whitespace: %q", label, value)
	}
	if len(value) > upper {
		return errors.Invalid(CodeInvalidInput,
			"%s can be at most %d bytes: %d", label, upper, len(value))
	}
	return nil
}

// checkAmount verifies that an amount is within the allowed range.
func checkAmount(label string, value, upper int64) error {
	if value < 0 {
		return errors.Internal(CodeAmountInvalid, "%s cannot be negative: %d", label, value)
	}
	if value > upper {
		return errors.Internal(CodeAmountInvalid,
			"%s can be at most %d: %d", label, upper, value)
	}
	return nil
}

// mulAmount multiplies a unit price by a quantity WITHOUT OVERFLOW.
//
// The product is computed only to VERIFY the arithmetic; this package does not
// produce the amount.
func mulAmount(unitPrice, quantity int64) (int64, error) {
	if unitPrice < 0 || quantity < 0 {
		return 0, errors.Internal(CodeAmountInvalid,
			"the unit price and the quantity cannot be negative: %d x %d", unitPrice, quantity)
	}
	if unitPrice == 0 || quantity == 0 {
		return 0, nil
	}
	if quantity > MaxTotal/unitPrice {
		return 0, errors.Internal(CodeAmountInvalid,
			"the line subtotal exceeds the limit: %d x %d > %d", unitPrice, quantity, MaxTotal)
	}
	return unitPrice * quantity, nil
}

// addAmount adds two amounts WITHOUT OVERFLOW.
func addAmount(a, b int64) (int64, error) {
	if a < 0 || b < 0 {
		return 0, errors.Internal(CodeAmountInvalid, "the amounts cannot be negative: %d + %d", a, b)
	}
	if a > MaxTotal-b {
		return 0, errors.Internal(CodeAmountInvalid,
			"the amount total exceeds the limit: %d + %d > %d", a, b, MaxTotal)
	}
	return a + b, nil
}
