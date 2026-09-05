// Package repository is the database access of the order module.
//
// It touches ONLY the tables of this module (plan Section 4). The sqlc
// generated code is under repository/orderdb and is not edited by hand; this
// package adds two things on top of it:
//
//   - Conversion: pgtype and the generated row types DO NOT LEAVE THIS
//     PACKAGE, they are converted to models types (see convert.go).
//   - Classification: driver errors are converted into errors typed by
//     core/errors; a missing row becomes NotFound, a uniqueness violation
//     Conflict, an identity violation Invalid.
//
// # Carrying the transaction
//
// [Repository.WithTx] opens a transaction and puts it into the CONTEXT; every
// repository method called during the transaction runs in that same
// transaction as long as it receives that context. The alternative was to put
// a separate interface type carrying the transaction handle into the method
// signatures; in that case the service could not match this package
// STRUCTURALLY with the narrow interface it defines in its own package — in Go
// the named types in a signature have to be exactly the same, which means the
// service would have had to import the repository (ADR 0001 forbids that).
// Carrying it in the context reduces the signatures to the types both sides
// share (context.Context, models.*).
//
// [Repository.LockOrder] returns an error if it is called OUTSIDE a
// transaction: because a FOR UPDATE lock is released once the transaction
// ends, a lock without a transaction would silently protect nothing.
//
// # Why the state transitions are guarded here as well
//
// The CancelOrder / CompleteOrder / ArchiveOrder queries write the EXPECTED
// STATE into the WHERE condition. The service already does the same check
// under the lock and with a readable error; the condition here is the second
// gate, the one that also covers a transition made directly with SQL or with a
// call that bypasses the service's locking frame. If no row is affected,
// Conflict is returned: the row HAS BEEN READ under the lock, so its absence
// is not the explanation — the only explanation is that ITS STATE HAS CHANGED.
package repository

import (
	"context"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository/orderdb"
)

// rollbackTimeout is the time granted to a rollback on a canceled context.
// The rollback must be attempted even when the caller's ctx has expired;
// otherwise the transaction would stay open until the connection returns to
// the pool.
const rollbackTimeout = 5 * time.Second

// txKeyType is the type of the context key; it is unexported so that it cannot
// be produced from the outside.
type txKeyType struct{}

// txKey is the key of the transaction handle in the context.
var txKey = txKeyType{}

// Repository is the access to the order tables. It is safe for concurrent use.
type Repository struct {
	pool *pgxpool.Pool
}

