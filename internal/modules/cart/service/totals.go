package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// Totals are the cart totals the workflow calculated.
//
// All of the fields are WHOLE NUMBERS in the minor unit (plan Section 8).
// DiscountTotal is given as a POSITIVE number and is subtracted from the total;
// a negative discount is not a discount but a surcharge.
type Totals struct {
	// Revision is the cart shape the calculation RESTS ON
	// ([models.Cart.Revision]); it is REQUIRED.
	//
	// The workflow reads the cart, does its calculation OUTSIDE THE LOCK and
	// writes the result with this method; the cart may have changed between the
	// read and the write. This field forces the caller to DECLARE which shape
	// the calculation belongs to (see [Service.SetTotals]).
	//
	// The default zero is a VALID value — it is the shape of a cart that has
	// never been changed — so it is not interpreted as "not given" and there is
	// no fallback to the old behavior: a caller who forgets to fill the field in
	// gets an error if the cart has changed.
	Revision int64
	// Subtotal is the sum of the line subtotals.
	Subtotal int64
	// DiscountTotal is the total discount; it is given as a positive number.
	DiscountTotal int64
	// TaxTotal is the total tax.
	TaxTotal int64
	// ShippingTotal is the total shipping amount.
	ShippingTotal int64
	// Total is the amount payable:
	// Subtotal - DiscountTotal + TaxTotal + ShippingTotal.
	Total int64
	// Lines are the amounts calculated per line and they MUST COVER ALL of the
	// cart's lines; each line is given EXACTLY ONCE.
	//
	// A line left out is NOT accepted (see [Service.SetTotals]). On a cart
	// without lines this field stays empty; only in that case can a "write the
	// shipping only" call be made without lines.
	Lines []LineTotals
}

// LineTotals are the calculated amounts of a single cart line.
//
// The quantity IS NOT HERE: the quantity is the cart service's data, the amounts
// are the workflow's. The separation is deliberate — a calculation round cannot
// silently change the quantity.
type LineTotals struct {
	// LineItemID is the line the amounts belong to; it is REQUIRED.
	LineItemID string
	// UnitPrice is the unit price (minor unit).
	UnitPrice int64
	// Subtotal is the line's subtotal: UnitPrice x Quantity.
	Subtotal int64
	// DiscountTotal is the discount falling on the line; it is given as a
	// positive number.
	DiscountTotal int64
	// TaxTotal is the tax falling on the line.
	TaxTotal int64
	// Total is the line's total: Subtotal - DiscountTotal + TaxTotal.
	Total int64
}

