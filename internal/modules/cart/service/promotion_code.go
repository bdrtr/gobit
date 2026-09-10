package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// MaxPromotionCodes is the largest number of coupon codes a cart may hold.
//
// A limit has to exist: every code becomes a database read and a rule evaluation
// inside the discount round, so an unbounded list would let one cart make every
// totals call expensive.
//
// The number is the promotion module's own `MaxCodesPerCompute`, restated
// because this module cannot import it (Principle 2.1). Drift is SAFE in one
// direction and only one: were promotion's limit lowered, the round would refuse
// the request, the flow would remove the code it had just written, and the
// shopper would be told the code cannot be applied. What must not happen is a
// cart that can never be priced again, and that is what this ceiling prevents.
// The relation is pinned behaviourally by the end-to-end test that fills a cart
// to this number and prices it.
const MaxPromotionCodes = 20

// maxPromotionCodeLen bounds one code.
//
// It is promotion's `MaxCodeLen`, restated for the same reason and with the same
// safe drift: a code this module accepts and that one refuses comes back
// unmatched, which the flow reports as a code that cannot be applied.
const maxPromotionCodeLen = 64

// CodeTooManyPromotionCodes is the refusal of a cart holding more codes than the
// discount round can carry.
const CodeTooManyPromotionCodes = "cart_too_many_promotion_codes"

// AddPromotionCode writes a coupon code onto the cart and returns the codes it
// holds afterwards.
//
// # What this module does NOT decide
//
// Whether the code names a real promotion. This module cannot ask the promotion
// module (Principle 2.1), so the code is stored as TEXT and what it means is
// settled by the discount round. The caller that CAN ask is the cart flow, and
// it does: a code the round cannot match is removed again and refused.
//
// What is checked here is what this module can defend alone — a code that is
// empty, one longer than the column should hold, and a cart already carrying as
// many as the round can take.
//
// # The shape counter goes up
//
// A code changes what the cart COSTS, so the totals no longer belong to its
// current shape. Skipping the bump would let a cart be completed at the total it
// had before the coupon — which is the same defect as completing a cart whose
// line was added after the totals were computed.
func (s *Service) AddPromotionCode(
	ctx context.Context, cartID, code string,
) ([]string, error) {
	normalized, err := normalizePromotionCode(code)
	if err != nil {
		return nil, err
	}

	var codes []string
	_, err = s.mutate(ctx, cartID, func(ctx context.Context, cart models.Cart) error {
		existing, err := s.store.ListPromotionCodes(ctx, cart.ID)
		if err != nil {
			return err
		}
		// A code the cart already holds is a double press, not a second coupon:
		// the write absorbs it and the ceiling is not spent on it either.
		for i := range existing {
			if existing[i] == normalized {
				codes = existing
				return nil
			}
		}
		if len(existing) >= MaxPromotionCodes {
			return errors.Invalid(CodeTooManyPromotionCodes,
				"the cart can hold at most %d coupon codes", MaxPromotionCodes)
		}
		if err := s.store.AddPromotionCode(ctx, cart.ID, normalized); err != nil {
			return err
		}

		codes, err = s.store.ListPromotionCodes(ctx, cart.ID)

		return err
	})
	if err != nil {
		return nil, err
	}

	return codes, nil
}

// RemovePromotionCode takes a coupon code off the cart.
//
// The row is DELETED rather than stamped, and it is the one write in this module
// that is: the row is a binding and not a record of something that happened. A
// coupon the shopper decided against should leave nothing behind, and what the
// shop wants to remember — which promotion was actually spent — is written at
// order time, in the promotion module's own ledger.
func (s *Service) RemovePromotionCode(ctx context.Context, cartID, code string) error {
	normalized, err := normalizePromotionCode(code)
	if err != nil {
		return err
	}

	_, err = s.mutate(ctx, cartID, func(ctx context.Context, cart models.Cart) error {
		return s.store.RemovePromotionCode(ctx, cart.ID, normalized)
	})

	return err
}

// normalizePromotionCode trims a code and puts it in UPPER case.
//
// The case is a STORAGE decision and it is the promotion module's: a coupon is
// not case sensitive, so "summer20" and "SUMMER20" are one coupon. Keeping the
// difference would let a cart hold both and ask the other module about a code it
// cannot match.
func normalizePromotionCode(code string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	if normalized == "" {
		return "", errors.Invalid(CodeInvalidInput, "code cannot be empty")
	}
	if len(normalized) > maxPromotionCodeLen {
		return "", errors.Invalid(CodeInvalidInput,
			"code can be at most %d characters (given: %d)",
			maxPromotionCodeLen, len(normalized))
	}
	// ASCII ONLY, and the reason is the column's CHECK rather than taste.
	//
	// The row asserts `code = upper(code)`, and upper() folds the letters the
	// CLUSTER's CTYPE knows: on a --locale=C database that is ASCII and nothing
	// else. A code carrying "é" would be upper-cased differently by Go and by the
	// database, so the same insert would succeed on one installation and be
	// refused on another. Keeping the value ASCII makes the two agree everywhere.
	//
	// The promotion module's own alphabet is narrower still (letters, digits,
	// hyphen and underscore) and this module deliberately does not restate it:
	// what a code MEANS is that module's to decide, and a code this one accepts
	// and that one cannot use comes back as a code that cannot be applied.
	for i := range len(normalized) {
		if normalized[i] >= 0x80 {
			return "", errors.Invalid(CodeInvalidInput,
				"code can only contain ASCII characters: %q", code)
		}
	}

	return normalized, nil
}