// New produces a Repository working on the given pool.
func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// WithTx runs fn inside a single database transaction.
//
// The context given to fn carries the transaction; every repository method
// called with that context runs in the same transaction. If fn returns an
// error or panics, the transaction is rolled back and the error (on a panic,
// the panic) is passed upwards.
//
// If the calls nest, a new transaction is NOT opened, the existing one is
// used: opening a nested transaction means a savepoint in PostgreSQL and would
// give misleading confidence about the atomicity of the outer transaction.
func (r *Repository) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := txFromContext(ctx); ok {
		return fn(ctx)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return classify(err, "order_tx_begin_failed", "could not begin transaction")
	}

	committed := false
	defer func() {
		if committed {
			return
		}
		// A short-lived context independent of the caller's is used: if the
		// caller's ctx has been canceled, a rollback made with it would fail
		// instantly too.
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()

	if err := fn(context.WithValue(ctx, txKey, tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return classify(err, "order_tx_commit_failed", "could not commit transaction")
	}
	committed = true
	return nil
}

// WithReadTx runs fn in a read-only, REPEATABLE READ transaction.
//
// It is meant for a READ path that has more than one query (the service's
// GetOrder fetches the order, the lines and the summary with separate
// queries): so that all of the queries see the SAME state of the order. No
// lock is taken.
//
// # Why REPEATABLE READ
//
// PostgreSQL's default is READ COMMITTED, and there the snapshot is taken per
// STATEMENT, not per TRANSACTION; wrapping the queries in an ordinary
// transaction would not prevent a torn view. The level that freezes the view
// at the transaction's first statement and keeps it until the end is
// REPEATABLE READ. Marking it read-only is deliberate as well: a write on this
// path by mistake is blocked by the database, and the serialization errors
// that REPEATABLE READ would bring at the write level never arise at all.
//
// If a transaction is already open, a new one is NOT opened, the existing one
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
		return classify(err, "order_tx_begin_failed", "could not begin read-only transaction")
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		// In a read-only transaction there is nothing to write; commit and
		// rollback come to the same thing, and rollback works on a canceled
		// context too.
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
func (r *Repository) queries(ctx context.Context) *orderdb.Queries {
	if tx, ok := txFromContext(ctx); ok {
		return orderdb.New(tx)
	}
	return orderdb.New(r.pool)
}

// requireTx verifies that the locking methods are called inside a transaction.
func requireTx(ctx context.Context, op string) error {
	if _, ok := txFromContext(ctx); !ok {
		return errors.Internal(codeTxRequired,
			"%s must be called inside a transaction; a FOR UPDATE lock without a transaction protects nothing", op)
	}
	return nil
}

// orderNotFound produces the common error for a missing order.
func orderNotFound(id string) error {
	return errors.NotFound(codeOrderNotFound, "order not found: %s", id)
}

// stateChanged produces the common error for a transition whose state
// condition did not hold.
func stateChanged(id, op string) error {
	return errors.Conflict(codeStateChanged,
		"%s could not be applied: the order's status differs from the expected one (%s)", op, id)
}

// --- orders ------------------------------------------------------------------

// CreateOrder records a new order.
//
// display_id is NOT GIVEN as a parameter; its value is produced by the
// database's IDENTITY column and read back with RETURNING. That is why it is
// impossible for two concurrent calls to get the same number.
func (r *Repository) CreateOrder(ctx context.Context, order models.Order) (models.Order, error) {
	meta, err := fromJSONMap(order.Metadata)
	if err != nil {
		return models.Order{}, err
	}

	row, err := r.queries(ctx).CreateOrder(ctx, orderdb.CreateOrderParams{
		ID:             order.ID,
		Status:         order.Status.String(),
		RegionID:       order.RegionID,
		CustomerID:     nullString(order.CustomerID),
		Email:          nullString(order.Email),
		CurrencyCode:   order.CurrencyCode,
		CartID:         nullString(order.CartID),
		IdempotencyKey: nullString(order.IdempotencyKey),
		Subtotal:       order.Subtotal,
		DiscountTotal:  order.DiscountTotal,
		TaxTotal:       order.TaxTotal,
		ShippingTotal:  order.ShippingTotal,
		Total:          order.Total,
		Metadata:       meta,
	})
	if err != nil {
		return models.Order{}, classify(err, codeQueryFailed, "could not create the order")
	}
	return toOrder(row)
}

// GetOrder returns the order by its identifier; NotFound if there is none.
func (r *Repository) GetOrder(ctx context.Context, id string) (models.Order, error) {
	row, err := r.queries(ctx).GetOrder(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Order{}, orderNotFound(id)
		}
		return models.Order{}, classify(err, codeQueryFailed, "could not read the order")
	}
	return toOrder(row)
}

// GetOrderByDisplayID returns the order by its human-readable number; NotFound
// if there is none.
func (r *Repository) GetOrderByDisplayID(ctx context.Context, displayID int64) (models.Order, error) {
	row, err := r.queries(ctx).GetOrderByDisplayID(ctx, displayID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Order{}, errors.NotFound(codeOrderNotFound,
				"order not found: #%d", displayID)
		}
		return models.Order{}, classify(err, codeQueryFailed, "could not read the order")
	}
	return toOrder(row)
}

// GetOrderByIdempotencyKey returns the order opened with the key; NotFound if
// there is none.
func (r *Repository) GetOrderByIdempotencyKey(ctx context.Context, key string) (models.Order, error) {
	row, err := r.queries(ctx).GetOrderByIdempotencyKey(ctx, &key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Order{}, errors.NotFound(codeOrderNotFound,
				"no order found with this idempotency key")
		}
		return models.Order{}, classify(err, codeQueryFailed, "could not read the order")
	}
	return toOrder(row)
}

// LockOrder locks the order for the duration of the transaction and returns
// its current state.
func (r *Repository) LockOrder(ctx context.Context, id string) (models.Order, error) {
	if err := requireTx(ctx, "LockOrder"); err != nil {
		return models.Order{}, err
	}
	row, err := r.queries(ctx).LockOrder(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Order{}, orderNotFound(id)
		}
		return models.Order{}, classify(err, codeQueryFailed, "could not lock the order")
	}
	return toOrder(row)
}

