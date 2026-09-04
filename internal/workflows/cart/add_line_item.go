package cart

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// MaxLineItems is the largest number of DISTINCT lines a cart may carry.
//
// The limit IS NOT SILENT and there is no truncation: a request that wants to
// open a NEW line on a cart that has reached the ceiling is rejected with
// errors.Invalid ([CodeCartLineLimit]) and the message writes both the ceiling
// and the number of lines in the cart.
//
// # Why there is a ceiling
//
// Every request that adds a line reprices ALL the cart's lines and REWRITES the
// amount of ALL of them, so the cost of building an N-line cart grows not with
// N but with N². The price read was made bulk and brought down to linear (see
// [Workflows.unitPrices]) but the WRITE side is still per line: the cart
// module's SetTotals writes each line's amount with a separate UPDATE and under
// the cart's lock. Measured (with this package's fakes, by counting the calls):
// building a 100-line cart costs 5,050 line amount writes; a 1,000-line cart
// costs 500,500. A cart with no ceiling would leave the time a single client
// can keep the database busy unbounded.
//
// # Why 100
//
// The value is the same as the cart module's page size ceiling (MaxLimit) — the
// lines of a cart pressed against the ceiling fit on a SINGLE page of that
// module — and is one tenth of pricing's bulk price request ceiling
// (MaxCalculateItems, 1000 today); the gap between the two is deliberate and is
// the subject of the paragraph below.
//
// # The ceiling is applied only on the path that OPENS a line
//
// Adding a variant already sitting in the cart again does not open a new line,
// it increases the quantity of the existing line and DOES NOT HIT the ceiling;
// had it hit, the owner of a full cart could not even increase the quantity of
// their own line. For the same reason the calculation round, the quantity
// update and the order path never ask the ceiling: a cart opened BEFORE the
// ceiling was put in place, and carrying more than 100 lines today, must stay
// calculable and completable — rejecting it would render the customer's
// existing cart unpayable.
const MaxLineItems = 100

// AddLineItemInput is the input of the line to be added to the cart.
type AddLineItemInput struct {
	// CartID is the cart the line will be added to; it is MANDATORY.
	CartID string
	// VariantID is the product variant to be added; it is MANDATORY.
	VariantID string
	// Quantity is the quantity to be added; it must be POSITIVE.
	//
	// The value is not the ABSOLUTE quantity but the quantity TO BE ADDED: if
	// the same variant is already in the cart no new line is opened, the
	// quantity of the existing line INCREASES by this much.
	Quantity int64
	// Metadata is the free-form JSON object to attach to the line; it is
	// OPTIONAL.
	//
	// The flow does not read it and does not take it into account; it only
	// carries it to the cart module. The field holds the storefront's per-line
	// intent (a gift note, personalization) and, since this flow is the only
	// path that opens a line, it has no other carrier.
	//
	// It IS NOT WRITTEN on a merge: if the same variant is already in the cart
	// the cart module only increases the quantity and preserves the existing
	// line's metadata (see AddLineItem in the cart service).
	Metadata json.RawMessage
}

// AddLineItemResult is the result of the added line and of the recalculated
// totals.
type AddLineItemResult struct {
	// LineItemID is the id of the line that was added (or whose quantity was
	// increased).
	LineItemID string
	// VariantID is the variant the line points at.
	VariantID string
	// Title is the line's title copied from the catalog.
	Title string
	// UnitPrice is the unit price written WHILE the line was being opened.
	//
	// It is not the final price: the calculation round that runs after the line
	// is opened selects the price again according to the LAST quantity in the
	// cart and writes that one to the line. The two diverge only when there is
	// a merge (see [Workflows.AddLineItem]).
	UnitPrice int64
	// Totals are the cart totals after the line was added.
	Totals Totals
}

