package cart

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
)

// MaxTotalsAttempts is how many times a calculation round may be repeated.
//
// The calculation is done OUTSIDE the cart's lock: first the cart is read, then
// pricing and region are called, and only at the very end is the result written.
// If the cart changes in between, the write returns errors.Conflict and the
// calculation has to be done from the start
// (see [Workflows.CalculateTotals]).
//
// Three attempts were chosen to fit the size of the real race: a conflict only
// happens when the customer touches their cart while the calculation is in
// flight, and a human getting in the way twice in a row is already out of the
// ordinary. The limit MUST exist — an unbounded loop would keep pricing busy
// forever on a cart that keeps changing (a broken client or a stuck retry loop).
// If the limit is exceeded the caller gets errors.Conflict and retries at ITS
// OWN pace; the number of requests in flight must be decided by the caller, not
// by the server.
const MaxTotalsAttempts = 3

// Totals is the result of a calculation round and is written to the cart as
// JSON.
//
// All the fields are WHOLE NUMBER minor units (plan Section 8). The identity
// always holds: Total = Subtotal - DiscountTotal + TaxTotal + ShippingTotal.
type Totals struct {
	// Revision is the cart shape the calculation IS BASED ON; if it does not
	// match the cart's shape at the moment of the write, the calculation is
	// rejected.
	Revision int64 `json:"revision"`
	// Subtotal is the sum of the line subtotals.
	Subtotal int64 `json:"subtotal"`
	// DiscountTotal is the total discount; it is carried positive and is
	// subtracted from the total. It is the SUM of the line discounts (see the
	// package comment, "Discount").
	DiscountTotal int64 `json:"discount_total"`
	// TaxTotal is the total tax; it is the sum of the line taxes.
	TaxTotal int64 `json:"tax_total"`
	// ShippingTotal is the sum of the cart's shipping methods.
	ShippingTotal int64 `json:"shipping_total"`
	// Total is the amount to be paid.
	Total int64 `json:"total"`
	// TaxSource is WHICH source the tax came from; its values are
	// [TaxSourceTax], [TaxSourceTaxUnconfigured] and [TaxSourceRegion].
	//
	// The field IS NOT MONEY and, in the body written to the cart, it is ignored
	// by the cart module (the cart's totals schema does not know it). It is
	// still part of the body and it returns to the caller: which authority an
	// amount rests on is as important as the amount itself. On a cart whose tax
	// comes out 0, "the rate was zero" and "there was no configuration" are told
	// apart only here.
	TaxSource string `json:"tax_source"`
	// Lines are the amounts calculated per line and they cover ALL the lines of
	// the cart.
	Lines []LineTotals `json:"lines"`
}

// LineTotals are the calculated amounts of a single cart line.
//
// The quantity IS NOT HERE: the quantity is the cart's data and a calculation
// round cannot change it. The line's subtotal IS BASED ON the quantity but it
// does not WRITE it.
type LineTotals struct {
	// LineItemID is the line the amounts belong to.
	LineItemID string `json:"line_item_id"`
	// UnitPrice is the unit price pricing selected.
	UnitPrice int64 `json:"unit_price"`
	// Subtotal is the line's subtotal: UnitPrice x Quantity.
	Subtotal int64 `json:"subtotal"`
	// DiscountTotal is the discount falling on the line; it NEVER exceeds the
	// line's subtotal.
	DiscountTotal int64 `json:"discount_total"`
	// TaxTotal is the tax falling on the line.
	TaxTotal int64 `json:"tax_total"`
	// TaxRateBps is the rate the tax was computed at, in BASIS POINTS
	// (2000 = 20%).
	//
	// # Why the rate is carried and not just the amount
	//
	// The amount alone cannot be turned back into the rate: the tax is rounded
	// DOWN per line, so 1899 kurus at 20% and at 19.99% produce the same
	// figure. An invoice has to print the rate of every line — in Turkey the
	// KDV rate is a required field on an e-fatura, not a derived one — and a
	// figure recomputed later from the amount is a different claim from the one
	// the customer was charged under.
	TaxRateBps int32 `json:"tax_rate_bps"`
	// Total is the line's total: Subtotal - DiscountTotal + TaxTotal.
	Total int64 `json:"total"`
}