// ListOrders filters and pages the orders; the second value is the total count.
func (r *Repository) ListOrders(ctx context.Context, filter models.OrderFilter) ([]models.Order, int64, error) {
	var status *string
	if filter.Status != nil {
		value := filter.Status.String()
		status = &value
	}

	// The cursor arrives as SQL NULL when it names no position; the COALESCE
	// sentinels in the query turn that into "start at the top".
	afterAt := pgtype.Timestamptz{}
	if !filter.After.Time.IsZero() {
		afterAt = pgtype.Timestamptz{Time: filter.After.Time, Valid: true}
	}

	var afterID *string
	if filter.After.ID != "" {
		afterID = &filter.After.ID
	}

	rows, err := r.queries(ctx).ListOrders(ctx, orderdb.ListOrdersParams{
		CustomerID: filter.CustomerID,
		RegionID:   filter.RegionID,
		Status:     status,
		RowLimit:   filter.Limit,
		RowOffset:  filter.Offset,
		AfterAt:    afterAt,
		AfterID:    afterID,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not list the orders")
	}

	total, err := r.queries(ctx).CountOrders(ctx, orderdb.CountOrdersParams{
		CustomerID: filter.CustomerID,
		RegionID:   filter.RegionID,
		Status:     status,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not count the orders")
	}

	orders, err := toOrders(rows)
	if err != nil {
		return nil, 0, err
	}
	return orders, total, nil
}

// OrdersByIDs fetches a set of identifiers in a SINGLE query (no N+1).
func (r *Repository) OrdersByIDs(ctx context.Context, ids []string) ([]models.Order, error) {
	if len(ids) == 0 {
		return []models.Order{}, nil
	}
	rows, err := r.queries(ctx).GetOrdersByIDs(ctx, ids)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not read the orders")
	}
	return toOrders(rows)
}

// CancelOrder cancels the order and stamps the moment of cancellation.
//
// Only an order in the 'pending' state is canceled; in any other state no row
// is affected and Conflict is returned (see the package documentation).
func (r *Repository) CancelOrder(ctx context.Context, id, reason string) (models.Order, error) {
	row, err := r.queries(ctx).CancelOrder(ctx, orderdb.CancelOrderParams{
		ID:           id,
		CancelReason: nullString(reason),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Order{}, stateChanged(id, "cancel")
		}
		return models.Order{}, classify(err, codeQueryFailed, "could not cancel the order")
	}
	return toOrder(row)
}

// CompleteOrder stamps the order as completed.
func (r *Repository) CompleteOrder(ctx context.Context, id string) (models.Order, error) {
	row, err := r.queries(ctx).CompleteOrder(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Order{}, stateChanged(id, "completion")
		}
		return models.Order{}, classify(err, codeQueryFailed, "could not complete the order")
	}
	return toOrder(row)
}

// ArchiveOrder moves a completed order into the archive.
func (r *Repository) ArchiveOrder(ctx context.Context, id string) (models.Order, error) {
	row, err := r.queries(ctx).ArchiveOrder(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Order{}, stateChanged(id, "archiving")
		}
		return models.Order{}, classify(err, codeQueryFailed, "could not archive the order")
	}
	return toOrder(row)
}

// --- order lines -------------------------------------------------------------

// CreateLineItem records a new order line.
func (r *Repository) CreateLineItem(ctx context.Context, item models.OrderLineItem) (models.OrderLineItem, error) {
	meta, err := fromJSONMap(item.Metadata)
	if err != nil {
		return models.OrderLineItem{}, err
	}

	row, err := r.queries(ctx).CreateOrderLineItem(ctx, orderdb.CreateOrderLineItemParams{
		ID:            item.ID,
		OrderID:       item.OrderID,
		VariantID:     item.VariantID,
		Title:         item.Title,
		Quantity:      item.Quantity,
		UnitPrice:     item.UnitPrice,
		Subtotal:      item.Subtotal,
		DiscountTotal: item.DiscountTotal,
		TaxTotal:      item.TaxTotal,
		TaxRateBps:    item.TaxRateBps,
		Total:         item.Total,
		Metadata:      meta,
	})
	if err != nil {
		return models.OrderLineItem{}, classify(err, codeQueryFailed, "could not create the order line")
	}
	return toLineItem(row)
}

// ListLineItems returns the order's lines in the order they were created.
func (r *Repository) ListLineItems(ctx context.Context, orderID string) ([]models.OrderLineItem, error) {
	rows, err := r.queries(ctx).ListOrderLineItems(ctx, orderID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not read the order lines")
	}
	return toLineItems(rows)
}

// --- summary -----------------------------------------------------------------

