//go:build integration

package paymentpaytr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
)

// refundSpy stands where PayTR's refund endpoint would and records what it is
// asked to send back, in the major units the call carries.
type refundSpy struct {
	*httptest.Server

	mu   sync.Mutex
	sent []string
}

func newRefundSpy(t *testing.T) *refundSpy {
	t.Helper()

	spy := &refundSpy{}
	spy.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		spy.mu.Lock()
		spy.sent = append(spy.sent, r.PostForm.Get("merchant_oid")+":"+r.PostForm.Get("return_amount"))
		spy.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success"})
	}))
	t.Cleanup(spy.Close)

	return spy
}

func (s *refundSpy) refunds() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.sent...)
}

// paidInInstallments opens a 10 000 payment and records PayTR reporting a
// total of 10 500: the customer paid in installments.
func paidInInstallments(t *testing.T, m *paytrModule) {
	t.Helper()

	require.NoError(t, m.store.open(t.Context(), payment{
		MerchantOID: "ORDER1", Amount: 10000, CurrencyCode: "TRY"}))
	require.Equal(t, "OK", callback(t, m, "ORDER1", "success", "10500").Body.String())
}

// TestAnInstallmentPaymentIsAuthorizedForTheSessionsAmount: PayTR's signed
// total carries the installment difference, and a hold above the session is a
// contract violation the payment module answers with a full refund, so the
// shop's part is what is authorized, captured and inspected (ADR 0438). What
// was paid stays on the row and goes to the session's data.
func TestAnInstallmentPaymentIsAuthorizedForTheSessionsAmount(t *testing.T) {
	m := freshModule(t)
	paidInInstallments(t, m)

	result, err := m.prov.Authorize(t.Context(), "ORDER1")

	require.NoError(t, err)
	assert.Equal(t, coreprovider.SessionAuthorized, result.Status)
	assert.Equal(t, int64(10000), result.AuthorizedAmount)
	assert.JSONEq(t, `{"paid_amount":10500,"installment_difference":500}`, string(result.Data))

	require.NoError(t, m.prov.Capture(t.Context(), "ORDER1", 10000))
	overCapture := m.prov.Capture(t.Context(), "ORDER1", 10001)
	require.Error(t, overCapture, "the installment difference is not the shop's to capture")
	assert.Equal(t, codeAmountMismatch, coreerrors.CodeOf(overCapture))

	inspection, err := m.prov.InspectSession(t.Context(), "ORDER1")
	require.NoError(t, err)
	assert.Equal(t, coreprovider.SessionCaptured, inspection.Status)
	assert.Equal(t, int64(10000), inspection.AuthorizedAmount)
	assert.Equal(t, int64(10000), inspection.CapturedAmount)

	row, err := m.store.get(t.Context(), "ORDER1")
	require.NoError(t, err)
	assert.Equal(t, int64(10500), row.PaidAmount, "the row keeps what PayTR said was paid")
}

// TestAPaymentWithoutInstallmentsSaysWhatWasPaid: no difference, no
// difference key, and the whole payment authorized.
func TestAPaymentWithoutInstallmentsSaysWhatWasPaid(t *testing.T) {
	m := freshModule(t)
	require.NoError(t, m.store.open(t.Context(), payment{
		MerchantOID: "ORDER1", Amount: 10000, CurrencyCode: "TRY"}))
	require.Equal(t, "OK", callback(t, m, "ORDER1", "success", "10000").Body.String())

	result, err := m.prov.Authorize(t.Context(), "ORDER1")

	require.NoError(t, err)
	assert.Equal(t, int64(10000), result.AuthorizedAmount)
	assert.JSONEq(t, `{"paid_amount":10000}`, string(result.Data))
}

// TestAPaymentBelowTheSessionIsAuthorizedForWhatWasPaid: a short payment is
// reported short, so the checkout's full payment rule refuses the order and
// refunds it.
func TestAPaymentBelowTheSessionIsAuthorizedForWhatWasPaid(t *testing.T) {
	m := freshModule(t)
	require.NoError(t, m.store.open(t.Context(), payment{
		MerchantOID: "ORDER1", Amount: 10000, CurrencyCode: "TRY"}))
	require.Equal(t, "OK", callback(t, m, "ORDER1", "success", "9000").Body.String())

	result, err := m.prov.Authorize(t.Context(), "ORDER1")

	require.NoError(t, err)
	assert.Equal(t, int64(9000), result.AuthorizedAmount)
	assert.JSONEq(t, `{"paid_amount":9000}`, string(result.Data))
}