// AddLineItem finds the variant's price, adds the line and recalculates the
// totals.
//
// The order: the cart's currency is read -> the variant's title is taken from
// the catalog -> the variant's price set is found over the link -> the unit
// price is calculated from pricing -> the line is written ->
// [Workflows.CalculateTotals] runs.
//
// # A variant with no price
//
// It is rejected (errors.Invalid); the rationale is in the
// [Workflows.priceSetsFor] godoc. If the price set exists but there is no valid
// price in the cart's currency the error is again errors.Invalid and the
// message writes the currency.
//
// # Merging and the price tier
//
// If the same variant is already in the cart the cart module does not open a
// new line, it increases the quantity. In that case the unit price calculated
// HERE belongs to the quantity being added and may not belong to the merged
// quantity — pricing selects the price according to the quantity range, that
// is, once 3 + 2 merge the line may move into the "5+" tier. The difference is
// immaterial because the calculation round that runs right after the line is
// written reprices ALL the lines with the CURRENT quantity in the cart; the
// value here is only the line's opening value and is never the amount shown to
// the customer.
//
// # If the totals calculation blows up
//
// The line HAS BEEN WRITTEN and is not taken back. The error is returned
// wrapped with the [CodeTotalsAfterChange] code; the cart stays in the "stale
// totals" state the cart model recognizes, and that cart becoming an order is
// separately rejected. Deleting the line would mean destroying the customer's
// request because of a temporary pricing/region fault (see the package comment,
// "Why none of the flows is a saga").
func (w *Workflows) AddLineItem(ctx context.Context, in AddLineItemInput) (AddLineItemResult, error) {
	if err := requireID("cart_id", in.CartID); err != nil {
		return AddLineItemResult{}, err
	}
	if err := requireID("variant_id", in.VariantID); err != nil {
		return AddLineItemResult{}, err
	}
	quantity, err := quantity32(in.Quantity)
	if err != nil {
		return AddLineItemResult{}, err
	}

	snap, err := w.snapshot(ctx, in.CartID)
	if err != nil {
		return AddLineItemResult{}, err
	}
	if snap.Completed {
		return AddLineItemResult{}, errors.Conflict(CodeCartCompleted,
			"no line can be added to a completed cart: %s", in.CartID)
	}
	// The ceiling is checked BEFORE the catalog and price reads: there is no
	// point in keeping two modules busy for a request whose outcome is known in
	// advance.
	if err := checkLineLimit(snap, in.VariantID); err != nil {
		return AddLineItemResult{}, err
	}

	title, err := w.variantTitle(ctx, in.VariantID)
	if err != nil {
		return AddLineItemResult{}, err
	}
	priceSets, err := w.priceSetsFor(ctx, []string{in.VariantID})
	if err != nil {
		return AddLineItemResult{}, err
	}

	unitPrice, err := w.prices.CalculateAmount(ctx, priceSets[in.VariantID], snap.CurrencyCode, quantity,
		map[string]string{attrRegionID: snap.RegionID})
	if err != nil {
		if errors.IsNotFound(err) {
			return AddLineItemResult{}, errors.Wrap(err, errors.KindInvalid, CodePriceUnavailable,
				"variant %s has no price in currency %s at quantity %d",
				in.VariantID, snap.CurrencyCode, in.Quantity)
		}
		return AddLineItemResult{}, err
	}
	if err := checkAmount("unit_price", unitPrice, MaxAmount); err != nil {
		return AddLineItemResult{}, err
	}

	lineID, err := w.carts.AddCartLineItem(ctx, in.CartID, in.VariantID, title, in.Quantity, unitPrice, in.Metadata)
	if err != nil {
		return AddLineItemResult{}, err
	}

	totals, err := w.CalculateTotals(ctx, in.CartID)
	if err != nil {
		return AddLineItemResult{}, totalsAfterChange(err, in.CartID, "line added")
	}

	return AddLineItemResult{
		LineItemID: lineID,
		VariantID:  in.VariantID,
		Title:      title,
		UnitPrice:  unitPrice,
		Totals:     totals,
	}, nil
}

// checkLineLimit checks whether the request will open a NEW line in the cart
// and, if it will, whether it fits within the [MaxLineItems] ceiling.
//
// If the variant is already in the cart the request is a merge, it DOES NOT
// CHANGE the number of lines and is exempt from the ceiling; the rationale is
// in the [MaxLineItems] godoc. The source of the comparison is the snapshot and
// another request can slip in between the snapshot and the write: the ceiling
// is therefore not an EXACT upper bound but a gate that cuts off a cart's
// unbounded growth — the cost of an overshoot of a few lines is far lower than
// the cost of taking line addition under the cart's lock.
func checkLineLimit(snap Snapshot, variantID string) error {
	if len(snap.Items) < MaxLineItems {
		return nil
	}
	for i := range snap.Items {
		if snap.Items[i].VariantID == variantID {
			return nil
		}
	}
	return errors.Invalid(CodeCartLineLimit,
		"a cart can carry at most %d lines; cart %s has %d lines (the quantity of an existing line can be increased)",
		MaxLineItems, snap.ID, len(snap.Items))
}

// totalsAfterChange wraps the error of the calculation that blew up AFTER the
// cart CHANGED.
//
// The wrapping is there so that the caller can tell two states apart: the
// request was rejected (the cart did not change) versus the request was applied
// but the amount could not be calculated. In the second case repeating the
// request would add the line a SECOND TIME; the correct behavior is only to run
// the calculation again. The error's KIND is preserved so that the layer
// translating it into a status code writes the right one.
func totalsAfterChange(err error, cartID, what string) error {
	return errors.Wrap(err, errors.KindOf(err), CodeTotalsAfterChange,
		"%s (%s) but the totals could not be calculated; the cart's totals are stale, the calculation has to be run again",
		what, cartID)
}