// CreateSummary opens the order's summary record with its totals zeroed.
func (r *Repository) CreateSummary(ctx context.Context, summary models.OrderSummary) (models.OrderSummary, error) {
	row, err := r.queries(ctx).CreateOrderSummary(ctx, orderdb.CreateOrderSummaryParams{
		ID:      summary.ID,
		OrderID: summary.OrderID,
	})
	if err != nil {
		return models.OrderSummary{}, classify(err, codeQueryFailed, "could not create the order summary")
	}
	return toSummary(row), nil
}

// GetSummary returns the order's summary; NotFound if there is none.
func (r *Repository) GetSummary(ctx context.Context, orderID string) (models.OrderSummary, error) {
	row, err := r.queries(ctx).GetOrderSummary(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.OrderSummary{}, errors.NotFound(codeSummaryNotFound,
				"order summary not found: %s", orderID)
		}
		return models.OrderSummary{}, classify(err, codeQueryFailed, "could not read the order summary")
	}
	return toSummary(row), nil
}

// SetSummaryTotals MERGES the cumulative paid and refunded amounts.
//
// The merge (GREATEST) is in the query itself; for the rationale see
// queries/order_summaries.sql. Both fields only ever grow, so a delayed or
// repeated payment event cannot erase an amount that has been recorded.
func (r *Repository) SetSummaryTotals(ctx context.Context, orderID string, paid, refunded int64) (models.OrderSummary, error) {
	row, err := r.queries(ctx).SetOrderSummaryTotals(ctx, orderdb.SetOrderSummaryTotalsParams{
		OrderID:       orderID,
		PaidTotal:     paid,
		RefundedTotal: refunded,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.OrderSummary{}, errors.NotFound(codeSummaryNotFound,
				"order summary not found: %s", orderID)
		}
		return models.OrderSummary{}, classify(err, codeQueryFailed, "could not write the order summary")
	}
	return toSummary(row), nil
}

// --- returns / exchanges / claims --------------------------------------------

// CreateReturn opens a new return record.
func (r *Repository) CreateReturn(ctx context.Context, ret models.Return) (models.Return, error) {
	meta, err := fromJSONMap(ret.Metadata)
	if err != nil {
		return models.Return{}, err
	}

	row, err := r.queries(ctx).CreateOrderReturn(ctx, orderdb.CreateOrderReturnParams{
		ID:           ret.ID,
		OrderID:      ret.OrderID,
		Status:       ret.Status.String(),
		RefundAmount: ret.RefundAmount,
		Reason:       nullString(ret.Reason),
		Note:         nullString(ret.Note),
		Metadata:     meta,
	})
	if err != nil {
		return models.Return{}, classify(err, codeQueryFailed, "could not create the return record")
	}
	return toReturn(row)
}

// GetReturn returns the return record by its identifier; NotFound if there is
// none.
func (r *Repository) GetReturn(ctx context.Context, id string) (models.Return, error) {
	row, err := r.queries(ctx).GetOrderReturn(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Return{}, errors.NotFound(codeReturnNotFound, "return record not found: %s", id)
		}
		return models.Return{}, classify(err, codeQueryFailed, "could not read the return record")
	}
	return toReturn(row)
}

// ListReturns pages the order's return records; the second value is the total
// count.
func (r *Repository) ListReturns(ctx context.Context, filter models.ChildFilter) ([]models.Return, int64, error) {
	rows, err := r.queries(ctx).ListOrderReturns(ctx, orderdb.ListOrderReturnsParams{
		OrderID:   filter.OrderID,
		RowLimit:  filter.Limit,
		RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not list the return records")
	}
	total, err := r.queries(ctx).CountOrderReturns(ctx, filter.OrderID)
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not count the return records")
	}
	items, err := toReturns(rows)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// CreateExchange opens a new exchange record.
func (r *Repository) CreateExchange(ctx context.Context, exchange models.Exchange) (models.Exchange, error) {
	meta, err := fromJSONMap(exchange.Metadata)
	if err != nil {
		return models.Exchange{}, err
	}

	row, err := r.queries(ctx).CreateOrderExchange(ctx, orderdb.CreateOrderExchangeParams{
		ID:            exchange.ID,
		OrderID:       exchange.OrderID,
		Status:        exchange.Status.String(),
		DifferenceDue: exchange.DifferenceDue,
		Note:          nullString(exchange.Note),
		Metadata:      meta,
	})
	if err != nil {
		return models.Exchange{}, classify(err, codeQueryFailed, "could not create the exchange record")
	}
	return toExchange(row)
}

// GetExchange returns the exchange record by its identifier; NotFound if there
// is none.
func (r *Repository) GetExchange(ctx context.Context, id string) (models.Exchange, error) {
	row, err := r.queries(ctx).GetOrderExchange(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Exchange{}, errors.NotFound(codeExchangeNotFound, "exchange record not found: %s", id)
		}
		return models.Exchange{}, classify(err, codeQueryFailed, "could not read the exchange record")
	}
	return toExchange(row)
}

