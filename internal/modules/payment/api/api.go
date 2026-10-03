// Package api is the payment module's HTTP surface.
//
// There are two surfaces and their authority differs:
//
//   - /admin/v1 — manages ALL stages of the payment: opening a collection,
//     opening a session, authorization, capture, cancellation and refund.
//   - /store/v1 — the LEAST surface the customer's payment flow needs: reading
//     the collection and opening a session at a provider. Authorization and
//     capture are NOT TRIGGERED by the store; the order completion workflow
//     runs them (plan Phase 6). A customer being able to trigger a capture from
//     their own browser would mean money being taken from a cart whose order
//     never came into being.
//
// The same reasoning also narrows the BODY of the session-opening endpoint on
// the store surface: the amount is not taken from the client (it is always the
// whole of the collection's remainder) and the provider's behavior keys are
// rejected. An endpoint that cannot trigger the capture but can write the
// payment's amount or outcome would open the same gate from the back.
//
// # Scopes
//
// The admin endpoints REQUIRE a scope, and the scope is enforced endpoint by
// endpoint (see [Handler.Routes]):
//
//   - [ScopeRead] ("payment:read") — opens ALL the GET endpoints this module
//     binds under /admin/v1.
//   - [ScopeWrite] ("payment:write") — opens ALL the POST endpoints this module
//     binds under /admin/v1.
//
// Both name the SURFACE; they do not count the resources. There was a sentence
// that counted them and it was stale: ADR 0152 bound three store credit
// endpoints, none of them entered any list and no gate said so; ADR 0164 added
// two more points endpoints. A prose list silently becomes wrong the moment its
// population grows (D111, and D102 made the same repair for a target list).
//
// corehttp.ScopeAdmin ("admin") is a SUPERSCOPE; it satisfies both on its own
// (see corehttp.Principal.HasScope).
//
// The store endpoints get NO scope: the identity of /store/v1 is the
// publishable key, and that key by definition carries no scope. What keeps the
// store surface narrow is not a scope but the surface ITSELF — for the reason
// given above, there is no capture endpoint there at all.
//
// Handlers do NOT CHOOSE the status code: the service returns a core/errors
// typed error, and corehttp.WriteError writes the code that fits its kind (plan
// Section 8).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// Route paths. Module routes are registered with the FULL PATH; a prefix such
// as "/admin/v1" is NOT MOUNTED, because the first module to mount it would own
// that whole subtree and collide with the other modules that use the same
// prefix.
const (
	pathAdminProviders = "/admin/v1/payment-providers"
	// pathAdminStoreCredits and pathAdminStoreCreditBalance are the two admin
	// endpoints of store credit (ADR 0152).
	//
	// The balance is a SEPARATE address, not a field added to the list: the
	// list envelope is {data, count, offset, limit}, and putting a fifth field
	// into it would make every client that reads that envelope write a branch
	// special to this endpoint. Two questions, two addresses.
	//
	// The reason for the suppression on the two lines below: G101 takes them
	// for a secret because it sees "cred" in the name. Both are ROUTE ADDRESSES
	// — text that goes into the document and that the client writes — and the
	// word the name carries is "credit", not "credential".
	pathAdminStoreCredits       = "/admin/v1/store-credits"         //nolint:gosec // G101: a route address, not a secret
	pathAdminStoreCreditBalance = "/admin/v1/store-credits/balance" //nolint:gosec // G101: a route address, not a secret

	// pathAdminLoyaltyPoints and pathAdminLoyaltyPointsBalance are the two admin
	// endpoints of loyalty points (ADR 0164).
	//
	// Store credit's shape: the balance is a SEPARATE address, not a field added
	// to the list. Two questions, two addresses. There is NO write endpoint —
	// the only thing that writes points is the money itself, and an operator
	// cannot move them by hand.
	pathAdminLoyaltyPoints        = "/admin/v1/loyalty-points"
	pathAdminLoyaltyPointsBalance = "/admin/v1/loyalty-points/balance"

	pathAdminCollections      = "/admin/v1/payment-collections"
	pathAdminCollection       = "/admin/v1/payment-collections/{id}"
	pathAdminCollectionSess   = "/admin/v1/payment-collections/{id}/payment-sessions"
	pathAdminCollectionPays   = "/admin/v1/payment-collections/{id}/payments"
	pathAdminSessionAuthorize = "/admin/v1/payment-sessions/{id}/authorize"
	pathAdminSessionCapture   = "/admin/v1/payment-sessions/{id}/capture"
	pathAdminSessionCancel    = "/admin/v1/payment-sessions/{id}/cancel"
	pathAdminSession          = "/admin/v1/payment-sessions/{id}"
	pathAdminPayment          = "/admin/v1/payments/{id}"
	pathAdminPaymentRefund    = "/admin/v1/payments/{id}/refunds"

	pathStoreProviders   = "/store/v1/payment-providers"
	pathStoreCollection  = "/store/v1/payment-collections/{id}"
	pathStoreCollectSess = "/store/v1/payment-collections/{id}/payment-sessions"
	// pathStoreSessionCancel is the customer releasing a session they opened
	// THEMSELVES.
	//
	// The reservation is held at the collection level (an open session covers
	// the collection's remaining amount) — that is what prevents a double
	// capture. Without a way to release it, a customer who chose "credit card"
	// on the storefront and then wanted to switch to "bank transfer" would stay
	// locked until an ADMINISTRATOR canceled the session by hand.
	pathStoreSessionCancel = "/store/v1/payment-sessions/{id}/cancel"
)

