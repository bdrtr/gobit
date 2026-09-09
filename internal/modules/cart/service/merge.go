package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// CodeRegionMismatch is the refusal of a merge across regions.
const CodeRegionMismatch = "cart_region_mismatch"

// CodeCurrencyMismatch is the refusal of a merge across currencies.
const CodeCurrencyMismatch = "cart_currency_mismatch"

// MergeCart folds the source cart's lines into the target and closes the source.
//
// # Why this exists beside [Service.UpdateCart]
//
// Naming a customer on a guest cart TRANSFERS it: the cart keeps its lines and
// gains an owner. That works while the member has no cart of their own, and the
// moment they do it is refused — the member is left holding two carts and one of
// them is invisible on their next visit. This is the other half.
//
// # The quantity is SUMMED, and that is not a new decision
//
// It is [Service.AddLineItem]'s, applied to a batch. Adding the same variant
// twice raises the quantity of the one line, for three reasons written down
// there: the price tier is picked off the summed quantity, one line means one
// reservation, and the same product on two lines reads as two products. A merge
// IS the adds it replaces, and the same two adds must not answer differently for
// having been made in two sessions.
//
// # What moves, and what does not
//
// Only the LINES. The target's email, addresses, shipping method and metadata
// are untouched: the merge moves goods and not identity, and the address a
// member chose while signed in is not something a guest session gets to
// overwrite.
//
// # What is refused
//
// A source in another region or currency (409). A line's unit price is a
// snapshot priced FOR a region and a currency; carrying it across would put a
// price into a cart where it was never valid.
//
// A source that belongs to another customer (409), which is
// [Service.UpdateCart]'s refusal seen from the other end.
//
// A completed cart on either side (409), because a completed cart is the record
// an order rests on.
func (s *Service) MergeCart(ctx context.Context, sourceID, targetID string) (models.Cart, error) {
	if err := requireID("source_cart_id", sourceID); err != nil {
		return models.Cart{}, err
	}
	if err := requireID("target_cart_id", targetID); err != nil {
		return models.Cart{}, err
	}
	if sourceID == targetID {
		return models.Cart{}, errors.Invalid(CodeInvalidInput,
			"a cart cannot be merged into itself: %s", sourceID)
	}

	var merged models.Cart
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		source, target, err := s.lockPair(ctx, sourceID, targetID)
		if err != nil {
			return err
		}
		if err := mergeable(source, target); err != nil {
			return err
		}

		moved, err := s.foldLines(ctx, source, target)
		if err != nil {
			return err
		}
		if err := s.closeSource(ctx, source.ID); err != nil {
			return err
		}

		// The revision is the totals workflow's signal, and a merge that moved
		// nothing changed no shape. Bumping anyway would stale the target's
		// totals and buy a repricing round for an empty cart.
		if moved == 0 {
			merged = target
			return nil
		}
		merged, err = s.store.BumpCartRevision(ctx, target.ID)

		return err
	})
	if err != nil {
		return models.Cart{}, err
	}

	return merged, nil
}

// lockPair locks both carts in a fixed order and returns them the way they were
// asked for.
//
// The order is by IDENTIFIER and not by role. Two merges running the other way
// round — a phone folding into a laptop while the laptop folds into the phone —
// would each hold the row the other waits for, and PostgreSQL would break the
// deadlock by killing one of them. Sorting the pair means both take the same row
// first, so one waits instead.
func (s *Service) lockPair(
	ctx context.Context, sourceID, targetID string,
) (source, target models.Cart, err error) {
	first, second := sourceID, targetID
	if first > second {
		first, second = second, first
	}

	former, err := s.store.LockCart(ctx, first)
	if err != nil {
		return models.Cart{}, models.Cart{}, err
	}
	latter, err := s.store.LockCart(ctx, second)
	if err != nil {
		return models.Cart{}, models.Cart{}, err
	}

	if first == sourceID {
		return former, latter, nil
	}

	return latter, former, nil
}

// mergeable reports whether the two carts may be folded together.
func mergeable(source, target models.Cart) error {
	if source.Completed() {
		return completedError(source.ID)
	}
	if target.Completed() {
		return completedError(target.ID)
	}
	if source.RegionID != target.RegionID {
		return errors.Conflict(CodeRegionMismatch,
			"the carts are in different regions: %s and %s", source.RegionID, target.RegionID)
	}
	if source.CurrencyCode != target.CurrencyCode {
		return errors.Conflict(CodeCurrencyMismatch,
			"the carts are in different currencies: %s and %s",
			source.CurrencyCode, target.CurrencyCode)
	}
	if source.CustomerID != "" && source.CustomerID != target.CustomerID {
		return errors.Conflict(CodeCustomerMismatch,
			"the cart belongs to another customer: %s (merging into: %s)",
			source.CustomerID, target.CustomerID)
	}

	return nil
}

// foldLines carries the source's lines onto the target and reports how many
// moved.
func (s *Service) foldLines(
	ctx context.Context, source, target models.Cart,
) (int, error) {
	lines, err := s.store.ListLineItems(ctx, source.ID)
	if err != nil {
		return 0, err
	}

	for i := range lines {
		line := lines[i]

		existing, err := s.store.GetLineItemByVariant(ctx, target.ID, line.VariantID)
		switch {
		case err == nil:
			if existing.Quantity > models.MaxQuantity-line.Quantity {
				return 0, errors.Invalid(CodeInvalidInput,
					"the line quantity exceeds the limit once merged: %d + %d > %d",
					existing.Quantity, line.Quantity, models.MaxQuantity)
			}
			if _, err := s.store.SetLineItemQuantity(ctx, target.ID, existing.ID,
				existing.Quantity+line.Quantity); err != nil {
				return 0, err
			}
		case errors.IsNotFound(err):
			// The title, the unit price and the metadata travel with the line.
			// The price is the snapshot the source's session was quoted; it is
			// of the same region and currency, and the next totals round
			// reprices it exactly as it reprices a line that had been sitting in
			// the target all along.
			if _, err := s.store.CreateLineItem(ctx, models.LineItem{
				ID:        models.NewLineItemID(),
				CartID:    target.ID,
				VariantID: line.VariantID,
				Title:     line.Title,
				Quantity:  line.Quantity,
				UnitPrice: line.UnitPrice,
				Metadata:  line.Metadata,
			}); err != nil {
				return 0, err
			}
		default:
			return 0, err
		}
	}

	return len(lines), nil
}

// closeSource empties the merged cart and deletes it.
//
// It is [Service.DeleteCart]'s body without the lock and the completeness check,
// which the merge has already done. Leaving the source alive is what makes this
// necessary rather than tidy: a cart still holding its lines can still be
// completed, and the same goods would be bought twice.
func (s *Service) closeSource(ctx context.Context, sourceID string) error {
	if err := s.store.SoftDeleteLineItemsByCart(ctx, sourceID); err != nil {
		return err
	}
	if err := s.store.SoftDeleteCartAddressesByCart(ctx, sourceID); err != nil {
		return err
	}
	if err := s.store.SoftDeleteShippingMethodsByCart(ctx, sourceID); err != nil {
		return err
	}

	return s.store.SoftDeleteCart(ctx, sourceID)
}
