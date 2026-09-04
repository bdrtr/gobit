// Package service is the business logic of the payment module.
//
// The module's responsibility in one sentence: to know at WHICH stage the
// MONEY for a cart or an order is — is it on hold, was it drawn, was it
// refunded.
//
// # State machine
//
// The session's transition table stands on [models.SessionStatus], in the
// AuthorizeAction, CaptureAction and CancelAction methods, as pure functions;
// this package only turns the result into a typed error. Every invalid
// transition returns errors.Conflict (e.g. authorizing a captured session). A
// transition that is already in the target status, on the other hand, is NOT
// an error but a silent no-op; the idempotency comes from there.
//
// The collection's status is stored but it is DERIVED: after every mutation it
// is recomputed from the amounts and the session counts and written
// (see [models.CollectionStatusFor]).
//
// # Concurrency and lock ordering
//
// EVERY flow that writes money runs in a single database transaction and
// ALWAYS takes the locks in the same order: first the COLLECTION, then the
// SESSION, then the CAPTURE. Had the order changed from flow to flow, two
// flows would ask for the same two rows in the opposite order, lock each other
// out, and the database would kill one of the transactions.
//
// The collection lock is not merely an existence check; it is the ordering
// itself, and it makes the collection's derived status be written out of a
// single computation. Of two calls authorizing the same session at the same
// time EXACTLY ONE goes to the provider; the second one sees the status the
// first one wrote and falls into the no-op.
//
// # The provider call is INSIDE the transaction
//
// Authorization, capture and cancel call the provider UNDER the row lock. The
// price is plain: a slow provider holds the session's row lock for that whole
// time. What is bought in return is the "exactly one authorization" guarantee
// — had the lock been released before the provider call, both concurrent calls
// would go to the provider and the uniqueness would be left to the provider's
// own idempotency alone; not every provider offers that. The alternative is a
// two-phase write with an intermediate "authorizing" status, and it belongs to
// the hardening phase (Phase 9).
//
// Because the manual provider shares the same store, its call JOINS this
// transaction (see repository.Repository.WithTx: a nested call does not open a
// new transaction). This keeps the simulated provider's ledger atomic with the
// module's records; in a real network provider there is no such guarantee, and
// the saga compensation exists for exactly that.
//
// # Module isolation
//
// This module knows no other module (Principle 2.1/2.4, ADR 0001).
// [models.PaymentCollection.Reference] is a cart or order identifier; it is
// stored as free text, no foreign key is given (Principle 2.2) and its
// existence is not validated here — validating it is the job of the workflow
// that knows those modules.
package service