// maxBodyBytes is the upper bound for the request body. Without a bound a
// single request could exhaust the server's memory.
const maxBodyBytes int64 = 1 << 20 // 1 MiB

// codeInvalidRequest is the error code returned when the body or a parameter
// cannot be parsed.
const codeInvalidRequest = "payment_invalid_request"

// Payments is the surface the handlers need from the service.
//
// Keeping it narrow simplifies the tests: the HTTP behavior can be verified
// without a real database, with a fake of a few lines.
type Payments interface {
	// ProviderIDs returns the IDs of the registered providers.
	ProviderIDs(ctx context.Context) []string

	// IssueCredit issues store credit to a customer (ADR 0152).
	IssueCredit(ctx context.Context, in service.IssueCreditInput) (models.StoreCreditEntry, error)
	// StoreCreditBalance returns the customer's balance in a single currency.
	StoreCreditBalance(ctx context.Context, customerID, currencyCode string) (int64, error)
	// ListStoreCredit pages the customer's credit history.
	ListStoreCredit(
		ctx context.Context, in service.ListStoreCreditInput,
	) ([]models.StoreCreditEntry, int64, error)

	// LoyaltyBalance returns the customer's points in a single currency.
	LoyaltyBalance(ctx context.Context, customerID, currencyCode string) (int64, error)

	// Journal derives the module's books over a window (ADR 0186).
	Journal(ctx context.Context, q service.JournalQuery) (service.Journal, error)

	// IssueGiftCard issues a gift card and returns its code once (ADR 0208).
	IssueGiftCard(ctx context.Context, in service.IssueGiftCardInput) (service.IssuedGiftCard, error)
	// GetGiftCard returns a card and its balance.
	GetGiftCard(ctx context.Context, id string) (service.GiftCardWithBalance, error)
	// ListGiftCards pages the cards with their balances.
	ListGiftCards(ctx context.Context, page service.Page) ([]service.GiftCardWithBalance, int64, error)
	// ListGiftCardEntries pages a card's history.
	ListGiftCardEntries(ctx context.Context, id string, page service.Page) ([]models.GiftCardEntry, int64, error)
	// ReplaceGiftCardCode gives a card a new code and returns it once (ADR 0210).
	ReplaceGiftCardCode(ctx context.Context, id string) (service.IssuedGiftCard, error)
	// DisableGiftCard closes a card and voids what it held (ADR 0213).
	DisableGiftCard(ctx context.Context, id, reason string) (service.GiftCardWithBalance, error)
	// ListLoyalty pages the customer's points history.
	ListLoyalty(
		ctx context.Context, in service.ListLoyaltyInput,
	) ([]models.LoyaltyEntry, int64, error)

	// CreatePaymentCollection creates a new payment collection.
	CreatePaymentCollection(ctx context.Context, in service.CreateCollectionInput) (models.PaymentCollection, error)
	// GetPaymentCollection returns the collection by its ID.
	GetPaymentCollection(ctx context.Context, id string) (models.PaymentCollection, error)
	// ListPaymentCollections pages the collections.
	ListPaymentCollections(ctx context.Context, in service.ListCollectionsInput) ([]models.PaymentCollection, int64, error)

	// CreateSession opens a payment session at a provider.
	CreateSession(ctx context.Context, collectionID, providerID string, in service.CreateSessionInput) (models.PaymentSession, error)
	// GetPaymentSession returns the session by its ID.
	GetPaymentSession(ctx context.Context, id string) (models.PaymentSession, error)
	// ListPaymentSessions returns the collection's sessions.
	ListPaymentSessions(ctx context.Context, collectionID string) ([]models.PaymentSession, error)
	// AuthorizePayment authorizes the session.
	AuthorizePayment(ctx context.Context, sessionID string) (models.PaymentSession, error)
	// CapturePayment captures the held amount.
	CapturePayment(ctx context.Context, sessionID string, amount int64) (models.Payment, error)
	// CancelPayment cancels the session (saga compensation).
	CancelPayment(ctx context.Context, sessionID string) error

	// GetPayment returns the capture by its ID.
	GetPayment(ctx context.Context, id string) (models.Payment, error)
	// ListPayments returns the collection's captures.
	ListPayments(ctx context.Context, collectionID string) ([]models.Payment, error)
	// RefundPayment refunds the capture.
	RefundPayment(ctx context.Context, paymentID string, amount int64, reason string) (models.Refund, error)
	// ListRefunds returns the capture's refunds.
	ListRefunds(ctx context.Context, paymentID string) ([]models.Refund, error)
}