// ListExchanges pages the order's exchange records; the second value is the
// total count.
func (r *Repository) ListExchanges(ctx context.Context, filter models.ChildFilter) ([]models.Exchange, int64, error) {
	rows, err := r.queries(ctx).ListOrderExchanges(ctx, orderdb.ListOrderExchangesParams{
		OrderID:   filter.OrderID,
		RowLimit:  filter.Limit,
		RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not list the exchange records")
	}
	total, err := r.queries(ctx).CountOrderExchanges(ctx, filter.OrderID)
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not count the exchange records")
	}
	items, err := toExchanges(rows)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// CreateClaim opens a new claim record.
func (r *Repository) CreateClaim(ctx context.Context, claim models.Claim) (models.Claim, error) {
	meta, err := fromJSONMap(claim.Metadata)
	if err != nil {
		return models.Claim{}, err
	}

	row, err := r.queries(ctx).CreateOrderClaim(ctx, orderdb.CreateOrderClaimParams{
		ID:           claim.ID,
		OrderID:      claim.OrderID,
		ClaimType:    claim.Type.String(),
		Status:       claim.Status.String(),
		RefundAmount: claim.RefundAmount,
		Reason:       nullString(claim.Reason),
		Note:         nullString(claim.Note),
		Metadata:     meta,
	})
	if err != nil {
		return models.Claim{}, classify(err, codeQueryFailed, "could not create the claim record")
	}
	return toClaim(row)
}

// GetClaim returns the claim record by its identifier; NotFound if there is
// none.
func (r *Repository) GetClaim(ctx context.Context, id string) (models.Claim, error) {
	row, err := r.queries(ctx).GetOrderClaim(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Claim{}, errors.NotFound(codeClaimNotFound, "claim record not found: %s", id)
		}
		return models.Claim{}, classify(err, codeQueryFailed, "could not read the claim record")
	}
	return toClaim(row)
}

