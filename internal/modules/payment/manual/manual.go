// Package manual is the test/manual payment provider, which makes no real
// network call (plan Phase 6).
//
// [Provider] satisfies the PaymentProvider contract in core/provider and meets
// the IDEMPOTENCY requirements written in that contract's godoc:
//
//   - A second [Provider.CreateSession] with the same IdempotencyKey does NOT
//     open a NEW session; it returns the existing one.
//   - [Provider.Authorize], [Provider.Capture] and [Provider.Refund] can be
//     called again on the same session; the second call is NOT an error, it
//     returns the current state.
//   - [Provider.Cancel] is the saga compensation and is IDEMPOTENT: a session
//     canceled twice returns no error on the second call.
//
// # Why the state is kept in the DATABASE
//
// A ledger kept in memory would be reset every time the process restarts. The
// price would be paid in three places:
//
//   - The e2e flows (internal/e2e) and the Phase 9 load test must be able to
//     find a session that was OPENED before the process restarted; otherwise
//     the payment step fails with "session not found".
//   - The saga compensation has to work in exactly the scenario where the
//     process went down. With a provider that relies on memory, Cancel could
//     never run after a restart and the held amount would stay hanging
//     forever.
//   - Several processes (or horizontal scaling) would not see the same
//     session; the provider would behave correctly only on a server running a
//     single instance.
//
// A real payment institution's state also lives in its own system and is not
// affected by process restarts; the imitation therefore has to be durable.
//
// # The separate ledger
//
// The provider's state is in the payment_manual_sessions table and is SEPARATE
// from the payment service's tables. The service never touches this table; it
// reaches the provider only through the PaymentProvider interface. The
// separation structurally prevents the module from accidentally reading the
// provider's internal state — with a real provider such a read is not possible
// either.
//
// # Failure injection for tests
//
// Saga tests MUST BE ABLE TO BLOW UP the payment step. The behavior is read
// from the Data field given when the session is opened and is stored durably
// with the session, so the same session behaves the same way even if the
// process restarts. See [DataKeyOutcome], [DataKeyDeclineReason] and
// [DataKeyAuthorizedAmount].
package manual

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	coreprovider "github.com/bdrtr/gobit/core/provider"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// ID is the provider's identifier; sessions are opened under this name.
const ID = "manual"

// The Data keys that steer the provider's behavior.
//
// The keys arrive in the session's Data field and are STORED with the session.
// Storing them is necessary: authorization happens in a different request from
// the call that opened the session (even in a different process), and that
// call holds nothing but the session identifier.
const (
	// DataKeyOutcome decides the outcome of the authorization; its values are
	// [OutcomeAuthorize], [OutcomeDecline] and [OutcomeError]. If it is not
	// given, [OutcomeAuthorize] is assumed.
	DataKeyOutcome = "manual_outcome"
	// DataKeyDeclineReason sets the reason for the decline; it is meaningful
	// only with [OutcomeDecline].
	DataKeyDeclineReason = "manual_decline_reason"
	// DataKeyAuthorizedAmount exercises PARTIAL authorization: if it is given,
	// this amount is held instead of the session amount. It cannot be greater
	// than the session amount.
	DataKeyAuthorizedAmount = "manual_authorized_amount"
)

// Authorization outcomes (the values of [DataKeyOutcome]).
const (
	// OutcomeAuthorize holds the amount; it is the default behavior.
	OutcomeAuthorize = "authorize"
	// OutcomeDecline DECLINES the authorization: the session becomes "failed"
	// and the AuthResult carries the decline reason. It does NOT return an
	// error; from the provider's point of view a decline is a successful
	// response.
	OutcomeDecline = "decline"
	// OutcomeError imitates the provider being UNREACHABLE: the method returns
	// an error and the session's state does NOT CHANGE. It is there to exercise
	// the saga's "the step blew up" branch; what sets it apart from
	// [OutcomeDecline] is that it can be retried.
	OutcomeError = "error"
)