// CalculateTotals recalculates the cart's totals from scratch and writes them to
// the cart.
//
// This is the heart of Phase 5, and a single round consists of these steps:
//
//  1. The cart's snapshot is taken in a SINGLE read; the shape counter
//     (revision) is noted.
//  2. The variants of the lines are turned into price sets with a SINGLE link
//     query (there is no N+1).
//  3. Every line's unit price is fetched from pricing AGAIN; a stored amount is
//     never trusted.
//  4. The discount is taken PER ITEM from the promotion module and written onto
//     the lines (see [Workflows.applyDiscounts]).
//  5. The tax is calculated per line over the AFTER-DISCOUNT base; the tax
//     module does the calculation, and when it cannot, the region's rate is
//     used and the source that was used is reported in the [Totals.TaxSource]
//     field (see [Workflows.applyTaxes]).
//  6. Shipping is the sum of the cart's shipping methods and DOES NOT ENTER the
//     tax base.
//  7. The result is written stamped with the shape noted in step 1.
//
// # Conflict
//
// The calculation is done outside the cart's lock. If the cart's shape has
// changed at the moment of the write, the cart module returns errors.Conflict;
// in that case the round is done FROM THE START, at most [MaxTotalsAttempts]
// times. If the limit is exceeded the error passes to the caller as
// errors.Conflict: writing a stale calculation would mean charging the customer
// for less than the goods in their cart.
//
// # Completed cart
//
// No calculation is done on a completed cart and errors.Conflict is returned.
// The cart module would reject the write anyway; the reason for returning early
// here is not to call pricing for nothing on a round whose outcome is known in
// advance.
//
// # Cart with no lines
//
// It is valid: the subtotal and the tax are zero, the total is shipping alone.
// No error is returned — selecting shipping before a line is added to the cart
// is a possible state, and the cart module separately rejects a cart with no
// lines becoming an ORDER.
func (w *Workflows) CalculateTotals(ctx context.Context, cartID string) (Totals, error) {
	if err := requireID("cart_id", cartID); err != nil {
		return Totals{}, err
	}

	var lastErr error
	for attempt := 1; attempt <= MaxTotalsAttempts; attempt++ {
		totals, stale, err := w.totalsRound(ctx, cartID)
		if err == nil {
			return totals, nil
		}
		if !stale {
			return Totals{}, err
		}

		lastErr = err
		w.log.DebugContext(ctx, "the cart calculation conflicted, the round is being redone",
			"cart_id", cartID, "attempt", attempt, "max_attempts", MaxTotalsAttempts)
	}

	return Totals{}, errors.Wrap(lastErr, errors.KindConflict, CodeTotalsConflict,
		"cart %s changed too often for the calculation to be written (%d attempts); the request has to be sent again",
		cartID, MaxTotalsAttempts)
}

// totalsRound is a single calculation round.
//
// The second return value tells whether the error is a shape conflict WORTH
// RETRYING. Making the distinction here is deliberate: the calling loop is not
// forced to recognize the cart module's error CODES — if it did, this package
// would copy the code strings of a module it cannot import and those codes
// could silently drift apart.
func (w *Workflows) totalsRound(ctx context.Context, cartID string) (out Totals, stale bool, err error) {
	snap, err := w.snapshot(ctx, cartID)
	if err != nil {
		return Totals{}, false, err
	}
	if snap.Completed {
		return Totals{}, false, errors.Conflict(CodeCartCompleted,
			"no calculation can be done on a completed cart: %s", cartID)
	}

	totals, err := w.computeTotals(ctx, snap)
	if err != nil {
		return Totals{}, false, err
	}

	payload, err := json.Marshal(totals)
	if err != nil {
		return Totals{}, false, errors.Wrap(err, errors.KindInternal, CodeSnapshotInvalid,
			"the cart totals could not be encoded to JSON: %s", cartID)
	}
	if err := w.carts.SetCartTotalsJSON(ctx, cartID, payload); err != nil {
		// A conflict means the cart changed between the read and the write: the
		// round can be done from the start. Completion is a Conflict too, and in
		// that case the new round's snapshot sees the cart closed and returns
		// early.
		return Totals{}, errors.IsConflict(err), err
	}
	return totals, false, nil
}

