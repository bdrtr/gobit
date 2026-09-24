// Package loyaltypoints is the payment provider that spends a customer's
// loyalty points (ADR 0165).
//
// The state machine is [balancetender.Machine], the one store credit runs
// (ADR 0152); what this package owns is the identity, the error codes, and the
// adapter that turns the machine's movements into rows of the point ledger. A
// point is worth ONE MINOR UNIT of the currency it was earned in, so the
// contract's amounts and the ledger's points are one number and nothing is
// converted at the boundary.
//
// The ledger is the module's (payment_loyalty_entries), shared with the earn
// path that writes earn and reverse rows; this tender writes hold, release and
// refund rows, every one referencing ITS OWN session and never a collection —
// the earn target is recomputed per collection, and a hold carrying a
// collection id would read as points already written. The session is this
// provider's own (payment_loyalty_sessions), for the reason the manual and
// store-credit providers keep theirs: the module reaches a provider only
// through the contract.
//
// Money captured through this tender earns no points; the earn path excludes
// it by [models.LoyaltyTenderID], which is why the identity lives beside the
// ledger rather than here alone.
package loyaltypoints

import (
	"context"
	"log/slog"

	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/balancetender"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// ID is the provider's identity; sessions are opened under this name.
//
// A storefront asks for it by this string in the completion body's
// payment_provider_id, exactly as it asks for "manual" or "store_credit".
const ID = models.LoyaltyTenderID

// The provider's error codes; what each one means is written on
// [balancetender.Codes].
const (
	// CodeInvalidInput reports a call this provider cannot make sense of.
	CodeInvalidInput = "payment_loyalty_points_invalid_input"
	// CodeNoCustomer reports a session opened without an owner; a CONFLICT.
	CodeNoCustomer = "payment_loyalty_points_no_customer"
	// CodeInsufficient reports a balance that does not cover the session; it
	// travels as a declined authorization, not as an error.
	CodeInsufficient = "payment_loyalty_points_insufficient"
	// CodeInvalidState reports a transition the session's status does not allow.
	CodeInvalidState = "payment_loyalty_points_invalid_state"
)

// Store is the persistence this provider needs.
//
// It is declared HERE and narrow, like every consumer surface in this tree: the
// provider needs its own sessions and the ledger's three operations, and naming
// only those means a change to the module's other queries cannot break it.
type Store interface {
	// WithTx runs fn in a single transaction.
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error

	// InsertLoyaltySessionIfAbsent writes the session only if the idempotency
	// key is unused; the second return says whether the row was written.
	InsertLoyaltySessionIfAbsent(
		ctx context.Context, session models.TenderSession,
	) (models.TenderSession, bool, error)
	// LoyaltySessionByIdempotencyKey returns the session by its key.
	LoyaltySessionByIdempotencyKey(ctx context.Context, key string) (models.TenderSession, error)
	// LoyaltySession returns the session by its id.
	LoyaltySession(ctx context.Context, id string) (models.TenderSession, error)
	// LockLoyaltySession locks the session for the transaction.
	LockLoyaltySession(ctx context.Context, id string) (models.TenderSession, error)
	// UpdateLoyaltySessionState writes the status and amounts as ABSOLUTE
	// values.
	UpdateLoyaltySessionState(
		ctx context.Context,
		id string,
		status models.SessionStatus,
		authorized, captured, refunded int64,
		declineReason string,
	) (models.TenderSession, error)

	// AppendLoyaltyEntry appends one row to the ledger.
	AppendLoyaltyEntry(ctx context.Context, entry models.LoyaltyEntry) (models.LoyaltyEntry, error)
	// LoyaltyBalance sums a customer's points in one currency.
	LoyaltyBalance(ctx context.Context, customerID, currencyCode string) (int64, error)
	// LockLoyaltyBalance locks that customer's point balance in one currency
	// for the transaction.
	LockLoyaltyBalance(ctx context.Context, customerID, currencyCode string) error
}

// Provider spends loyalty points. It is safe for concurrent use.
type Provider struct {
	*balancetender.Machine
}

// That the core contract and the optional inspector are satisfied is pinned at
// compile time.
var (
	_ coreprovider.PaymentProvider  = (*Provider)(nil)
	_ coreprovider.SessionInspector = (*Provider)(nil)
)

// New builds the provider on the given store.
func New(store Store, log *slog.Logger) *Provider {
	return &Provider{Machine: balancetender.New(ledger{store: store}, balancetender.Identity{
		Unit: "loyalty points",
		Codes: balancetender.Codes{
			InvalidInput: CodeInvalidInput,
			NoCustomer:   CodeNoCustomer,
			Insufficient: CodeInsufficient,
			InvalidState: CodeInvalidState,
		},
		NewSessionID: models.NewLoyaltySessionID,
	}, log)}
}

// ID returns the provider's identity.
func (p *Provider) ID() string { return ID }

// ledger adapts [Store] to the machine's surface: the point ledger and the
// provider's own session table, under the machine's neutral names.
type ledger struct {
	store Store
}

// movementKinds maps the machine's movements to the ledger's vocabulary. The
// two earning kinds are not here: this tender never writes them.
var movementKinds = map[balancetender.Movement]models.LoyaltyKind{
	balancetender.Hold:    models.LoyaltyHold,
	balancetender.Release: models.LoyaltyRelease,
	balancetender.Refund:  models.LoyaltyRefund,
}

func (l ledger) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return l.store.WithTx(ctx, fn)
}

func (l ledger) InsertSessionIfAbsent(
	ctx context.Context, session models.TenderSession,
) (models.TenderSession, bool, error) {
	return l.store.InsertLoyaltySessionIfAbsent(ctx, session)
}

func (l ledger) SessionByIdempotencyKey(ctx context.Context, key string) (models.TenderSession, error) {
	return l.store.LoyaltySessionByIdempotencyKey(ctx, key)
}

func (l ledger) Session(ctx context.Context, id string) (models.TenderSession, error) {
	return l.store.LoyaltySession(ctx, id)
}

func (l ledger) LockSession(ctx context.Context, id string) (models.TenderSession, error) {
	return l.store.LockLoyaltySession(ctx, id)
}

func (l ledger) UpdateSessionState(
	ctx context.Context,
	id string,
	status models.SessionStatus,
	authorized, captured, refunded int64,
	declineReason string,
) (models.TenderSession, error) {
	return l.store.UpdateLoyaltySessionState(ctx, id, status, authorized, captured, refunded, declineReason)
}

// Move writes one movement as a point ledger row, referenced by the session.
func (l ledger) Move(ctx context.Context, entry balancetender.Entry) error {
	_, err := l.store.AppendLoyaltyEntry(ctx, models.LoyaltyEntry{
		ID:           models.NewLoyaltyEntryID(),
		CustomerID:   entry.CustomerID,
		CurrencyCode: entry.CurrencyCode,
		Points:       entry.Amount,
		Kind:         movementKinds[entry.Movement],
		Reference:    entry.SessionID,
	})

	return err
}

func (l ledger) Balance(ctx context.Context, customerID, currencyCode string) (int64, error) {
	return l.store.LoyaltyBalance(ctx, customerID, currencyCode)
}

func (l ledger) LockBalance(ctx context.Context, customerID, currencyCode string) error {
	return l.store.LockLoyaltyBalance(ctx, customerID, currencyCode)
}