// Error codes. Clients may branch on these; the messages may change, the codes
// do not.
const (
	// CodeInvalidInput reports that the input did not pass validation.
	CodeInvalidInput = "payment_manual_invalid_input"
	// CodeInvalidState reports that an invalid transition of the session's
	// state was attempted.
	CodeInvalidState = "payment_manual_invalid_state"
	// CodeIdempotencyMismatch reports that the same key was reused with a
	// DIFFERENT body.
	CodeIdempotencyMismatch = "payment_manual_idempotency_mismatch"
	// CodeSimulatedFailure reports a failure injected for a test.
	CodeSimulatedFailure = "payment_manual_simulated_failure"
	// CodeDataInvalid reports that the session data could not be decoded.
	CodeDataInvalid = "payment_manual_data_invalid"
)

// declineReasonDefault is the decline reason used when no reason is given.
const declineReasonDefault = "declined by the manual provider (test)"

// Store is the persistence surface the provider needs.
//
// The interface is defined on the CONSUMING side, that is, here (the pattern
// of ADR 0001). The provider DOES NOT import the repository package; the
// concrete store satisfies these signatures structurally and the wiring is
// done in module.go. That way the provider's idempotency behavior can be
// exercised without a real database, with a fake store a few lines long.
//
// The method that takes a lock ([Store.LockManualSession]) may only be called
// inside [Store.WithTx]: a FOR UPDATE lock without a transaction protects
// nothing.
type Store interface {
	// WithTx runs fn in a single transaction; if fn returns an error the
	// transaction is rolled back.
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error

	// InsertManualSessionIfAbsent writes the session only if the idempotency
	// key has not been used yet. The second return value reports whether the
	// row was written; a conflict is NOT AN ERROR.
	InsertManualSessionIfAbsent(ctx context.Context, ses models.ManualSession) (models.ManualSession, bool, error)
	// ManualSessionByIdempotencyKey returns the session by its key; NotFound if
	// there is none.
	ManualSessionByIdempotencyKey(ctx context.Context, key string) (models.ManualSession, error)
	// ManualSession returns the session by its identifier; NotFound if there is
	// none.
	ManualSession(ctx context.Context, id string) (models.ManualSession, error)
	// LockManualSession locks the session for the rest of the transaction and
	// returns its current state.
	LockManualSession(ctx context.Context, id string) (models.ManualSession, error)
	// UpdateManualSessionState writes the status and the amounts as ABSOLUTE
	// values.
	UpdateManualSessionState(
		ctx context.Context,
		id string,
		status models.SessionStatus,
		authorized, captured, refunded int64,
		declineReason string,
	) (models.ManualSession, error)
}

// Provider is the manual/test payment provider. It is safe for concurrent use.
type Provider struct {
	store Store
	log   *slog.Logger
}

// That Provider satisfies the core contract is verified at compile time; a
// signature drift is not left to run time.
var _ coreprovider.PaymentProvider = (*Provider)(nil)

// New builds a manual provider that works on the given store.
// If log is nil, the logs are discarded.
func New(store Store, log *slog.Logger) *Provider {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Provider{store: store, log: log}
}

// That the provider also satisfies the core's OPTIONAL reconciliation
// capability is pinned at compile time.
//
// [coreprovider.SessionInspector] is looked up with a type assertion: had the
// signature drifted, nothing would break, the reconciliation job would only say
// "this provider cannot be asked" and a money discrepancy would stay invisible.
// This line closes that silence.
var _ coreprovider.SessionInspector = (*Provider)(nil)

// ID returns the provider's identifier.
func (p *Provider) ID() string { return ID }