// Handler is the payment module's set of HTTP handlers.
type Handler struct {
	svc Payments
	// identity proves the customer a storefront balance read names; nil
	// refuses every such read (ADR 0253).
	identity corehttp.Identity
}

// New produces the set of handlers working on the given service.
func New(svc Payments) *Handler { return &Handler{svc: svc} }

// The scope vocabulary: the scopes payment's admin endpoints ask for.
//
// The split is on READ/WRITE, not on the resource. Per-resource scopes such as
// "payment_refunds:write" grow the list but produce no new decision that can
// be made today: there is no need yet for an identity that can refund but
// cannot capture, and a scope name defined for a need that does not exist is a
// name whose purpose nobody knows on the day it is first granted.
const (
	// ScopeRead is the scope the READ endpoints on payment's admin surface ask
	// for.
	//
	// It opens every GET endpoint of the module under /admin/v1 and opens no
	// endpoint that gives rise to a money movement. It does not count what it
	// reads one by one, because a sentence that counts falls behind a growing
	// population (D111): what can be read is what [Handler.Routes] says, and
	// among what can be read are the customer's store credit and loyalty points
	// too. Fully authorized identities need not be granted it separately: a
	// caller carrying corehttp.ScopeAdmin satisfies this one too
	// (see corehttp.Principal.HasScope).
	ScopeRead = "payment:read"

	// ScopeWrite is the scope the WRITE endpoints on payment's admin surface
	// ask for.
	//
	// In this module, writing means a MONEY MOVEMENT: a capture takes from the
	// customer's card, a refund takes out of the till, a cancellation releases
	// the held amount, issuing credit creates money the customer can spend.
	// That is why it is separate from the read scope — an identity granted for
	// reporting must not reach the till.
	//
	// The sentence's scope also holds a DECISION: loyalty points have NO write
	// endpoint, because the only thing that moves points is the money itself,
	// and a manual correction would make this sentence wrong (ADR 0164).
	ScopeWrite = "payment:write"
)

