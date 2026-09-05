package checkout

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// Snapshot is the shape of the cart that becomes an order.
//
// The type is the schema of the [Carts.CartSnapshotJSON] body: the cart module
// produces these fields, this package reads them. The schema is DELIBERATELY
// narrow — it is whatever enters the order and nothing more. Unrecognized
// fields (shipping methods, for one) are silently skipped so that this package
// need not be updated when the cart module grows the schema.
//
// The AMOUNTS of the lines are not here and must not be; the totals produce
// them (see [CartTotals]). That the two sources belong to the same instant is
// proven by [Snapshot.Revision].
type Snapshot struct {
	// ID is the identity of the cart.
	ID string `json:"id"`
	// RegionID is the region of the cart; the order is written to the same region.
	RegionID string `json:"region_id"`
	// CustomerID is the owner of the cart; when empty the order is a guest's.
	CustomerID string `json:"customer_id"`
	// CurrencyCode is the currency of the cart (ISO 4217).
	CurrencyCode string `json:"currency_code"`
	// Revision is the shape counter of the cart; it is the stamp of the totals.
	Revision int64 `json:"revision"`
	// Completed reports whether the cart has been completed.
	Completed bool `json:"completed"`
	// Items are the lines of the cart.
	Items []SnapshotItem `json:"items"`
}

// SnapshotItem is the set of fields of a cart line that enter the order.
type SnapshotItem struct {
	// ID is the identity of the line; the reservation is bound to this identity.
	ID string `json:"id"`
	// VariantID is the product variant the line points at.
	VariantID string `json:"variant_id"`
	// Quantity is the count on the line.
	Quantity int64 `json:"quantity"`
}