// CreateSession opens a payment session in the provider's ledger.
//
// A second call with the same IdempotencyKey does NOT open a NEW session; it
// returns the existing one (a requirement of the core contract). If the key is
// the same but the amount or the currency is DIFFERENT, errors.Conflict is
// returned: idempotency means "repeating the same request", not "sending a
// different request under an old key" — accepting the second silently would
// mean the amount the caller believes it sent is never applied.
func (p *Provider) CreateSession(
	ctx context.Context,
	in coreprovider.CreateSessionInput,
) (coreprovider.Session, error) {
	key := strings.TrimSpace(in.IdempotencyKey)
	if key == "" {
		return coreprovider.Session{}, errors.Invalid(CodeInvalidInput,
			"the idempotency key is required")
	}
	reference := strings.TrimSpace(in.Reference)
	if reference == "" {
		return coreprovider.Session{}, errors.Invalid(CodeInvalidInput, "the reference is required")
	}
	if in.Amount < models.MinAmount || in.Amount > models.MaxAmount {
		return coreprovider.Session{}, errors.Invalid(CodeInvalidInput,
			"the amount has to be between %d and %d: %d", models.MinAmount, models.MaxAmount, in.Amount)
	}
	currency := strings.ToUpper(strings.TrimSpace(in.CurrencyCode))
	if len(currency) != 3 {
		return coreprovider.Session{}, errors.Invalid(CodeInvalidInput,
			"the currency has to be a three-letter ISO 4217 code: %q", in.CurrencyCode)
	}

	raw, err := json.Marshal(in.Data)
	if err != nil {
		return coreprovider.Session{}, errors.Wrap(err, errors.KindInvalid, CodeDataInvalid,
			"the session data could not be encoded")
	}
	// The data is validated early: a broken behavior key should be reported
	// when the session is opened; having it blow up at authorization time would
	// make it harder to diagnose.
	if _, err := parseSessionData(raw); err != nil {
		return coreprovider.Session{}, err
	}

	created, inserted, err := p.store.InsertManualSessionIfAbsent(ctx, models.ManualSession{
		ID:             models.NewManualSessionID(),
		IdempotencyKey: key,
		Reference:      reference,
		Amount:         in.Amount,
		CurrencyCode:   currency,
		Status:         models.SessionPending,
		Data:           raw,
	})
	if err != nil {
		return coreprovider.Session{}, err
	}
	if inserted {
		return toProviderSession(created), nil
	}

	existing, err := p.store.ManualSessionByIdempotencyKey(ctx, key)
	if err != nil {
		return coreprovider.Session{}, err
	}
	if existing.Amount != in.Amount || existing.CurrencyCode != currency {
		return coreprovider.Session{}, errors.Conflict(CodeIdempotencyMismatch,
			"the same idempotency key was used with a different amount: existing %d %s, requested %d %s",
			existing.Amount, existing.CurrencyCode, in.Amount, currency)
	}
	p.log.DebugContext(ctx, "the manual provider returned the existing session",
		"session", existing.ID, "key", key)
	return toProviderSession(existing), nil
}

// Authorize HOLDS the amount on the customer; it does not capture.
//
// For a session that has already concluded it does NOT return an error; it
// returns the current state (a requirement of the core contract). A decline
// ([OutcomeDecline]) is not an error either: the result comes back with the
// SessionFailed status and the decline reason. Only [OutcomeError] produces a
// real error, and in that case the session's state does NOT CHANGE.
func (p *Provider) Authorize(ctx context.Context, sessionID string) (coreprovider.AuthResult, error) {
	if strings.TrimSpace(sessionID) == "" {
		return coreprovider.AuthResult{}, errors.Invalid(CodeInvalidInput, "the session identifier is required")
	}

	var out coreprovider.AuthResult
	err := p.store.WithTx(ctx, func(ctx context.Context) error {
		ses, err := p.store.LockManualSession(ctx, sessionID)
		if err != nil {
			return err
		}

		if ses.Status != models.SessionPending {
			// A concluded session: the current state is returned as it is.
			out = coreprovider.AuthResult{
				Status:           coreprovider.SessionStatus(ses.Status),
				AuthorizedAmount: ses.AuthorizedAmount,
				Data:             ses.Data,
				DeclineReason:    ses.DeclineReason,
			}
			return nil
		}

		decision, err := decideAuthorize(ses)
		if err != nil {
			return err
		}

		updated, err := p.store.UpdateManualSessionState(ctx, ses.ID,
			decision.Status, decision.AuthorizedAmount, ses.CapturedAmount, ses.RefundedAmount,
			decision.DeclineReason)
		if err != nil {
			return err
		}

		out = coreprovider.AuthResult{
			Status:           coreprovider.SessionStatus(updated.Status),
			AuthorizedAmount: updated.AuthorizedAmount,
			Data:             updated.Data,
			DeclineReason:    updated.DeclineReason,
		}
		return nil
	})
	if err != nil {
		return coreprovider.AuthResult{}, err
	}
	return out, nil
}

