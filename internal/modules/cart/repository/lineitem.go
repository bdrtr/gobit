package repository

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/repository/cartdb"
)

// This file is the access to the cart_line_items table. The lines are a table of
// their own with their own queries, and they carry rules the cart row does not:
// the quantity write reads the line's OWN state, and the totals write is a batch
// over many lines at once. The cart itself is in repository.go.

// --- line items --------------------------------------------------------------

// CreateLineItem records a new cart line item.
func (r *Repository) CreateLineItem(ctx context.Context, item models.LineItem) (models.LineItem, error) {
	meta, err := fromJSONMap(item.Metadata)
	if err != nil {
		return models.LineItem{}, err
	}

	row, err := r.queries(ctx).CreateLineItem(ctx, cartdb.CreateLineItemParams{
		ID:        item.ID,
		CartID:    item.CartID,
		VariantID: item.VariantID,
		Title:     item.Title,
		Quantity:  item.Quantity,
		UnitPrice: item.UnitPrice,
		Metadata:  meta,
	})
	if err != nil {
		return models.LineItem{}, classify(err, codeQueryFailed, "the cart line item could not be created")
	}
	return toLineItem(row)
}

// GetLineItem returns the line item by its ID; NotFound if there is none.
//
// The cart ID is required as well: another cart's line item cannot be read even
// when its ID is known.
func (r *Repository) GetLineItem(ctx context.Context, cartID, lineID string) (models.LineItem, error) {
	row, err := r.queries(ctx).GetLineItem(ctx, cartdb.GetLineItemParams{
		ID:     lineID,
		CartID: cartID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.LineItem{}, lineItemNotFound(cartID, lineID)
		}
		return models.LineItem{}, classify(err, codeQueryFailed, "the cart line item could not be read")
	}
	return toLineItem(row)
}

// GetLineItemByVariant returns the living line item of the variant in the cart;
// NotFound if there is none.
func (r *Repository) GetLineItemByVariant(ctx context.Context, cartID, variantID string) (models.LineItem, error) {
	row, err := r.queries(ctx).GetLineItemByVariant(ctx, cartdb.GetLineItemByVariantParams{
		CartID:    cartID,
		VariantID: variantID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.LineItem{}, errors.NotFound(codeLineItemNotFound,
				"the cart has no line item for this variant (cart: %s, variant: %s)", cartID, variantID)
		}
		return models.LineItem{}, classify(err, codeQueryFailed, "the cart line item could not be read")
	}
	return toLineItem(row)
}

// ListLineItems returns the cart's line items in creation order.
func (r *Repository) ListLineItems(ctx context.Context, cartID string) ([]models.LineItem, error) {
	rows, err := r.queries(ctx).ListLineItems(ctx, cartID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the cart line items could not be listed")
	}
	return toLineItems(rows)
}

// LineItemsByCartIDs returns the line items of several carts in a SINGLE query
// (no N+1).
func (r *Repository) LineItemsByCartIDs(ctx context.Context, cartIDs []string) ([]models.LineItem, error) {
	if len(cartIDs) == 0 {
		return []models.LineItem{}, nil
	}
	rows, err := r.queries(ctx).ListLineItemsByCartIDs(ctx, cartIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the cart line items could not be listed")
	}
	return toLineItems(rows)
}

// SetLineItemQuantity writes the line item's quantity with an ABSOLUTE value.
//
// An incremental update (quantity = quantity + n) is deliberately not used: the
// new value is calculated from the value read under the lock, and the number the
// deciding code saw is the same as the number written.
func (r *Repository) SetLineItemQuantity(ctx context.Context, cartID, lineID string, quantity int64) (models.LineItem, error) {
	row, err := r.queries(ctx).SetLineItemQuantity(ctx, cartdb.SetLineItemQuantityParams{
		ID:       lineID,
		CartID:   cartID,
		Quantity: quantity,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.LineItem{}, lineItemNotFound(cartID, lineID)
		}
		return models.LineItem{}, classify(err, codeQueryFailed, "the cart line item's quantity could not be updated")
	}
	return toLineItem(row)
}

