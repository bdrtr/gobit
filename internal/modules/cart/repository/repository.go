// Package repository is the cart module's database access.
//
// It touches ONLY this module's tables (plan Section 4). The sqlc generated code
// lives under repository/cartdb and is not edited by hand; this package adds two
// things on top of it:
//
//   - Conversion: pgtype and the generated row types DO NOT LEAVE THIS PACKAGE,
//     they are converted to models types (see convert.go).
//   - Classification: driver errors are converted to core/errors typed errors; a
//     missing row becomes NotFound, a uniqueness violation Conflict, an identity
//     violation Invalid.
//
// # Carrying the transaction
//
// [Repository.WithTx] opens a transaction and puts it into the CONTEXT; every
// repository method called during that transaction runs in the same transaction
// as long as it receives that context. The alternative was to put a separate
// interface type carrying the transaction handle into the method signatures; in
// that case the service could not match this package STRUCTURALLY with the narrow
// interface it declares in its own package — in Go the named types in a signature
// have to be identical, meaning the service would have been forced to import the
// repository (ADR 0001 forbids that). Carrying it in the context reduces the
// signatures to the types both sides share (context.Context, models.*).
//
// [Repository.LockCart] returns an error if it is called OUTSIDE a transaction:
// since a FOR UPDATE lock is released once the transaction ends, a lock without a
// transaction would silently protect nothing.
package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/repository/cartdb"
)

// rollbackTimeout is the time granted to a rollback on a canceled context. The
// rollback has to be attempted even when the caller's ctx has expired; otherwise
// the transaction would stay open until the connection returns to the pool.
const rollbackTimeout = 5 * time.Second

// txKeyType is the type of the context key; it is unexported so that it cannot be
// produced from outside.
type txKeyType struct{}

// txKey is the transaction handle's key in the context.
var txKey = txKeyType{}

// Repository is the access to the cart tables. It is safe for concurrent use.
type Repository struct {
	pool *pgxpool.Pool
}

// New builds a Repository running on the given pool.
func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// WithTx runs fn in a single database transaction.
//
// The context given to fn carries the transaction; every repository method called
// with that context runs in the same transaction. If fn returns an error or
// panics, the transaction is rolled back and the error (on a panic, the panic) is
// handed upward.
//
// If the calls nest, a new transaction is NOT opened, the existing one is used:
// opening a nested transaction means a savepoint in PostgreSQL and would give
// misleading confidence about the outer transaction's atomicity.
func (r *Repository) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := txFromContext(ctx); ok {
		return fn(ctx)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return classify(err, "cart_tx_begin_failed", "the transaction could not be started")
	}

	committed := false
	defer func() {
		if committed {
			return
		}
		// A short-lived context detached from the caller's is used: if the
		// caller's ctx has been canceled, a rollback made with it would fail
		// immediately as well.
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()

	if err := fn(context.WithValue(ctx, txKey, tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return classify(err, "cart_tx_commit_failed", "the transaction could not be committed")
	}
	committed = true
	return nil
}

// WithReadTx runs fn in a read-only, REPEATABLE READ transaction.
//
// It is meant for a READ path that has more than one query (the service's GetCart
// fetches the cart, the line items, the addresses and the shipping methods with
// separate queries): so that all the queries see the SAME state of the cart. No
// lock is taken.
//
// # Why REPEATABLE READ
//
// PostgreSQL's default is READ COMMITTED and there the snapshot is taken per
// STATEMENT, not per TRANSACTION; wrapping the queries in an ordinary transaction
// would not have prevented a torn view. The level that freezes the view at the
// transaction's first statement and keeps it to the end is REPEATABLE READ.
// Marking it read-only is deliberate too: a write on this path by mistake is
// blocked by the database, and the serialization errors REPEATABLE READ would
// bring at the write level never arise at all.
//
// If a transaction is already open a new one is NOT opened, the existing one
// is used: when this path is called from inside a write transaction, that
// transaction's view is already consistent, and trying to change the outer
// transaction's isolation level from the inside would raise an error.
func (r *Repository) WithReadTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := txFromContext(ctx); ok {
		return fn(ctx)
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return classify(err, "cart_tx_begin_failed", "the read-only transaction could not be started")
	}
	defer func() {
		// A short-lived context detached from the caller's is used: if the
		// caller's ctx has been canceled, a rollback made with it would fail
		// immediately as well.
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		// In a read-only transaction there is nothing to write; commit and
		// rollback come to the same thing and rollback also works on a canceled
		// context.
		_ = tx.Rollback(rollbackCtx)
	}()

	return fn(context.WithValue(ctx, txKey, tx))
}

// txFromContext returns the transaction handle in the context.
func txFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey).(pgx.Tx)
	return tx, ok
}

