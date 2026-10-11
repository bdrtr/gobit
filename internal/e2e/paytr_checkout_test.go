//go:build integration

package e2e

// A storefront's PayTR checkout on the ground, against a fake PayTR (ADR 0438).
//
// PayTR's customer pays inside PayTR's iframe and PayTR reports the outcome by
// posting back. Before the decision the documented single completion opened
// the payment, asked PayTR whether it was paid in the same request and failed
// every time: the iframe token never left the server, a payment made anyway
// met a canceled session, and a retried completion opened a new merchant_oid.
// The fake below verifies the get-token signature, hands out iframe tokens and
// records refunds; the callback is posted to the real ring with PayTR's
// signature.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	ordermodels "github.com/bdrtr/gobit/internal/modules/order/models"
	paymentmodels "github.com/bdrtr/gobit/internal/modules/payment/models"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
	"github.com/bdrtr/gobit/plugins/paymentpaytr"
)

// The merchant the ground's PayTR plugin is configured with (groundEnvironment).
const (
	paytrMerchantID = "123456"
	paytrKey        = "E2EPAYTRKEY"
	paytrSalt       = "E2EPAYTRSALT"
	paytrUserIP     = "198.51.100.7"
	paytrEmail      = "shopper@example.test"
)

// paytrTotal is what one unit of a PayTR scenario's product costs with its
// tax: 10 000 net at the ground's 20 percent.
const paytrTotal int64 = 12_000

// fakePayTRServer stands where www.paytr.com would; setUpHarness starts it
// before the installation opens, because the plugin is configured with its
// address.
var fakePayTRServer *fakePayTR

// fakePayTR answers PayTR's get-token and refund calls.
type fakePayTR struct {
	*httptest.Server

	mu      sync.Mutex
	tokens  []fakeToken
	refunds []string
}

// fakeToken is one get-token call.
type fakeToken struct {
	OID, Amount, Token string
	SignatureOK        bool
}

func (f *fakePayTR) issued() []fakeToken {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]fakeToken(nil), f.tokens...)
}

// issuedFor counts the get-token calls made for one merchant_oid.
func (f *fakePayTR) issuedFor(oid string) int {
	n := 0
	for _, token := range f.issued() {
		if token.OID == oid {
			n++
		}
	}

	return n
}

// refundedFor lists the refunds sent for one merchant_oid, in major units as
// PayTR's refund call takes them.
func (f *fakePayTR) refundedFor(oid string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []string
	for _, refund := range f.refunds {
		if strings.HasPrefix(refund, oid+":") {
			out = append(out, strings.TrimPrefix(refund, oid+":"))
		}
	}

	return out
}

func newFakePayTR() *fakePayTR {
	f := &fakePayTR{}
	mux := http.NewServeMux()
	mux.HandleFunc("/odeme/api/get-token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form := r.PostForm
		body := form.Get("merchant_id") + form.Get("user_ip") + form.Get("merchant_oid") + form.Get("email") +
			form.Get("payment_amount") + form.Get("user_basket") + form.Get("no_installment") +
			form.Get("max_installment") + form.Get("currency") + form.Get("test_mode")
		mac := hmac.New(sha256.New, []byte(paytrKey))
		mac.Write([]byte(body + paytrSalt))
		ok := hmac.Equal([]byte(base64.StdEncoding.EncodeToString(mac.Sum(nil))), []byte(form.Get("paytr_token")))

		oid := form.Get("merchant_oid")
		token := "iframe-" + oid
		f.mu.Lock()
		f.tokens = append(f.tokens, fakeToken{OID: oid, Amount: form.Get("payment_amount"), Token: token, SignatureOK: ok})
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "success", "token": token})
	})
	mux.HandleFunc("/odeme/iade", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.refunds = append(f.refunds, r.PostForm.Get("merchant_oid")+":"+r.PostForm.Get("return_amount"))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success"})
	})
	f.Server = httptest.NewServer(mux)

	return f
}