// ListClaims pages the order's claim records; the second value is the total
// count.
func (r *Repository) ListClaims(ctx context.Context, filter models.ChildFilter) ([]models.Claim, int64, error) {
	rows, err := r.queries(ctx).ListOrderClaims(ctx, orderdb.ListOrderClaimsParams{
		OrderID:   filter.OrderID,
		RowLimit:  filter.Limit,
		RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not list the claim records")
	}
	total, err := r.queries(ctx).CountOrderClaims(ctx, filter.OrderID)
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not count the claim records")
	}
	items, err := toClaims(rows)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// --- spending ----------------------------------------------------------------

// advisoryLockSQL takes the transaction-lifetime advisory lock per customer.
//
// The lock is NOT GENERATED through sqlc and is run directly with the
// transaction handle: the query touches none of this module's tables, it is a
// concurrency primitive. It carries no schema information for sqlc either.
const advisoryLockSQL = `SELECT pg_advisory_xact_lock($1)`

// spendingLockClass is the CLASS number of the advisory lock and is written
// into the UPPER 32 bits of the key.
//
// The key space of pg_advisory_xact_lock is a single one ACROSS THE WHOLE
// DATABASE: code that locks the same number for another purpose would hold
// this lock up without being aware of it. The class number in the upper bits
// makes it impossible for another advisory lock added later (with another
// class) to collide with this lock.
const spendingLockClass int64 = 1

// LockCustomerSpending locks the customer's spend total UNTIL THE END OF THE
// TRANSACTION.
//
// # Why not a row lock
//
// What is protected is not a ROW but a TOTAL: "the sum of this customer's
// orders within the window". The total has no single row to lock, and
// SELECT ... FOR UPDATE locks the rows that exist, not the one NOT YET
// WRITTEN. Two concurrent orders would race for exactly that reason: both read
// the total, both see it below the limit, both write (the classic write skew).
// The advisory lock closes that gap — the lock is bound not to rows but to the
// CUSTOMER IDENTIFIER and is held until the transaction commits, which means
// the waiting second transaction reads the total together with the row the
// first one wrote.
//
// The SERIALIZABLE isolation level would solve this class of race as well, but
// its price is that conflicting transactions fail with a serialization error
// and the caller takes the retry on itself; the lock waits and then carries on,
// and puts the responsibility for retrying on no existing flow.
//
// # The key
//
// The upper 32 bits of the key are the class ([spendingLockClass]), the lower
// 32 bits are the FNV-1a digest of the customer identifier. It is possible for
// two different identifiers to fall on the same digest; the consequence is ONLY
// an unnecessary wait, not a wrong result — the lock is a correctness gate, not
// an identity.
//
// It can only be called inside [Repository.WithTx]: a pg_advisory_xact_lock
// without a transaction is released immediately and protects nothing.
func (r *Repository) LockCustomerSpending(ctx context.Context, customerID string) error {
	if err := requireTx(ctx, "LockCustomerSpending"); err != nil {
		return err
	}
	tx, _ := txFromContext(ctx)
	if _, err := tx.Exec(ctx, advisoryLockSQL, spendingLockKey(customerID)); err != nil {
		return classify(err, codeQueryFailed, "could not take the customer's spending lock")
	}
	return nil
}

// spendingLockKey converts the customer id into an advisory lock key.
//
// The digest is FNV-1a: it does not need to be cryptographic, it only needs to
// produce the same number for the same identifier. Widening from uint32 to
// int64 is lossless, so the key is the same across processes and versions.
func spendingLockKey(customerID string) int64 {
	h := fnv.New32a()
	// hash.Hash.Write never returns an error (a documented contract).
	_, _ = h.Write([]byte(customerID))
	return spendingLockClass<<32 | int64(h.Sum32())
}

// SumCustomerSpend returns the customer's spend within the window.
//
// What is summed (cancellations excluded, refunds deducted, currency fixed)
// and the bounds of the window are documented in queries/spending.sql. If
// windowStart is nil, the customer's WHOLE history is summed.
func (r *Repository) SumCustomerSpend(
	ctx context.Context,
	customerID, currencyCode string,
	windowStart *time.Time,
) (int64, error) {
	var window pgtype.Timestamptz
	if windowStart != nil {
		window = pgtype.Timestamptz{Time: windowStart.UTC(), Valid: true}
	}

	spent, err := r.queries(ctx).SumCustomerSpend(ctx, orderdb.SumCustomerSpendParams{
		CustomerID:   customerID,
		CurrencyCode: currencyCode,
		WindowStart:  window,
	})
	if err != nil {
		return 0, classify(err, codeQueryFailed, "could not read the customer's spend")
	}
	return spent, nil
}

// CreateOrderAddress writes one address of the order.
//
// It is called INSIDE the order's transaction; see the service's writeOrder.
func (r *Repository) CreateOrderAddress(
	ctx context.Context, address models.OrderAddress,
) (models.OrderAddress, error) {
	metadata, err := fromJSONMap(address.Metadata)
	if err != nil {
		return models.OrderAddress{}, err
	}

	row, err := r.queries(ctx).CreateOrderAddress(ctx, orderdb.CreateOrderAddressParams{
		ID:              address.ID,
		OrderID:         address.OrderID,
		AddressType:     string(address.Type),
		SourceAddressID: nullString(address.SourceAddressID),
		FirstName:       nullString(address.FirstName),
		LastName:        nullString(address.LastName),
		Company:         nullString(address.Company),
		Address1:        nullString(address.Address1),
		Address2:        nullString(address.Address2),
		City:            nullString(address.City),
		Province:        nullString(address.Province),
		PostalCode:      nullString(address.PostalCode),
		CountryCode:     nullString(address.CountryCode),
		Phone:           nullString(address.Phone),
		Metadata:        metadata,
	})
	if err != nil {
		return models.OrderAddress{}, classify(err, codeQueryFailed,
			"could not write the order address")
	}

	return toOrderAddress(row)
}

// OrderAddressesByOrderIDs reads the addresses of several orders in a SINGLE
// query; there is no query per order (N+1).
func (r *Repository) OrderAddressesByOrderIDs(
	ctx context.Context, orderIDs []string,
) (map[string][]models.OrderAddress, error) {
	if len(orderIDs) == 0 {
		return map[string][]models.OrderAddress{}, nil
	}

	rows, err := r.queries(ctx).ListOrderAddressesByOrderIDs(ctx, orderIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not read the order addresses")
	}

	out := make(map[string][]models.OrderAddress, len(orderIDs))
	for i := range rows {
		address, convErr := toOrderAddress(rows[i])
		if convErr != nil {
			return nil, convErr
		}
		out[address.OrderID] = append(out[address.OrderID], address)
	}

	return out, nil
}
