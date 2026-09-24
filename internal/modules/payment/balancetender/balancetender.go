// Package balancetender is the ONE state machine the payment module's own
// tenders run: store credit (ADR 0152) and loyalty points (ADR 0165).
//
// A balance tender spends money a customer already holds in a ledger of this
// module. What differs between the two is the ledger and the name; what is the
// same is everything the core contract asks of a provider, and that is written
// here once rather than twice by hand. [Machine] meets the idempotency
// conditions in core/provider's PaymentProvider godoc the way the manual
// provider does:
//
//   - a second CreateSession with the same IdempotencyKey opens no new session;
//   - Authorize, Capture and Refund may be called again and return the current
//     state rather than an error;
//   - Cancel is the saga's compensation and is IDEMPOTENT.
//
// # What each verb does to the ledger
//
//	CreateSession  writes nothing — it opens a session
//	Authorize      writes a HOLD (negative): the balance stops being spendable
//	Capture        writes no spend — the hold already took it; a PARTIAL capture
//	               writes a RELEASE (positive) of the part it did not take
//	Cancel         writes a RELEASE (positive): the hold comes back
//	Refund         writes a REFUND (positive): a captured amount is given back
//
// A capture that wrote a second negative row would take the balance twice, and a
// balance that only fell at capture time would let two checkouts spend the same
// money while both were still deciding. The hold is what makes the balance safe
// to read.
//
// # Whose balance it is
//
// The customer comes from [coreprovider.CreateSessionInput.CustomerID], which the
// payment module fills from the COLLECTION. It does not come from Data: that map
// is the client's, and a shopper naming somebody else's id would spend their
// balance. A session opened with no customer is refused rather than served with
// an empty owner.
//
// # The provider package is the door
//
// A tender is its own package — the compliance gate counts one provider per
// package — and that package owns three things this machine does not: the
// identity string, the error codes, and the adapter that turns a [Movement]
// into a row of ITS ledger. Every ledger row a tender writes is therefore
// written from the tender's package, which is what lets an arch gate derive
// the ledger's second door from the tender's identity rather than from a list
// of function names.
package balancetender

