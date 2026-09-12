// Package storecredit is the payment provider that spends a customer's store
// credit (ADR 0152).
//
// [Provider] satisfies core/provider's PaymentProvider contract and meets the
// idempotency conditions written in that contract's godoc, the same way the
// manual provider does:
//
//   - a second CreateSession with the same IdempotencyKey opens no new session;
//   - Authorize, Capture and Refund may be called again and return the current
//     state rather than an error;
//   - Cancel is the saga's compensation and is IDEMPOTENT.
//
// # What each verb does to the money
//
// The ledger is the module's (payment_store_credit_entries) and the session is
// this provider's own (payment_store_credit_sessions), which is the separation
// the manual provider keeps for the same reason: the module reaches a provider
// only through the contract.
//
//	CreateSession  writes nothing to the ledger — it opens a session
//	Authorize      writes a HOLD (negative): the money stops being spendable
//	Capture        writes nothing — the hold already took it; the session records it
//	Cancel         writes a RELEASE (positive): the hold comes back
//	Refund         writes a REFUND (positive): a captured amount is given back
//
// A capture that wrote a second negative row would take the money twice, and a
// balance that only fell at capture time would let two checkouts spend the same
// credit while both were still deciding. The hold is what makes the balance safe
// to read.
//
// # Whose money it is
//
// The customer comes from [coreprovider.CreateSessionInput.CustomerID], which the
// payment module fills from the COLLECTION. It does not come from Data: that map
// is the client's, and a shopper naming somebody else's id would spend their
// balance. A session opened with no customer is refused rather than served with
// an empty owner.
package storecredit

import (
	"context"
	"log/slog"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// ID is the provider's identity; sessions are opened under this name.
//
// A storefront asks for it by this string in the completion body's
// payment_provider_id, exactly as it asks for "manual".
const ID = "store_credit"

// The provider's error codes.
const (
	// CodeInvalidInput reports a call this provider cannot make sense of.
	CodeInvalidInput = "payment_store_credit_invalid_input"
	// CodeNoCustomer reports a session opened without an owner.
	//
	// It is an INTERNAL error rather than a client one: the payment module fills
	// the field from the collection, so reaching here without it means the caller
	// opened a collection for nobody and then chose a tender that belongs to
	// somebody. Telling the shopper "invalid request" would send them to fix a
	// body that is not wrong.
	CodeNoCustomer = "payment_store_credit_no_customer"
	// CodeInsufficient reports a balance that does not cover the session.
	//
	// It is the provider's REFUSAL and travels as a declined authorization, not as
	// an error: not having enough credit is an ordinary outcome of paying, like a
	// card being declined.
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
		ctx context.Context, session models.StoreCreditSession,
	) (models.StoreCreditSession, bool, error)
	// StoreCreditSessionByIdempotencyKey returns the session by its key.
	StoreCreditSessionByIdempotencyKey(ctx context.Context, key string) (models.StoreCreditSession, error)
	// StoreCreditSession returns the session by its id.
	StoreCreditSession(ctx context.Context, id string) (models.StoreCreditSession, error)
	// LockStoreCreditSession locks the session for the transaction.
	LockStoreCreditSession(ctx context.Context, id string) (models.StoreCreditSession, error)
	// UpdateStoreCreditSessionState writes the status and amounts as ABSOLUTE
	// values.
	UpdateStoreCreditSessionState(
		ctx context.Context,
		id string,
		status models.SessionStatus,
		authorized, captured, refunded int64,
		declineReason string,
	) (models.StoreCreditSession, error)

	// AppendStoreCreditEntry appends one event to the ledger.
	AppendStoreCreditEntry(ctx context.Context, entry models.StoreCreditEntry) (models.StoreCreditEntry, error)
	// StoreCreditBalance sums a customer's entries in one currency.
	StoreCreditBalance(ctx context.Context, customerID, currencyCode string) (int64, error)
	// LockStoreCreditEntries locks that customer's rows for the transaction.
	LockStoreCreditEntries(ctx context.Context, customerID, currencyCode string) error
}

// Provider spends store credit. It is safe for concurrent use.
type Provider struct {
	store Store
	log   *slog.Logger
}

// That the core contract is satisfied is pinned at compile time.
var _ coreprovider.PaymentProvider = (*Provider)(nil)

// New builds the provider on the given store.
func New(store Store, log *slog.Logger) *Provider {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Provider{store: store, log: log}
}

// ID returns the provider's identity.
func (p *Provider) ID() string { return ID }