// queries returns the query set matching the context: the one bound to the
// transaction if there is one, otherwise the one bound to the pool.
func (r *Repository) queries(ctx context.Context) *cartdb.Queries {
	if tx, ok := txFromContext(ctx); ok {
		return cartdb.New(tx)
	}
	return cartdb.New(r.pool)
}

// requireTx verifies that the lock-taking methods are called inside a
// transaction.
func requireTx(ctx context.Context, op string) error {
	if _, ok := txFromContext(ctx); !ok {
		return errors.Internal(codeTxRequired,
			"%s has to be called inside a transaction; a FOR UPDATE lock without a transaction protects nothing", op)
	}
	return nil
}

// cartNotFound builds the shared error for a missing cart.
func cartNotFound(id string) error {
	return errors.NotFound(codeCartNotFound, "cart not found: %s", id)
}

// --- carts -------------------------------------------------------------------

// CreateCart records a new cart.
func (r *Repository) CreateCart(ctx context.Context, cart models.Cart) (models.Cart, error) {
	meta, err := fromJSONMap(cart.Metadata)
	if err != nil {
		return models.Cart{}, err
	}

	row, err := r.queries(ctx).CreateCart(ctx, cartdb.CreateCartParams{
		ID:           cart.ID,
		RegionID:     cart.RegionID,
		CustomerID:   nullString(cart.CustomerID),
		Email:        nullString(cart.Email),
		CurrencyCode: cart.CurrencyCode,
		Metadata:     meta,
	})
	if err != nil {
		return models.Cart{}, classify(err, codeQueryFailed, "the cart could not be created")
	}
	return toCart(row)
}

// GetCart returns the cart by its ID; NotFound if there is none.
func (r *Repository) GetCart(ctx context.Context, id string) (models.Cart, error) {
	row, err := r.queries(ctx).GetCart(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Cart{}, cartNotFound(id)
		}
		return models.Cart{}, classify(err, codeQueryFailed, "the cart could not be read")
	}
	return toCart(row)
}

// LockCart locks the cart for the duration of the transaction and returns its
// current state.
//
// EVERY flow that changes the cart starts with this; the lock order is single and
// the same in every flow (the cart first, then the child rows). NotFound if the
// cart does not exist; an error if it is called outside a transaction.
func (r *Repository) LockCart(ctx context.Context, id string) (models.Cart, error) {
	if err := requireTx(ctx, "LockCart"); err != nil {
		return models.Cart{}, err
	}
	row, err := r.queries(ctx).LockCart(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Cart{}, cartNotFound(id)
		}
		return models.Cart{}, classify(err, codeQueryFailed, "the cart could not be locked")
	}
	return toCart(row)
}

// ListCarts returns carts filtered and paginated.
//
// The second return value is the count of ALL the rows matching the filter, not
// of the page. The total comes from a SEPARATE query and applies the same filters
// as the list; it is correct even when the page is out of range and no row comes
// back. A row written between the two queries can change the total by one: the
// total is the informative field of the pagination envelope, no decision about an
// operation is based on it.
func (r *Repository) ListCarts(ctx context.Context, filter models.CartFilter) ([]models.Cart, int64, error) {
	rows, err := r.queries(ctx).ListCarts(ctx, cartdb.ListCartsParams{
		CustomerID: filter.CustomerID,
		RegionID:   filter.RegionID,
		Completed:  filter.Completed,
		RowLimit:   filter.Limit,
		RowOffset:  filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "the carts could not be listed")
	}

	total, err := r.queries(ctx).CountCarts(ctx, cartdb.CountCartsParams{
		CustomerID: filter.CustomerID,
		RegionID:   filter.RegionID,
		Completed:  filter.Completed,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "the carts could not be counted")
	}

	carts, err := toCarts(rows)
	if err != nil {
		return nil, 0, err
	}
	return carts, total, nil
}

// CartsByIDs returns the carts of the given IDs in a SINGLE query.
// No row comes back for an ID that is not found; that is not an error.
func (r *Repository) CartsByIDs(ctx context.Context, ids []string) ([]models.Cart, error) {
	if len(ids) == 0 {
		return []models.Cart{}, nil
	}
	rows, err := r.queries(ctx).GetCartsByIDs(ctx, ids)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the carts could not be read")
	}
	return toCarts(rows)
}

