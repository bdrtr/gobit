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

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/payment/api"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// giftCardRouter mounts the payment routes over the fake.
func giftCardRouter(svc *fakePayments) chi.Router {
	r := chi.NewRouter()
	api.New(svc).Routes(r)

	return r
}

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
	r := giftCardRouter(svc)

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
	r := giftCardRouter(svc)
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
	r := giftCardRouter(svc)
	reader := corehttp.Principal{ID: "user_reader", Kind: "user", Scopes: []string{api.ScopeRead}}

	refused := giftCardRequest(t, r, http.MethodPost, "/admin/v1/gift-cards/gcard_1/code", "", reader)
	assert.Equal(t, http.StatusForbidden, refused.Code)

	replaced := giftCardRequest(t, r, http.MethodPost, "/admin/v1/gift-cards/gcard_1/code", "", operator)
	require.Equal(t, http.StatusOK, replaced.Code, replaced.Body.String())
	assert.Equal(t, "gcard_1", svc.lastGiftID)
	assert.Contains(t, replaced.Body.String(), `"code":"QRST-UVWX-YZ01-WXYZ"`)
	assert.Contains(t, replaced.Body.String(), `"source":"sold"`)
}