// Capture captures the held amount. If amount is zero the whole of it is
// taken, and amount CANNOT be greater than the authorized amount.
//
// On a partial capture the hold that is NOT TAKEN is released: the held amount
// in the ledger drops to the captured amount. Since the session can no longer
// be canceled, the difference would stay hanging forever if it were not
// released.
//
// A second call on an already captured session with the same amount (or with
// zero) does NOT return an error; if a different amount is requested,
// errors.Conflict is returned, because that is no longer a repeat, it is a new
// request.
func (p *Provider) Capture(ctx context.Context, sessionID string, amount int64) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.Invalid(CodeInvalidInput, "the session identifier is required")
	}
	if amount < 0 {
		return errors.Invalid(CodeInvalidInput, "the capture amount cannot be negative: %d", amount)
	}

	return p.store.WithTx(ctx, func(ctx context.Context) error {
		ses, err := p.store.LockManualSession(ctx, sessionID)
		if err != nil {
			return err
		}

		switch ses.Status.CaptureAction() {
		case models.ActionNoop:
			if amount != 0 && amount != ses.CapturedAmount {
				return errors.Conflict(CodeInvalidState,
					"the session was captured with an amount of %d; it cannot be captured again with %d (%s)",
					ses.CapturedAmount, amount, sessionID)
			}
			p.log.DebugContext(ctx, "the manual provider's session is already captured", "session", sessionID)
			return nil
		case models.ActionConflict:
			return errors.Conflict(CodeInvalidState,
				"a session in the %q state cannot be captured: %s", ses.Status, sessionID)
		case models.ActionProceed:
			// Handled below.
		}

		captured := amount
		if captured == 0 {
			captured = ses.AuthorizedAmount
		}
		if captured > ses.AuthorizedAmount {
			return errors.Conflict(CodeInvalidState,
				"the capture amount cannot exceed the authorized amount: %d requested, %d held (%s)",
				captured, ses.AuthorizedAmount, sessionID)
		}

		// The hold that is not taken is released: the held amount in the ledger
		// drops to the amount actually captured. Real providers also release the
		// remaining hold on capture; the imitation's ledger must not diverge
		// from the module's record.
		_, err = p.store.UpdateManualSessionState(ctx, ses.ID,
			models.SessionCaptured, captured, captured, ses.RefundedAmount, ses.DeclineReason)
		return err
	})
}

