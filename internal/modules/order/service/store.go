package service

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// Store is the persistence surface the service needs.
//
// The interface is defined on the CONSUMING side, that is, here (the ADR 0001
// pattern). The service DOES NOT import the repository package; the concrete
// store satisfies these signatures structurally and the wiring is done in
// module.go. This way the unit tests can be written without a real database,
// with a fake store of a few hundred lines.
//
// # Transaction boundary
//
// [Store.WithTx] runs the given function in a single database transaction and
// carries the transaction with the context the function receives. That is why
// every call inside the transaction has to be made with THE CONTEXT GIVEN TO THE
// FUNCTION; if the outer ctx is used that call stays outside the transaction and
// the atomicity is silently lost.
//
// [Store.LockOrder] locks the order until the end of the transaction and can
// only be called inside [Store.WithTx]. Every flow that changes the STATUS of
// the order does its read with this method: a status read without a lock can be
// stale at the moment of the write and a concurrent cancellation and completion
// would overwrite each other.
//
// [Store.WithReadTx] is for reads that do not write but make MORE THAN ONE query
// (see [Service.GetOrder]). It takes no lock; the only guarantee it gives is
// that all the queries inside it see the SAME state of the order. The reason it
// is a separate method is the isolation level: at PostgreSQL's default READ
// COMMITTED level every STATEMENT takes its own snapshot, that is, wrapping the
// queries in an ordinary transaction DOES NOT PREVENT a torn view; what prevents
// it is REPEATABLE READ.
//
// # Lock order
//
// The locks are taken in the same order IN EVERY FLOW: first the ORDER, then the
// child records. The children are not locked separately — the order lock already
// serializes all the status transitions of that order and a single lock makes it
// impossible for the order to be reversed (and therefore for a deadlock to
// happen).
//
// [Store.LockCustomerSpending] is not OUTSIDE this order but IN FRONT of it: the
// spending lock is taken only on the order OPENING path and always as the first
// job. No wait can form in the reverse direction, because none of the flows that
// lock the order row (cancel, complete, archive) asks for the spending lock; as
// long as there is no flow asking for the two locks in the reverse order, no
// cycle can be built.
type Store interface {
	// WithTx runs fn in a single transaction; if fn returns an error the
	// transaction is rolled back.
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
	// WithReadTx runs fn in a read-only transaction with a SINGLE SNAPSHOT.
	WithReadTx(ctx context.Context, fn func(ctx context.Context) error) error

	// CreateOrder records a new order. display_id IS NOT GIVEN; the database
	// produces it and it is present in the returned record.
	CreateOrder(ctx context.Context, order models.Order) (models.Order, error)
	// GetOrder returns the order by its identifier; NotFound when there is none.
	GetOrder(ctx context.Context, id string) (models.Order, error)
	// GetOrderByDisplayID returns the order by its human readable number;
	// NotFound when there is none.
	GetOrderByDisplayID(ctx context.Context, displayID int64) (models.Order, error)
	// GetOrderByIdempotencyKey returns the order opened with the key; NotFound
	// when there is none.
	GetOrderByIdempotencyKey(ctx context.Context, key string) (models.Order, error)
	// LockOrder locks the order for the duration of the transaction and returns
	// its current state.
	LockOrder(ctx context.Context, id string) (models.Order, error)
	// ListOrders filters and pages the orders; the second value is the total
	// count.
	ListOrders(ctx context.Context, filter models.OrderFilter) ([]models.Order, int64, error)
	// OrdersByIDs fetches a set of identifiers in a SINGLE query (no N+1).
	OrdersByIDs(ctx context.Context, ids []string) ([]models.Order, error)
	// CancelOrder cancels the order; it only takes effect in the 'pending'
	// status.
	CancelOrder(ctx context.Context, id, reason string) (models.Order, error)
	// CompleteOrder completes the order; it only takes effect in the 'pending'
	// status.
	CompleteOrder(ctx context.Context, id string) (models.Order, error)
	// ArchiveOrder archives the order; it only takes effect in the 'completed'
	// status.
	ArchiveOrder(ctx context.Context, id string) (models.Order, error)

	// LockCustomerSpending locks the SUM of the customer's spend until the end
	// of the transaction and can only be called inside [Store.WithTx].
	//
	// The lock is bound not to a row but to the customer id: what is protected
	// is a SUM that also covers an order that has not been written yet, and FOR
	// UPDATE, which locks existing rows, cannot protect it (see the repository
	// package).
	LockCustomerSpending(ctx context.Context, customerID string) error
	// SumCustomerSpend returns the customer's spend in the given currency; when
	// windowStart is nil the WHOLE history is summed.
	//
	// Canceled and soft-deleted orders do not enter the sum, the refunded amount
	// is subtracted (see queries/spending.sql).
	SumCustomerSpend(ctx context.Context, customerID, currencyCode string, windowStart *time.Time) (int64, error)

	// CreateLineItem records a new order line.
	CreateLineItem(ctx context.Context, item models.OrderLineItem) (models.OrderLineItem, error)
	// ListLineItems returns the lines of the order in creation order.
	ListLineItems(ctx context.Context, orderID string) ([]models.OrderLineItem, error)

	// CreateSummary opens the summary record of the order in a zeroed state.
	CreateSummary(ctx context.Context, summary models.OrderSummary) (models.OrderSummary, error)
	// GetSummary returns the summary of the order; NotFound when there is none.
	GetSummary(ctx context.Context, orderID string) (models.OrderSummary, error)
	// SetSummaryTotals MERGES the cumulative paid and refunded amounts: every
	// field can only grow, a shrinking value is not written.
	SetSummaryTotals(ctx context.Context, orderID string, paid, refunded int64) (models.OrderSummary, error)

	// CreateReturn opens a new return record.
	CreateReturn(ctx context.Context, ret models.Return) (models.Return, error)
	// GetReturn returns the return record by its identifier; NotFound when there
	// is none.
	GetReturn(ctx context.Context, id string) (models.Return, error)
	// ListReturns pages the return records of the order.
	ListReturns(ctx context.Context, filter models.ChildFilter) ([]models.Return, int64, error)

	// CreateExchange opens a new exchange record.
	CreateExchange(ctx context.Context, exchange models.Exchange) (models.Exchange, error)
	// GetExchange returns the exchange record by its identifier; NotFound when
	// there is none.
	GetExchange(ctx context.Context, id string) (models.Exchange, error)
	// ListExchanges pages the exchange records of the order.
	ListExchanges(ctx context.Context, filter models.ChildFilter) ([]models.Exchange, int64, error)

	// CreateClaim opens a new claim record.
	CreateClaim(ctx context.Context, claim models.Claim) (models.Claim, error)
	// GetClaim returns the claim record by its identifier; NotFound when there
	// is none.
	GetClaim(ctx context.Context, id string) (models.Claim, error)
	// ListClaims pages the claim records of the order.
	ListClaims(ctx context.Context, filter models.ChildFilter) ([]models.Claim, int64, error)
}

// EventPublisher is the NARROW surface the service needs from the event bus.
//
// core/eventbus is CORE and importing it is free (Principle 2.4); the narrowing
// here is meant to reduce the dependency: the module only PUBLISHES, it does not
// subscribe and it does not close the bus. Depending on the whole of
// [eventbus.EventBus] would give the impression that the module has the
// authority to subscribe and to shut down.
//
// The [eventbus.Event] type is used as it is: the shape of the event is the
// contract of the core and redefining it here would lead to the two types
// diverging.
type EventPublisher interface {
	// Publish publishes the event and DOES NOT WAIT for the handlers.
	Publish(ctx context.Context, e eventbus.Event) error
}
