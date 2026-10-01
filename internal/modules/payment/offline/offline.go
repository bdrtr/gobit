// Package offline is the payment provider of money that arrives outside gobit:
// a bank transfer, cash on delivery (ADR 0284).
//
// Nothing is held when such a payment is authorized and nothing is drawn when it
// is captured. The authorization is the customer's promise to pay what the
// session asks; the capture is an operator recording that the money arrived,
// through the admin's session capture. Neither act has a counterpart anywhere
// this provider could ask, so it keeps no ledger of its own: the payment
// module's session is the only record, and every amount the module hands it was
// checked against that record before the call.
//
// # One provider per method
//
// An installation names its methods — "bank_transfer", "cash_on_delivery" —
// and each is registered as a provider of its own under that name, so a shopper
// chooses among them and an order records which one it was promised with. They
// differ in nothing else.
//
// # The checkout does not capture it
//
// [Provider.CapturesLater] says the money comes later. The checkout places the
// order owing the provider's part, and the reconciliation leaves its sessions
// out: a session that stays authorized for days is this provider working, not
// a capture in flight.
package offline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
)

// Error codes. Clients may branch on them; messages may change, codes do not.
const (
	// CodeInvalidMethod reports a method name the provider cannot be
	// registered under.
	CodeInvalidMethod = "payment_offline_invalid_method"
	// CodeInvalidInput reports a session opened without an idempotency key.
	CodeInvalidInput = "payment_offline_invalid_input"
	// CodeUnknownSession reports a session id this provider never gave.
	CodeUnknownSession = "payment_offline_unknown_session"
)

// maxMethodLen bounds a method name; it is typed into configuration and
// prefixes every session id of the method.
const maxMethodLen = 40

// sessionDigestLen is the length of the hex digest after a session id's prefix.
const sessionDigestLen = 32

// Provider is one offline payment method.
type Provider struct {
	method string
}

// New builds the provider of one method. The name is lower-case ASCII letters,
// digits and underscores, starting with a letter: it is what an operator types
// into PAYMENT_OFFLINE_METHODS and what a shopper's client sends as the
// payment provider.
func New(method string) (*Provider, error) {
	if !validMethod(method) {
		return nil, errors.Invalid(CodeInvalidMethod,
			"an offline payment method is named with lower-case letters, digits and underscores, "+
				"starting with a letter and at most %d long: %q", maxMethodLen, method)
	}

	return &Provider{method: method}, nil
}

// validMethod reports whether a name can be a method.
func validMethod(method string) bool {
	if method == "" || len(method) > maxMethodLen || method[0] < 'a' || method[0] > 'z' {
		return false
	}
	for _, r := range method {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}

	return true
}

// ID returns the method's name.
func (p *Provider) ID() string { return p.method }

// CapturesLater reports that the money of this provider's sessions arrives
// after the order is placed (ADR 0284).
func (p *Provider) CapturesLater() bool { return true }

// CreateSession opens a session for the amount asked. The session's id is
// derived from the method and the idempotency key, so a second call with the
// same key returns the same session, as the core contract asks, with nothing
// stored.
func (p *Provider) CreateSession(
	_ context.Context, in coreprovider.CreateSessionInput,
) (coreprovider.Session, error) {
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return coreprovider.Session{}, errors.Invalid(CodeInvalidInput,
			"an offline session is opened with an idempotency key; its id is derived from it")
	}
	digest := sha256.Sum256([]byte(p.method + "\x00" + in.IdempotencyKey))

	return coreprovider.Session{
		ID:           p.sessionPrefix() + hex.EncodeToString(digest[:sessionDigestLen/2]),
		Status:       coreprovider.SessionPending,
		Amount:       in.Amount,
		CurrencyCode: in.CurrencyCode,
	}, nil
}

// Authorize records the customer's promise. It reports no amount, which the
// payment module reads as the session's own amount: the promise covers what
// the session asks, and nothing is held anywhere.
func (p *Provider) Authorize(_ context.Context, sessionID string) (coreprovider.AuthResult, error) {
	if err := p.owns(sessionID); err != nil {
		return coreprovider.AuthResult{}, err
	}

	return coreprovider.AuthResult{Status: coreprovider.SessionAuthorized}, nil
}

// Capture is the operator recording that the money arrived; the payment module
// has checked the amount against the session before the call.
func (p *Provider) Capture(_ context.Context, sessionID string, _ int64) error {
	return p.owns(sessionID)
}

// Refund is the operator recording that money was paid back, by the same hand
// that received it.
func (p *Provider) Refund(_ context.Context, sessionID string, _ int64) error {
	return p.owns(sessionID)
}

// Cancel withdraws the promise; nothing was held, so nothing is released.
func (p *Provider) Cancel(_ context.Context, sessionID string) error {
	return p.owns(sessionID)
}

// sessionPrefix is what every session id of this method starts with.
func (p *Provider) sessionPrefix() string { return p.method + "_" }

// owns refuses a session id this method never gave: its name, an underscore
// and the digest's hex, and nothing else, so a method whose name begins another
// method's name does not own that method's sessions.
func (p *Provider) owns(sessionID string) error {
	digest, found := strings.CutPrefix(sessionID, p.sessionPrefix())
	if found && len(digest) == sessionDigestLen {
		if _, err := hex.DecodeString(digest); err == nil {
			return nil
		}
	}

	return errors.NotFound(CodeUnknownSession,
		"the offline method %q gave no session %q", p.method, sessionID)
}