// Routes binds the module's admin and store routes to the router.
//
// # PROTECTION
//
// The admin endpoints are protected by two layers, and both are needed:
//
//  1. IDENTITY — corehttp.RequireAdmin is mounted on the side that builds the
//     router (see corehttp.APIGuards); it is not this module's job.
//  2. SCOPE — the endpoints are marked HERE, endpoint by endpoint, with
//     corehttp.RequireScope.
//
// Without the second layer authentication would stand in for authorization:
// an admin user whose scopes were left EMPTY could log in and refund a
// capture. Identity answers the question "who", not "what may they do".
//
// The store endpoints STAY THE SAME: the identity there is the publishable key
// and it carries no scope. The handlers the two surfaces SHARE (listProviders,
// getCollection) ask for a scope only on the admin path; the scope is attached
// to the route, not to the handler.
func (h *Handler) Routes(r chi.Router) {
	read := r.With(corehttp.RequireScope(ScopeRead))
	write := r.With(corehttp.RequireScope(ScopeWrite))

	read.Get(pathAdminProviders, h.listProviders)

	// Store credit: issuing is a WRITE, the balance and the history a READ. The
	// action that issues credit creates money the customer can spend, so it is
	// under the payment write scope (ADR 0152).
	write.Post(pathAdminStoreCredits, h.issueStoreCredit)
	read.Get(pathAdminStoreCredits, h.listStoreCredit)
	read.Get(pathAdminStoreCreditBalance, h.storeCreditBalance)

	// Loyalty points: READ only. The action that writes points is the capture
	// itself, that is, something already under the write scope in this module;
	// a separate write endpoint would open the operator a way to move points
	// that has nothing to do with money, and would make ScopeWrite's sentence
	// "writing means a MONEY MOVEMENT" wrong (ADR 0164).
	read.Get(pathAdminLoyaltyPoints, h.listLoyaltyPoints)
	read.Get(pathAdminLoyaltyPointsBalance, h.loyaltyPointBalance)

	read.Get(pathAdminPaymentJournal, h.paymentJournal)

	// Gift cards (ADR 0208): issuing one creates money a holder can spend, so it
	// is a write under the payment scope, as store credit's issue is.
	write.Post(pathAdminGiftCards, h.issueGiftCard)
	read.Get(pathAdminGiftCards, h.listGiftCards)
	read.Get(pathAdminGiftCard, h.getGiftCard)
	read.Get(pathAdminGiftCardEntries, h.listGiftCardEntries)
	write.Post(pathAdminGiftCardCode, h.replaceGiftCardCode)
	write.Post(pathAdminGiftCardDisable, h.disableGiftCard)

	write.Post(pathAdminCollections, h.createCollection)
	read.Get(pathAdminCollections, h.listCollections)
	read.Get(pathAdminCollection, h.getCollection)
	read.Get(pathAdminCollectionSess, h.listSessions)
	write.Post(pathAdminCollectionSess, h.createSession)
	read.Get(pathAdminCollectionPays, h.listPayments)

	read.Get(pathAdminSession, h.getSession)
	write.Post(pathAdminSessionAuthorize, h.authorizeSession)
	write.Post(pathAdminSessionCapture, h.captureSession)
	write.Post(pathAdminSessionCancel, h.cancelSession)

	read.Get(pathAdminPayment, h.getPayment)
	read.Get(pathAdminPaymentRefund, h.listRefunds)
	write.Post(pathAdminPaymentRefund, h.refundPayment)

	r.Get(pathStoreProviders, h.listProviders)
	r.Get(pathStoreCollection, h.getCollection)
	// Opening a session exists on both surfaces but it is NOT the same handler:
	// the store endpoint does not leave the amount and the provider behavior to
	// the client.
	r.Post(pathStoreCollectSess, h.createStoreSession)
	r.Post(pathStoreSessionCancel, h.cancelStoreSession)

	// A customer reads their own balances, and only their own (ADR 0253).
	r.Get(pathStoreOwnStoreCredit, h.ownStoreCreditBalance)
	r.Get(pathStoreOwnLoyalty, h.ownLoyaltyBalance)
}