// computeTotals produces the totals from the snapshot; it WRITES NOTHING.
//
// The separation is deliberate: the whole calculation is free of side effects
// and can be tested on its own. The write lives only inside
// [Workflows.totalsRound].
//
// # The ORDER of the steps is a contract
//
// Subtotal -> discount -> tax. The discount MUST come before the tax because the
// tax base is the after-discount one (see the package comment, "Tax contract");
// if the order is broken no check blows up, the customer merely pays the tax on
// money they never paid.
func (w *Workflows) computeTotals(ctx context.Context, snap Snapshot) (Totals, error) {
	lines, err := w.lineSubtotals(ctx, snap)
	if err != nil {
		return Totals{}, err
	}
	shippingTotal, err := shippingTotalOf(snap)
	if err != nil {
		return Totals{}, err
	}
	if err := w.applyDiscounts(ctx, snap, lines); err != nil {
		return Totals{}, err
	}
	taxSource, err := w.applyTaxes(ctx, snap, shippingTotal, lines)
	if err != nil {
		return Totals{}, err
	}
	return assembleTotals(snap, lines, shippingTotal, taxSource)
}

// lineSubtotals calculates every line's unit price and subtotal.
//
// The discount and tax fields are left ZERO; [Workflows.applyDiscounts] and
// [Workflows.applyTaxes] fill them in. The returned slice is in the SAME ORDER
// and of the SAME LENGTH as the lines in the snapshot; matching the requests
// going to the two modules with the responses coming back rests on this
// invariant.
func (w *Workflows) lineSubtotals(ctx context.Context, snap Snapshot) ([]LineTotals, error) {
	priceSets, err := w.priceSetsFor(ctx, snap.VariantIDs())
	if err != nil {
		return nil, err
	}

	unitPrices, err := w.unitPrices(ctx, snap, priceSets)
	if err != nil {
		return nil, err
	}

	lines := make([]LineTotals, 0, len(snap.Items))
	for i := range snap.Items {
		item := snap.Items[i]

		subtotal, mulErr := mulAmount(unitPrices[i], item.Quantity)
		if mulErr != nil {
			return nil, mulErr
		}

		lines = append(lines, LineTotals{
			LineItemID: item.ID,
			UnitPrice:  unitPrices[i],
			Subtotal:   subtotal,
		})
	}
	return lines, nil
}

// shippingTotalOf calculates the sum of the shipping methods WITHOUT OVERFLOW.
func shippingTotalOf(snap Snapshot) (int64, error) {
	var total int64
	for i := range snap.ShippingMethods {
		next, err := addAmount(total, snap.ShippingMethods[i].Amount)
		if err != nil {
			return 0, err
		}
		total = next
	}
	return total, nil
}