import (
	"context"
	"log/slog"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// Movement is what a session does to the ledger.
type Movement string

// The three movements a session can make. An earn or an issue is not among
// them: those are written by the module's service, not by a tender.
const (
	// Hold puts the amount aside; the ledger row is NEGATIVE.
	Hold Movement = "hold"
	// Release gives a hold back; positive.
	Release Movement = "release"
	// Refund gives a captured amount back; positive.
	Refund Movement = "refund"
)

// Entry is one ledger row the machine asks a tender to write.
type Entry struct {
	// CustomerID and CurrencyCode say whose balance, in which currency.
	CustomerID   string
	CurrencyCode string
	// Movement is what happened and Amount is SIGNED accordingly: a hold is
	// negative, a release and a refund are positive.
	Movement Movement
	Amount   int64
	// SessionID is the tender's OWN session the row belongs to. It is never a
	// collection id: the loyalty earn target is recomputed per collection, and
	// a hold referencing one would read as points already written (ADR 0165).
	SessionID string
}

// Store is the persistence the machine needs, declared HERE and narrow.
//
// A tender adapts its ledger and its session table to this surface; the machine
// never names a table.
type Store interface {
	// WithTx runs fn in a single transaction.
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error

	// InsertSessionIfAbsent writes the session only if the idempotency key is
	// unused; the second return says whether the row was written.
	InsertSessionIfAbsent(ctx context.Context, session models.TenderSession) (models.TenderSession, bool, error)
	// SessionByIdempotencyKey returns the session by its key.
	SessionByIdempotencyKey(ctx context.Context, key string) (models.TenderSession, error)
	// Session returns the session by its id.
	Session(ctx context.Context, id string) (models.TenderSession, error)
	// LockSession locks the session for the transaction.
	LockSession(ctx context.Context, id string) (models.TenderSession, error)
	// UpdateSessionState writes the status and amounts as ABSOLUTE values.
	UpdateSessionState(
		ctx context.Context,
		id string,
		status models.SessionStatus,
		authorized, captured, refunded int64,
		declineReason string,
	) (models.TenderSession, error)

	// Move appends one row to the ledger.
	Move(ctx context.Context, entry Entry) error
	// Balance sums a customer's rows in one currency.
	Balance(ctx context.Context, customerID, currencyCode string) (int64, error)
	// LockBalance locks that customer's balance in one currency for the
	// transaction. It is a lock on the BALANCE rather than on its rows: a sum has
	// no row, and a lock on the rows that exist takes nothing from a customer who
	// has none yet (D118).
	LockBalance(ctx context.Context, customerID, currencyCode string) error
}

// Identity is what a tender brings to the machine besides its store.
type Identity struct {
	// Unit is the noun the machine uses in messages: "store credit", "loyalty
	// points".
	Unit string
	// Codes are the tender's own error codes, so a caller can tell which
	// tender refused.
	Codes Codes
	// NewSessionID mints the tender's own session identifier.
	NewSessionID func() string
}

// Codes are the four error codes a tender declares.
type Codes struct {
	// InvalidInput reports a call the machine cannot make sense of.
	InvalidInput string
	// NoCustomer reports a session opened without an owner.
	//
	// It travels as a CONFLICT: at the storefront the chooser is a shopper who
	// read a list that offered this tender for a cart naming nobody, and on the
	// admin surface it is an operator opening a session on a collection that
	// names nobody. Either way the request is well formed and the state it meets
	// is what refuses it — which is what a conflict says, and what a server error
	// does not (ADR 0165).
	NoCustomer string
	// Insufficient reports a balance that does not cover the session.
	//
	// It is the tender's REFUSAL and travels as a declined authorization, not as
	// an error: not having enough is an ordinary outcome of paying, like a card
	// being declined.
	Insufficient string
	// InvalidState reports a transition the session's status does not allow.
	InvalidState string
}

// Machine is the state machine. It is safe for concurrent use.
type Machine struct {
	store Store
	id    Identity
	log   *slog.Logger
}

// New builds the machine on the given store.
func New(store Store, id Identity, log *slog.Logger) *Machine {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Machine{store: store, id: id, log: log}
}

// CreateSession opens a session against the customer's balance.
//
// It takes NOTHING: the balance is not even read here. A session is the
// statement "this much is about to be asked of this customer's balance", and
// the asking is [Machine.Authorize] — which is the same order a card provider
// works in, and the reason the saga can open a session and still change its
// mind.
func (m *Machine) CreateSession(
	ctx context.Context, in coreprovider.CreateSessionInput,
) (coreprovider.Session, error) {
	customerID := strings.TrimSpace(in.CustomerID)
	if customerID == "" {
		return coreprovider.Session{}, errors.Conflict(m.id.Codes.NoCustomer,
			"a balance of %s belongs to one person and this session names nobody: the "+
				"payment collection was opened without a customer", m.id.Unit)
	}
	if in.Amount <= 0 {
		return coreprovider.Session{}, errors.Invalid(m.id.Codes.InvalidInput,
			"the session amount has to be positive, %d given", in.Amount)
	}
	key := strings.TrimSpace(in.IdempotencyKey)
	if key == "" {
		return coreprovider.Session{}, errors.Invalid(m.id.Codes.InvalidInput,
			"the idempotency key is required")
	}

	session := models.TenderSession{
		ID:             m.id.NewSessionID(),
		IdempotencyKey: key,
		Reference:      in.Reference,
		CustomerID:     customerID,
		Amount:         in.Amount,
		CurrencyCode:   in.CurrencyCode,
		Status:         models.SessionPending,
	}

	written, inserted, err := m.store.InsertSessionIfAbsent(ctx, session)
	if err != nil {
		return coreprovider.Session{}, err
	}
	if !inserted {
		// The key is already in use: the contract says return the EXISTING
		// session rather than opening a second one.
		written, err = m.store.SessionByIdempotencyKey(ctx, key)
		if err != nil {
			return coreprovider.Session{}, err
		}
	}

	return coreprovider.Session{
		ID:           written.ID,
		Status:       coreprovider.SessionStatus(written.Status),
		Amount:       written.Amount,
		CurrencyCode: written.CurrencyCode,
	}, nil
}

// Authorize holds the amount against the customer's balance.
//
// # The lock is the whole correctness argument
//
// The balance is read and acted on, so two authorizations of two different
// sessions belonging to one customer must not both see the same money. The
// customer's balance is locked first — the balance, not its rows, because a
// customer with no rows yet would lock nothing (D118) — so the second call
// waits, and the sum it takes afterwards sees the hold the first one wrote.
//
// # An insufficient balance is a DECLINE and not an error
//
// The session moves to "failed" with a reason, exactly as a declined card does.
// Returning an error instead would make the saga treat an ordinary outcome as a
// fault and compensate a checkout that simply needs another tender.
func (m *Machine) Authorize(
	ctx context.Context, sessionID string,
) (coreprovider.AuthResult, error) {
	if strings.TrimSpace(sessionID) == "" {
		return coreprovider.AuthResult{}, errors.Invalid(m.id.Codes.InvalidInput,
			"the session identifier is required")
	}

	var out coreprovider.AuthResult
	err := m.store.WithTx(ctx, func(ctx context.Context) error {
		session, err := m.store.LockSession(ctx, sessionID)
		if err != nil {
			return err
		}

		if session.Status != models.SessionPending {
			// A session that already moved answers with what it holds; the
			// contract asks for the current state rather than an error.
			out = authResultOf(session)

			return nil
		}

		// The ledger lock comes AFTER the session lock, and the order is the
		// module's own: every flow takes the collection first and the children
		// after, so a pair of transactions can never hold one another's next lock.
		if err := m.store.LockBalance(ctx, session.CustomerID, session.CurrencyCode); err != nil {
			return err
		}

		balance, err := m.store.Balance(ctx, session.CustomerID, session.CurrencyCode)
		if err != nil {
			return err
		}

		if balance < session.Amount {
			updated, updateErr := m.store.UpdateSessionState(ctx, session.ID,
				models.SessionFailed, 0, session.CapturedAmount, session.RefundedAmount,
				m.insufficientReason(balance, session))
			if updateErr != nil {
				return updateErr
			}

			m.log.DebugContext(ctx, "balance tender declined",
				"unit", m.id.Unit, "session", session.ID, "customer", session.CustomerID,
				"balance", balance, "asked", session.Amount)

			out = authResultOf(updated)

			return nil
		}

		if err := m.store.Move(ctx, Entry{
			CustomerID:   session.CustomerID,
			CurrencyCode: session.CurrencyCode,
			Movement:     Hold,
			// NEGATIVE: from this moment the amount is not spendable by anything
			// else, which is what makes the balance above safe to act on.
			Amount:    -session.Amount,
			SessionID: session.ID,
		}); err != nil {
			return err
		}

		updated, err := m.store.UpdateSessionState(ctx, session.ID,
			models.SessionAuthorized, session.Amount, session.CapturedAmount,
			session.RefundedAmount, "")
		if err != nil {
			return err
		}

		out = authResultOf(updated)

		return nil
	})
	if err != nil {
		return coreprovider.AuthResult{}, err
	}

	return out, nil
}

// Capture collects the held amount.
//
// It writes no spend to the ledger, and that is the design: the hold already
// took the amount out of the balance, so a second negative row would take it
// twice. What capture records is that the hold became a spend, and the session
// is where that is recorded.
//
// A PARTIAL capture releases the difference, for the reason the manual provider
// releases it: the session cannot be canceled afterwards, so an unreleased
// remainder would hang forever.
func (m *Machine) Capture(ctx context.Context, sessionID string, amount int64) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.Invalid(m.id.Codes.InvalidInput, "the session identifier is required")
	}

	return m.store.WithTx(ctx, func(ctx context.Context) error {
		session, err := m.store.LockSession(ctx, sessionID)
		if err != nil {
			return err
		}

		switch session.Status {
		case models.SessionCaptured:
			// Already captured: the contract asks for the current state.
			return nil
		case models.SessionAuthorized:
			// Handled below.
		default:
			return errors.Conflict(m.id.Codes.InvalidState,
				"a session in the %q state cannot be captured: %s", session.Status, session.ID)
		}

		taken := amount
		if taken == 0 {
			taken = session.AuthorizedAmount
		}
		if taken > session.AuthorizedAmount {
			return errors.Conflict(m.id.Codes.InvalidState,
				"the capture cannot exceed the authorized amount: asked %d, held %d (%s)",
				taken, session.AuthorizedAmount, session.ID)
		}

		if remainder := session.AuthorizedAmount - taken; remainder > 0 {
			if err := m.store.Move(ctx, Entry{
				CustomerID:   session.CustomerID,
				CurrencyCode: session.CurrencyCode,
				Movement:     Release,
				Amount:       remainder,
				SessionID:    session.ID,
			}); err != nil {
				return err
			}
		}

		_, err = m.store.UpdateSessionState(ctx, session.ID,
			models.SessionCaptured, taken, taken, session.RefundedAmount, "")

		return err
	})
}

