// Package giftcard is the payment provider that spends a gift card (ADR 0208).
//
// The state machine is [balancetender.Machine], the one store credit and
// loyalty points run. What this package owns is the identity, the error codes,
// the adapter onto the card's ledger and sessions, and the one thing the other
// two tenders do not have: the owner is found from what the client sends. A
// card belongs to whoever presents its code, so the code in the payment's data
// is the credential, and the collection's customer plays no part.
//
// Every row this tender writes is written from THIS package, which is how the
// ledger's arch gate admits it as the ledger's second door.
package giftcard

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/balancetender"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// ID is the provider's identity; a storefront asks for it in the completion
// body's payment_provider_id and sends the code in payment_data.
const ID = models.GiftCardTenderID

// DataCode is the key of the code in a payment's data.
const DataCode = "code"

// The provider's error codes; what the machine's four mean is written on
// [balancetender.Codes].
const (
	// CodeInvalidInput reports a call this provider cannot make sense of.
	CodeInvalidInput = "payment_gift_card_invalid_input"
	// CodeUnknown reports a code no card has, or no code at all. The two are one
	// answer on purpose: telling a malformed code from an unissued one helps
	// nobody but a guesser.
	CodeUnknown = "payment_gift_card_unknown"
	// CodeCurrency reports a card in another currency than the payment's.
	CodeCurrency = "payment_gift_card_currency"
	// CodeInsufficient reports a balance that does not cover the session; it
	// travels as a declined authorization, not as an error.
	CodeInsufficient = "payment_gift_card_insufficient"
	// CodeInvalidState reports a transition the session's status does not allow.
	CodeInvalidState = "payment_gift_card_invalid_state"
	// CodeDisabled reports a card an operator closed (ADR 0213): it opens no
	// payment and takes no refund.
	CodeDisabled = "payment_gift_card_disabled"
	// CodeExpired reports a card whose moment has come (ADR 0214), answered
	// the same way from that moment, before the expiry job closes it.
	CodeExpired = "payment_gift_card_expired"
)

// Store is the persistence this provider needs, declared here and narrow.
type Store interface {
	// WithTx runs fn in a single transaction.
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error

	// GiftCardByDigest returns the card a code's digest opens, or NotFound.
	GiftCardByDigest(ctx context.Context, digest string) (models.GiftCard, error)
	// GiftCard returns a card by its id, or NotFound.
	GiftCard(ctx context.Context, id string) (models.GiftCard, error)

	// InsertGiftCardSessionIfAbsent writes the session only if the idempotency
	// key is unused; the second return says whether the row was written.
	InsertGiftCardSessionIfAbsent(ctx context.Context, session models.TenderSession) (models.TenderSession, bool, error)
	// GiftCardSessionByIdempotencyKey returns the session by its key.
	GiftCardSessionByIdempotencyKey(ctx context.Context, key string) (models.TenderSession, error)
	// GiftCardSession returns the session by its id.
	GiftCardSession(ctx context.Context, id string) (models.TenderSession, error)
	// LockGiftCardSession locks the session for the transaction.
	LockGiftCardSession(ctx context.Context, id string) (models.TenderSession, error)
	// UpdateGiftCardSessionState writes the status and amounts as ABSOLUTE values.
	UpdateGiftCardSessionState(
		ctx context.Context,
		id string,
		status models.SessionStatus,
		authorized, captured, refunded int64,
		declineReason string,
	) (models.TenderSession, error)

	// AppendGiftCardEntry appends one event to a card's ledger.
	AppendGiftCardEntry(ctx context.Context, entry models.GiftCardEntry) (models.GiftCardEntry, error)
	// GiftCardBalance sums a card's entries.
	GiftCardBalance(ctx context.Context, cardID string) (int64, error)
	// LockGiftCardBalance locks a card's balance for the transaction.
	LockGiftCardBalance(ctx context.Context, cardID, currencyCode string) error
}

// Provider spends gift cards. It is safe for concurrent use.
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
		Unit: "gift card",
		Codes: balancetender.Codes{
			InvalidInput: CodeInvalidInput,
			// No session of this tender is ever opened without an owner: the
			// owner is the card the code opens, or the session is refused.
			NoCustomer:   CodeUnknown,
			Insufficient: CodeInsufficient,
			InvalidState: CodeInvalidState,
		},
		NewSessionID: models.NewGiftCardSessionID,
		// A card pays what it holds and leaves the rest to another tender
		// (ADR 0209); an empty card declines.
		Partial: true,
		Owner:   cardOf(store),
	}, log)}
}

// ID returns the provider's identity.
func (p *Provider) ID() string { return ID }