// UpdateCartContact writes the cart's email and customer fields with ABSOLUTE
// values.
//
// The empty string is stored as NULL: if "has no email" and "has an empty text as
// email" were two separate states in the database, the same cart would look
// different in two different queries.
//
// If the cart does not exist, has been deleted or is COMPLETED, no row is
// updated; the query's WHERE leaves a completed cart out and in that case
// Conflict is returned.
func (r *Repository) UpdateCartContact(ctx context.Context, id string, contact models.CartContact) (models.Cart, error) {
	row, err := r.queries(ctx).UpdateCartContact(ctx, cartdb.UpdateCartContactParams{
		ID:         id,
		Email:      nullString(contact.Email),
		CustomerID: nullString(contact.CustomerID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Cart{}, r.writeBlocked(ctx, id, "the cart could not be updated")
		}
		return models.Cart{}, classify(err, codeQueryFailed, "the cart's contact fields could not be updated")
	}
	return toCart(row)
}

// UpdateCartTotals writes the cart's totals fields and stamps which shape they
// were calculated for.
//
// If the cart does not exist, has been deleted or is COMPLETED, no row is
// updated; the query's WHERE leaves a completed cart out and in that case
// Conflict is returned.
func (r *Repository) UpdateCartTotals(ctx context.Context, id string, totals models.CartTotals) (models.Cart, error) {
	row, err := r.queries(ctx).UpdateCartTotals(ctx, cartdb.UpdateCartTotalsParams{
		ID:             id,
		Subtotal:       totals.Subtotal,
		DiscountTotal:  totals.DiscountTotal,
		TaxTotal:       totals.TaxTotal,
		ShippingTotal:  totals.ShippingTotal,
		Total:          totals.Total,
		TotalsRevision: totals.Revision,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Cart{}, r.writeBlocked(ctx, id, "the totals could not be written")
		}
		return models.Cart{}, classify(err, codeQueryFailed, "the cart totals could not be updated")
	}
	return toCart(row)
}

// BumpCartRevision raises the cart's shape counter by one.
//
// It is called in the SAME transaction after every structural change that affects
// the totals; that way the totals having gone stale becomes readable with
// [models.Cart.TotalsStale].
func (r *Repository) BumpCartRevision(ctx context.Context, id string) (models.Cart, error) {
	row, err := r.queries(ctx).BumpCartRevision(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Cart{}, r.writeBlocked(ctx, id, "the cart could not be updated")
		}
		return models.Cart{}, classify(err, codeQueryFailed, "the cart revision could not be raised")
	}
	return toCart(row)
}

// MarkCartCompleted stamps the cart as completed.
//
// If the cart is already completed no row is updated and Conflict is returned: a
// second order cannot be born out of the same cart.
func (r *Repository) MarkCartCompleted(ctx context.Context, id string) (models.Cart, error) {
	row, err := r.queries(ctx).MarkCartCompleted(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Cart{}, r.writeBlocked(ctx, id, "the cart could not be completed")
		}
		return models.Cart{}, classify(err, codeQueryFailed, "the cart could not be completed")
	}
	return toCart(row)
}

// SoftDeleteCart soft-deletes the cart; it returns NotFound if the cart does not
// exist or is already deleted.
func (r *Repository) SoftDeleteCart(ctx context.Context, id string) error {
	affected, err := r.queries(ctx).SoftDeleteCart(ctx, id)
	if err != nil {
		return classify(err, codeQueryFailed, "the cart could not be deleted")
	}
	if affected == 0 {
		return cartNotFound(id)
	}
	return nil
}

// writeBlocked reads the REASON why a writing query affected no row at all.
//
// The queries' WHERE says "not deleted AND not completed"; zero rows corresponds
// to two different situations and the two are different error classes (404 vs
// 409). Returning a single error without reading the reason would have made it
// impossible for the caller to tell "the cart does not exist" from "the cart is
// closed".
func (r *Repository) writeBlocked(ctx context.Context, id, what string) error {
	cart, err := r.GetCart(ctx, id)
	if err != nil {
		return err
	}
	if cart.Completed() {
		return errors.Conflict(codeCartCompleted,
			"%s: the cart is completed and cannot be changed (%s)", what, id)
	}
	// The row is there, not deleted and not completed: the only difference left
	// is that a concurrent operation has changed the record. It can be retried.
	return errors.Conflict(codeConcurrentUpdate,
		"%s: the cart changed concurrently, the request can be retried (%s)", what, id)
}

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

// --- addresses ---------------------------------------------------------------

// UpsertCartAddress writes the cart's address of the given type; it overwrites an
// existing one.
//
// The ID is used only for a NEW row; while an existing record is updated its ID
// is KEPT. The ID staying stable means that a reference given to the address (a
// log record, an order copy) stays valid after a correction as well.
func (r *Repository) UpsertCartAddress(ctx context.Context, addr models.CartAddress) (models.CartAddress, error) {
	meta, err := fromJSONMap(addr.Metadata)
	if err != nil {
		return models.CartAddress{}, err
	}

	row, err := r.queries(ctx).UpsertCartAddress(ctx, cartdb.UpsertCartAddressParams{
		ID:              addr.ID,
		CartID:          addr.CartID,
		AddressType:     addr.Type.String(),
		SourceAddressID: nullString(addr.SourceAddressID),
		FirstName:       nullString(addr.FirstName),
		LastName:        nullString(addr.LastName),
		Company:         nullString(addr.Company),
		Address1:        nullString(addr.Address1),
		Address2:        nullString(addr.Address2),
		City:            nullString(addr.City),
		Province:        nullString(addr.Province),
		PostalCode:      nullString(addr.PostalCode),
		CountryCode:     nullString(addr.CountryCode),
		Phone:           nullString(addr.Phone),
		Metadata:        meta,
	})
	if err != nil {
		return models.CartAddress{}, classify(err, codeQueryFailed, "the cart address could not be written")
	}
	return toCartAddress(row)
}