// VariantIDs returns the variant identities of the lines WITHOUT DUPLICATES and
// in line order.
//
// The order is preserved so that the input of the bulk link and catalog queries
// (and therefore the error messages they produce) stays reproducible; the
// deduplication is there so that a cart holding two lines of the same variant
// does not grow the query needlessly.
func (s Snapshot) VariantIDs() []string {
	seen := make(map[string]struct{}, len(s.Items))
	out := make([]string, 0, len(s.Items))
	for i := range s.Items {
		id := s.Items[i].VariantID
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// checkoutPlan is the IMMUTABLE input of the saga: everything resolved during
// the preparation phase sits here.
//
// The plan is handed to the engine as input and written to the execution record
// as JSON; in an execution that needs manual intervention it is the answer to
// the operator's question "what was this trying to do". Steps reach the plan
// through a POINTER and do NOT modify it; the only thing that flows between
// steps is [workflow.StepContext].Shared.
type checkoutPlan struct {
	// CartID is the cart the order was born from.
	CartID string `json:"cart_id"`
	// RegionID is the region of the order.
	RegionID string `json:"region_id"`
	// CustomerID is the owner of the order; empty on a guest order.
	CustomerID string `json:"customer_id"`
	// Email is the contact address of the order; it may be empty.
	Email string `json:"email"`
	// CurrencyCode is the currency of the order (ISO 4217).
	CurrencyCode string `json:"currency_code"`
	// Revision is the SHARED shape counter of the totals and the snapshot.
	Revision int64 `json:"revision"`
	// LocationID is the stock location the caller DECLARED; it may be empty.
	//
	// When empty the location is chosen per line and during the saga
	// (see [reserveInventoryStep.locationFor]). The result of that choice is
	// NOT written back here: the plan is the immutable input of the saga and
	// steps do not modify it; which line was taken from which warehouse is
	// written to the reservation trail (see [reservationRef]).
	LocationID string `json:"location_id"`
	// PaymentProviderID is the provider the payment is opened at.
	PaymentProviderID string `json:"payment_provider_id"`
	// Amount is the total to be collected (minor unit).
	Amount int64 `json:"amount"`
	// Subtotal is the sum of the line subtotals.
	Subtotal int64 `json:"subtotal"`
	// DiscountTotal is the total discount; carried positive and subtracted.
	DiscountTotal int64 `json:"discount_total"`
	// TaxTotal is the total tax.
	TaxTotal int64 `json:"tax_total"`
	// ShippingTotal is the total shipping amount.
	ShippingTotal int64 `json:"shipping_total"`
	// Lines are the lines that will enter the order and the reservation.
	Lines []planLine `json:"lines"`

	// PaymentData is the free-form data passed to the provider and it is NOT
	// WRITTEN TO THE RECORD.
	//
	// The field may carry sensitive data such as a card token; the execution
	// record, on the other hand, is a durable ledger and is read during manual
	// intervention. Section 8 of the plan asks that sensitive data not be
	// carried along, which is why the field is EXCLUDED from JSON and lives
	// only in memory, up to the step's call.
	PaymentData json.RawMessage `json:"-"`
}

// planLine is the form of a cart line that enters the order and the reservation.
type planLine struct {
	// LineItemID is the identity of the cart line; the reservation binds to it.
	LineItemID string `json:"line_item_id"`
	// VariantID is the product variant the line points at.
	VariantID string `json:"variant_id"`
	// InventoryItemID is the inventory item the variant is linked to.
	InventoryItemID string `json:"inventory_item_id"`
	// Title is the displayed name of the line; it is COPIED from the catalog.
	Title string `json:"title"`
	// Quantity is the count on the line.
	Quantity int64 `json:"quantity"`
	// UnitPrice is the unit price (minor unit).
	UnitPrice int64 `json:"unit_price"`
	// Subtotal is the subtotal of the line: UnitPrice x Quantity.
	Subtotal int64 `json:"subtotal"`
	// DiscountTotal is the discount falling on the line; carried positive.
	DiscountTotal int64 `json:"discount_total"`
	// TaxTotal is the tax falling on the line.
	TaxTotal int64 `json:"tax_total"`
	// TaxRateBps is the rate the tax was computed at, in BASIS POINTS.
	//
	// It travels with the amount because the amount cannot be turned back into
	// it: the tax is rounded down per line, so more than one rate produces the
	// same figure. An invoice prints the rate of every line and must print the
	// one that was CHARGED, not one recomputed afterwards.
	TaxRateBps int32 `json:"tax_rate_bps"`
	// Total is the total of the line: Subtotal - DiscountTotal + TaxTotal.
	Total int64 `json:"total"`
}

// prepare builds the input of the saga and leaves NO reversible side effect.
//
// The order is deliberate:
//
//  1. The totals are REFRESHED. This is mandatory for two things: the order
//     needs a per-line amount, and the cart module refuses to complete a cart
//     whose totals are STALE. Had the totals been refreshed in the last step of
//     the saga, we would be facing a MarkCompleted that fails after the money
//     has already been taken.
//  2. The snapshot is read AFTER the totals and the shape counters of the two
//     sides are compared. If they are not equal the cart changed in between and
//     the totals no longer belong to that cart; the call stops with
//     errors.Conflict.
//  3. Titles and inventory items are read IN BULK (there is no N+1).
//
// The only operation that could count as a write is writing the totals to the
// cart, and that one needs NO compensation: writing totals is idempotent,
// staleness is an already visible state, and returning the customer's cart to
// an old amount because of a transient fault would fix nothing.
func (w *Workflows) prepare(ctx context.Context, in CompleteCartInput) (*checkoutPlan, error) {
	totals, err := w.totals.CalculateTotals(ctx, in.CartID)
	if err != nil {
		return nil, err
	}

	snap, err := w.snapshot(ctx, in.CartID)
	if err != nil {
		return nil, err
	}
	if snap.Completed {
		return nil, errors.Conflict(CodeCartCompleted,
			"cannot create an order from a completed cart: %s", in.CartID)
	}
	if len(snap.Items) == 0 {
		return nil, errors.Conflict(CodeCartEmpty,
			"cannot create an order from a cart with no lines: %s", in.CartID)
	}
	if snap.Revision != totals.Revision {
		return nil, errors.Conflict(CodeCartChanged,
			"cart changed between the totals and the read: %s (totals %d, cart %d); the request must be resent",
			in.CartID, totals.Revision, snap.Revision)
	}

	lines, err := w.planLines(ctx, snap, totals)
	if err != nil {
		return nil, err
	}

	plan := &checkoutPlan{
		CartID:            snap.ID,
		RegionID:          snap.RegionID,
		CustomerID:        snap.CustomerID,
		Email:             in.Email,
		CurrencyCode:      snap.CurrencyCode,
		Revision:          snap.Revision,
		LocationID:        in.LocationID,
		PaymentProviderID: in.PaymentProviderID,
		Amount:            totals.Total,
		Subtotal:          totals.Subtotal,
		DiscountTotal:     totals.DiscountTotal,
		TaxTotal:          totals.TaxTotal,
		ShippingTotal:     totals.ShippingTotal,
		Lines:             lines,
		PaymentData:       in.PaymentData,
	}
	if err := plan.validate(); err != nil {
		return nil, err
	}
	if in.ExpectedTotal > 0 && in.ExpectedTotal != plan.Amount {
		return nil, errors.Conflict(CodeTotalMismatch,
			"cart amount differs from the approved amount: approved %d, calculated %d (%s)",
			in.ExpectedTotal, plan.Amount, plan.CartID)
	}
	return plan, nil
}

// snapshot reads and decodes the snapshot of the cart.
func (w *Workflows) snapshot(ctx context.Context, cartID string) (Snapshot, error) {
	payload, err := w.carts.CartSnapshotJSON(ctx, cartID)
	if err != nil {
		return Snapshot{}, err
	}
	return decodeSnapshot(cartID, payload)
}

// decodeSnapshot decodes and VALIDATES the body coming from the cart module.
//
// The validation is done even though the body comes from the cart module: this
// boundary is the one boundary the compiler cannot check (the accepted price of
// ADR 0006), and if a corrupt field silently made its way into the order, the
// mistake would show up on the customer's invoice. A corrupt body is
// errors.Internal — there is nothing the caller could fix, the provider has
// broken the contract.
func decodeSnapshot(cartID string, payload json.RawMessage) (Snapshot, error) {
	var snap Snapshot
	if len(payload) == 0 {
		return Snapshot{}, errors.Internal(CodeSnapshotInvalid,
			"cart snapshot came back empty: %s", cartID)
	}
	if err := json.Unmarshal(payload, &snap); err != nil {
		return Snapshot{}, errors.Wrap(err, errors.KindInternal, CodeSnapshotInvalid,
			"cart snapshot could not be decoded: %s", cartID)
	}
	if snap.ID != cartID {
		return Snapshot{}, errors.Internal(CodeSnapshotInvalid,
			"snapshot belongs to another cart: requested %s, received %q", cartID, snap.ID)
	}
	if snap.RegionID == "" {
		return Snapshot{}, errors.Internal(CodeSnapshotInvalid, "cart region is empty: %s", cartID)
	}
	if snap.CurrencyCode == "" {
		return Snapshot{}, errors.Internal(CodeSnapshotInvalid, "cart currency is empty: %s", cartID)
	}

	for i := range snap.Items {
		if snap.Items[i].ID == "" {
			return Snapshot{}, errors.Internal(CodeSnapshotInvalid,
				"cart has a line without an identity: %s", cartID)
		}
		if snap.Items[i].VariantID == "" {
			return Snapshot{}, errors.Internal(CodeSnapshotInvalid,
				"line variant is empty: %s (%q)", cartID, snap.Items[i].ID)
		}
	}
	return snap, nil
}

// planLines JOINS the snapshot with the totals.
//
// The join is done over the line identity; the line order of the totals is not
// trusted. A line present in the cart but absent from the totals is
// errors.Internal: the totals are obliged to cover ALL lines of the cart (see
// SetTotals in the cart module) and a missing line means goods the customer did
// not pay for.
func (w *Workflows) planLines(ctx context.Context, snap Snapshot, totals cartwf.Totals) ([]planLine, error) {
	byLine := make(map[string]cartwf.LineTotals, len(totals.Lines))
	for i := range totals.Lines {
		byLine[totals.Lines[i].LineItemID] = totals.Lines[i]
	}
	if len(byLine) != len(snap.Items) {
		return nil, errors.Internal(CodeTotalsInvalid,
			"the totals do not cover the lines of the cart: %s (cart %d lines, totals %d lines)",
			snap.ID, len(snap.Items), len(byLine))
	}

	variantIDs := snap.VariantIDs()
	titles, err := w.variantTitles(ctx, variantIDs)
	if err != nil {
		return nil, err
	}
	items, err := w.inventoryItems(ctx, variantIDs)
	if err != nil {
		return nil, err
	}

	lines := make([]planLine, 0, len(snap.Items))
	for i := range snap.Items {
		item := snap.Items[i]
		amounts, ok := byLine[item.ID]
		if !ok {
			return nil, errors.Internal(CodeTotalsInvalid,
				"the line has no totals: %s (%q)", snap.ID, item.ID)
		}

		lines = append(lines, planLine{
			LineItemID:      item.ID,
			VariantID:       item.VariantID,
			InventoryItemID: items[item.VariantID],
			Title:           titles[item.VariantID],
			Quantity:        item.Quantity,
			UnitPrice:       amounts.UnitPrice,
			Subtotal:        amounts.Subtotal,
			DiscountTotal:   amounts.DiscountTotal,
			TaxTotal:        amounts.TaxTotal,
			TaxRateBps:      amounts.TaxRateBps,
			Total:           amounts.Total,
		})
	}
	return lines, nil
}

// variantTitles reads the catalog titles of the variants in a SINGLE query.
//
// # Why the title is read from the catalog
//
// The title of an order line is MANDATORY and is COPIED from the variant: even
// if the catalog changes afterwards, the name seen on the order does not
// change. The cart module keeps the title on its own line but does not publish
// it on its cross-module surface, and the order module does not know product;
// the only party that could copy it is this flow.
//
// The read goes through Query because the read signatures of the product
// service speak in its own model types and are closed to cross-module calls;
// Query exists for exactly this gap (ADR 0004).
func (w *Workflows) variantTitles(ctx context.Context, variantIDs []string) (map[string]string, error) {
	if len(variantIDs) == 0 {
		return map[string]string{}, nil
	}

	records, err := w.catalog.Graph(ctx, query.GraphSpec{
		Entity:  EntityVariant,
		Fields:  []string{query.IDField, FieldTitle},
		Filters: map[string]any{FilterIDs: variantIDs},
		Limit:   len(variantIDs),
	})
	if err != nil {
		// An infrastructure fault is not reported as a BUSINESS state: "the
		// variant is not in the catalog" is a permanent state and the client
		// branches on it, whereas a transient read fault can be retried. The
		// kind of the underlying error is PRESERVED.
		return nil, errors.Wrap(err, errors.KindOf(err), CodeCatalogReadFailed,
			"the variants could not be read from the catalog (%d variants)", len(variantIDs))
	}

	titles := make(map[string]string, len(records))
	for i := range records {
		id, idOK := records[i][query.IDField].(string)
		title, titleOK := records[i][FieldTitle].(string)
		if !idOK || !titleOK || title == "" {
			return nil, errors.Internal(CodeVariantUnknown,
				"the catalog record could not be read: %v", records[i])
		}
		titles[id] = title
	}

	for _, variantID := range variantIDs {
		if titles[variantID] == "" {
			return nil, errors.NotFound(CodeVariantUnknown,
				"variant %s is not in the catalog; an order line cannot be written without a title", variantID)
		}
	}
	return titles, nil
}

// inventoryItems resolves the inventory items of the variants with a SINGLE
// link query.
//
// # A variant with no inventory item is REJECTED
//
// The decision is errors.Invalid. No reservation can be opened for a variant
// that has no inventory item; SKIPPING it silently would mean selling goods
// whose stock was never set aside. The error is not NotFound because the
// variant DOES exist; what is missing is its being linked to inventory
// tracking, and the caller can fix the request.
//
// # More than one item
//
// The "product_variant_inventory" definition is singular. If more than one item
// is seen nonetheless, which one the stock is taken from is undefined; silently
// picking the first would tie the goods sold to an ordering accident. That is
// why the situation is reported with errors.Internal: the data has gone corrupt
// behind the constraint.
func (w *Workflows) inventoryItems(ctx context.Context, variantIDs []string) (map[string]string, error) {
	if len(variantIDs) == 0 {
		return map[string]string{}, nil
	}

	linked, err := w.links.ListMany(ctx, LinkVariantInventory, variantIDs)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeLinkReadFailed,
			"the %q link could not be read (%d variants)", LinkVariantInventory, len(variantIDs))
	}

	out := make(map[string]string, len(variantIDs))
	for _, variantID := range variantIDs {
		items := linked[variantID]
		switch len(items) {
		case 0:
			return nil, errors.Invalid(CodeVariantNotStocked,
				"variant %s is not linked to any inventory item; a product whose stock cannot be reserved cannot be ordered",
				variantID)
		case 1:
			out[variantID] = items[0]
		default:
			return nil, errors.Internal(CodeVariantInventoryAmbiguous,
				"variant %s appears to be linked to %d inventory items; the %q definition must be singular",
				variantID, len(items), LinkVariantInventory)
		}
	}
	return out, nil
}