// assembleTotals produces the cart totals from the filled-in lines and writes
// the line totals.
//
// # The Σ identities ARE BORN here
//
// The cart's discount and tax are produced by SUMMING the line values; the
// totals reported by promotion and tax are not reused (they were compared
// against the line values in their own places). This way the identities
// Σ(line discount) = cart discount and Σ(line tax) = cart tax hold by the
// structure of the calculation, and do not depend on some check being
// remembered.
//
// # WHERE the cent remainder stays
//
// This function does no division at all, so it PRODUCES no remainder. A
// remainder is only born in rate calculations and falls on the LINE where it was
// born: the discount percentage and the tax rate are rounded DOWN per line, and
// the leftover fraction is NOT CARRIED to another line and is not redistributed
// at the cart level. Their directions are opposite and both are smaller than one
// minor unit per line: a tax rounded down is IN FAVOR of the customer (less
// tax), a discount rounded down is in favor of the seller (less discount). The
// reason carrying is rejected is the same one — adding the remainder to a line
// would make that line's tax or discount different from what its own rate says,
// and the invoice would no longer be explainable line by line.
func assembleTotals(snap Snapshot, lines []LineTotals, shippingTotal int64, taxSource string) (Totals, error) {
	totals := Totals{
		Revision:      snap.Revision,
		ShippingTotal: shippingTotal,
		TaxSource:     taxSource,
		Lines:         lines,
	}

	var err error
	for i := range lines {
		line := &lines[i]

		if totals.Subtotal, err = addAmount(totals.Subtotal, line.Subtotal); err != nil {
			return Totals{}, err
		}
		if totals.DiscountTotal, err = addAmount(totals.DiscountTotal, line.DiscountTotal); err != nil {
			return Totals{}, err
		}
		if totals.TaxTotal, err = addAmount(totals.TaxTotal, line.TaxTotal); err != nil {
			return Totals{}, err
		}
		if line.Total, err = addAmount(line.Subtotal-line.DiscountTotal, line.TaxTotal); err != nil {
			return Totals{}, err
		}
	}

	total, err := addAmount(totals.Subtotal-totals.DiscountTotal, totals.TaxTotal)
	if err != nil {
		return Totals{}, err
	}
	if total, err = addAmount(total, totals.ShippingTotal); err != nil {
		return Totals{}, err
	}
	totals.Total = total
	return totals, nil
}

// priceRequest is the JSON schema of the BULK price request going to the pricing
// module.
//
// The field names MUST be EXACTLY the same as pricing's interop schema: because
// the two packages cannot import each other the compiler cannot see the match
// (the accepted cost of ADR 0006) and the match can only be proven by an
// integration test (see internal/e2e/cart_totals_test.go).
//
// The currency and the context are not carried PER ITEM: all the lines of a cart
// are in the same currency and in the same region, and repeating the field per
// item would give the impression that two lines can be priced with different
// contexts.
type priceRequest struct {
	// CurrencyCode is the cart's currency (ISO 4217).
	CurrencyCode string `json:"currency_code"`
	// Attributes is the context the price rules will look at; today only the
	// region is put in it, and why the customer segment stayed out is in the
	// package comment.
	Attributes map[string]string `json:"attributes"`
	// Items are the items to be priced and they go in the cart's line ORDER.
	Items []priceRequestItem `json:"items"`
}

// priceRequestItem is the schema of a single item in the bulk price request.
type priceRequestItem struct {
	// PriceSetID is the price container the line's variant is attached to.
	PriceSetID string `json:"price_set_id"`
	// Quantity is the line's CURRENT quantity; the price tier is selected by it.
	Quantity int32 `json:"quantity"`
}

// priceResponse is the JSON schema of the bulk price result returned by the
// pricing module.
//
// Unknown fields are SILENTLY SKIPPED (unlike the request): when pricing grows
// its schema, this package should not have to be updated in the same round. The
// silence is only for UNRECOGNIZED fields; the invariants the recognized ones
// carry are verified one by one inside [Workflows.unitPrices].
type priceResponse struct {
	// Items are the results, in the SAME ORDER and of the SAME LENGTH as the
	// items in the request.
	Items []priceResponseItem `json:"items"`
}

