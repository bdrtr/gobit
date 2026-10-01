package adminui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// The order page's awaited money and the operator recording it (ADR 0287).

// fakeReceiver records the session it was asked about and answers as scripted.
type fakeReceiver struct {
	got []string
	err error
	// methods are the offline methods it lists (ADR 0306).
	methods []string
}

func (f *fakeReceiver) OfflineMethods(context.Context) []string { return f.methods }

func (f *fakeReceiver) RecordReceived(
	_ context.Context, sessionID string,
) (paymentID string, amount int64, currencyCode string, err error) {
	f.got = append(f.got, sessionID)
	if f.err != nil {
		return "", 0, "", f.err
	}

	return "pay_1", 108_000, "TRY", nil
}

// awaitingCatalog is the order whose collection awaits a bank transfer.
func awaitingCatalog() *fakeCatalog {
	catalog := linkedOrderCatalog()
	base := catalog.answer
	catalog.answer = func(spec query.GraphSpec) ([]query.Record, error, bool) {
		records, err, handled := base(spec)
		if len(spec.Expand) == 1 && spec.Expand[0].Link == linkOrderPayment && err == nil {
			records[0][linkOrderPayment] = query.Record{
				"status": "authorized", "amount": int64(108_000), "currency_code": "TRY",
				"authorized_amount": int64(108_000), "captured_amount": int64(0),
				"refunded_amount": int64(0),
				fieldAwaiting: []map[string]any{{
					awaitingSessionID: "payses_transfer", awaitingProviderID: "bank_transfer",
					awaitingAmount: int64(108_000),
				}},
			}
		}

		return records, err, handled
	}

	return catalog
}

// paymentRouter mounts the order page and the record route.
func paymentRouter(panel *UI) chi.Router {
	r := orderRouter(panel)
	r.Post(OrderPaymentReceivedPath, panel.submitPaymentReceived)

	return r
}

// receivedPath is the route recording the fixture's session.
const receivedPath = OrdersPath + "/order_1/payment/payses_transfer/received"

// TestTheOrderPageNamesWhatItAwaits: the awaited transfer is printed with its
// amount and method, and its form is offered only to an operator who may
// write payments, on a panel that has the payment module's surface.
func TestTheOrderPageNamesWhatItAwaits(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		scopes   []string
		receiver PaymentReceiver
		form     bool
	}{
		"a payment writer":             {[]string{scopePaymentRead, scopePaymentWrite}, &fakeReceiver{}, true},
		"a payment reader":             {[]string{scopePaymentRead}, &fakeReceiver{}, false},
		"no surface in the install":    {[]string{scopePaymentRead, scopePaymentWrite}, nil, false},
		"an administrator":             {[]string{corehttp.ScopeAdmin}, &fakeReceiver{}, true},
		"an order writer, not a payer": {[]string{scopePaymentRead, scopeOrderWrite}, &fakeReceiver{}, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			panel := newCatalogPanel(t, awaitingCatalog())
			panel.payments = tc.receiver
			rec := getOrderPageAs(panel, OrdersPath+"/order_1",
				corehttp.Principal{ID: "user_1", Kind: "user", Scopes: tc.scopes})

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			body := rec.Body.String()
			assert.Contains(t, body, "Awaiting <span class=\"num\">1080.00 TRY</span> by bank_transfer.")
			assert.Equal(t, tc.form, strings.Contains(body, `action="`+receivedPath+`"`))
		})
	}
}

// TestTheOperatorRecordsTheMoney: the form reaches the surface with the
// session the page named, and the page says what was recorded.
func TestTheOperatorRecordsTheMoney(t *testing.T) {
	t.Parallel()

	receiver := &fakeReceiver{}
	panel := newCatalogPanel(t, awaitingCatalog())
	panel.payments = receiver

	rec := postPaymentAs(panel, receivedPath, []string{scopeOrderRead, scopePaymentRead, scopePaymentWrite})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"payses_transfer"}, receiver.got)
	assert.Contains(t, rec.Body.String(), "The payment of 1080.00 TRY was recorded as received.")
}

// TestARefusedRecordSaysWhy: the module's refusal — a card's session, say —
// is printed on the page rather than turned into a failure.
func TestARefusedRecordSaysWhy(t *testing.T) {
	t.Parallel()

	receiver := &fakeReceiver{err: errors.Conflict("payment_session_captures_now",
		"session payses_card is paid through the provider at the checkout")}
	panel := newCatalogPanel(t, awaitingCatalog())
	panel.payments = receiver

	rec := postPaymentAs(panel, OrdersPath+"/order_1/payment/payses_card/received",
		[]string{scopeOrderRead, scopePaymentRead, scopePaymentWrite})

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `<p role="alert">`)
	assert.Contains(t, rec.Body.String(), "paid through the provider at the checkout")
}

// TestRecordingWithoutTheSurfaceIsUnavailable: an installation without the
// payment module's surface answers 503 and reaches nothing.
func TestRecordingWithoutTheSurfaceIsUnavailable(t *testing.T) {
	t.Parallel()

	rec := postPaymentAs(newCatalogPanel(t, awaitingCatalog()), receivedPath,
		[]string{scopeOrderRead, scopePaymentWrite})

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
}

// postPaymentAs posts the record form as an operator holding the scopes.
func postPaymentAs(panel *UI, path string, scopes []string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, http.NoBody)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = request.WithContext(corehttp.WithPrincipal(request.Context(),
		corehttp.Principal{ID: "user_1", Kind: "user", Scopes: scopes}))

	rec := httptest.NewRecorder()
	paymentRouter(panel).ServeHTTP(rec, request)

	return rec
}