// TestAnInstallmentPaymentIsRefundedForAtMostTheOrder: what the shop gives
// back through gobit is what it sold the order for; the installment difference
// was never its money (ADR 0438).
func TestAnInstallmentPaymentIsRefundedForAtMostTheOrder(t *testing.T) {
	m := freshModule(t)
	spy := newRefundSpy(t)
	m.prov.cfg.BaseURL = spy.URL
	paidInInstallments(t, m)

	tooMuch := m.prov.Refund(t.Context(), "ORDER1", 10001)
	require.Error(t, tooMuch)
	assert.Equal(t, codeAmountMismatch, coreerrors.CodeOf(tooMuch))
	assert.Empty(t, spy.refunds(), "the refusal comes before PayTR is called")

	require.NoError(t, m.prov.Cancel(t.Context(), "ORDER1"))
	require.NoError(t, m.prov.Cancel(t.Context(), "ORDER1"), "a second compensation sends nothing")

	assert.Equal(t, []string{"ORDER1:100.00"}, spy.refunds())
	row, err := m.store.get(t.Context(), "ORDER1")
	require.NoError(t, err)
	assert.Equal(t, int64(10000), row.RefundedAmount)
}

// TestThePendingListAnswersInTheEnvelope: the operator's list is under "data",
// as every described endpoint's is, and under its old key too while that is
// deprecated (ADR 0438).
func TestThePendingListAnswersInTheEnvelope(t *testing.T) {
	m := freshModule(t)
	require.NoError(t, m.store.open(t.Context(), payment{
		MerchantOID: "OLD", Amount: 10000, CurrencyCode: "TRY"}))
	_, err := testPool.Pool().Exec(t.Context(),
		`UPDATE paytr_payment SET created_at = now() - interval '2 hours' WHERE merchant_oid = 'OLD'`)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	m.handlePending(rec, httptest.NewRequest(http.MethodGet, PendingPath, http.NoBody))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	row := `{"merchant_oid":"OLD","amount":10000,"currency_code":"TRY","status":"pending"}`
	assert.JSONEq(t, `{"data":[`+row+`],"pending":[`+row+`]}`, rec.Body.String())
}

// openingFor is a session request for the order id ORDER1, as the payment
// module sends it for a collection.
func openingFor(amount int64) coreprovider.CreateSessionInput {
	return coreprovider.CreateSessionInput{
		Reference: "ORDER1", Amount: amount, CurrencyCode: "TRY", IdempotencyKey: "k2",
		Data: map[string]any{DataKeyEmail: "a@b.c", DataKeyUserIP: "198.51.100.7"},
	}
}

// TestAnOrderIDPayTRReportedOnIsNotOpenedAgain: a second session on the same
// collection reaches the same order id, and once PayTR reported on it the row
// is a payment that already happened — refused, or paid and given back — so
// reading it again would place an order on money that never stayed (ADR 0438).
func TestAnOrderIDPayTRReportedOnIsNotOpenedAgain(t *testing.T) {
	for name, report := range map[string]func(t *testing.T, m *paytrModule){
		"paid and given back": func(t *testing.T, m *paytrModule) {
			require.Equal(t, "OK", callback(t, m, "ORDER1", "success", "10000").Body.String())
			require.NoError(t, m.store.addRefund(t.Context(), "ORDER1", 10000))
		},
		"refused": func(t *testing.T, m *paytrModule) {
			require.Equal(t, "OK", callback(t, m, "ORDER1", "failed", "0").Body.String())
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := freshModule(t)
			spy := newRefundSpy(t)
			m.prov.cfg.BaseURL = spy.URL
			require.NoError(t, m.store.open(t.Context(), payment{
				MerchantOID: "ORDER1", Amount: 10000, CurrencyCode: "TRY"}))
			report(t, m)

			_, err := m.prov.CreateSession(t.Context(), openingFor(10000))

			require.Error(t, err)
			assert.Equal(t, codeOrderIDTaken, coreerrors.CodeOf(err))
			assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))
			assert.Empty(t, spy.refunds(), "PayTR was not asked for a token")
		})
	}

	t.Run("a paid page no session holds is adopted", func(t *testing.T) {
		m := freshModule(t)
		spy := newRefundSpy(t)
		m.prov.cfg.BaseURL = spy.URL
		require.NoError(t, m.store.open(t.Context(), payment{
			MerchantOID: "ORDER1", Amount: 10000, CurrencyCode: "TRY"}))
		require.Equal(t, "OK", callback(t, m, "ORDER1", "success", "10000").Body.String())

		_, err := m.prov.CreateSession(t.Context(), openingFor(10000))

		// The refusal is not the order id's: PayTR was asked, and the spy,
		// which issues no token, is what answered.
		require.Error(t, err)
		assert.NotEqual(t, codeOrderIDTaken, coreerrors.CodeOf(err), "%v", err)
		assert.Equal(t, []string{"ORDER1:"}, spy.refunds(), "the get-token call reached PayTR")
	})

	t.Run("another amount asked for a page still open", func(t *testing.T) {
		m := freshModule(t)
		require.NoError(t, m.store.open(t.Context(), payment{
			MerchantOID: "ORDER1", Amount: 10000, CurrencyCode: "TRY"}))

		_, err := m.prov.CreateSession(t.Context(), openingFor(9000))

		require.Error(t, err)
		assert.Equal(t, codeOrderIDTaken, coreerrors.CodeOf(err))
	})
}