// priceResponseItem is the schema of a single item in the bulk price response.
type priceResponseItem struct {
	// Amount is the selected unit price (minor unit); it is meaningless if
	// Priced is false.
	Amount int64 `json:"amount"`
	// Priced reports whether a valid price WAS FOUND for the item.
	//
	// A separate flag is a MUST: zero is a VALID price (a free item is a real
	// scenario), so "amount 0" and "no price" cannot be told apart from the
	// amount itself. Without the flag a variant that has no price would enter
	// the cart FOR FREE.
	Priced bool `json:"priced"`
}

// unitPrices fetches the unit price of ALL the cart's lines in a SINGLE round.
//
// The returned slice is in the SAME ORDER and of the SAME LENGTH as the lines in
// the snapshot; [Workflows.lineSubtotals] matches them by index.
//
// # Why bulk
//
// A calculation round used to open two queries per line (price candidates +
// rules) and every line addition repriced ALL the lines before it, meaning the
// cost of building a cart grew QUADRATICALLY with the number of lines. Measured
// (with this package's fakes, by counting the calls): building a 100-line cart
// cost 5150 price calls — 10,300 queries; on the bulk path the same cart is 200
// calls and 400 queries.
//
// The query itself was measured too (gobit_load, 54,000 containers, localhost
// TCP, best of seven rounds): for 50 containers the per-container path takes
// 4.9 ms and the bulk path 0.25 ms; for 100 containers 9.9 ms and 0.33 ms. On a
// SINGLE container the bulk path has no advantage (the candidate query is 66 µs
// against 77 µs by the median of 500 rounds; both plans scan the same partial
// index, and the one with the array parameter adds a sort step on top), which is
// why the single price asked WHILE a line is being opened is still asked with
// the singular method (see [Workflows.AddLineItem]).
//
// # The selected amount IS THE SAME
//
// The two paths run pricing's SAME pure selection function and see the same
// candidate rows; the only difference is that the bulk path reads the clock
// once, and that difference is in the bulk path's favor — a campaign ending at
// exactly that moment cannot price two lines of the same cart from different
// worlds. The equality claim is proven in pricing's own test
// (TestCalculateAmountsJSONMatchesCalculateAmount).
//
// # A line with no price
//
// On the bulk path pricing returns a flag rather than an error; the error
// classification is done HERE and is exactly the same as on the singular path
// (errors.Invalid, [CodePriceUnavailable]): the line IS STILL in the cart, what
// is missing is its price in this currency. Had it passed as NotFound the client
// would read "no cart/line" (404) and would take a genuinely fixable state for a
// lost one.
//
// What the flag buys is spent here: ALL the lines with no price are counted in a
// single error (see [priceUnavailable]); it does not return on the first line
// that has none.
//
// # The response IS VERIFIED
//
// The length and the amount range are checked. The compiler does not check the
// other side of the boundary; if a response that had lost its alignment passed
// silently, every line of the cart would be written with ANOTHER variant's price
// and no gate would see it.
//
// # The request IS NOT SPLIT, and that has a limit
//
// All the cart's lines go in a single request. If pricing's own item ceiling
// (MaxCalculateItems, 1000 today) is exceeded the request is rejected and that
// cart's total CANNOT be calculated at all. Today it is an unreachable state:
// the only path that opens a line is subject to the [MaxLineItems] (100) ceiling
// and the only way to go above 1000 is to grow that constant — if it is grown,
// pricing's ceiling has to be grown along with it. Before growing it, the plan
// table in the MaxCalculateItems godoc must be looked at: pricing's bulk read
// abandons the index and turns to scanning the table somewhere between 280 and
// 300 ids, meaning the cost is not linear up to 1000.
//
// The request IS NOT SPLIT by the item ceiling, because splitting would take
// back the "single moment" guarantee above: every part reads the clock again and
// a campaign ending at exactly that moment would price the cart's first part
// from one world and its second part from another.
func (w *Workflows) unitPrices(ctx context.Context, snap Snapshot, priceSets map[string]string) ([]int64, error) {
	if len(snap.Items) == 0 {
		return nil, nil
	}

	req := priceRequest{
		CurrencyCode: snap.CurrencyCode,
		Attributes:   map[string]string{attrRegionID: snap.RegionID},
		Items:        make([]priceRequestItem, 0, len(snap.Items)),
	}
	for i := range snap.Items {
		quantity, err := quantity32(snap.Items[i].Quantity)
		if err != nil {
			return nil, err
		}
		req.Items = append(req.Items, priceRequestItem{
			PriceSetID: priceSets[snap.Items[i].VariantID],
			Quantity:   quantity,
		})
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodePriceResponseInvalid,
			"the bulk price request could not be encoded to JSON: %s", snap.ID)
	}

	raw, err := w.prices.CalculateAmountsJSON(ctx, payload)
	if err != nil {
		// The kind and the code are PRESERVED: pricing's error passes through as
		// it does on the singular path, and wrapping it would lose the
		// distinction between "there is no price" and "the price could not be
		// asked for".
		return nil, err
	}

	var resp priceResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodePriceResponseInvalid,
			"the bulk price result could not be decoded: %s", snap.ID)
	}
	if len(resp.Items) != len(snap.Items) {
		return nil, errors.Internal(CodePriceResponseInvalid,
			"for %d lines the bulk price result returned %d records (%s)",
			len(snap.Items), len(resp.Items), snap.ID)
	}

	var unpriced []int
	for i := range resp.Items {
		if !resp.Items[i].Priced {
			unpriced = append(unpriced, i)
		}
	}
	if len(unpriced) > 0 {
		return nil, priceUnavailable(snap, unpriced)
	}

	out := make([]int64, 0, len(resp.Items))
	for i := range resp.Items {
		if err := checkAmount("unit_price", resp.Items[i].Amount, MaxAmount); err != nil {
			return nil, err
		}
		out = append(out, resp.Items[i].Amount)
	}
	return out, nil
}