// SetTotals writes the totals the workflow calculated to the cart.
//
// This method is the ONLY write surface the cart module opens to the
// calculate_totals workflow. The module does not calculate the price or the tax
// itself (ADR 0006); what it does here is to validate that the incoming result
// is consistent AS A WHOLE and, if it is, to store it.
//
// # A call IS A COMPLETE CALCULATION ROUND
//
// SetTotals does not accept a partial update: [Totals.Lines] must cover all of
// the cart's lines AT THAT MOMENT. The contract is deliberately this narrow,
// because the alternative was silently producing wrong amounts: the STORED
// amounts of a line left out were being preserved as they were, and the stored
// subtotal of a newly opened line is ZERO ([Service.AddLineItem] writes only the
// quantity and the first unit price). That is, "forgetting to send the lines" —
// the most likely mistake the workflow can make — was showing an unpriced cart
// as CONSISTENT with `Subtotal: 0, Total: 0`, and the cart could be completed
// with a total of 0. The coverage requirement closes that path: every line is
// written by the caller EXPLICITLY declaring its amount, and every declared line
// goes through the multiplication check.
//
// The price of this is that there is no shortcut like "update the shipping
// only". The price does not really exist: every operation that changes the
// cart's shape already makes the totals stale ([models.Cart.TotalsStale]) and
// requires calculate_totals to run from the start; the workflow prices all of
// the lines on every round anyway.
//
// # THE CALLER declares the shape the calculation rests on
//
// [Totals.Revision] is required and it must match the cart's shape at the moment
// of the write EXACTLY; if it does not match, errors.Conflict (CodeTotalsStale)
// is returned and nothing is written. The stamp is applied with the shape the
// caller declared as well.
//
// The reason is that the calculation is done OUTSIDE the lock: the workflow
// first reads the cart, then calls pricing and tax, and writes here at the very
// end. Had the stamp been taken from the shape at the moment of the write, a
// line being added to the cart or a shipping method being chosen between the
// read and the write would stamp a STALE calculation as CURRENT;
// [Service.MarkCompleted]'s staleness gate would open too and the customer would
// pay less than the goods in their cart. With the caller declaring the shape,
// that round is rejected and the workflow recalculates.
//
// # Validation layers
//
// The order runs from cheap to expensive; the lock is held only as long as it
// needs to be.
//
//  1. Range: no amount can be negative and none can exceed the upper bound
//     (overflow protection).
//  2. Cart identity: Total = Subtotal - DiscountTotal + TaxTotal + ShippingTotal.
//     If it does not hold, errors.Invalid; a calculation error in the workflow
//     cannot be written to the database SILENTLY.
//  3. Shape: [Totals.Revision] must equal the cart's current shape.
//  4. Coverage: every line of the cart must have been given EXACTLY ONCE; an
//     unknown, repeated or skipped line is rejected.
//  5. Line subtotal: Subtotal = UnitPrice x Quantity. Because the quantity is
//     the cart's own data, this is the only place that can validate this
//     multiplication; a line priced with the wrong quantity would be caught at
//     no other gate.
//  6. Line identity: for every line, Total = Subtotal - DiscountTotal + TaxTotal.
//  7. Cart subtotal: Subtotal is the SUM of the line subtotals. Because the
//     discount and the tax can also arise at the cart level (a campaign,
//     shipping tax), only the subtotal is subject to this rule.
//
// All of the validation is done BEFORE THE WRITE: there is no partially written
// calculation round. The write happens in a single transaction and under the
// cart's lock.
//
// # The write is a SINGLE statement
//
// All of the line amounts are given to [Store.SetLineItemTotals] in one call and
// become a single UPDATE. It used to run one UPDATE per line, and that held the
// cart's lock for a duration DIRECTLY PROPORTIONAL to the number of lines:
// because the lock queues up EVERY flow that writes to that cart, the duration
// is directly the cart's write capacity.
//
// Measured (local container, TCP round trip ~30 µs, a 100-line cart, from the
// taking of the lock to the return of the LAST WRITE, p50): one UPDATE per line
// 8.0 ms, a single statement 0.55 ms. The number of lines, up to the ceiling
// (workflows/cart.MaxLineItems, today 100), now barely lengthens the lock
// duration at all: 0.28 ms at 10 lines, 0.55 ms at 100 lines.
//
// These numbers DO NOT INCLUDE THE COMMIT'S WAL FLUSH, and they mislead unless
// that is stated: the test harness's container runs with fsync=off. The flush is
// under the same lock too (the lock is released at commit) and this change does
// not touch it — measured on a durable cluster, 6.2 ms independently of the
// number of lines. Therefore the lock duration an operator would see goes down
// from ~14.2 ms to ~6.8 ms: ~2x. The 14x is only the ratio inside the write
// phase itself.
//
// The cost of building the cart still grows with the SQUARE of the number of
// lines — every addition re-prices and rewrites all of the lines — but what
// grows is no longer the number of statements, it is the array size of the
// single statement: building a 100-line cart makes 100 UPDATEs instead of 5,050.
// Measured (the same fsync=off harness): the SetTotals total of building that
// cart went down from 548 ms to 86 ms; because on a durable cluster a flush
// would ride on top of every round, the ratio there is smaller than this one.
//
// A completed cart cannot be written to: errors.Conflict is returned.
func (s *Service) SetTotals(ctx context.Context, cartID string, in Totals) error {
	if err := requireID("cart_id", cartID); err != nil {
		return err
	}
	if err := validateCartTotals(in); err != nil {
		return err
	}

	return s.store.WithTx(ctx, func(ctx context.Context) error {
		cart, err := s.store.LockCart(ctx, cartID)
		if err != nil {
			return err
		}
		if cart.Completed() {
			return completedError(cart.ID)
		}
		if cart.Revision != in.Revision {
			return errors.Conflict(CodeTotalsStale,
				"the calculation does not belong to the cart's current shape: it was made for %d, the cart is at %d; calculate_totals must be run again",
				in.Revision, cart.Revision)
		}

		stored, err := s.store.ListLineItems(ctx, cart.ID)
		if err != nil {
			return err
		}
		applied, err := applyLineTotals(stored, in.Lines)
		if err != nil {
			return err
		}
		sum, err := sumSubtotals(applied)
		if err != nil {
			return err
		}
		if in.Subtotal != sum {
			return errors.Invalid(CodeTotalsInconsistent,
				"the cart subtotal must equal the sum of the line subtotals: %d given, the lines come to %d",
				in.Subtotal, sum)
		}

		if err := s.store.SetLineItemTotals(ctx, cart.ID, storeLineTotals(in.Lines)); err != nil {
			return err
		}

		_, err = s.store.UpdateCartTotals(ctx, cart.ID, models.CartTotals{
			Subtotal:      in.Subtotal,
			DiscountTotal: in.DiscountTotal,
			TaxTotal:      in.TaxTotal,
			ShippingTotal: in.ShippingTotal,
			Total:         in.Total,
			Revision:      in.Revision,
		})
		return err
	})
}