// Refund gives a captured amount back to the balance.
//
// The amount returns to the ledger it came from, which is the only destination
// it can have: a balance tender holds no card and no account, so "repaying" is a
// positive row in the customer's own balance.
func (m *Machine) Refund(ctx context.Context, sessionID string, amount int64) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.Invalid(m.id.Codes.InvalidInput, "the session identifier is required")
	}

	return m.store.WithTx(ctx, func(ctx context.Context) error {
		session, err := m.store.LockSession(ctx, sessionID)
		if err != nil {
			return err
		}

		if session.Status != models.SessionCaptured {
			return errors.Conflict(m.id.Codes.InvalidState,
				"only a captured session can be refunded, this one is %q: %s",
				session.Status, session.ID)
		}

		given := amount
		if given == 0 {
			given = session.CapturedAmount - session.RefundedAmount
		}
		if given <= 0 {
			// Nothing left to give back; a repeated call is not an error.
			return nil
		}
		if session.RefundedAmount+given > session.CapturedAmount {
			return errors.Conflict(m.id.Codes.InvalidState,
				"the refund cannot exceed what was captured: asked %d, captured %d, "+
					"already refunded %d (%s)",
				given, session.CapturedAmount, session.RefundedAmount, session.ID)
		}

		if err := m.store.Move(ctx, Entry{
			CustomerID:   session.CustomerID,
			CurrencyCode: session.CurrencyCode,
			Movement:     Refund,
			Amount:       given,
			SessionID:    session.ID,
		}); err != nil {
			return err
		}

		_, err = m.store.UpdateSessionState(ctx, session.ID,
			models.SessionCaptured, session.AuthorizedAmount, session.CapturedAmount,
			session.RefundedAmount+given, "")

		return err
	})
}