// priceUnavailable reports ALL the lines with no price in a single error.
//
// Stopping and returning on the first line with no price would mean THROWING
// AWAY the information already at hand: the bulk response carries all the lines
// at once, so the owner of a cart with two dead variants can learn about both of
// them in this one request — had they been returned one by one, they would
// discover the next one after every fix and would repair their cart request by
// request. This is what the bulk path returning a FLAG per item (rather than an
// error) buys here; the flag is not there to save a round trip, it is there so
// that this sentence can be written.
//
// The kind and the code stay the same as on the singular path (errors.Invalid,
// [CodePriceUnavailable]): the line IS STILL in the cart, what is missing is its
// price in this currency. The single-line message is kept exactly as it is too —
// the plural form is only built when more than one line really has no price.
func priceUnavailable(snap Snapshot, unpriced []int) error {
	if len(unpriced) == 1 {
		item := snap.Items[unpriced[0]]
		return errors.Invalid(CodePriceUnavailable,
			"variant %s has no price in currency %s at quantity %d (line: %s)",
			item.VariantID, snap.CurrencyCode, item.Quantity, item.ID)
	}

	parts := make([]string, 0, len(unpriced))
	for _, i := range unpriced {
		item := snap.Items[i]
		parts = append(parts, fmt.Sprintf("%s (line: %s, quantity: %d)",
			item.VariantID, item.ID, item.Quantity))
	}
	return errors.Invalid(CodePriceUnavailable,
		"%d lines have no price in currency %s: %s",
		len(unpriced), snap.CurrencyCode, strings.Join(parts, ", "))
}

// snapshot reads and decodes the cart's snapshot.
func (w *Workflows) snapshot(ctx context.Context, cartID string) (Snapshot, error) {
	payload, err := w.carts.CartSnapshotJSON(ctx, cartID)
	if err != nil {
		return Snapshot{}, err
	}
	return decodeSnapshot(cartID, payload)
}