// storeLineTotals converts the service input into the pair the store expects.
//
// The identifier and the amounts are carried in a SINGLE struct and the
// conversion is done in a single loop: a signature that split the identifiers
// and the amounts into separate slices would allow the orders to drift apart and
// the wrong amount to be written to the wrong line. There is no gate on the write
// side that would catch this — the cart total still looks consistent, only the
// money taken from the customer is wrong.
func storeLineTotals(lines []LineTotals) []models.LineItemTotals {
	out := make([]models.LineItemTotals, len(lines))
	for i, line := range lines {
		out[i] = models.LineItemTotals{
			LineItemID: line.LineItemID,
			Totals: models.LineTotals{
				UnitPrice:     line.UnitPrice,
				Subtotal:      line.Subtotal,
				DiscountTotal: line.DiscountTotal,
				TaxTotal:      line.TaxTotal,
				Total:         line.Total,
			},
		}
	}
	return out
}

// validateCartTotals validates the range and the identity of the cart-level
// amounts.
//
// It is called BEFORE the lock is taken: there is no point in locking the cart
// for a request that is inconsistent with itself, and the lock duration would be
// lengthened for nothing.
func validateCartTotals(in Totals) error {
	if in.Revision < 0 {
		return errors.Invalid(CodeInvalidInput,
			"revision cannot be negative: %d", in.Revision)
	}
	for _, field := range []struct {
		label string
		value int64
	}{
		{"subtotal", in.Subtotal},
		{"discount_total", in.DiscountTotal},
		{"tax_total", in.TaxTotal},
		{"shipping_total", in.ShippingTotal},
		{"total", in.Total},
	} {
		if err := checkAmount(field.label, field.value, models.MaxTotal); err != nil {
			return err
		}
	}

	// The discount CANNOT EXCEED the subtotal.
	//
	// The identity check (below) does not suffice on its own: when an excessive
	// discount is swallowed by the tax and the shipping, the identity HOLDS and
	// the total does not even go negative. Example: subtotal=1000,
	// discount=3000, shipping=2500 -> total=500. The customer pays 500 for 1000
	// worth of goods together with 2500 worth of shipping, and neither the
	// service nor the carts_totals_consistent constraint sees it.
	//
	// A shipping discount is OUTSIDE this rule: a flow that wants to discount the
	// shipping does it by lowering shipping_total, not by inflating the discount.
	if in.DiscountTotal > in.Subtotal {
		return errors.Invalid(CodeTotalsInconsistent,
			"the discount cannot exceed the subtotal: discount_total=%d, subtotal=%d",
			in.DiscountTotal, in.Subtotal)
	}

	expected := in.Subtotal - in.DiscountTotal + in.TaxTotal + in.ShippingTotal
	if in.Total != expected {
		return errors.Invalid(CodeTotalsInconsistent,
			"the cart total is inconsistent: total=%d given, subtotal(%d) - discount_total(%d) + tax_total(%d) + shipping_total(%d) = %d",
			in.Total, in.Subtotal, in.DiscountTotal, in.TaxTotal, in.ShippingTotal, expected)
	}
	return nil
}

// applyLineTotals APPLIES the given line amounts to the stored lines and returns
// the result; it writes nothing.
//
// The returned slice carries the subtotals the lines would take had the write
// been done; the cart subtotal check is made over it.
//
// The line set must match EXACTLY: an unknown or repeated line, or one whose
// amount was never given, is caught here — before the write begins. Rejecting a
// skipped line is the essence of the contract; trusting its stored amount would
// count an unpriced line as valid with a zero amount (see [Service.SetTotals]).
func applyLineTotals(stored []models.LineItem, updates []LineTotals) ([]models.LineItem, error) {
	byID := make(map[string]int, len(stored))
	for i := range stored {
		byID[stored[i].ID] = i
	}

	applied := make([]models.LineItem, len(stored))
	copy(applied, stored)

	seen := make(map[string]struct{}, len(updates))
	for _, line := range updates {
		if err := requireID("line_item_id", line.LineItemID); err != nil {
			return nil, err
		}
		if _, dup := seen[line.LineItemID]; dup {
			return nil, errors.Invalid(CodeTotalsInconsistent,
				"more than one amount was given for the same line: %s", line.LineItemID)
		}
		seen[line.LineItemID] = struct{}{}

		idx, ok := byID[line.LineItemID]
		if !ok {
			return nil, errors.NotFound(CodeLineItemNotFound,
				"the line the amount is to be written to is not in the cart: %s", line.LineItemID)
		}
		if err := validateLineTotals(line, applied[idx].Quantity); err != nil {
			return nil, err
		}

		applied[idx].UnitPrice = line.UnitPrice
		applied[idx].Subtotal = line.Subtotal
		applied[idx].DiscountTotal = line.DiscountTotal
		applied[idx].TaxTotal = line.TaxTotal
		applied[idx].Total = line.Total
	}

	// Because the unknown and the repeated identifiers were eliminated above, the
	// equality of the counts tells us that the coverage is COMPLETE.
	if len(seen) != len(stored) {
		return nil, errors.Invalid(CodeTotalsInconsistent,
			"the calculation must cover ALL of the cart's lines; the lines with no amount given: %s",
			strings.Join(missingIDs(stored, seen), ", "))
	}
	return applied, nil
}