// SetLineItemTotals writes ALL the line amounts of one calculation round in a
// SINGLE statement; it does not touch the quantity.
//
// # Why a single statement
//
// The write happens under the cart's lock and in a single transaction; running
// one UPDATE per line held the lock for a time DIRECTLY PROPORTIONAL to the
// number of lines. It was measured (local container, TCP round trip ~30 µs,
// 100-line cart, from the taking of the lock to the return of the LAST WRITE,
// p50): one UPDATE per line 8.0 ms, the single statement here 0.55 ms — 14x in
// the write phase. Sending the same UPDATEs down a single pipeline (pgx batch)
// stayed at 3.0 ms, that is 63% of the gain; only bringing the statement count
// down to 1 gives the rest.
//
// The numbers DO NOT INCLUDE the commit's WAL flush (the harness runs fsync=off)
// and that flush is under the same lock: on a durable cluster it is 6.2 ms and
// this change does not touch it, so the end-to-end gain is ~2x. The distinction
// is detailed in the
// [github.com/bdrtr/gobit/internal/modules/cart/service.Service.SetTotals] godoc.
//
// There is NO SEPARATE ceiling for the slice size and none was added: since the
// caller has to give all of the cart's line items (see service.SetTotals) the
// size is the cart's line item count and workflows/cart.MaxLineItems (100 today)
// bounds that.
//
// # A missing write round DROPS everything
//
// The statement silently SKIPS a line whose ID does not match: a deleted line, an
// ID that never existed or ANOTHER CART'S line writes nothing (cart_id is in the
// WHERE). That is why the written IDs are compared with the requested ones and
// NotFound is returned if any is missing — the transaction is rolled back and the
// cart either takes all of the new amounts or none of them. Writing silently
// incomplete would split the cart's subtotal from the sum of its lines and the
// customer would be charged the wrong amount.
//
// The rule is the SECOND line of defense today: the service reads the line set
// under the lock and looks for full coverage, and every path that changes the
// cart takes the same lock, so a line cannot disappear between the read and the
// write. The check here keeps a path that skips the lock (direct SQL, a flow to
// be added later) from staying silent.
func (r *Repository) SetLineItemTotals(ctx context.Context, cartID string, lines []models.LineItemTotals) error {
	if len(lines) == 0 {
		return nil
	}

	// The slices are built in a SINGLE loop: the equality of the lengths and the
	// alignment of the indices are structurally guaranteed here. Separate loops
	// would bring back the possibility of pairing an amount with another line.
	arg := cartdb.SetLineItemTotalsParams{
		CartID:         cartID,
		LineIds:        make([]string, len(lines)),
		UnitPrices:     make([]int64, len(lines)),
		Subtotals:      make([]int64, len(lines)),
		DiscountTotals: make([]int64, len(lines)),
		TaxTotals:      make([]int64, len(lines)),
		Totals:         make([]int64, len(lines)),
	}
	requested := make(map[string]struct{}, len(lines))
	for i, line := range lines {
		// The same ID cannot be given twice: UPDATE ... FROM does not define
		// WHICH amount wins when one target row matches several source rows, so
		// the cart would take one of the two amounts at random. The service
		// already weeds this out; weeding it out here takes the statement's
		// undefined behavior out of the store and also protects a test that
		// calls directly.
		if _, dup := requested[line.LineItemID]; dup {
			return errors.Invalid(codeTotalsInconsistent,
				"more than one amount was given for the same line: %s", line.LineItemID)
		}
		requested[line.LineItemID] = struct{}{}

		arg.LineIds[i] = line.LineItemID
		arg.UnitPrices[i] = line.Totals.UnitPrice
		arg.Subtotals[i] = line.Totals.Subtotal
		arg.DiscountTotals[i] = line.Totals.DiscountTotal
		arg.TaxTotals[i] = line.Totals.TaxTotal
		arg.Totals[i] = line.Totals.Total
	}

	written, err := r.queries(ctx).SetLineItemTotals(ctx, arg)
	if err != nil {
		return classify(err, codeQueryFailed, "the amounts of the cart line items could not be updated")
	}
	if len(written) != len(lines) {
		return lineItemNotFound(cartID, firstUnwritten(lines, written))
	}
	return nil
}

// firstUnwritten returns the ID of the FIRST line not written, in the order the
// caller gave.
//
// The order comes from the caller's slice, not from RETURNING: PostgreSQL does
// not guarantee the RETURNING order and walking over a map would produce
// different error messages for the same input. The message being reproducible
// means the operator is able to tell two different failures apart.
//
// Since the IDs carry no duplicates (they are weeded out above) a count mismatch
// means at least one ID was not written; the loop always finds an ID.
func firstUnwritten(lines []models.LineItemTotals, written []string) string {
	writtenIDs := make(map[string]struct{}, len(written))
	for _, id := range written {
		writtenIDs[id] = struct{}{}
	}
	for _, line := range lines {
		if _, ok := writtenIDs[line.LineItemID]; !ok {
			return line.LineItemID
		}
	}
	return ""
}

// SoftDeleteLineItem soft-deletes the line item; it returns NotFound if there is
// no such line.
func (r *Repository) SoftDeleteLineItem(ctx context.Context, cartID, lineID string) error {
	affected, err := r.queries(ctx).SoftDeleteLineItem(ctx, cartdb.SoftDeleteLineItemParams{
		ID:     lineID,
		CartID: cartID,
	})
	if err != nil {
		return classify(err, codeQueryFailed, "the cart line item could not be deleted")
	}
	if affected == 0 {
		return lineItemNotFound(cartID, lineID)
	}
	return nil
}

// SoftDeleteLineItemsByCart soft-deletes all of the cart's line items.
func (r *Repository) SoftDeleteLineItemsByCart(ctx context.Context, cartID string) error {
	if err := r.queries(ctx).SoftDeleteLineItemsByCart(ctx, cartID); err != nil {
		return classify(err, codeQueryFailed, "the cart line items could not be deleted")
	}
	return nil
}

// lineItemNotFound builds the shared error for a missing line item.
func lineItemNotFound(cartID, lineID string) error {
	return errors.NotFound(codeLineItemNotFound,
		"cart line item not found (cart: %s, line: %s)", cartID, lineID)
}