// CreateSession opens a session against the customer's credit.
//
// It takes NO money: the balance is not even read here. A session is the
// statement "this much is about to be asked of this customer's credit", and the
// asking is [Provider.Authorize] — which is the same order a card provider works
// in, and the reason the saga can open a session and still change its mind.
func (p *Provider) CreateSession(
	ctx context.Context, in coreprovider.CreateSessionInput,
) (coreprovider.Session, error) {
	customerID := strings.TrimSpace(in.CustomerID)
	if customerID == "" {
		return coreprovider.Session{}, errors.Internal(CodeNoCustomer,
			"store credit is one person's money and this session names nobody: the payment "+
				"collection was opened without a customer")
	}
	if in.Amount <= 0 {
		return coreprovider.Session{}, errors.Invalid(CodeInvalidInput,
			"the session amount has to be positive, %d given", in.Amount)
	}
	key := strings.TrimSpace(in.IdempotencyKey)
	if key == "" {
		return coreprovider.Session{}, errors.Invalid(CodeInvalidInput,
			"the idempotency key is required")
	}

	session := models.StoreCreditSession{
		ID:             models.NewStoreCreditSessionID(),
		IdempotencyKey: key,
		Reference:      in.Reference,
		CustomerID:     customerID,
		Amount:         in.Amount,
		CurrencyCode:   in.CurrencyCode,
		Status:         models.SessionPending,
	}

	written, inserted, err := p.store.InsertStoreCreditSessionIfAbsent(ctx, session)
	if err != nil {
		return coreprovider.Session{}, err
	}
	if !inserted {
		// The key is already in use: the contract says return the EXISTING
		// session rather than opening a second one.
		written, err = p.store.StoreCreditSessionByIdempotencyKey(ctx, key)
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
// customer's ledger rows are locked first; the second call then waits, and the
// sum it takes afterwards sees the hold the first one wrote.
//
// # An insufficient balance is a DECLINE and not an error
//
// The session moves to "failed" with a reason, exactly as a declined card does.
// Returning an error instead would make the saga treat an ordinary outcome as a
// fault and compensate a checkout that simply needs another tender.
func (p *Provider) Authorize(
	ctx context.Context, sessionID string,
) (coreprovider.AuthResult, error) {
	if strings.TrimSpace(sessionID) == "" {
		return coreprovider.AuthResult{}, errors.Invalid(CodeInvalidInput,
			"the session identifier is required")
	}

	var out coreprovider.AuthResult
	err := p.store.WithTx(ctx, func(ctx context.Context) error {
		session, err := p.store.LockStoreCreditSession(ctx, sessionID)
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
		if err := p.store.LockStoreCreditEntries(ctx, session.CustomerID, session.CurrencyCode); err != nil {
			return err
		}

		balance, err := p.store.StoreCreditBalance(ctx, session.CustomerID, session.CurrencyCode)
		if err != nil {
			return err
		}

		if balance < session.Amount {
			updated, updateErr := p.store.UpdateStoreCreditSessionState(ctx, session.ID,
				models.SessionFailed, 0, session.CapturedAmount, session.RefundedAmount,
				insufficientReason(balance, session))
			if updateErr != nil {
				return updateErr
			}

			out = authResultOf(updated)

			return nil
		}

		if _, err := p.store.AppendStoreCreditEntry(ctx, models.StoreCreditEntry{
			ID:           models.NewStoreCreditEntryID(),
			CustomerID:   session.CustomerID,
			CurrencyCode: session.CurrencyCode,
			// NEGATIVE: from this moment the money is not spendable by anything
			// else, which is what makes the balance above safe to act on.
			Amount:    -session.Amount,
			Kind:      models.StoreCreditHold,
			Reference: session.ID,
		}); err != nil {
			return err
		}

		updated, err := p.store.UpdateStoreCreditSessionState(ctx, session.ID,
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
// It writes NOTHING to the ledger, and that is the design: the hold already took
// the money out of the balance, so a second negative row would take it twice. What
// capture records is that the hold became a spend, and the session is where that
// is recorded.
//
// A PARTIAL capture releases the difference, for the reason the manual provider
// releases it: the session cannot be canceled afterwards, so an unreleased
// remainder would hang forever.
func (p *Provider) Capture(ctx context.Context, sessionID string, amount int64) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.Invalid(CodeInvalidInput, "the session identifier is required")
	}

	return p.store.WithTx(ctx, func(ctx context.Context) error {
		session, err := p.store.LockStoreCreditSession(ctx, sessionID)
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
			return errors.Conflict(CodeInvalidState,
				"a session in the %q state cannot be captured: %s", session.Status, session.ID)
		}

		taken := amount
		if taken == 0 {
			taken = session.AuthorizedAmount
		}
		if taken > session.AuthorizedAmount {
			return errors.Conflict(CodeInvalidState,
				"the capture cannot exceed the authorized amount: asked %d, held %d (%s)",
				taken, session.AuthorizedAmount, session.ID)
		}

		if remainder := session.AuthorizedAmount - taken; remainder > 0 {
			if _, err := p.store.AppendStoreCreditEntry(ctx, models.StoreCreditEntry{
				ID:           models.NewStoreCreditEntryID(),
				CustomerID:   session.CustomerID,
				CurrencyCode: session.CurrencyCode,
				Amount:       remainder,
				Kind:         models.StoreCreditRelease,
				Reference:    session.ID,
			}); err != nil {
				return err
			}
		}

		_, err = p.store.UpdateStoreCreditSessionState(ctx, session.ID,
			models.SessionCaptured, taken, taken, session.RefundedAmount, "")

		return err
	})
}

// Refund gives a captured amount back as credit.
//
// The money returns to the ledger it came from, which is the only destination it
// can have: this provider holds no card and no account, so "repaying" is a
// positive row in the customer's own balance.
func (p *Provider) Refund(ctx context.Context, sessionID string, amount int64) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.Invalid(CodeInvalidInput, "the session identifier is required")
	}

	return p.store.WithTx(ctx, func(ctx context.Context) error {
		session, err := p.store.LockStoreCreditSession(ctx, sessionID)
		if err != nil {
			return err
		}

		if session.Status != models.SessionCaptured {
			return errors.Conflict(CodeInvalidState,
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
			return errors.Conflict(CodeInvalidState,
				"the refund cannot exceed what was captured: asked %d, captured %d, "+
					"already refunded %d (%s)",
				given, session.CapturedAmount, session.RefundedAmount, session.ID)
		}

		if _, err := p.store.AppendStoreCreditEntry(ctx, models.StoreCreditEntry{
			ID:           models.NewStoreCreditEntryID(),
			CustomerID:   session.CustomerID,
			CurrencyCode: session.CurrencyCode,
			Amount:       given,
			Kind:         models.StoreCreditRefund,
			Reference:    session.ID,
		}); err != nil {
			return err
		}

		_, err = p.store.UpdateStoreCreditSessionState(ctx, session.ID,
			models.SessionCaptured, session.AuthorizedAmount, session.CapturedAmount,
			session.RefundedAmount+given, "")

		return err
	})
}

// Cancel releases the hold; it is the saga's compensation and is IDEMPOTENT.
func (p *Provider) Cancel(ctx context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.Invalid(CodeInvalidInput, "the session identifier is required")
	}

	return p.store.WithTx(ctx, func(ctx context.Context) error {
		session, err := p.store.LockStoreCreditSession(ctx, sessionID)
		if err != nil {
			return err
		}

		switch session.Status {
		case models.SessionCanceled, models.SessionFailed:
			// Already closed; a second cancel is not a failure.
			return nil
		case models.SessionCaptured:
			return errors.Conflict(CodeInvalidState,
				"a captured session cannot be canceled, it is refunded: %s", session.ID)
		case models.SessionPending:
			// Nothing was held, so nothing comes back.
			_, err = p.store.UpdateStoreCreditSessionState(ctx, session.ID,
				models.SessionCanceled, 0, session.CapturedAmount, session.RefundedAmount, "")

			return err
		case models.SessionAuthorized:
			// Handled below.
		}

		if session.AuthorizedAmount > 0 {
			if _, err := p.store.AppendStoreCreditEntry(ctx, models.StoreCreditEntry{
				ID:           models.NewStoreCreditEntryID(),
				CustomerID:   session.CustomerID,
				CurrencyCode: session.CurrencyCode,
				Amount:       session.AuthorizedAmount,
				Kind:         models.StoreCreditRelease,
				Reference:    session.ID,
			}); err != nil {
				return err
			}
		}

		_, err = p.store.UpdateStoreCreditSessionState(ctx, session.ID,
			models.SessionCanceled, 0, session.CapturedAmount, session.RefundedAmount, "")

		return err
	})
}

// authResultOf turns a session into the contract's answer.
func authResultOf(session models.StoreCreditSession) coreprovider.AuthResult {
	return coreprovider.AuthResult{
		Status:           coreprovider.SessionStatus(session.Status),
		AuthorizedAmount: session.AuthorizedAmount,
		DeclineReason:    session.DeclineReason,
	}
}

// insufficientReason says what was missing, for an operator rather than a shopper.
//
// The contract's own godoc says a decline reason is for diagnosis and is NOT to be
// shown to the customer, which is why it carries the balance: telling a shopper
// how much credit they hold on a failed payment would answer a question they did
// not ask, on a screen that is about something else.
func insufficientReason(balance int64, session models.StoreCreditSession) string {
	return errors.Invalid(CodeInsufficient,
		"the balance does not cover this payment: %d held, %d asked (%s)",
		balance, session.Amount, session.CurrencyCode).Error()
}
