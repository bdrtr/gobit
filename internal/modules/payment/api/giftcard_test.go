package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/payment/api"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// giftCardRequest sends a request as the given principal; the router is built
// without the identity ring, so the principal is put in place by hand.
func giftCardRequest(
	t *testing.T, r chi.Router, method, path, body string, as corehttp.Principal,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), as))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// operator is a principal holding every scope.
var operator = corehttp.Principal{ID: "user_admin", Kind: "user", Scopes: []string{corehttp.ScopeAdmin}}

// TestAnIssuedGiftCardAnswersWithItsCodeOnce is ADR 0208's issue: the answer
// is the only one that carries the code; a read of the same card does not.
func TestAnIssuedGiftCardAnswersWithItsCodeOnce(t *testing.T) {
	t.Parallel()

	card := models.GiftCard{
		ID: "gcard_1", CodeTail: "PQRS", CurrencyCode: "TRY", Reason: "a gift",
		CreatedAt: time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC),
	}
	svc := &fakePayments{
		issuedCard: service.IssuedGiftCard{Card: card, Code: "ABCD-EFGH-JKMN-PQRS", Balance: 5_000},
		giftCards:  []service.GiftCardWithBalance{{Card: card, Balance: 5_000}},
	}
	r := newTestRouter(svc)

	issued := giftCardRequest(t, r, http.MethodPost, "/admin/v1/gift-cards",
		`{"currency_code":"try","amount":5000,"reason":"a gift"}`, operator)
	require.Equal(t, http.StatusCreated, issued.Code, issued.Body.String())
	assert.Equal(t, service.IssueGiftCardInput{CurrencyCode: "try", Amount: 5_000, Reason: "a gift"}, svc.lastGiftInput)
	var body struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(issued.Body.Bytes(), &body))
	assert.Equal(t, "ABCD-EFGH-JKMN-PQRS", body.Data["code"])
	assert.Equal(t, "PQRS", body.Data["code_tail"])
	assert.InDelta(t, 5_000, body.Data["balance"], 0)

	read := giftCardRequest(t, r, http.MethodGet, "/admin/v1/gift-cards/gcard_1", "", operator)
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	assert.Equal(t, "gcard_1", svc.lastGiftID)
	assert.NotContains(t, read.Body.String(), `"code":`, "the code is not shown again")
	assert.Contains(t, read.Body.String(), `"code_tail":"PQRS"`)

	listed := giftCardRequest(t, r, http.MethodGet, "/admin/v1/gift-cards", "", operator)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	assert.NotContains(t, listed.Body.String(), `"code":`)
}

// TestAGiftCardIsIssuedUnderThePaymentWrite: issuing one creates money its
// holder can spend.
func TestAGiftCardIsIssuedUnderThePaymentWrite(t *testing.T) {
	t.Parallel()

	svc := &fakePayments{}
	r := newTestRouter(svc)
	reader := corehttp.Principal{ID: "user_reader", Kind: "user", Scopes: []string{api.ScopeRead}}

	refused := giftCardRequest(t, r, http.MethodPost, "/admin/v1/gift-cards",
		`{"currency_code":"TRY","amount":5000,"reason":"a gift"}`, reader)
	assert.Equal(t, http.StatusForbidden, refused.Code)
	assert.Equal(t, service.IssueGiftCardInput{}, svc.lastGiftInput, "the service was not asked")

	svc.giftEntries = []models.GiftCardEntry{{ID: "gcentry_1", GiftCardID: "gcard_1", Amount: 5_000, Kind: models.GiftCardIssue}}
	history := giftCardRequest(t, r, http.MethodGet, "/admin/v1/gift-cards/gcard_1/entries", "", reader)
	require.Equal(t, http.StatusOK, history.Code, history.Body.String())
	assert.Contains(t, history.Body.String(), `"kind":"issue"`)
}

// TestAReplacedCodeIsAnsweredOnceUnderThePaymentWrite (ADR 0210).
func TestAReplacedCodeIsAnsweredOnceUnderThePaymentWrite(t *testing.T) {
	t.Parallel()

	svc := &fakePayments{issuedCard: service.IssuedGiftCard{
		Card: models.GiftCard{ID: "gcard_1", CodeTail: "WXYZ", CurrencyCode: "TRY", Source: models.GiftCardSold},
		Code: "QRST-UVWX-YZ01-WXYZ", Balance: 3_000,
	}}
	r := newTestRouter(svc)
	reader := corehttp.Principal{ID: "user_reader", Kind: "user", Scopes: []string{api.ScopeRead}}

	refused := giftCardRequest(t, r, http.MethodPost, "/admin/v1/gift-cards/gcard_1/code", "", reader)
	assert.Equal(t, http.StatusForbidden, refused.Code)

	replaced := giftCardRequest(t, r, http.MethodPost, "/admin/v1/gift-cards/gcard_1/code", "", operator)
	require.Equal(t, http.StatusOK, replaced.Code, replaced.Body.String())
	assert.Equal(t, "gcard_1", svc.lastGiftID)
	assert.Contains(t, replaced.Body.String(), `"code":"QRST-UVWX-YZ01-WXYZ"`)
	assert.Contains(t, replaced.Body.String(), `"source":"sold"`)
}

// TestAGiftCardIsClosedWithAReasonUnderThePaymentWrite (ADR 0213): the reason
// reaches the service, the answer says when and why, and a card a payment holds
// is answered 409.
func TestAGiftCardIsClosedWithAReasonUnderThePaymentWrite(t *testing.T) {
	t.Parallel()

	closedAt := time.Unix(2_000, 0).UTC()
	svc := &fakePayments{giftCards: []service.GiftCardWithBalance{{Card: models.GiftCard{
		ID: "gcard_1", CodeTail: "WXYZ", CurrencyCode: "TRY", DisabledAt: &closedAt, DisableReason: "sold by mistake",
	}}}}
	r := newTestRouter(svc)
	reader := corehttp.Principal{ID: "user_reader", Kind: "user", Scopes: []string{api.ScopeRead}}

	refused := giftCardRequest(t, r, http.MethodPost, "/admin/v1/gift-cards/gcard_1/disable",
		`{"reason":"sold by mistake"}`, reader)
	assert.Equal(t, http.StatusForbidden, refused.Code)
	assert.Empty(t, svc.lastDisableReason, "the service was not asked")

	closed := giftCardRequest(t, r, http.MethodPost, "/admin/v1/gift-cards/gcard_1/disable",
		`{"reason":"sold by mistake"}`, operator)
	require.Equal(t, http.StatusOK, closed.Code, closed.Body.String())
	assert.Equal(t, "gcard_1", svc.lastGiftID)
	assert.Equal(t, "sold by mistake", svc.lastDisableReason)
	assert.Contains(t, closed.Body.String(), `"disabled_at":"1970-01-01T00:33:20Z"`)
	assert.Contains(t, closed.Body.String(), `"disable_reason":"sold by mistake"`)
	assert.Contains(t, closed.Body.String(), `"balance":0`)

	svc.err = errors.Conflict(service.CodeGiftCardHeld, "held")
	held := giftCardRequest(t, r, http.MethodPost, "/admin/v1/gift-cards/gcard_1/disable",
		`{"reason":"sold by mistake"}`, operator)
	assert.Equal(t, http.StatusConflict, held.Code)
	assert.Contains(t, held.Body.String(), service.CodeGiftCardHeld)
}