// validate verifies the arithmetic and the bounds of the plan.
//
// The validation is done in the order module as well and the repetition is
// DELIBERATE: the check over there runs after the stock has been reserved, the
// one here runs with NO side effect applied. The price of corrupt totals must
// not be stock that is reserved and released again plus an execution record
// opened for nothing.
//
// The identities checked are these: line subtotal = unit price x quantity, line
// total = subtotal - discount + tax, the cart subtotal is the sum of the line
// subtotals, and the amount to be collected = subtotal - discount + tax +
// shipping.
//
// # Every term is clamped to its range BEFORE the identity is tested
//
// The cart-level discount and tax go through [checkAmount] just like the
// line-level ones, and this is MANDATORY: the identity is tested with raw int64
// arithmetic, meaning that a self-validating set of totals can be produced out
// of unchecked terms. There were two concrete leaks — a negative cart discount
// INFLATES the amount to be collected without breaking the identity
// (2500 - (-100000) + … overcharges the customer), while an overflowing tax and
// an overflowing discount cancel each other out, "satisfy" the identity and the
// order was opened with MaxInt64 tax.
// Once every term is clamped into the [0, MaxTotal] range, the sum of the four
// terms is at most 3 x 10^18 and fits in int64; overflow becomes structurally
// impossible.
func (p *checkoutPlan) validate() error {
	if len(p.Lines) == 0 {
		return errors.Conflict(CodeCartEmpty,
			"cannot create an order from a cart with no lines: %s", p.CartID)
	}

	var subtotal int64
	for i := range p.Lines {
		line := p.Lines[i]
		if line.Quantity < MinQuantity || line.Quantity > MaxQuantity {
			return errors.Internal(CodeAmountInvalid,
				"the line quantity must be within [%d, %d]: %s -> %d",
				MinQuantity, MaxQuantity, line.LineItemID, line.Quantity)
		}
		if err := checkAmount("unit_price", line.UnitPrice, MaxAmount); err != nil {
			return err
		}
		if err := checkAmount("line_tax_total", line.TaxTotal, MaxTotal); err != nil {
			return err
		}
		if err := checkAmount("line_discount_total", line.DiscountTotal, MaxTotal); err != nil {
			return err
		}

		expected, err := mulAmount(line.UnitPrice, line.Quantity)
		if err != nil {
			return err
		}
		if expected != line.Subtotal {
			return errors.Internal(CodeAmountInvalid,
				"the line subtotal is not unit price x quantity: %s (%d x %d ≠ %d)",
				line.LineItemID, line.UnitPrice, line.Quantity, line.Subtotal)
		}
		if line.Total != line.Subtotal-line.DiscountTotal+line.TaxTotal {
			return errors.Internal(CodeAmountInvalid,
				"the line total does not satisfy the identity: %s (%d ≠ %d - %d + %d)",
				line.LineItemID, line.Total, line.Subtotal, line.DiscountTotal, line.TaxTotal)
		}

		subtotal, err = addAmount(subtotal, line.Subtotal)
		if err != nil {
			return err
		}
	}

	if subtotal != p.Subtotal {
		return errors.Internal(CodeAmountInvalid,
			"the cart subtotal is not the sum of the line subtotals: %s (%d ≠ %d)",
			p.CartID, p.Subtotal, subtotal)
	}
	if err := checkAmount("discount_total", p.DiscountTotal, MaxTotal); err != nil {
		return err
	}
	if err := checkAmount("tax_total", p.TaxTotal, MaxTotal); err != nil {
		return err
	}
	if err := checkAmount("shipping_total", p.ShippingTotal, MaxTotal); err != nil {
		return err
	}
	if p.Amount != p.Subtotal-p.DiscountTotal+p.TaxTotal+p.ShippingTotal {
		return errors.Internal(CodeAmountInvalid,
			"the cart total does not satisfy the identity: %s (%d ≠ %d - %d + %d + %d)",
			p.CartID, p.Amount, p.Subtotal, p.DiscountTotal, p.TaxTotal, p.ShippingTotal)
	}
	if p.Amount <= 0 {
		// The payment module rejects a zero-amount collection and it is right: a
		// collection that can never become "captured" is a dead record waiting
		// for payment forever. A free order (a fully discounted cart) is a
		// separate flow with NO payment step and its plan belongs to Phase 7+.
		return errors.Invalid(CodeAmountInvalid,
			"the amount to be collected must be positive: %s -> %d", p.CartID, p.Amount)
	}
	return checkAmount("amount", p.Amount, MaxTotal)
}