// paytrCallback posts what PayTR posts after the customer paid in the iframe.
func paytrCallback(t *testing.T, oid, status string, totalAmount int64) *httptest.ResponseRecorder {
	t.Helper()

	total := strconv.FormatInt(totalAmount, 10)
	mac := hmac.New(sha256.New, []byte(paytrKey))
	mac.Write([]byte(oid + paytrSalt + status + total))
	form := url.Values{}
	form.Set("merchant_oid", oid)
	form.Set("status", status)
	form.Set("total_amount", total)
	form.Set("hash", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	form.Set("payment_type", "card")
	form.Set("currency", "TL")

	request := httptest.NewRequest(http.MethodPost, paymentpaytr.CallbackPath, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	testRouter.ServeHTTP(recorder, request)

	return recorder
}

// openedPayment is the answer of POST /store/v1/carts/{id}/payment-sessions,
// with the collection the session was opened on read from the database: the
// answer does not publish it.
type openedPayment struct {
	PaymentSessionID    string `json:"payment_session_id"`
	PaymentCollectionID string `json:"-"`
	ProviderID          string `json:"provider_id"`
	Status              string `json:"status"`
	Amount              int64  `json:"amount"`
	CurrencyCode        string `json:"currency_code"`
	Data                struct {
		IframeToken string `json:"iframe_token"`
	} `json:"data"`
	CompletionSecret string `json:"completion_secret"`
}

// completionSecrets keeps each opened payment's secret by its session, the
// way a storefront keeps it beside the session id, so a completion helper
// sends it as a storefront does.
var completionSecrets sync.Map

// oid is the merchant_oid PayTR knows the payment by: the collection's id in
// letters and digits.
func (p openedPayment) oid() string { return oidOf(p.PaymentCollectionID) }

// openPayment opens a cart's payment at a provider and returns the answer.
func openPayment(t *testing.T, cartID, providerID string, total int64) openedPayment {
	t.Helper()

	rec := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/payment-sessions",
		fmt.Sprintf(`{"payment_provider_id":%q,"payment_data":{"email":%q,"user_ip":%q},"expected_total":%d}`,
			providerID, paytrEmail, paytrUserIP, total))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var envelope struct {
		Data openedPayment `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), rec.Body.String())
	require.NotEmpty(t, envelope.Data.PaymentSessionID, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "payment_collection_id",
		"no storefront route takes the collection, so the answer does not name it")
	require.NoError(t, testPool.Pool().QueryRow(t.Context(),
		`SELECT payment_collection_id FROM payment_sessions WHERE id = $1`,
		envelope.Data.PaymentSessionID).Scan(&envelope.Data.PaymentCollectionID))
	require.True(t, strings.HasPrefix(envelope.Data.CompletionSecret, paymentmodels.CompletionSecretPrefix),
		"the opening hands the storefront its completion secret: %s", rec.Body.String())
	completionSecrets.Store(envelope.Data.PaymentSessionID, envelope.Data.CompletionSecret)

	return envelope.Data
}

// cancelPayment cancels a payment opened for a cart, under the cart.
func cancelPayment(t *testing.T, cartID, sessionID string) *httptest.ResponseRecorder {
	t.Helper()

	return storefrontRequest(t, http.MethodPost,
		"/store/v1/carts/"+cartID+"/payment-sessions/"+sessionID+"/cancel", "")
}

// completeWithSession completes a cart naming a payment session.
func completeWithSession(t *testing.T, cartID, providerID, sessionID string, total int64) *httptest.ResponseRecorder {
	t.Helper()

	return completeWithSessionKeyed(t, cartID, providerID, sessionID, total, "")
}

// completeWithSessionKeyed is [completeWithSession] sent under an
// Idempotency-Key, as a storefront that retries a completion sends it.
func completeWithSessionKeyed(
	t *testing.T, cartID, providerID, sessionID string, total int64, key string,
) *httptest.ResponseRecorder {
	t.Helper()

	secret, _ := completionSecrets.Load(sessionID)
	presented, _ := secret.(string)
	request := httptest.NewRequest(http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		strings.NewReader(fmt.Sprintf(
			`{"payment_provider_id":%q,"payment_session_id":%q,"completion_secret":%q,"expected_total":%d}`,
			providerID, sessionID, presented, total)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(corehttp.PublishableKeyHeader, publishableKey)
	if key != "" {
		request.Header.Set(corehttp.IdempotencyKeyHeader, key)
	}
	recorder := httptest.NewRecorder()
	testRouter.ServeHTTP(recorder, request)

	return recorder
}

// paytrCart opens a guest cart holding one unit of a fresh PayTR product,
// stocked five, and returns it with its total and the product's inventory item.
func paytrCart(t *testing.T, title string) (cartID string, total int64, itemID string) {
	t.Helper()

	variantID, itemID := newStockedVariant(t.Context(), t, title, map[string]int64{taxedCurrency: 10_000}, 5)
	cartID, total = giftCart(t, variantID)
	require.Equal(t, paytrTotal, total, "the scenario's figures assume a 20 percent tax on 10 000")
	// The shopper's address, which the order and its mail carry.
	contact := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID, fmt.Sprintf(`{"email":%q}`, paytrEmail))
	require.Equal(t, http.StatusOK, contact.Code, contact.Body.String())

	return cartID, total, itemID
}

// paytrState is everything the database says about one cart's payment.
type paytrState struct {
	Orders     []string // status per order of the cart
	Sessions   []string // provider:status per session of the cart's collections
	PayTRRows  []string // status:paid per PayTR row of the cart's collections
	Captured   []string // provider:amount per capture of the cart's collections
	Executions int      // complete_cart executions of the cart
}

func readPayTRState(t *testing.T, cartID string) paytrState {
	t.Helper()
	ctx := t.Context()
	pool := testPool.Pool()

	scan := func(query string, args ...any) []string {
		rows, err := pool.Query(ctx, query, args...)
		require.NoError(t, err, query)
		defer rows.Close()
		var out []string
		for rows.Next() {
			var v string
			require.NoError(t, rows.Scan(&v))
			out = append(out, v)
		}
		require.NoError(t, rows.Err())

		return out
	}

	var s paytrState
	s.Orders = scan(`SELECT status FROM orders WHERE cart_id = $1 ORDER BY created_at`, cartID)
	s.Sessions = scan(`
        SELECT s.provider_id || ':' || s.status
        FROM payment_sessions s JOIN payment_collections c ON c.id = s.payment_collection_id
        WHERE c.reference = $1 ORDER BY s.created_at`, cartID)
	for _, col := range scan(`SELECT id FROM payment_collections WHERE reference = $1 ORDER BY created_at`, cartID) {
		s.PayTRRows = append(s.PayTRRows, scan(`
            SELECT status || ':' || paid_amount::text FROM paytr_payment WHERE merchant_oid = $1`, oidOf(col))...)
	}
	s.Captured = scan(`
        SELECT s.provider_id || ':' || p.amount::text
        FROM payments p
        JOIN payment_sessions s ON s.id = p.payment_session_id
        JOIN payment_collections c ON c.id = p.payment_collection_id
        WHERE c.reference = $1 ORDER BY p.created_at`, cartID)
	require.NoError(t, pool.QueryRow(ctx, `
        SELECT count(*) FROM workflow_executions
        WHERE workflow = 'complete_cart' AND input::text LIKE '%' || $1 || '%'`, cartID).Scan(&s.Executions))

	return s
}

// oidOf is the plugin's merchant_oid for a collection id: letters and digits.
func oidOf(collectionID string) string {
	var b strings.Builder
	for _, r := range collectionID {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
	}

	return b.String()
}

// reportOf is the payment module's report of a session: its outcome, empty
// while it is open, and how many failed attempts it counted.
func reportOf(t *testing.T, sessionID string) (outcome string, attempts int, nextAttempt time.Time) {
	t.Helper()

	require.NoError(t, testPool.Pool().QueryRow(t.Context(), `
        SELECT outcome, attempts, next_attempt_at FROM payment_session_reports WHERE session_id = $1`,
		sessionID).Scan(&outcome, &attempts, &nextAttempt))

	return outcome, attempts, nextAttempt
}

// grow adds a line of a fresh 5 000 net product to the cart: 6 000 with its
// tax, so a 12 000 cart totals 18 000 afterwards.
func grow(t *testing.T, cartID, title string) {
	t.Helper()

	extra, _ := newStockedVariant(t.Context(), t, title, map[string]int64{taxedCurrency: 5_000}, 5)
	added := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/line-items",
		fmt.Sprintf(`{"variant_id":%q,"quantity":1}`, extra))
	require.Equal(t, http.StatusCreated, added.Code, added.Body.String())
}

// orderIDOf reads the order a completion answered with.
func orderIDOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	orderID, ok := storefrontData(t, rec)["order_id"].(string)
	require.True(t, ok, rec.Body.String())

	return orderID
}

// placedOrderOf is the one order placed for a cart, read from the database.
func placedOrderOf(t *testing.T, cartID string) string {
	t.Helper()

	var orderID string
	require.NoError(t, testPool.Pool().QueryRow(t.Context(),
		`SELECT id FROM orders WHERE cart_id = $1 AND status = 'pending'`, cartID).Scan(&orderID))

	return orderID
}

// TestAPayTRCheckoutPlacesAnOrder is the default path with the storefront
// first: the payment is opened and its token reaches the storefront, a
// completion before PayTR reported does nothing and says when to ask again,
// PayTR's callback places the order, and the storefront's completion sent
// again under its first Idempotency-Key answers with that order.
func TestAPayTRCheckoutPlacesAnOrder(t *testing.T) {
	ctx := t.Context()
	cartID, total, itemID := paytrCart(t, "PayTR checkout product")

	opened := openPayment(t, cartID, paymentpaytr.ProviderID, total)

	assert.Equal(t, paymentpaytr.ProviderID, opened.ProviderID)
	assert.Equal(t, checkoutwf.SessionPending, opened.Status)
	assert.Equal(t, int64(12_000), opened.Amount)
	assert.Equal(t, taxedCurrency, opened.CurrencyCode)
	assert.Equal(t, "iframe-"+opened.oid(), opened.Data.IframeToken,
		"the storefront is handed the token PayTR issued, which is what opens the iframe")
	require.Equal(t, 1, fakePayTRServer.issuedFor(opened.oid()))
	for _, token := range fakePayTRServer.issued() {
		if token.OID == opened.oid() {
			assert.True(t, token.SignatureOK, "the get-token call is signed as PayTR checks it")
			assert.Equal(t, "12000", token.Amount)
		}
	}

	key := "complete-" + cartID
	early := completeWithSessionKeyed(t, cartID, paymentpaytr.ProviderID, opened.PaymentSessionID, total, key)

	require.Equal(t, http.StatusConflict, early.Code, early.Body.String())
	assert.Equal(t, checkoutwf.CodePaymentPending, errorCode(t, early))
	assert.Equal(t, "2", early.Header().Get("Retry-After"),
		"the refusal says when to ask again, which keeps it out of the key's record")
	before := readPayTRState(t, cartID)
	assert.Empty(t, before.Orders, "a completion before PayTR reported opens no order")
	assert.Zero(t, before.Executions, "and runs no saga")
	assert.Equal(t, []string{"paytr:pending"}, before.Sessions, "the session stays open")
	assert.Equal(t, int64(5), sellableQuantity(ctx, t, itemID), "and reserves no stock")

	callback := paytrCallback(t, opened.oid(), "success", total)
	require.Equal(t, http.StatusOK, callback.Code)
	require.Equal(t, "OK", callback.Body.String())

	placed := readPayTRState(t, cartID)
	assert.Equal(t, []string{"pending"}, placed.Orders, "PayTR's report placed the order")
	assert.Equal(t, []string{"paytr:captured"}, placed.Sessions, "the opened session is the one captured")
	assert.Equal(t, []string{"paytr:12000"}, placed.Captured)
	assert.Equal(t, []string{"success:12000"}, placed.PayTRRows)
	assert.Equal(t, int64(4), sellableQuantity(ctx, t, itemID))
	outcome, attempts, _ := reportOf(t, opened.PaymentSessionID)
	assert.Equal(t, checkoutwf.ReportCompleted, outcome)
	assert.Zero(t, attempts)

	done := completeWithSessionKeyed(t, cartID, paymentpaytr.ProviderID, opened.PaymentSessionID, total, key)

	require.Equal(t, http.StatusOK, done.Code, done.Body.String())
	assert.Empty(t, done.Header().Get(corehttp.IdempotencyReplayedHeader),
		"the retry under the same key ran rather than replaying the refusal")
	order := storefrontData(t, done)
	assert.EqualValues(t, 12_000, order["total"])
	assert.EqualValues(t, 0, order["outstanding"])
	orderID := orderIDOf(t, done)
	assert.Equal(t, placedOrderOf(t, cartID), orderID, "the storefront is told the order the report placed")
	after := readPayTRState(t, cartID)
	assert.Equal(t, []string{"pending"}, after.Orders, "and no second order is placed")
	assert.Equal(t, []string{"paytr:12000"}, after.Captured, "nor a second capture")
	assert.Equal(t, 1, fakePayTRServer.issuedFor(opened.oid()), "the completion opened no second payment")
	assert.Empty(t, fakePayTRServer.refundedFor(opened.oid()))

	bound, err := links.ListMany(ctx, checkoutwf.LinkOrderPayment, []string{orderID})
	require.NoError(t, err)
	assert.Equal(t, []string{opened.PaymentCollectionID}, bound[orderID],
		"the order is bound to the collection the storefront's payment was opened on")

	// The answer carries the order's access token, which opens the order the
	// report placed (ADR 0439); a repeat under the key answers it again, and
	// one under another key is the completed cart's 404.
	token, _ := order["order_token"].(string)
	require.True(t, strings.HasPrefix(token, ordermodels.AccessTokenPrefix), "no token in %v", order)
	read := tokenStorefrontRequest(t, token, http.MethodGet, "/store/v1/orders/"+orderID, "")
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	replayed := completeWithSessionKeyed(t, cartID, paymentpaytr.ProviderID, opened.PaymentSessionID, total, key)
	require.Equal(t, http.StatusOK, replayed.Code, replayed.Body.String())
	assert.Equal(t, token, storefrontData(t, replayed)["order_token"])
	again := completeWithSessionKeyed(t, cartID, paymentpaytr.ProviderID, opened.PaymentSessionID, total,
		"another-"+key)
	require.Equal(t, http.StatusNotFound, again.Code, again.Body.String())
	assert.Equal(t, "cart_not_found", errorCode(t, again), "the one answer was given")
	assert.Equal(t, []string{"pending"}, readPayTRState(t, cartID).Orders)
}

// TestAPayTRCustomerWhoNeverReturnsGetsTheOrder: the shopper pays and closes
// the browser, so the storefront never completes the cart; PayTR's callback
// alone places the order.
func TestAPayTRCustomerWhoNeverReturnsGetsTheOrder(t *testing.T) {
	cartID, total, _ := paytrCart(t, "PayTR closed browser product")
	opened := openPayment(t, cartID, paymentpaytr.ProviderID, total)

	require.Equal(t, http.StatusOK, paytrCallback(t, opened.oid(), "success", total).Code)

	state := readPayTRState(t, cartID)
	assert.Equal(t, []string{"pending"}, state.Orders)
	assert.Equal(t, []string{"paytr:12000"}, state.Captured)
	assert.Equal(t, 1, state.Executions)

	report, err := paymentSvc.Reconcile(t.Context(), 15*time.Minute, 50)
	require.NoError(t, err)
	for _, divergence := range report.Divergences {
		assert.NotEqual(t, opened.PaymentSessionID, divergence.SessionID, "a placed order is not reported")
	}

	// Whoever holds the cart's id and the session's — off a log, an old
	// page — is told nothing: the completed cart's 404, and the shopper's
	// secret stays unspent.
	for _, body := range []string{
		fmt.Sprintf(`{"payment_provider_id":%q,"payment_session_id":%q,"expected_total":%d}`,
			paymentpaytr.ProviderID, opened.PaymentSessionID, total),
		fmt.Sprintf(`{"payment_provider_id":%q,"payment_session_id":%q,"completion_secret":"cpsec_guess","expected_total":%d}`,
			paymentpaytr.ProviderID, opened.PaymentSessionID, total),
	} {
		idsAlone := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete", body)
		require.Equal(t, http.StatusNotFound, idsAlone.Code, idsAlone.Body.String())
		assert.Equal(t, "cart_not_found", errorCode(t, idsAlone))
	}
	shopper := completeWithSession(t, cartID, paymentpaytr.ProviderID, opened.PaymentSessionID, total)
	require.Equal(t, http.StatusOK, shopper.Code, "the secret's holder is still answered: %s", shopper.Body.String())

	// The shopper who never returned reaches the order through its mail.
	orderID := placedOrderOf(t, cartID)
	var mailed string
	require.Eventually(t, func() bool {
		for _, sent := range notificationSpy.notificationsFor(orderID) {
			if sent.Template == "order.placed" {
				mailed = sent.Data["order_token"]
			}
		}

		return mailed != ""
	}, 10*time.Second, 50*time.Millisecond, "the order mail carried no token")
	read := tokenStorefrontRequest(t, mailed, http.MethodGet, "/store/v1/orders/"+orderID, "")
	assert.Equal(t, http.StatusOK, read.Code, read.Body.String())
}

// TestThePaymentReportAndTheStorefrontCompleteOnceAtOnce: PayTR's callback and
// the storefront's completion arrive together. Whichever runs second waits for
// or answers with the first one's order: exactly one order, one capture, and
// the storefront, sending again while told "not yet", ends with that order.
func TestThePaymentReportAndTheStorefrontCompleteOnceAtOnce(t *testing.T) {
	for round := range 3 {
		cartID, total, _ := paytrCart(t, fmt.Sprintf("PayTR race product %d", round))
		opened := openPayment(t, cartID, paymentpaytr.ProviderID, total)

		start := make(chan struct{})
		var wg sync.WaitGroup
		var storefrontOrder string
		var storefrontCodes []int
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			paytrCallback(t, opened.oid(), "success", total)
		}()
		go func() {
			defer wg.Done()
			<-start
			for range 50 {
				rec := completeWithSession(t, cartID, paymentpaytr.ProviderID, opened.PaymentSessionID, total)
				storefrontCodes = append(storefrontCodes, rec.Code)
				if rec.Code == http.StatusOK {
					var envelope struct {
						Data struct {
							OrderID string `json:"order_id"`
						} `json:"data"`
					}
					if json.Unmarshal(rec.Body.Bytes(), &envelope) == nil {
						storefrontOrder = envelope.Data.OrderID
					}

					return
				}
				time.Sleep(20 * time.Millisecond)
			}
		}()
		close(start)
		wg.Wait()

		state := readPayTRState(t, cartID)
		require.Equal(t, []string{"pending"}, state.Orders, "round %d: exactly one order", round)
		assert.Equal(t, []string{"paytr:12000"}, state.Captured, "round %d: one capture", round)
		assert.Equal(t, placedOrderOf(t, cartID), storefrontOrder,
			"round %d: the storefront ends with the one order (answers %v)", round, storefrontCodes)
		for _, code := range storefrontCodes[:len(storefrontCodes)-1] {
			assert.Equal(t, http.StatusConflict, code, "round %d: only \"not yet\" before the order", round)
		}
		assert.Empty(t, fakePayTRServer.refundedFor(opened.oid()))
	}
}

// TestAOneCallPayTRCompletionIsRefusedBeforeItStarts: the documented single
// call naming PayTR and no session is refused before a token is minted, so no
// orphan payment row and no false stuck-payment alarm are left behind.
func TestAOneCallPayTRCompletionIsRefusedBeforeItStarts(t *testing.T) {
	cartID, total, _ := paytrCart(t, "PayTR one call product")
	issuedBefore := len(fakePayTRServer.issued())

	rec := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		fmt.Sprintf(`{"payment_provider_id":%q,"payment_data":{"email":%q,"user_ip":%q},"expected_total":%d}`,
			paymentpaytr.ProviderID, paytrEmail, paytrUserIP, total))

	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Equal(t, checkoutwf.CodePaymentSessionRequired, errorCode(t, rec))
	assert.Len(t, fakePayTRServer.issued(), issuedBefore, "no iframe token was minted")
	state := readPayTRState(t, cartID)
	assert.Empty(t, state.Orders)
	assert.Empty(t, state.Sessions)
	assert.Empty(t, state.PayTRRows)
	assert.Zero(t, state.Executions)
}

// TestAPayTRSessionPaysOnlyForItsOwnCart: a session named for another cart, or
// under another provider, is refused before anything is done, and its own cart
// is completed with it when PayTR reports.
func TestAPayTRSessionPaysOnlyForItsOwnCart(t *testing.T) {
	cartA, totalA, _ := paytrCart(t, "PayTR own cart product A")
	cartB, totalB, _ := paytrCart(t, "PayTR own cart product B")
	sessionA := openPayment(t, cartA, paymentpaytr.ProviderID, totalA)

	foreign := completeWithSession(t, cartB, paymentpaytr.ProviderID, sessionA.PaymentSessionID, totalB)

	require.Equal(t, http.StatusConflict, foreign.Code, foreign.Body.String())
	assert.Equal(t, checkoutwf.CodePaymentSessionForeign, errorCode(t, foreign))
	assert.NotContains(t, foreign.Body.String(), cartA, "the refusal does not name the other cart")
	assert.Empty(t, readPayTRState(t, cartB).Orders)

	otherProvider := completeWithSession(t, cartA, "manual", sessionA.PaymentSessionID, totalA)

	require.Equal(t, http.StatusConflict, otherProvider.Code, otherProvider.Body.String())
	assert.Equal(t, checkoutwf.CodePaymentSessionForeign, errorCode(t, otherProvider),
		"a PayTR session completed under another provider's name is refused")
	assert.Empty(t, readPayTRState(t, cartA).Orders)

	require.Equal(t, http.StatusOK, paytrCallback(t, sessionA.oid(), "success", totalA).Code)

	assert.Equal(t, []string{"paytr:12000"}, readPayTRState(t, cartA).Captured)
	assert.Empty(t, readPayTRState(t, cartB).Orders)

	taken := completeWithSession(t, cartB, paymentpaytr.ProviderID, sessionA.PaymentSessionID, totalB)

	require.Equal(t, http.StatusConflict, taken.Code, taken.Body.String())
	assert.Equal(t, checkoutwf.CodePaymentSessionForeign, errorCode(t, taken),
		"a session an order took answers with that order only its own cart")
	assert.Empty(t, readPayTRState(t, cartB).Orders)
}

// TestAPayTRPaymentForACartThatChangedIsRefunded: the cart grew after its
// payment was opened, so the payment PayTR reports can never place it; the
// report refuses the order, gives the money back and says so, and the
// storefront opens a new payment for the new total.
func TestAPayTRPaymentForACartThatChangedIsRefunded(t *testing.T) {
	cartID, total, _ := paytrCart(t, "PayTR stale product")
	paid := openPayment(t, cartID, paymentpaytr.ProviderID, total)
	grow(t, cartID, "PayTR stale extra")
	grown := int64(18_000)

	require.Equal(t, http.StatusOK, paytrCallback(t, paid.oid(), "success", total).Code)

	assert.Empty(t, readPayTRState(t, cartID).Orders, "the cart is not placed for the total it was paid before")
	assert.Equal(t, []string{"120.00"}, fakePayTRServer.refundedFor(paid.oid()),
		"what was paid for the old total goes back")
	outcome, _, _ := reportOf(t, paid.PaymentSessionID)
	assert.Equal(t, checkoutwf.ReportRefunded, outcome, "the report says the payment was refunded")

	stale := completeWithSession(t, cartID, paymentpaytr.ProviderID, paid.PaymentSessionID, grown)
	require.Equal(t, http.StatusConflict, stale.Code, stale.Body.String())
	assert.Equal(t, checkoutwf.CodePaymentSessionClosed, errorCode(t, stale))

	reopened := openPayment(t, cartID, paymentpaytr.ProviderID, grown)
	require.Equal(t, grown, reopened.Amount)
	require.NotEqual(t, paid.oid(), reopened.oid(), "the new payment is a new merchant_oid")
	require.Equal(t, http.StatusOK, paytrCallback(t, reopened.oid(), "success", grown).Code)

	state := readPayTRState(t, cartID)
	assert.Equal(t, []string{"pending"}, state.Orders)
	assert.Equal(t, []string{"paytr:canceled", "paytr:captured"}, state.Sessions)
	assert.Equal(t, []string{"paytr:18000"}, state.Captured)
}

// TestAPayTRPaymentForACartCompletedOtherwiseIsRefunded: the shopper paid the
// cart another way while the PayTR page was open, and then paid on it too; the
// second payment PayTR reports is given back.
func TestAPayTRPaymentForACartCompletedOtherwiseIsRefunded(t *testing.T) {
	cartID, total, _ := paytrCart(t, "PayTR double payment product")
	page := openPayment(t, cartID, paymentpaytr.ProviderID, total)
	other := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		fmt.Sprintf(`{"payment_provider_id":"manual","expected_total":%d}`, total))
	require.Equal(t, http.StatusOK, other.Code, other.Body.String())

	require.Equal(t, http.StatusOK, paytrCallback(t, page.oid(), "success", total).Code)

	assert.Equal(t, []string{"120.00"}, fakePayTRServer.refundedFor(page.oid()))
	state := readPayTRState(t, cartID)
	assert.Equal(t, []string{"pending"}, state.Orders, "the one order the other payment placed")
	assert.Equal(t, []string{"manual:12000"}, state.Captured)
	outcome, _, _ := reportOf(t, page.PaymentSessionID)
	assert.Equal(t, checkoutwf.ReportRefunded, outcome)
}

// TestAPayTRPaymentInInstallmentsPlacesTheOrderForItsTotal: PayTR's signed
// total carries the installment difference; the order, the authorization and
// the capture hold the cart's total, the session's data says what was paid,
// and a refund gives back at most the cart's total.
func TestAPayTRPaymentInInstallmentsPlacesTheOrderForItsTotal(t *testing.T) {
	ctx := t.Context()
	cartID, total, _ := paytrCart(t, "PayTR installment product")
	opened := openPayment(t, cartID, paymentpaytr.ProviderID, total)

	require.Equal(t, http.StatusOK, paytrCallback(t, opened.oid(), "success", total+500).Code)

	state := readPayTRState(t, cartID)
	assert.Equal(t, []string{"pending"}, state.Orders)
	assert.Equal(t, []string{"paytr:12000"}, state.Captured, "the shop captured the cart's total")
	assert.Equal(t, []string{"success:12500"}, state.PayTRRows, "the row keeps what PayTR said was paid")
	assert.Empty(t, fakePayTRServer.refundedFor(opened.oid()), "nothing was refunded")
	done := completeWithSession(t, cartID, paymentpaytr.ProviderID, opened.PaymentSessionID, total)
	require.Equal(t, http.StatusOK, done.Code, done.Body.String())
	assert.EqualValues(t, 12_000, storefrontData(t, done)["total"])

	session, err := paymentSvc.GetPaymentSession(ctx, opened.PaymentSessionID)
	require.NoError(t, err)
	assert.Equal(t, int64(12_000), session.AuthorizedAmount)
	assert.JSONEq(t, `{"paid_amount":12500,"installment_difference":500}`, string(session.Data),
		"the session says what was paid and what the installments cost")

	// A payment in installments that no order can take is given back for at
	// most the total.
	otherCart, otherTotal, _ := paytrCart(t, "PayTR installment refund product")
	unused := openPayment(t, otherCart, paymentpaytr.ProviderID, otherTotal)
	grow(t, otherCart, "PayTR installment refund extra")
	require.Equal(t, http.StatusOK, paytrCallback(t, unused.oid(), "success", otherTotal+500).Code)
	assert.Equal(t, []string{"120.00"}, fakePayTRServer.refundedFor(unused.oid()),
		"the shop gives back what it sold the order for, not the installment difference")
}

// TestAServerCompletionThatFailsIsTriedAgainAndReported: the stock was sold
// while the shopper paid, so the completion PayTR's report makes fails; the
// report counts the attempt and its next try, reconciliation reports the money
// held with no order meanwhile, and the sweep places the order once the stock
// is back.
func TestAServerCompletionThatFailsIsTriedAgainAndReported(t *testing.T) {
	ctx := t.Context()
	cartID, total, itemID := paytrCart(t, "PayTR retry product")
	opened := openPayment(t, cartID, paymentpaytr.ProviderID, total)
	_, err := inventorySvc.SetInventoryLevel(ctx, itemID, stockLocationID, 0)
	require.NoError(t, err)

	reported := time.Now()
	require.Equal(t, http.StatusOK, paytrCallback(t, opened.oid(), "success", total).Code)

	assert.Empty(t, readPayTRState(t, cartID).Orders, "no stock, no order")
	assert.Empty(t, fakePayTRServer.refundedFor(opened.oid()), "stock may come back, so nothing is refunded")
	outcome, attempts, next := reportOf(t, opened.PaymentSessionID)
	assert.Empty(t, outcome, "the report is still open")
	assert.Equal(t, 1, attempts)
	assert.WithinDuration(t, reported.Add(checkoutwf.ReportRetryBase), next, 30*time.Second,
		"the next try is one retry base away")

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE payment_sessions SET updated_at = now() - interval '1 hour' WHERE id = $1`, opened.PaymentSessionID)
	require.NoError(t, err)
	report, err := paymentSvc.Reconcile(ctx, 15*time.Minute, 50)
	require.NoError(t, err)
	var found bool
	for _, divergence := range report.Divergences {
		if divergence.SessionID == opened.PaymentSessionID {
			found = true
			assert.Equal(t, checkoutwf.SessionPending, string(divergence.LocalStatus))
			assert.Equal(t, int64(12_000), divergence.ProviderCaptured)
		}
	}
	require.True(t, found, "a payment held with no order is reported meanwhile: %+v", report)

	_, err = inventorySvc.SetInventoryLevel(ctx, itemID, stockLocationID, 5)
	require.NoError(t, err)
	early, err := orderWorkflows.CompleteReportedPayments(ctx, time.Now())
	require.NoError(t, err)
	assert.Empty(t, readPayTRState(t, cartID).Orders, "a sweep before the next try leaves it waiting: %+v", early)

	pass, err := orderWorkflows.CompleteReportedPayments(ctx, time.Now().Add(checkoutwf.ReportRetryBase+time.Second))

	require.NoError(t, err)
	assert.Positive(t, pass.Completed)
	assert.Equal(t, []string{"pending"}, readPayTRState(t, cartID).Orders, "the sweep placed the order")
	assert.Equal(t, []string{"paytr:12000"}, readPayTRState(t, cartID).Captured)
	outcome, _, _ = reportOf(t, opened.PaymentSessionID)
	assert.Equal(t, checkoutwf.ReportCompleted, outcome)
}

// TestAReportThatNeverArrivedIsFoundBySweep: PayTR's payment is recorded but
// its report never reached the checkout — a process that stopped between
// the two — and the sweep finds it by asking PayTR, and places the order.
func TestAReportThatNeverArrivedIsFoundBySweep(t *testing.T) {
	ctx := t.Context()
	cartID, total, _ := paytrCart(t, "PayTR lost report product")
	opened := openPayment(t, cartID, paymentpaytr.ProviderID, total)
	_, err := testPool.Pool().Exec(ctx, `
        UPDATE paytr_payment SET status = 'success', paid_amount = $2, callback_at = now()
        WHERE merchant_oid = $1`, opened.oid(), total)
	require.NoError(t, err)
	require.Empty(t, readPayTRState(t, cartID).Orders)

	pass, err := orderWorkflows.CompleteReportedPayments(ctx, time.Now())

	require.NoError(t, err)
	assert.Positive(t, pass.Discovered)
	assert.Equal(t, []string{"pending"}, readPayTRState(t, cartID).Orders)
	outcome, _, _ := reportOf(t, opened.PaymentSessionID)
	assert.Equal(t, checkoutwf.ReportCompleted, outcome)
}

// TestAPaymentMadeAfterItsSessionWasCanceledIsReported: the storefront
// canceled the payment while PayTR's page was still open, and the customer
// paid on it anyway. No cart is completed with a canceled session; the report
// is taken and says so, and reconciliation reports the money for the day
// after the cancel.
func TestAPaymentMadeAfterItsSessionWasCanceledIsReported(t *testing.T) {
	ctx := t.Context()
	cartID, total, _ := paytrCart(t, "PayTR late payment product")
	opened := openPayment(t, cartID, paymentpaytr.ProviderID, total)
	canceled := cancelPayment(t, cartID, opened.PaymentSessionID)
	require.Equal(t, http.StatusNoContent, canceled.Code, canceled.Body.String())

	callback := paytrCallback(t, opened.oid(), "success", total)

	require.Equal(t, http.StatusOK, callback.Code)
	require.Equal(t, "OK", callback.Body.String(), "PayTR is told the payment was recorded")
	state := readPayTRState(t, cartID)
	assert.Empty(t, state.Orders)
	assert.Equal(t, []string{"paytr:canceled"}, state.Sessions)
	assert.Equal(t, []string{"success:12000"}, state.PayTRRows)
	var reports int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM payment_session_reports WHERE session_id = $1`, opened.PaymentSessionID).Scan(&reports))
	assert.Zero(t, reports, "a canceled session completes no cart")

	_, err := testPool.Pool().Exec(ctx,
		`UPDATE payment_sessions SET updated_at = now() - interval '1 hour' WHERE id = $1`, opened.PaymentSessionID)
	require.NoError(t, err)
	report, err := paymentSvc.Reconcile(ctx, 15*time.Minute, 50)
	require.NoError(t, err)
	var found bool
	for _, divergence := range report.Divergences {
		if divergence.SessionID == opened.PaymentSessionID {
			found = true
			assert.Equal(t, int64(12_000), divergence.ProviderCaptured)
		}
	}
	assert.True(t, found, "the money taken on a canceled session is reported: %+v", report)
}

// TestADrivenProviderCanBeOpenedFirst: the opened payment is not PayTR's
// alone; a provider the module drives authorizes the opened session at the
// completion and opens no second one.
func TestADrivenProviderCanBeOpenedFirst(t *testing.T) {
	cartID, total, _ := paytrCart(t, "Opened manual product")
	opened := openPayment(t, cartID, "manual", total)
	require.Equal(t, checkoutwf.SessionPending, opened.Status)

	done := completeWithSession(t, cartID, "manual", opened.PaymentSessionID, total)

	require.Equal(t, http.StatusOK, done.Code, done.Body.String())
	state := readPayTRState(t, cartID)
	assert.Equal(t, []string{"manual:captured"}, state.Sessions, "the opened session is the only one")
	assert.Equal(t, []string{"manual:12000"}, state.Captured)
}

// TestAPayTROrderIDIsNotPaidTwice: a second session opened on a payment's
// collection — an operator's, through the admin route — reaches the same
// merchant_oid. Once PayTR reported on that order id and the payment went
// back, a second session is refused, so no order is placed on money that went
// back to the customer (ADR 0438).
func TestAPayTROrderIDIsNotPaidTwice(t *testing.T) {
	cartID, total, _ := paytrCart(t, "PayTR order id product")
	paid := openPayment(t, cartID, paymentpaytr.ProviderID, total)
	grow(t, cartID, "PayTR order id extra")
	require.Equal(t, http.StatusOK, paytrCallback(t, paid.oid(), "success", total).Code)
	require.Equal(t, []string{"120.00"}, fakePayTRServer.refundedFor(paid.oid()))

	again, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/payment-collections/"+paid.PaymentCollectionID+"/payment-sessions",
		map[string]any{
			"provider_id": paymentpaytr.ProviderID, "idempotency_key": "again-" + cartID,
			"data": map[string]string{"email": paytrEmail, "user_ip": paytrUserIP},
		})
	require.NoError(t, err)

	require.Equal(t, http.StatusConflict, again.Code, again.Body.String())
	assert.Equal(t, "paytr_order_id_taken", errorCode(t, again))
	assert.Equal(t, 1, fakePayTRServer.issuedFor(paid.oid()), "PayTR was not asked for a second token")
	state := readPayTRState(t, cartID)
	assert.Equal(t, []string{"paytr:canceled"}, state.Sessions, "no second session was written")
	assert.Empty(t, state.Orders)
}

// TestAPaymentIsCanceledOnlyThroughItsOwnCart: the cart is the storefront's
// capability, so another cart's session is not canceled through it, and a
// completed cart's payment is its order's (ADR 0438).
func TestAPaymentIsCanceledOnlyThroughItsOwnCart(t *testing.T) {
	cartA, totalA, _ := paytrCart(t, "PayTR cancel product A")
	cartB, _, _ := paytrCart(t, "PayTR cancel product B")
	sessionA := openPayment(t, cartA, paymentpaytr.ProviderID, totalA)

	foreign := cancelPayment(t, cartB, sessionA.PaymentSessionID)

	require.Equal(t, http.StatusConflict, foreign.Code, foreign.Body.String())
	assert.Equal(t, checkoutwf.CodePaymentSessionForeign, errorCode(t, foreign))
	assert.NotContains(t, foreign.Body.String(), cartA, "the refusal does not name the other cart")
	assert.Equal(t, []string{"paytr:pending"}, readPayTRState(t, cartA).Sessions)

	require.Equal(t, http.StatusOK, paytrCallback(t, sessionA.oid(), "success", totalA).Code)
	require.Equal(t, []string{"pending"}, readPayTRState(t, cartA).Orders)

	after := cancelPayment(t, cartA, sessionA.PaymentSessionID)

	assert.GreaterOrEqual(t, after.Code, http.StatusBadRequest, after.Body.String())
	assert.Empty(t, fakePayTRServer.refundedFor(sessionA.oid()), "a placed order's payment is not refunded here")
	assert.Equal(t, []string{"paytr:captured"}, readPayTRState(t, cartA).Sessions)
}