// ListCartAddresses returns the cart's addresses (in type order).
func (r *Repository) ListCartAddresses(ctx context.Context, cartID string) ([]models.CartAddress, error) {
	rows, err := r.queries(ctx).ListCartAddresses(ctx, cartID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the cart addresses could not be listed")
	}

	out := make([]models.CartAddress, 0, len(rows))
	for i := range rows {
		addr, convErr := toCartAddress(rows[i])
		if convErr != nil {
			return nil, convErr
		}
		out = append(out, addr)
	}
	return out, nil
}

// SoftDeleteCartAddressesByCart soft-deletes all of the cart's addresses.
func (r *Repository) SoftDeleteCartAddressesByCart(ctx context.Context, cartID string) error {
	if err := r.queries(ctx).SoftDeleteCartAddressesByCart(ctx, cartID); err != nil {
		return classify(err, codeQueryFailed, "the cart addresses could not be deleted")
	}
	return nil
}

// --- shipping methods --------------------------------------------------------

// CreateShippingMethod adds a shipping method to the cart.
func (r *Repository) CreateShippingMethod(ctx context.Context, method models.ShippingMethod) (models.ShippingMethod, error) {
	data, err := fromJSONMap(method.Data)
	if err != nil {
		return models.ShippingMethod{}, err
	}

	row, err := r.queries(ctx).CreateShippingMethod(ctx, cartdb.CreateShippingMethodParams{
		ID:               method.ID,
		CartID:           method.CartID,
		Name:             method.Name,
		ShippingOptionID: nullString(method.ShippingOptionID),
		Amount:           method.Amount,
		Data:             data,
	})
	if err != nil {
		return models.ShippingMethod{}, classify(err, codeQueryFailed, "the shipping method could not be added")
	}
	return toShippingMethod(row)
}

// GetShippingMethod returns the shipping method by its ID; NotFound if there is
// none.
func (r *Repository) GetShippingMethod(ctx context.Context, cartID, methodID string) (models.ShippingMethod, error) {
	row, err := r.queries(ctx).GetShippingMethod(ctx, cartdb.GetShippingMethodParams{
		ID:     methodID,
		CartID: cartID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ShippingMethod{}, shippingMethodNotFound(cartID, methodID)
		}
		return models.ShippingMethod{}, classify(err, codeQueryFailed, "the shipping method could not be read")
	}
	return toShippingMethod(row)
}

// ListShippingMethods returns the cart's shipping methods.
func (r *Repository) ListShippingMethods(ctx context.Context, cartID string) ([]models.ShippingMethod, error) {
	rows, err := r.queries(ctx).ListShippingMethods(ctx, cartID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the shipping methods could not be listed")
	}

	out := make([]models.ShippingMethod, 0, len(rows))
	for i := range rows {
		method, convErr := toShippingMethod(rows[i])
		if convErr != nil {
			return nil, convErr
		}
		out = append(out, method)
	}
	return out, nil
}

// SoftDeleteShippingMethod soft-deletes the shipping method; it returns NotFound
// if there is none.
func (r *Repository) SoftDeleteShippingMethod(ctx context.Context, cartID, methodID string) error {
	affected, err := r.queries(ctx).SoftDeleteShippingMethod(ctx, cartdb.SoftDeleteShippingMethodParams{
		ID:     methodID,
		CartID: cartID,
	})
	if err != nil {
		return classify(err, codeQueryFailed, "the shipping method could not be removed")
	}
	if affected == 0 {
		return shippingMethodNotFound(cartID, methodID)
	}
	return nil
}

// SoftDeleteShippingMethodsByCart soft-deletes all of the cart's shipping
// methods.
func (r *Repository) SoftDeleteShippingMethodsByCart(ctx context.Context, cartID string) error {
	if err := r.queries(ctx).SoftDeleteShippingMethodsByCart(ctx, cartID); err != nil {
		return classify(err, codeQueryFailed, "the shipping methods could not be deleted")
	}
	return nil
}

// shippingMethodNotFound builds the shared error for a missing shipping method.
func shippingMethodNotFound(cartID, methodID string) error {
	return errors.NotFound(codeShippingNotFound,
		"shipping method not found (cart: %s, method: %s)", cartID, methodID)
}
