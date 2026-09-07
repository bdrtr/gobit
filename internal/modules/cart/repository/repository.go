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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/core/errors"
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
	// The cursor arrives as SQL NULL when it names no position; the COALESCE
	// sentinels in the query turn that into "start at the top". A zero TIME sent
	// instead would make the first page come back empty with no error anywhere.
	afterAt := pgtype.Timestamptz{}
	if !filter.After.Time.IsZero() {
		afterAt = pgtype.Timestamptz{Time: filter.After.Time, Valid: true}
	}

	var afterID *string
	if filter.After.ID != "" {
		afterID = &filter.After.ID
	}

	rows, err := r.queries(ctx).ListCarts(ctx, cartdb.ListCartsParams{
		CustomerID: filter.CustomerID,
		RegionID:   filter.RegionID,
		Completed:  filter.Completed,
		RowLimit:   filter.Limit,
		RowOffset:  filter.Offset,
		AfterAt:    afterAt,
		AfterID:    afterID,
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