// cardOf resolves the card a payment's code opens.
//
// The code is the client's to send, and that is the difference from store
// credit, whose owner may never come from the client (ADR 0152): a gift card
// is a bearer instrument, and presenting its code is what owning it means.
func cardOf(store Store) func(ctx context.Context, in coreprovider.CreateSessionInput) (string, error) {
	return func(ctx context.Context, in coreprovider.CreateSessionInput) (string, error) {
		typed, _ := in.Data[DataCode].(string)
		code, ok := models.NormalizeGiftCardCode(typed)
		if !ok {
			return "", unknown()
		}
		card, err := store.GiftCardByDigest(ctx, models.GiftCardCodeDigest(code))
		if errors.IsNotFound(err) {
			return "", unknown()
		}
		if err != nil {
			return "", err
		}
		if err := unusable(card); err != nil {
			return "", err
		}
		if !strings.EqualFold(card.CurrencyCode, strings.TrimSpace(in.CurrencyCode)) {
			return "", errors.Conflict(CodeCurrency,
				"the gift card holds %s and this payment is in %s", card.CurrencyCode, in.CurrencyCode)
		}

		return card.ID, nil
	}
}

// unknown is the one answer to a code that opens no card.
func unknown() error {
	return errors.Invalid(CodeUnknown, "no gift card has this code")
}

// unusable is the answer about a card that pays nothing: one an operator
// closed, or one whose moment has come and the expiry job has not closed yet.
// It is told apart from an unknown code: whoever sends it holds a real card's
// code.
func unusable(card models.GiftCard) error {
	if card.DisabledAt != nil {
		return errors.Conflict(CodeDisabled, "gift card %s is closed", card.ID)
	}
	if card.ExpiredAt(time.Now()) {
		return errors.Conflict(CodeExpired, "gift card %s expired at %s",
			card.ID, card.ExpiresAt.UTC().Format(time.RFC3339))
	}

	return nil
}

// ledger adapts [Store] to the machine's surface.
type ledger struct {
	store Store
}

// movementKinds maps the machine's movements to the card ledger's vocabulary.
var movementKinds = map[balancetender.Movement]models.GiftCardKind{
	balancetender.Hold:    models.GiftCardHold,
	balancetender.Release: models.GiftCardRelease,
	balancetender.Refund:  models.GiftCardRefund,
}

func (l ledger) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return l.store.WithTx(ctx, fn)
}

func (l ledger) InsertSessionIfAbsent(
	ctx context.Context, session models.TenderSession,
) (models.TenderSession, bool, error) {
	return l.store.InsertGiftCardSessionIfAbsent(ctx, session)
}

func (l ledger) SessionByIdempotencyKey(ctx context.Context, key string) (models.TenderSession, error) {
	return l.store.GiftCardSessionByIdempotencyKey(ctx, key)
}

func (l ledger) Session(ctx context.Context, id string) (models.TenderSession, error) {
	return l.store.GiftCardSession(ctx, id)
}

func (l ledger) LockSession(ctx context.Context, id string) (models.TenderSession, error) {
	return l.store.LockGiftCardSession(ctx, id)
}

func (l ledger) UpdateSessionState(
	ctx context.Context,
	id string,
	status models.SessionStatus,
	authorized, captured, refunded int64,
	declineReason string,
) (models.TenderSession, error) {
	return l.store.UpdateGiftCardSessionState(ctx, id, status, authorized, captured, refunded, declineReason)
}

// Move writes one movement as a card ledger row, referenced by the session.
//
// A refund onto a closed card is refused (ADR 0213): the card holds nothing and
// pays nothing, so the money would reach nobody. An expired card is refused for
// the same reason, since the expiry job closes it (ADR 0214). The card's lock is taken first,
// the one a close takes, so a refund and a close cannot pass each other.
func (l ledger) Move(ctx context.Context, entry balancetender.Entry) error {
	if entry.Movement == balancetender.Refund {
		if err := l.store.LockGiftCardBalance(ctx, entry.OwnerID, entry.CurrencyCode); err != nil {
			return err
		}
		card, err := l.store.GiftCard(ctx, entry.OwnerID)
		if err != nil {
			return err
		}
		if err := unusable(card); err != nil {
			return err
		}
	}
	_, err := l.store.AppendGiftCardEntry(ctx, models.GiftCardEntry{
		ID:         models.NewGiftCardEntryID(),
		GiftCardID: entry.OwnerID,
		Amount:     entry.Amount,
		Kind:       movementKinds[entry.Movement],
		Reference:  entry.SessionID,
	})

	return err
}

// Balance sums the card's entries. The currency is the card's own: a session
// is opened only in it.
func (l ledger) Balance(ctx context.Context, cardID, _ string) (int64, error) {
	return l.store.GiftCardBalance(ctx, cardID)
}

func (l ledger) LockBalance(ctx context.Context, cardID, currencyCode string) error {
	return l.store.LockGiftCardBalance(ctx, cardID, currencyCode)
}