// Cancel releases the hold; it is the saga's compensation and is IDEMPOTENT.
func (m *Machine) Cancel(ctx context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.Invalid(m.id.Codes.InvalidInput, "the session identifier is required")
	}

	return m.store.WithTx(ctx, func(ctx context.Context) error {
		session, err := m.store.LockSession(ctx, sessionID)
		if err != nil {
			return err
		}

		switch session.Status {
		case models.SessionCanceled, models.SessionFailed:
			// Already closed; a second cancel is not a failure.
			return nil
		case models.SessionCaptured:
			return errors.Conflict(m.id.Codes.InvalidState,
				"a captured session cannot be canceled, it is refunded: %s", session.ID)
		case models.SessionPending:
			// Nothing was held, so nothing comes back.
			_, err = m.store.UpdateSessionState(ctx, session.ID,
				models.SessionCanceled, 0, session.CapturedAmount, session.RefundedAmount, "")

			return err
		case models.SessionAuthorized:
			// Handled below.
		}

		if session.AuthorizedAmount > 0 {
			if err := m.store.Move(ctx, Entry{
				CustomerID:   session.CustomerID,
				CurrencyCode: session.CurrencyCode,
				Movement:     Release,
				Amount:       session.AuthorizedAmount,
				SessionID:    session.ID,
			}); err != nil {
				return err
			}
		}

		_, err = m.store.UpdateSessionState(ctx, session.ID,
			models.SessionCanceled, 0, session.CapturedAmount, session.RefundedAmount, "")

		return err
	})
}

// InspectSession answers reconciliation from the tender's own session table.
//
// It satisfies [coreprovider.SessionInspector]. The hold moves in the module's
// own transaction, so the divergence reconciliation exists to find — the
// provider took the money and the module's commit failed — cannot happen here;
// what the answer buys is that the hourly job says "the two agree" for these
// sessions instead of "nobody asked", which the manual provider of the same
// shape already answers and store credit did not (ADR 0165).
func (m *Machine) InspectSession(
	ctx context.Context, sessionID string,
) (coreprovider.SessionInspection, error) {
	if strings.TrimSpace(sessionID) == "" {
		return coreprovider.SessionInspection{}, errors.Invalid(m.id.Codes.InvalidInput,
			"the session identifier is required")
	}

	session, err := m.store.Session(ctx, sessionID)
	if err != nil {
		return coreprovider.SessionInspection{}, err
	}

	return coreprovider.SessionInspection{
		Status:           coreprovider.SessionStatus(session.Status),
		AuthorizedAmount: session.AuthorizedAmount,
		CapturedAmount:   session.CapturedAmount,
		RefundedAmount:   session.RefundedAmount,
	}, nil
}

// authResultOf turns a session into the contract's answer.
func authResultOf(session models.TenderSession) coreprovider.AuthResult {
	return coreprovider.AuthResult{
		Status:           coreprovider.SessionStatus(session.Status),
		AuthorizedAmount: session.AuthorizedAmount,
		DeclineReason:    session.DeclineReason,
	}
}

// insufficientReason says what was missing, for an operator rather than a
// shopper.
//
// The contract's own godoc says a decline reason is for diagnosis and is NOT to
// be shown to the customer, which is why it carries the balance: telling a
// shopper how much they hold on a failed payment would answer a question they
// did not ask, on a screen that is about something else.
func (m *Machine) insufficientReason(balance int64, session models.TenderSession) string {
	return errors.Invalid(m.id.Codes.Insufficient,
		"the balance does not cover this payment: %d held, %d asked (%s)",
		balance, session.Amount, session.CurrencyCode).Error()
}