import (
	"context"
	"log/slog"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// EntityName is the entity name the module offers to the Query layer. The
// provider is registered in the container under the name "<EntityName>.query"
// (ADR 0004).
const EntityName = "payment_collection"

// Error codes. Clients may branch on these; the messages can change, the codes
// do not.
const (
	// CodeInvalidInput reports that the input did not pass validation.
	CodeInvalidInput = "payment_invalid_input"
	// CodeInvalidTransition reports that an invalid transition was attempted
	// in the state machine (e.g. authorizing a captured session).
	CodeInvalidTransition = "payment_invalid_transition"
	// CodeAuthorizationDeclined reports that the provider declined the
	// authorization. IT IS NOT a server error; it is an expected business
	// outcome.
	CodeAuthorizationDeclined = "payment_authorization_declined"
	// CodeProviderNotFound reports that the requested provider is not
	// registered.
	CodeProviderNotFound = "payment_provider_not_found"
	// CodeProviderExists reports that a second provider was about to be
	// registered under the same identifier.
	CodeProviderExists = "payment_provider_already_registered"
	// CodeIdempotencyMismatch reports that the same key was used for ANOTHER
	// collection.
	CodeIdempotencyMismatch = "payment_idempotency_key_mismatch"
	// CodeCollectionClosed reports that a new session was about to be opened
	// on a collection that has closed.
	CodeCollectionClosed = "payment_collection_closed"
	// CodeSessionTerminal reports that the idempotency key belongs to a
	// TERMINAL (canceled or declined) session; the caller has to continue with
	// a NEW key.
	CodeSessionTerminal = "payment_session_terminal"
	// CodeNothingToRefund reports that no amount is left to refund.
	CodeNothingToRefund = "payment_nothing_to_refund"
	// CodeProviderContract reports that the provider returned a response that
	// is outside the contract; it does not happen in normal operation.
	CodeProviderContract = "payment_provider_contract_violation"
	// CodeInconsistentState reports that the collection amounts and the child
	// records do not add up; it does not happen in normal operation.
	CodeInconsistentState = "payment_inconsistent_state"
	// CodeNotReady reports that the service was constructed with a missing
	// dependency.
	CodeNotReady = "payment_service_not_ready"
)

// Pagination limits (plan Section 8: limit/offset).
const (
	// DefaultLimit is the page size applied when no limit is given.
	DefaultLimit int64 = 50
	// MaxLimit is the largest page size that can be asked for in a single
	// request.
	MaxLimit int64 = 100
)

// maxTextLen is the upper bound for free-text fields. The bound prevents a
// single request from writing text of unbounded size to the database.
const maxTextLen = 512

// Store is the persistence surface the service needs.
//
// The interface is defined on the CONSUMING side, that is, here (the pattern
// of ADR 0001). The service DOES NOT import the repository package; the
// concrete store satisfies these signatures structurally and the wiring is
// done in module.go. That way the unit tests can be written without a real
// database, with a fake store a few lines long.
//
// # The manual provider's ledger IS NOT HERE
//
// The concrete store reaches the payment_manual_sessions table too, but those
// methods were DELIBERATELY left out of this interface: the provider's
// internal state is not the module's data, and the service must not be given
// the means to touch it. The boundary is not a comment, it is the type system.
//
// # Transaction boundary
//
// [Store.WithTx] runs the given function in a single database transaction and
// carries the transaction with the context the function receives. For that
// reason every call inside the transaction has to be made WITH THE CONTEXT
// GIVEN TO THE FUNCTION; if the outer ctx is used, that call is left outside
// the transaction and atomicity is silently lost.
//
// The methods that begin with Lock lock the row until the end of the
// transaction and may only be called inside [Store.WithTx]. The locks are
// ALWAYS taken in the order collection -> session -> capture.
type Store interface {
	// WithTx runs fn in a single transaction; if fn returns an error the
	// transaction is rolled back.
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error

	// CreatePaymentCollection records a new payment collection.
	CreatePaymentCollection(ctx context.Context, col models.PaymentCollection) (models.PaymentCollection, error)
	// GetPaymentCollection returns the collection by its identifier; NotFound
	// if there is none.
	GetPaymentCollection(ctx context.Context, id string) (models.PaymentCollection, error)
	// LockPaymentCollection locks the collection and returns its current form.
	LockPaymentCollection(ctx context.Context, id string) (models.PaymentCollection, error)
	// ListPaymentCollections filters and pages the collections; the second
	// value is the count of ALL the rows matching the filter.
	ListPaymentCollections(ctx context.Context, filter models.CollectionFilter) ([]models.PaymentCollection, int64, error)
	// PaymentCollectionsByIDs fetches the identifier set in a SINGLE query
	// (no N+1).
	PaymentCollectionsByIDs(ctx context.Context, ids []string) ([]models.PaymentCollection, error)
	// UpdatePaymentCollectionTotals writes the amounts and the derived status
	// with ABSOLUTE values.
	UpdatePaymentCollectionTotals(
		ctx context.Context,
		id string,
		status models.CollectionStatus,
		authorized, captured, refunded int64,
	) (models.PaymentCollection, error)

	// CreatePaymentSession records a new payment session.
	CreatePaymentSession(ctx context.Context, ses models.PaymentSession) (models.PaymentSession, error)
	// GetPaymentSession returns the session by its identifier; NotFound if
	// there is none.
	GetPaymentSession(ctx context.Context, id string) (models.PaymentSession, error)
	// LockPaymentSession locks the session and returns its current form.
	LockPaymentSession(ctx context.Context, id string) (models.PaymentSession, error)
	// PaymentSessionByIdempotencyKey returns the session opened with the same
	// key; NotFound if there is none.
	PaymentSessionByIdempotencyKey(ctx context.Context, providerID, key string) (models.PaymentSession, error)
	// ListPaymentSessionsByCollection returns the collection's sessions.
	ListPaymentSessionsByCollection(ctx context.Context, collectionID string) ([]models.PaymentSession, error)
	// SessionCounts counts the collection's sessions by status in a SINGLE
	// query.
	SessionCounts(ctx context.Context, collectionID string) (models.SessionCounts, error)
	// LiveSessionAmount returns the total amount reserved by the collection's
	// live (pending or authorized) sessions; 0 if there are none.
	LiveSessionAmount(ctx context.Context, collectionID string) (int64, error)
	// UpdatePaymentSessionState writes the session's status, the amount put on
	// hold, the raw provider data and the decline reason with ABSOLUTE values.
	UpdatePaymentSessionState(
		ctx context.Context,
		id string,
		status models.SessionStatus,
		authorizedAmount int64,
		data []byte,
		declineReason string,
	) (models.PaymentSession, error)

	// CreatePayment records a new capture.
	CreatePayment(ctx context.Context, pay models.Payment) (models.Payment, error)
	// GetPayment returns the capture by its identifier; NotFound if there is
	// none.
	GetPayment(ctx context.Context, id string) (models.Payment, error)
	// LockPayment locks the capture and returns its current form.
	LockPayment(ctx context.Context, id string) (models.Payment, error)
	// PaymentBySession returns the capture born out of the session; NotFound
	// if there is none.
	PaymentBySession(ctx context.Context, sessionID string) (models.Payment, error)
	// ListPaymentsByCollection returns the collection's captures.
	ListPaymentsByCollection(ctx context.Context, collectionID string) ([]models.Payment, error)
	// UpdatePaymentRefundedAmount writes the refunded amount with an ABSOLUTE
	// value.
	UpdatePaymentRefundedAmount(ctx context.Context, id string, refunded int64) (models.Payment, error)

	// CreateRefund records a new refund.
	CreateRefund(ctx context.Context, ref models.Refund) (models.Refund, error)
	// ListRefundsByPayment returns the capture's refunds.
	ListRefundsByPayment(ctx context.Context, paymentID string) ([]models.Refund, error)
}

// Options are the construction dependencies of the service.
type Options struct {
	// Store is the persistence surface; it is required.
	Store Store
	// Providers are the registered payment providers; they are required.
	Providers *ProviderRegistry
	// Logger, when given as nil, throws the logs away.
	Logger *slog.Logger
}

// Service is the payment module's outward-facing service.
// It is safe for concurrent use.
type Service struct {
	store     Store
	providers *ProviderRegistry
	log       *slog.Logger
}

// New produces a service with the given dependencies.
//
// A missing dependency is a construction error and it is returned EXPLICITLY:
// a service constructed with a nil store would produce a panic on the first
// request, and the error would come out long after the construction.
func New(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, errors.Internal(CodeNotReady, "the payment service cannot be constructed without a store")
	}
	if opts.Providers == nil {
		return nil, errors.Internal(CodeNotReady, "the payment service cannot be constructed without a provider registry")
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{store: opts.Store, providers: opts.Providers, log: log}, nil
}

// ProviderIDs returns the identifiers of the registered payment providers,
// sorted.
//
// The storefront's payment step learns from here which routes are open; the
// provider object itself DOES NOT LEAK out — the only thing opened to the
// outside is the identifier, and the payment flows ask for the provider by
// that identifier.
//
// ctx is not used today; the reason it stands in the signature is that the
// provider registry may in the future (the Phase 9 plugin system) be fed from
// outside the process. In this project every service method takes a context,
// and on that day the signature must not have to change.
func (s *Service) ProviderIDs(_ context.Context) []string { return s.providers.IDs() }

// Page holds the pagination parameters of list requests.
type Page struct {
	// Limit is the maximum number of rows to return; if 0, [DefaultLimit] is
	// applied.
	Limit int64
	// Offset is the number of rows to skip.
	Offset int64
}

// normalize validates the pagination parameters and applies the defaults.
func (p Page) normalize() (Page, error) {
	if p.Limit < 0 {
		return Page{}, errors.Invalid(CodeInvalidInput, "limit cannot be negative: %d", p.Limit)
	}
	if p.Offset < 0 {
		return Page{}, errors.Invalid(CodeInvalidInput, "offset cannot be negative: %d", p.Offset)
	}
	if p.Limit > MaxLimit {
		return Page{}, errors.Invalid(CodeInvalidInput,
			"limit can be at most %d: %d", MaxLimit, p.Limit)
	}
	if p.Limit == 0 {
		p.Limit = DefaultLimit
	}
	return p, nil
}

// writeCollectionTotals writes the collection's amounts and DERIVES its status
// AGAIN.
//
// The status is never assigned by hand in any flow; it always goes through
// here. The alternative — every flow writing its own status — meant the same
// rule spread over five places and one branch being forgotten. That is why the
// session count is read HERE, AFTER the session row has been updated.
//
// It must be called INSIDE a transaction; the collection's lock has been taken
// by the caller.
func (s *Service) writeCollectionTotals(
	ctx context.Context,
	col models.PaymentCollection,
	authorized, captured, refunded int64,
) (models.PaymentCollection, error) {
	if authorized < 0 || captured < 0 || refunded < 0 {
		return models.PaymentCollection{}, errors.Internal(CodeInconsistentState,
			"the collection amount would fall below zero: held %d, captured %d, refunded %d (%s)",
			authorized, captured, refunded, col.ID)
	}

	counts, err := s.store.SessionCounts(ctx, col.ID)
	if err != nil {
		return models.PaymentCollection{}, err
	}

	next := col
	next.AuthorizedAmount = authorized
	next.CapturedAmount = captured
	next.RefundedAmount = refunded

	return s.store.UpdatePaymentCollectionTotals(ctx, col.ID,
		models.CollectionStatusFor(next, counts), authorized, captured, refunded)
}