// missingIDs returns the identifiers of the lines whose amount was not given, in
// their stored order.
//
// The order coming from the stored list makes the error reproducible: walking
// over a map would produce differently ordered messages for the same input.
func missingIDs(stored []models.LineItem, seen map[string]struct{}) []string {
	out := make([]string, 0, len(stored)-len(seen))
	for i := range stored {
		if _, ok := seen[stored[i].ID]; !ok {
			out = append(out, stored[i].ID)
		}
	}
	return out
}

// validateLineTotals validates the amounts of a single line.
func validateLineTotals(line LineTotals, quantity int64) error {
	if err := checkAmount("unit_price", line.UnitPrice, models.MaxAmount); err != nil {
		return err
	}
	for _, field := range []struct {
		label string
		value int64
	}{
		{"subtotal", line.Subtotal},
		{"discount_total", line.DiscountTotal},
		{"tax_total", line.TaxTotal},
		{"total", line.Total},
	} {
		if err := checkAmount(field.label, field.value, models.MaxTotal); err != nil {
			return err
		}
	}

	expectedSubtotal, err := multiplyAmount(line.UnitPrice, quantity)
	if err != nil {
		return err
	}
	if line.Subtotal != expectedSubtotal {
		return errors.Invalid(CodeTotalsInconsistent,
			"the line subtotal is inconsistent (%s): subtotal=%d given, unit_price(%d) x quantity(%d) = %d",
			line.LineItemID, line.Subtotal, line.UnitPrice, quantity, expectedSubtotal)
	}

	// At the line level too the discount cannot exceed the subtotal; the same
	// rationale as at the cart level (the tax can swallow an excessive discount
	// and make the identity hold).
	if line.DiscountTotal > line.Subtotal {
		return errors.Invalid(CodeTotalsInconsistent,
			"the line discount cannot exceed the subtotal (%s): discount_total=%d, subtotal=%d",
			line.LineItemID, line.DiscountTotal, line.Subtotal)
	}

	expectedTotal := line.Subtotal - line.DiscountTotal + line.TaxTotal
	if line.Total != expectedTotal {
		return errors.Invalid(CodeTotalsInconsistent,
			"the line total is inconsistent (%s): total=%d given, subtotal(%d) - discount_total(%d) + tax_total(%d) = %d",
			line.LineItemID, line.Total, line.Subtotal, line.DiscountTotal, line.TaxTotal, expectedTotal)
	}
	return nil
}

// multiplyAmount multiplies the unit price by the quantity WITHOUT OVERFLOW.
//
// When the factors have passed the service validation the result is already
// below [models.MaxTotal]; the check is the last defense against an abnormal
// quantity written directly with SQL. An overflowing multiplication silently
// produces a negative subtotal and could pass the consistency check BY MISTAKE.
func multiplyAmount(unitPrice, quantity int64) (int64, error) {
	if unitPrice == 0 || quantity == 0 {
		return 0, nil
	}
	if quantity < 0 || unitPrice < 0 {
		return 0, errors.Invalid(CodeInvalidInput,
			"the unit price and the quantity cannot be negative: %d x %d", unitPrice, quantity)
	}
	if quantity > models.MaxTotal/unitPrice {
		return 0, errors.Invalid(CodeInvalidInput,
			"the line subtotal exceeds the limit: %d x %d > %d", unitPrice, quantity, models.MaxTotal)
	}
	return unitPrice * quantity, nil
}

// sumSubtotals sums the line subtotals WITHOUT OVERFLOW.
func sumSubtotals(lines []models.LineItem) (int64, error) {
	var sum int64
	// The loop is walked by index: the line struct is large and copying it by
	// value would carry a few hundred bytes for nothing on every turn.
	for i := range lines {
		subtotal := lines[i].Subtotal
		if subtotal < 0 {
			return 0, errors.Invalid(CodeTotalsInconsistent,
				"the line subtotal is negative: %s (%d)", lines[i].ID, subtotal)
		}
		if sum > models.MaxTotal-subtotal {
			return 0, errors.Invalid(CodeTotalsInconsistent,
				"the sum of the line subtotals exceeds the limit (%d)", models.MaxTotal)
		}
		sum += subtotal
	}
	return sum, nil
}
