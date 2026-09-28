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

// CodeAdditionMismatch is the refusal of a merge between carts that do not add
// to the same order.
const CodeAdditionMismatch = "cart_addition_mismatch"

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
// Two carts that do not add to the same order (409), one of them adding to
// nothing included (ADR 0192). The order a cart adds to is fixed when it is
// opened, so a merge would either carry lines into an addition the shopper did
// not open or out of the one they did.
//
// A completed cart on either side (409), because a completed cart is the record
// an order rests on.
//
// A fold that would open lines past [MaxLineItems] (422, ADR 0227), refused
// whole: the ceiling holds on the merged cart as it does on an add, and a
// merged cart past it would cost what the ceiling exists to bound.
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
		codes, err := s.foldCodes(ctx, source, target)
		if err != nil {
			return err
		}
		moved += codes

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
	if source.AddsToOrderID != target.AddsToOrderID {
		return errors.Conflict(CodeAdditionMismatch,
			"the carts do not add to the same order: %q and %q",
			source.AddsToOrderID, target.AddsToOrderID)
	}

	return nil
}

// foldLines carries the source's lines onto the target and reports how many
// moved.
//
// It walks twice (ADR 0229): the lines standing on their own first, each merged
// by its identity — variant, properties and the add-ons it carries — and then
// the add-ons, each following its line. A line merged into one the target
// already holds brings the same add-ons, which are already there and follow the
// summed quantity; a line opened on the target opens its add-ons under it.
func (s *Service) foldLines(
	ctx context.Context, source, target models.Cart,
) (int, error) {
	lines, err := s.store.ListLineItems(ctx, source.ID)
	if err != nil {
		return 0, err
	}
	// The target's lines are counted once; every line the fold opens adds one,
	// and the ceiling refuses the whole merge rather than half of it.
	held, err := s.store.CountLineItems(ctx, target.ID)
	if err != nil {
		return 0, err
	}

	// opened maps a source line to the target line it opened; raised holds the
	// source lines merged into a target line, whose add-ons are already there.
	opened := map[string]string{}
	raised := map[string]bool{}
	for i := range lines {
		line := lines[i]
		if line.ParentLineID != nil {
			continue
		}

		existing, err := s.store.GetLineItemByVariant(ctx, target.ID, line.VariantID, line.Properties, line.AddOnKey)
		switch {
		case err == nil:
			if existing.Quantity > models.MaxQuantity-line.Quantity {
				return 0, errors.Invalid(CodeInvalidInput,
					"the line quantity exceeds the limit once merged: %d + %d > %d",
					existing.Quantity, line.Quantity, models.MaxQuantity)
			}
			summed := existing.Quantity + line.Quantity
			if _, err := s.store.SetLineItemQuantity(ctx, target.ID, existing.ID, summed); err != nil {
				return 0, err
			}
			if line.AddOnKey != "" {
				if err := s.store.SetAddOnQuantities(ctx, target.ID, existing.ID, summed); err != nil {
					return 0, err
				}
			}
			raised[line.ID] = true
		case errors.IsNotFound(err):
			// The title, the unit price, the metadata and the properties travel
			// with the line; the properties are also what it merged by (ADR 0223).
			// The price is the snapshot the source's session was quoted; it is
			// of the same region and currency, and the next totals round
			// reprices it exactly as it reprices a line that had been sitting in
			// the target all along.
			created, err := s.openLine(ctx, models.LineItem{
				ID:         models.NewLineItemID(),
				CartID:     target.ID,
				VariantID:  line.VariantID,
				Title:      line.Title,
				Quantity:   line.Quantity,
				UnitPrice:  line.UnitPrice,
				Metadata:   line.Metadata,
				Properties: line.Properties,
				AddOnKey:   line.AddOnKey,
			}, held)
			if err != nil {
				return 0, err
			}
			held++
			opened[line.ID] = created.ID
		default:
			return 0, err
		}
	}

	for i := range lines {
		line := lines[i]
		if line.ParentLineID == nil {
			continue
		}
		parent := *line.ParentLineID
		if raised[parent] {
			continue
		}
		targetParent, ok := opened[parent]
		if !ok {
			return 0, errors.Internal(CodeInvalidInput,
				"the add-on line %s names %s, which the merge did not carry", line.ID, parent)
		}
		if _, err := s.openLine(ctx, models.LineItem{
			ID:           models.NewLineItemID(),
			CartID:       target.ID,
			VariantID:    line.VariantID,
			Title:        line.Title,
			Quantity:     line.Quantity,
			UnitPrice:    line.UnitPrice,
			Metadata:     line.Metadata,
			Properties:   line.Properties,
			ParentLineID: &targetParent,
		}, held); err != nil {
			return 0, err
		}
		held++
	}

	return len(lines), nil
}

// foldCodes carries the source's coupon codes onto the target and reports how
// many were not already there.
//
// The codes travel with the goods, and this AMENDS what ADR 0107 wrote: when the
// merge was decided the cart could not hold a code at all, so "only the lines
// move" described everything there was. Losing the coupon a shopper typed on
// their phone is the same complaint the merge exists to answer, one field over.
//
// The union is idempotent for the line rule's reason: a code the target already
// holds is a double press, not a second coupon.
func (s *Service) foldCodes(ctx context.Context, source, target models.Cart) (int, error) {
	codes, err := s.store.ListPromotionCodes(ctx, source.ID)
	if err != nil {
		return 0, err
	}
	if len(codes) == 0 {
		return 0, nil
	}

	held, err := s.store.ListPromotionCodes(ctx, target.ID)
	if err != nil {
		return 0, err
	}
	already := make(map[string]struct{}, len(held))
	for i := range held {
		already[held[i]] = struct{}{}
	}

	moved := 0
	for i := range codes {
		if _, there := already[codes[i]]; there {
			continue
		}
		if len(already)+moved >= MaxPromotionCodes {
			// The ceiling holds on the merged cart too. Carrying past it would
			// produce a cart the discount round refuses, which cannot be priced
			// and therefore cannot be bought.
			return 0, errors.Invalid(CodeTooManyPromotionCodes,
				"the merged cart would hold more than %d coupon codes", MaxPromotionCodes)
		}
		if err := s.store.AddPromotionCode(ctx, target.ID, codes[i]); err != nil {
			return 0, err
		}
		moved++
	}

	return moved, nil
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
	if err := s.store.DeletePromotionCodesByCart(ctx, sourceID); err != nil {
		return err
	}

	return s.store.SoftDeleteCart(ctx, sourceID)
}