// Refund refunds a captured amount. If amount is zero, the whole REMAINING
// amount is refunded.
//
// A second call with amount zero on a fully refunded session does NOT return
// an error: the remainder is zero and nothing is done. That way a full refund
// request can be retried safely. An explicit amount that EXCEEDS the
// remainder, on the other hand, produces errors.Conflict; that is not a
// repeat, it is a request to refund money that does not exist.
func (p *Provider) Refund(ctx context.Context, sessionID string, amount int64) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.Invalid(CodeInvalidInput, "the session identifier is required")
	}
	if amount < 0 {
		return errors.Invalid(CodeInvalidInput, "the refund amount cannot be negative: %d", amount)
	}

	return p.store.WithTx(ctx, func(ctx context.Context) error {
		ses, err := p.store.LockManualSession(ctx, sessionID)
		if err != nil {
			return err
		}
		if ses.Status != models.SessionCaptured {
			return errors.Conflict(CodeInvalidState,
				"a session in the %q state cannot be refunded: %s", ses.Status, sessionID)
		}

		remaining := ses.RefundableAmount()
		refund := amount
		if refund == 0 {
			if remaining == 0 {
				p.log.DebugContext(ctx, "the manual provider's session is already fully refunded",
					"session", sessionID)
				return nil
			}
			refund = remaining
		}
		if refund > remaining {
			return errors.Conflict(CodeInvalidState,
				"the refund amount cannot exceed the remaining amount: %d requested, %d remaining (%s)",
				refund, remaining, sessionID)
		}

		_, err = p.store.UpdateManualSessionState(ctx, ses.ID,
			ses.Status, ses.AuthorizedAmount, ses.CapturedAmount, ses.RefundedAmount+refund,
			ses.DeclineReason)
		return err
	})
}

// Cancel closes the session and releases the hold, if there is one.
//
// THIS IS THE SAGA COMPENSATION and it is IDEMPOTENT: for a session that is
// already canceled it returns no error and makes no second change to the
// ledger. A captured session CANNOT be canceled (errors.Conflict); the money
// has been taken and the way to reverse it is a refund.
//
// For an unknown identifier errors.NotFound is returned: idempotency does not
// mean "silently swallow everything". A REAL session canceled twice and an
// identifier that never existed are different situations, and the second is a
// fault on the caller's side. Because the session record is never deleted
// (only its status changes), the first situation can always be told apart.
func (p *Provider) Cancel(ctx context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.Invalid(CodeInvalidInput, "the session identifier is required")
	}

	return p.store.WithTx(ctx, func(ctx context.Context) error {
		ses, err := p.store.LockManualSession(ctx, sessionID)
		if err != nil {
			return err
		}

		switch ses.Status.CancelAction() {
		case models.ActionNoop:
			p.log.DebugContext(ctx, "the manual provider's session is already canceled", "session", sessionID)
			return nil
		case models.ActionConflict:
			return errors.Conflict(CodeInvalidState,
				"a session in the %q state cannot be canceled; use a refund: %s", ses.Status, sessionID)
		case models.ActionProceed:
			// Handled below.
		}

		// The hold is released: the held amount is set to zero. The decline
		// reason is KEPT; why a canceled session was declined must still be
		// readable for diagnosis.
		_, err = p.store.UpdateManualSessionState(ctx, ses.ID,
			models.SessionCanceled, 0, ses.CapturedAmount, ses.RefundedAmount, ses.DeclineReason)
		return err
	})
}

// GetSession returns the session in the provider's ledger; errors.NotFound if
// there is none.
//
// It is NOT part of the core contract and the payment service does NOT CALL
// it. It exists only for integration tests and diagnosis: a session's state on
// the provider's side has to be verified without looking at the module's own
// record — a fault in which the two ledgers diverge can only be seen that
// way.
func (p *Provider) GetSession(ctx context.Context, sessionID string) (models.ManualSession, error) {
	if strings.TrimSpace(sessionID) == "" {
		return models.ManualSession{}, errors.Invalid(CodeInvalidInput, "the session identifier is required")
	}
	return p.store.ManualSession(ctx, sessionID)
}

// authorizeDecision is the result of the authorization decision.
type authorizeDecision struct {
	// Status is the session's new status.
	Status models.SessionStatus
	// AuthorizedAmount is the amount to hold.
	AuthorizedAmount int64
	// DeclineReason is set only while Status is [models.SessionFailed].
	DeclineReason string
}

// sessionData holds the Data fields that steer the provider's behavior.
//
// Unrecognized fields are IGNORED: Data is free-form data the caller passes to
// the provider (a card token, a return address), and a field the provider does
// not understand is not an error.
//
// AuthorizedAmount is a POINTER: the distinction between an authorization of a
// zero amount and "the field was never given" has to be preserved. Had a value
// type been used, a call that never sends the field would count as having held
// a zero amount.
type sessionData struct {
	Outcome          string `json:"manual_outcome"`
	DeclineReason    string `json:"manual_decline_reason"`
	AuthorizedAmount *int64 `json:"manual_authorized_amount"`
}