// TestARefundedPaymentIsNotAuthorizedOrCaptured: the only refund before an
// order is a cancel that returns everything, so a refunded row holds nothing
// an order can be placed on.
func TestARefundedPaymentIsNotAuthorizedOrCaptured(t *testing.T) {
	m := freshModule(t)
	require.NoError(t, m.store.open(t.Context(), payment{
		MerchantOID: "ORDER1", Amount: 10000, CurrencyCode: "TRY"}))
	require.Equal(t, "OK", callback(t, m, "ORDER1", "success", "10000").Body.String())
	require.NoError(t, m.store.addRefund(t.Context(), "ORDER1", 10000))

	result, err := m.prov.Authorize(t.Context(), "ORDER1")
	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "%v", err)
	assert.Equal(t, coreprovider.SessionFailed, result.Status)

	inspection, err := m.prov.InspectSession(t.Context(), "ORDER1")
	require.NoError(t, err)
	assert.Equal(t, coreprovider.SessionCanceled, inspection.Status,
		"a payment given back is not reported held to a completion that asks")
	assert.Equal(t, int64(10000), inspection.CapturedAmount)
	assert.Equal(t, int64(10000), inspection.RefundedAmount)

	captured := m.prov.Capture(t.Context(), "ORDER1", 0)
	require.Error(t, captured)
	assert.Equal(t, codeAmountMismatch, coreerrors.CodeOf(captured))
}

// TestAReportOfNothingPaidIsNotAHoldOfEverything: the payment module reads a
// zero hold as the whole session, so a success PayTR reported for nothing is
// refused rather than handed over as zero.
func TestAReportOfNothingPaidIsNotAHoldOfEverything(t *testing.T) {
	m := freshModule(t)
	require.NoError(t, m.store.open(t.Context(), payment{
		MerchantOID: "ORDER1", Amount: 10000, CurrencyCode: "TRY"}))
	require.Equal(t, "OK", callback(t, m, "ORDER1", "success", "0").Body.String())

	_, err := m.prov.Authorize(t.Context(), "ORDER1")

	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "%v", err)
}

// TestNoRowIsWrittenForARequestPayTRCouldNeverAccept: a session without the
// customer's details is refused before its row exists, so the pending list
// does not count it.
func TestNoRowIsWrittenForARequestPayTRCouldNeverAccept(t *testing.T) {
	m := freshModule(t)
	in := openingFor(10000)
	delete(in.Data, DataKeyUserIP)

	_, err := m.prov.CreateSession(t.Context(), in)

	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "%v", err)
	_, err = m.store.get(t.Context(), "ORDER1")
	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "no row was written: %v", err)
}

// TestPayTRSaysItsCustomerAuthorizes: the method the payment module asserts
// for (ADR 0438). The pin that the module still finds it is at the composition
// root, which can import both.
func TestPayTRSaysItsCustomerAuthorizes(t *testing.T) {
	assert.True(t, (&provider{}).CustomerAuthorizes())
}

// recordedReports keeps what the plugin reported to the checkout.
type recordedReports struct {
	mu   sync.Mutex
	got  []string
	fail error
}

func (r *recordedReports) PaymentReported(_ context.Context, providerID, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, providerID+":"+sessionID)

	return r.fail
}

func (r *recordedReports) reported() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), r.got...)
}

// TestAPaidCallbackIsReportedToTheCheckout: a payment PayTR reports paid is
// handed to the checkout, which completes its cart; a refused one is not, and
// a report the checkout could not take still acknowledges PayTR, whose
// payment is recorded and found again by the checkout's sweep (ADR 0438).
func TestAPaidCallbackIsReportedToTheCheckout(t *testing.T) {
	m := freshModule(t)
	reports := &recordedReports{}
	m.reports = reports
	for _, oid := range []string{"PAID1", "REFUSED1", "PAID2"} {
		require.NoError(t, m.store.open(t.Context(), payment{MerchantOID: oid, Amount: 10000, CurrencyCode: "TRY"}))
	}

	require.Equal(t, "OK", callback(t, m, "PAID1", "success", "10000").Body.String())
	require.Equal(t, "OK", callback(t, m, "REFUSED1", "failed", "0").Body.String())

	assert.Equal(t, []string{"paytr:PAID1"}, reports.reported())

	reports.fail = coreerrors.Unavailable("checkout_down", "the checkout did not answer")
	answer := callback(t, m, "PAID2", "success", "10000")

	assert.Equal(t, http.StatusOK, answer.Code)
	assert.Equal(t, "OK", answer.Body.String(), "PayTR is acknowledged; the payment is recorded")
	assert.Equal(t, []string{"paytr:PAID1", "paytr:PAID2"}, reports.reported())
	row, err := m.store.get(t.Context(), "PAID2")
	require.NoError(t, err)
	assert.Equal(t, statusSuccess, row.Status)
}