// orderSnapshot is the JSON schema of the snapshot that becomes an order.
//
// The field names MUST be EXACTLY the same as the schema the order module
// expects; because this package cannot import that module the compiler cannot
// check the match, and the match can only be proven with an integration test
// (the accepted price of ADR 0006).
type orderSnapshot struct {
	CartID         string              `json:"cart_id"`
	RegionID       string              `json:"region_id"`
	CustomerID     string              `json:"customer_id"`
	Email          string              `json:"email"`
	CurrencyCode   string              `json:"currency_code"`
	IdempotencyKey string              `json:"idempotency_key"`
	Subtotal       int64               `json:"subtotal"`
	DiscountTotal  int64               `json:"discount_total"`
	TaxTotal       int64               `json:"tax_total"`
	ShippingTotal  int64               `json:"shipping_total"`
	Total          int64               `json:"total"`
	Items          []orderSnapshotItem `json:"items"`
}

// orderSnapshotItem is the JSON schema of an order line.
type orderSnapshotItem struct {
	VariantID     string `json:"variant_id"`
	Title         string `json:"title"`
	Quantity      int64  `json:"quantity"`
	UnitPrice     int64  `json:"unit_price"`
	Subtotal      int64  `json:"subtotal"`
	DiscountTotal int64  `json:"discount_total"`
	TaxTotal      int64  `json:"tax_total"`
	TaxRateBps    int32  `json:"tax_rate_bps"`
	Total         int64  `json:"total"`
}