// parseSessionData decodes the behavior keys in the session data.
func parseSessionData(raw []byte) (sessionData, error) {
	var out sessionData
	if len(raw) == 0 || string(raw) == "null" {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return sessionData{}, errors.Wrap(err, errors.KindInvalid, CodeDataInvalid,
			"the session data could not be decoded")
	}

	switch out.Outcome {
	case "", OutcomeAuthorize, OutcomeDecline, OutcomeError:
		return out, nil
	default:
		return sessionData{}, errors.Invalid(CodeInvalidInput,
			"%q is not a recognized %s value; it has to be %q, %q or %q",
			out.Outcome, DataKeyOutcome, OutcomeAuthorize, OutcomeDecline, OutcomeError)
	}
}

// decideAuthorize decides the authorization outcome of a pending session.
//
// It is a pure decision: it does not touch the database and looks only at the
// session itself and its stored behavior keys. Keeping it apart is deliberate
// — every injected failure branch can be exercised one by one without a
// database.
func decideAuthorize(ses models.ManualSession) (authorizeDecision, error) {
	data, err := parseSessionData(ses.Data)
	if err != nil {
		return authorizeDecision{}, err
	}

	switch data.Outcome {
	case OutcomeError:
		return authorizeDecision{}, errors.Unavailable(CodeSimulatedFailure,
			"the manual provider could not be reached (an error injected for a test): %s", ses.ID)
	case OutcomeDecline:
		reason := strings.TrimSpace(data.DeclineReason)
		if reason == "" {
			reason = declineReasonDefault
		}
		return authorizeDecision{Status: models.SessionFailed, DeclineReason: reason}, nil
	}

	authorized := ses.Amount
	if data.AuthorizedAmount != nil {
		authorized = *data.AuthorizedAmount
		if authorized <= 0 || authorized > ses.Amount {
			return authorizeDecision{}, errors.Invalid(CodeInvalidInput,
				"%s has to be between 1 and %d: %d", DataKeyAuthorizedAmount, ses.Amount, authorized)
		}
	}
	return authorizeDecision{Status: models.SessionAuthorized, AuthorizedAmount: authorized}, nil
}

// toProviderSession converts a ledger record into the core contract's session
// type.
func toProviderSession(ses models.ManualSession) coreprovider.Session {
	return coreprovider.Session{
		ID:           ses.ID,
		Status:       coreprovider.SessionStatus(ses.Status),
		Amount:       ses.Amount,
		CurrencyCode: ses.CurrencyCode,
		Data:         ses.Data,
	}
}

// InspectSession returns the session in the provider's own ledger in the
// CORE's neutral form.
//
// What sets it apart from [GetSession] is the type: that one gives the
// module's own ManualSession and is useful only to this module's tests; this
// one satisfies [coreprovider.SessionInspector], so the reconciliation job can
// ask without knowing which provider it is.
//
// The manual provider's ledger is a SEPARATE table and the payment service's
// [Store] interface has no method that would reach that table — so the module
// cannot see the provider's ledger at the type level. That is why the two
// ledgers reconciliation compares are really separate; had it read the same
// row twice, it could see no divergence.
func (p *Provider) InspectSession(
	ctx context.Context, sessionID string,
) (coreprovider.SessionInspection, error) {
	ses, err := p.GetSession(ctx, sessionID)
	if err != nil {
		return coreprovider.SessionInspection{}, err
	}

	return coreprovider.SessionInspection{
		Status:           coreprovider.SessionStatus(ses.Status),
		AuthorizedAmount: ses.AuthorizedAmount,
		CapturedAmount:   ses.CapturedAmount,
		RefundedAmount:   ses.RefundedAmount,
	}, nil
}