// --- envelopes and DTOs -------------------------------------------------------

// singleEnvelope is the envelope of single-record responses (plan Section 8).
type singleEnvelope struct {
	// Data is the body of the response.
	Data any `json:"data"`
}

// listEnvelope is the envelope of list responses (plan Section 8).
type listEnvelope struct {
	// Data is the records on the page.
	Data any `json:"data"`
	// Count is the number of ALL records matching the filter; not the number of
	// rows on the page.
	Count int64 `json:"count"`
	// Offset is the number of records skipped.
	Offset int64 `json:"offset"`
	// Limit is the requested page size.
	Limit int64 `json:"limit"`
}

// collectionDTO is the external representation of a payment collection.
type collectionDTO struct {
	ID               string         `json:"id"`
	Reference        string         `json:"reference"`
	Amount           int64          `json:"amount"`
	CurrencyCode     string         `json:"currency_code"`
	Status           string         `json:"status"`
	AuthorizedAmount int64          `json:"authorized_amount"`
	CapturedAmount   int64          `json:"captured_amount"`
	RefundedAmount   int64          `json:"refunded_amount"`
	Metadata         map[string]any `json:"metadata,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// sessionDTO is the external representation of a payment session.
//
// DeclineReason is filled only in the declined state and is for DIAGNOSIS; it
// is not a text to show the customer. That it is visible on the store surface
// too is deliberate: hiding the field would mean the developer writing the
// integration could never see the reason for the decline.
type sessionDTO struct {
	ID                  string          `json:"id"`
	PaymentCollectionID string          `json:"payment_collection_id"`
	ProviderID          string          `json:"provider_id"`
	ExternalID          string          `json:"external_id"`
	Status              string          `json:"status"`
	Amount              int64           `json:"amount"`
	AuthorizedAmount    int64           `json:"authorized_amount"`
	CurrencyCode        string          `json:"currency_code"`
	Data                json.RawMessage `json:"data,omitempty"`
	DeclineReason       string          `json:"decline_reason,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

// paymentDTO is the external representation of a capture.
type paymentDTO struct {
	ID                  string    `json:"id"`
	PaymentSessionID    string    `json:"payment_session_id"`
	PaymentCollectionID string    `json:"payment_collection_id"`
	Amount              int64     `json:"amount"`
	CurrencyCode        string    `json:"currency_code"`
	RefundedAmount      int64     `json:"refunded_amount"`
	CapturedAt          time.Time `json:"captured_at"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// refundDTO is the external representation of a refund.
type refundDTO struct {
	ID        string `json:"id"`
	PaymentID string `json:"payment_id"`
	Amount    int64  `json:"amount"`
	Reason    string `json:"reason,omitempty"`
	// Reference names the record that caused the refund — a return, a claim,
	// an exchange — and is absent for one an operator made here (ADR 0187).
	Reference string    `json:"reference,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// toCollectionDTO converts the model into the external representation.
func toCollectionDTO(col models.PaymentCollection) collectionDTO {
	return collectionDTO{
		ID:               col.ID,
		Reference:        col.Reference,
		Amount:           col.Amount,
		CurrencyCode:     col.CurrencyCode,
		Status:           col.Status.String(),
		AuthorizedAmount: col.AuthorizedAmount,
		CapturedAmount:   col.CapturedAmount,
		RefundedAmount:   col.RefundedAmount,
		Metadata:         col.Metadata,
		CreatedAt:        col.CreatedAt,
		UpdatedAt:        col.UpdatedAt,
	}
}

// toSessionDTO converts the model into the external representation.
func toSessionDTO(ses models.PaymentSession) sessionDTO {
	return sessionDTO{
		ID:                  ses.ID,
		PaymentCollectionID: ses.PaymentCollectionID,
		ProviderID:          ses.ProviderID,
		ExternalID:          ses.ExternalID,
		Status:              ses.Status.String(),
		Amount:              ses.Amount,
		AuthorizedAmount:    ses.AuthorizedAmount,
		CurrencyCode:        ses.CurrencyCode,
		Data:                ses.Data,
		DeclineReason:       ses.DeclineReason,
		CreatedAt:           ses.CreatedAt,
		UpdatedAt:           ses.UpdatedAt,
	}
}

// toPaymentDTO converts the model into the external representation.
func toPaymentDTO(pay models.Payment) paymentDTO {
	return paymentDTO{
		ID:                  pay.ID,
		PaymentSessionID:    pay.PaymentSessionID,
		PaymentCollectionID: pay.PaymentCollectionID,
		Amount:              pay.Amount,
		CurrencyCode:        pay.CurrencyCode,
		RefundedAmount:      pay.RefundedAmount,
		CapturedAt:          pay.CapturedAt,
		CreatedAt:           pay.CreatedAt,
		UpdatedAt:           pay.UpdatedAt,
	}
}

// toRefundDTO converts the model into the external representation.
func toRefundDTO(ref models.Refund) refundDTO {
	return refundDTO{
		ID:        ref.ID,
		PaymentID: ref.PaymentID,
		Amount:    ref.Amount,
		Reason:    ref.Reason,
		Reference: ref.Reference,
		CreatedAt: ref.CreatedAt,
		UpdatedAt: ref.UpdatedAt,
	}
}

// --- helpers ------------------------------------------------------------------

// decodeBody decodes the request body.
//
// The body size is bounded and UNKNOWN FIELDS are rejected: a silently
// swallowed field means a setting the client believes it sent but that is never
// applied.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return coreerrors.Invalid(codeInvalidRequest, "request body cannot be empty")
		}
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"request body could not be parsed")
	}
	// If more than a single JSON value was sent, that is a client error too.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return coreerrors.Invalid(codeInvalidRequest,
			"request body has to be a single JSON document")
	}
	return nil
}

// parsePage parses the limit/offset query parameters.
func parsePage(r *http.Request) (service.Page, error) {
	limit, err := parseInt64Param(r, "limit")
	if err != nil {
		return service.Page{}, err
	}
	offset, err := parseInt64Param(r, "offset")
	if err != nil {
		return service.Page{}, err
	}
	page := service.Page{Limit: limit, Offset: offset}
	if page.Limit == 0 {
		// The default is made visible here too, so that the limit field in the
		// response shows the bound that is really applied.
		page.Limit = service.DefaultLimit
	}
	return page, nil
}

// parseInt64Param converts a query parameter to an integer; returns 0 if it is
// absent.
func parseInt64Param(r *http.Request, name string) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"%s has to be an integer: %q", name, raw)
	}
	return value, nil
}

// writeList writes a slice with the list envelope.
//
// On endpoints that are not paged (such as a collection's sessions) count is
// the number of rows and is the same as limit: the envelope has the same shape
// everywhere, and the client does not have to learn two different response
// formats.
func writeList[T any](ctx context.Context, w http.ResponseWriter, items []T) {
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   items,
		Count:  int64(len(items)),
		Offset: 0,
		Limit:  int64(len(items)),
	})
}
