// Package storecredit is the payment provider that spends a customer's store
// credit (ADR 0152).
//
// The state machine is [balancetender.Machine], written once for the two
// tenders that spend a balance this module keeps (ADR 0165); what this package
// owns is the identity, the error codes, and the adapter that turns the
// machine's movements into rows of the credit ledger. The ledger is the
// module's (payment_store_credit_entries) and the session is this provider's
// own (payment_store_credit_sessions), which is the separation the manual
// provider keeps for the same reason: the module reaches a provider only
// through the contract.
//
// Every row this tender writes is written from THIS package, and the arch gate
// that admits the ledger's second door derives that door from the provider's
// identity — so the door is the package, not a list of function names.
package storecredit

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
// payment_provider_id, exactly as it asks for "manual".
const ID = "store_credit"

// The provider's error codes; what each one means is written on
// [balancetender.Codes].
const (
	// CodeInvalidInput reports a call this provider cannot make sense of.
	CodeInvalidInput = "payment_store_credit_invalid_input"
	// CodeNoCustomer reports a session opened without an owner; a CONFLICT.
	CodeNoCustomer = "payment_store_credit_no_customer"
	// CodeInsufficient reports a balance that does not cover the session; it
	// travels as a declined authorization, not as an error.
	CodeInsufficient = "payment_store_credit_insufficient"
	// CodeInvalidState reports a transition the session's status does not allow.
	CodeInvalidState = "payment_store_credit_invalid_state"
)

// Store is the persistence this provider needs.
//
// It is declared HERE and narrow, like every consumer surface in this tree: the
// provider needs its own sessions and the ledger's two operations, and naming
// only those means a change to the module's other queries cannot break it.
type Store interface {
	// WithTx runs fn in a single transaction.
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error

	// InsertStoreCreditSessionIfAbsent writes the session only if the idempotency
	// key is unused; the second return says whether the row was written.
	InsertStoreCreditSessionIfAbsent(
		ctx context.Context, session models.TenderSession,
	) (models.TenderSession, bool, error)
	// StoreCreditSessionByIdempotencyKey returns the session by its key.
	StoreCreditSessionByIdempotencyKey(ctx context.Context, key string) (models.TenderSession, error)
	// StoreCreditSession returns the session by its id.
	StoreCreditSession(ctx context.Context, id string) (models.TenderSession, error)
	// LockStoreCreditSession locks the session for the transaction.
	LockStoreCreditSession(ctx context.Context, id string) (models.TenderSession, error)
	// UpdateStoreCreditSessionState writes the status and amounts as ABSOLUTE
	// values.
	UpdateStoreCreditSessionState(
		ctx context.Context,
		id string,
		status models.SessionStatus,
		authorized, captured, refunded int64,
		declineReason string,
	) (models.TenderSession, error)

	// AppendStoreCreditEntry appends one event to the ledger.
	AppendStoreCreditEntry(ctx context.Context, entry models.StoreCreditEntry) (models.StoreCreditEntry, error)
	// StoreCreditBalance sums a customer's entries in one currency.
	StoreCreditBalance(ctx context.Context, customerID, currencyCode string) (int64, error)
	// LockStoreCreditBalance locks that customer's credit balance in one
	// currency for the transaction.
	LockStoreCreditBalance(ctx context.Context, customerID, currencyCode string) error
}

// Provider spends store credit. It is safe for concurrent use.
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
		Unit: "store credit",
		Codes: balancetender.Codes{
			InvalidInput: CodeInvalidInput,
			NoCustomer:   CodeNoCustomer,
			Insufficient: CodeInsufficient,
			InvalidState: CodeInvalidState,
		},
		NewSessionID: models.NewStoreCreditSessionID,
	}, log)}
}

// ID returns the provider's identity.
func (p *Provider) ID() string { return ID }

// ledger adapts [Store] to the machine's surface: the credit ledger and the
// provider's own session table, under the machine's neutral names.
type ledger struct {
	store Store
}

// movementKinds maps the machine's movements to the ledger's vocabulary.
var movementKinds = map[balancetender.Movement]models.StoreCreditKind{
	balancetender.Hold:    models.StoreCreditHold,
	balancetender.Release: models.StoreCreditRelease,
	balancetender.Refund:  models.StoreCreditRefund,
}

func (l ledger) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return l.store.WithTx(ctx, fn)
}

func (l ledger) InsertSessionIfAbsent(
	ctx context.Context, session models.TenderSession,
) (models.TenderSession, bool, error) {
	return l.store.InsertStoreCreditSessionIfAbsent(ctx, session)
}

func (l ledger) SessionByIdempotencyKey(ctx context.Context, key string) (models.TenderSession, error) {
	return l.store.StoreCreditSessionByIdempotencyKey(ctx, key)
}

func (l ledger) Session(ctx context.Context, id string) (models.TenderSession, error) {
	return l.store.StoreCreditSession(ctx, id)
}

func (l ledger) LockSession(ctx context.Context, id string) (models.TenderSession, error) {
	return l.store.LockStoreCreditSession(ctx, id)
}

func (l ledger) UpdateSessionState(
	ctx context.Context,
	id string,
	status models.SessionStatus,
	authorized, captured, refunded int64,
	declineReason string,
) (models.TenderSession, error) {
	return l.store.UpdateStoreCreditSessionState(ctx, id, status, authorized, captured, refunded, declineReason)
}

// Move writes one movement as a credit ledger row, referenced by the session.
func (l ledger) Move(ctx context.Context, entry balancetender.Entry) error {
	_, err := l.store.AppendStoreCreditEntry(ctx, models.StoreCreditEntry{
		ID:           models.NewStoreCreditEntryID(),
		CustomerID:   entry.CustomerID,
		CurrencyCode: entry.CurrencyCode,
		Amount:       entry.Amount,
		Kind:         movementKinds[entry.Movement],
		Reference:    entry.SessionID,
	})

	return err
}

func (l ledger) Balance(ctx context.Context, customerID, currencyCode string) (int64, error) {
	return l.store.StoreCreditBalance(ctx, customerID, currencyCode)
}

func (l ledger) LockBalance(ctx context.Context, customerID, currencyCode string) error {
	return l.store.LockStoreCreditBalance(ctx, customerID, currencyCode)
}