// orderSnapshotJSON converts the plan into the body the order expects.
//
// idempotencyKey is the identity of the execution: a call repeated within the
// same execution does not open a new order, it returns the identity of the
// existing order. A new execution gets a new identity, which means a flow
// started after a compensated attempt may open a new order.
func (p *checkoutPlan) orderSnapshotJSON(idempotencyKey string) (json.RawMessage, error) {
	items := make([]orderSnapshotItem, 0, len(p.Lines))
	for i := range p.Lines {
		items = append(items, orderSnapshotItem{
			VariantID:     p.Lines[i].VariantID,
			Title:         p.Lines[i].Title,
			Quantity:      p.Lines[i].Quantity,
			UnitPrice:     p.Lines[i].UnitPrice,
			Subtotal:      p.Lines[i].Subtotal,
			DiscountTotal: p.Lines[i].DiscountTotal,
			TaxTotal:      p.Lines[i].TaxTotal,
			TaxRateBps:    p.Lines[i].TaxRateBps,
			Total:         p.Lines[i].Total,
		})
	}

	payload, err := json.Marshal(orderSnapshot{
		CartID:         p.CartID,
		RegionID:       p.RegionID,
		CustomerID:     p.CustomerID,
		Email:          p.Email,
		CurrencyCode:   p.CurrencyCode,
		IdempotencyKey: idempotencyKey,
		Subtotal:       p.Subtotal,
		DiscountTotal:  p.DiscountTotal,
		TaxTotal:       p.TaxTotal,
		ShippingTotal:  p.ShippingTotal,
		Total:          p.Amount,
		Items:          items,
	})
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeSnapshotInvalid,
			"the order snapshot could not be converted to JSON: %s", p.CartID)
	}
	return payload, nil
}
