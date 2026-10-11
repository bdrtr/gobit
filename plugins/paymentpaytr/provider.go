package paymentpaytr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
)

// ProviderID is the provider's id.
//
// It is written into payment sessions in the database, so changing it makes
// existing rows unresolvable. It is fixed across releases.
const ProviderID = "paytr"

// The PayTR endpoints this provider calls.
//
// The first one opens a payment; PayTR calls the thing it returns a token, and
// the constant is named for what the CALL does rather than for what comes back,
// so that a reader looking for "where do we start a payment" finds it.
const (
	pathOpenPayment = "/odeme/api/get-token"
	pathRefund      = "/odeme/iade"
)

// payTRSuccess is the value PayTR puts in its "status" field ON THE WIRE.
//
// It is a different thing from [statusSuccess], which is the status of a row in
// this plugin's own table; the two happen to be the same string today and there
// is no reason they must stay so. Keeping them apart means a change on PayTR's
// side cannot silently reinterpret rows already written.
const payTRSuccess = "success"

// requestTimeout bounds one call to PayTR.
const requestTimeout = 30 * time.Second

// Error codes.
const (
	codeSessionFailed  = "paytr_session_failed"
	codeNotAuthorized  = "paytr_not_authorized"
	codeRefundFailed   = "paytr_refund_failed"
	codeCannotCancel   = "paytr_cannot_cancel"
	codeAmountMismatch = "paytr_amount_mismatch"
	// codeOrderIDTaken refuses a session whose order id PayTR already reported
	// on, or that an earlier opening asked a different amount for.
	codeOrderIDTaken = "paytr_order_id_taken"
)

// The keys the caller may put in [coreprovider.CreateSessionInput.Data].
const (
	// DataKeyEmail is the customer's email; PayTR requires one.
	DataKeyEmail = "email"
	// DataKeyUserIP is the CUSTOMER's address, not the server's. PayTR signs it
	// and matches it against the browser that opens the iframe.
	DataKeyUserIP = "user_ip"
	// DataKeyBasket is the basket PayTR shows the customer, as a JSON array of
	// [name, unit price, quantity] triples. It is base64 encoded before signing.
	DataKeyBasket = "basket"
	// DataKeyMaxInstallment caps the installment count offered; "0" means
	// PayTR's own default.
	DataKeyMaxInstallment = "max_installment"
	// DataKeyNoInstallment set to "1" offers single payment only.
	DataKeyNoInstallment = "no_installment"
)

// DataKeyIframeToken is the key the iframe token is returned under in
// [coreprovider.Session.Data].
//
// The storefront reads it from the answer of
// POST /store/v1/carts/{id}/payment-sessions, which opens this session before
// the cart is completed, and opens PayTR's iframe with it (ADR 0438). It is
// short lived and this plugin keeps no copy of it.
const DataKeyIframeToken = "iframe_token"

// The keys of the data [provider.Authorize] returns once PayTR reported a
// payment, which the payment module keeps on the session.
//
// DataKeyPaidAmount is what PayTR said the customer paid, in minor units, and
// DataKeyInstallmentDifference is the part of it above the session's amount:
// the cost of paying in installments, which PayTR adds to the customer's total
// (its total_amount) and which is not the shop's money. The order, the
// collection and every capture and refund hold the session's amount; the
// difference is recorded here and in the plugin's row, and nowhere is it
// counted as revenue (ADR 0438).
const (
	DataKeyPaidAmount            = "paid_amount"
	DataKeyInstallmentDifference = "installment_difference"
)

// provider is the [coreprovider.PaymentProvider] implementation over PayTR.
type provider struct {
	cfg    config
	store  *store
	client *http.Client
	log    *slog.Logger
}

// The core contract is satisfied at compile time.
var _ coreprovider.PaymentProvider = (*provider)(nil)

// CustomerAuthorizes reports that PayTR's customer authorizes the payment in
// PayTR's own iframe, which PayTR then reports by posting back.
//
// The payment module finds it by a type assertion, and it is what makes the
// checkout open this provider's session BEFORE the cart is completed and
// refuse a completion that names no session (ADR 0438): opened inside the
// completion, the iframe token never left the server and the customer could
// never pay. A plugin cannot import the module's interface, so the pin that
// this method still answers is at the composition root.
func (p *provider) CustomerAuthorizes() bool { return true }

// The OPTIONAL reconciliation capability is pinned at compile time too.
//
// [coreprovider.SessionInspector] is found by a TYPE ASSERTION, so a drifted
// signature would break nothing: the reconciler would simply report that this
// provider cannot be asked, and a payment difference would stay invisible. This
// line closes that silence.
var _ coreprovider.SessionInspector = (*provider)(nil)

// ID returns the provider's id.
func (p *provider) ID() string { return ProviderID }

// CreateSession opens a payment at PayTR and returns the iframe token.
//
// # merchant_oid is the join, and PayTR constrains its alphabet
//
// PayTR accepts only letters and digits in the order id, so the caller's
// reference is normalized. The normalized value is what comes back in the
// callback and what [provider.Authorize] is later asked about, so it — not the
// original — is the session id.
//
// # One order id is one payment
//
// The reference is the payment collection, so a second session opened on the
// same collection reaches the same order id and the same row. While that row
// is pending it is the same payment page asked again, for the same amount. A
// row PayTR reported paid and nothing gave back is a payment no live session
// holds — the payment module opens a second session on a collection only when
// the first was closed and nothing was captured — so the new session adopts
// it, which is how a payment made after its session was canceled reaches an
// order. A row PayTR reported refused, or paid and given back, is money that
// never stayed, and a session reading it would place an order on it; such a
// session is refused, and the cart's payment is opened anew, on a collection
// of its own (ADR 0438).
func (p *provider) CreateSession(
	ctx context.Context, in coreprovider.CreateSessionInput,
) (coreprovider.Session, error) {
	oid := normalizeOID(in.Reference)
	if oid == "" {
		return coreprovider.Session{}, coreerrors.Invalid(codeSessionFailed,
			"the payment reference has no letters or digits; PayTR needs an alphanumeric order id")
	}

	// The form is built, and the customer's details checked, BEFORE the row is
	// written: a request PayTR could never accept leaves no row behind to be
	// listed as a payment PayTR never reported on.
	form, err := p.getTokenForm(oid, in)
	if err != nil {
		return coreprovider.Session{}, err
	}

	// The row is written BEFORE the call to PayTR. If it were written after, a
	// process that died in between would leave a customer able to pay against
	// an order id this system has never heard of, and the callback would arrive
	// for a row that does not exist.
	if err := p.store.open(ctx, payment{
		MerchantOID:  oid,
		Amount:       in.Amount,
		CurrencyCode: in.CurrencyCode,
	}); err != nil {
		return coreprovider.Session{}, err
	}
	rec, err := p.store.get(ctx, oid)
	if err != nil {
		return coreprovider.Session{}, err
	}
	reusable := rec.Status == statusPending || (rec.Status == statusSuccess && rec.RefundedAmount == 0)
	if !reusable || rec.Amount != in.Amount || rec.CurrencyCode != in.CurrencyCode {
		return coreprovider.Session{}, coreerrors.Conflict(codeOrderIDTaken,
			"PayTR order id %s is already a %s payment of %d %s; open the cart's payment anew",
			oid, rec.Status, rec.Amount, rec.CurrencyCode)
	}

	token, err := p.requestToken(ctx, form)
	if err != nil {
		return coreprovider.Session{}, err
	}

	data, err := json.Marshal(map[string]string{DataKeyIframeToken: token})
	if err != nil {
		return coreprovider.Session{}, coreerrors.Wrap(err, coreerrors.KindInternal, codeSessionFailed,
			"the session data could not be encoded")
	}

	return coreprovider.Session{
		ID:           oid,
		Status:       coreprovider.SessionPending,
		Amount:       in.Amount,
		CurrencyCode: in.CurrencyCode,
		Data:         data,
	}, nil
}

// getTokenForm builds and signs the get-token request.
func (p *provider) getTokenForm(oid string, in coreprovider.CreateSessionInput) (url.Values, error) {
	email, _ := in.Data[DataKeyEmail].(string)
	if strings.TrimSpace(email) == "" {
		return nil, coreerrors.Invalid(codeSessionFailed,
			"PayTR requires the customer's email; put it in the session data under %q", DataKeyEmail)
	}

	// The CUSTOMER's address, not the server's. PayTR signs it and matches it
	// against the browser that opens the iframe, so sending the server's makes
	// every payment fail with an error that names neither.
	userIP, _ := in.Data[DataKeyUserIP].(string)
	if strings.TrimSpace(userIP) == "" {
		return nil, coreerrors.Invalid(codeSessionFailed,
			"PayTR requires the CUSTOMER's ip address; put it in the session data under %q", DataKeyUserIP)
	}

	basket := encodeBasket(in.Data[DataKeyBasket])
	noInstallment := stringOr(in.Data[DataKeyNoInstallment], "0")
	maxInstallment := stringOr(in.Data[DataKeyMaxInstallment], "0")
	currency := payTRCurrency(in.CurrencyCode)

	signed := getTokenInput{
		MerchantID:     p.cfg.MerchantID,
		UserIP:         userIP,
		MerchantOID:    oid,
		Email:          email,
		PaymentAmount:  minorUnits(in.Amount),
		UserBasket:     basket,
		NoInstallment:  noInstallment,
		MaxInstallment: maxInstallment,
		Currency:       currency,
		TestMode:       p.cfg.TestMode,
	}

	form := url.Values{}
	form.Set("merchant_id", signed.MerchantID)
	form.Set("user_ip", signed.UserIP)
	form.Set("merchant_oid", signed.MerchantOID)
	form.Set("email", signed.Email)
	form.Set("payment_amount", signed.PaymentAmount)
	form.Set("user_basket", signed.UserBasket)
	form.Set("no_installment", signed.NoInstallment)
	form.Set("max_installment", signed.MaxInstallment)
	form.Set("currency", signed.Currency)
	form.Set("test_mode", signed.TestMode)
	form.Set("paytr_token", getTokenSignature(signed, p.cfg.MerchantKey, p.cfg.MerchantSalt))
	form.Set("merchant_ok_url", p.cfg.SuccessURL)
	form.Set("merchant_fail_url", p.cfg.FailureURL)
	// The fields below are outside the signature; PayTR reads them for display
	// and for the timeout, and a wrong value cannot alter what is being paid.
	form.Set("user_name", stringOr(in.Data["user_name"], "-"))
	form.Set("user_address", stringOr(in.Data["user_address"], "-"))
	form.Set("user_phone", stringOr(in.Data["user_phone"], "-"))
	form.Set("timeout_limit", "30")
	form.Set("debug_on", "0")

	return form, nil
}

// requestToken posts the signed form and reads the token out of the answer.
func (p *provider) requestToken(ctx context.Context, form url.Values) (string, error) {
	body, err := p.post(ctx, pathOpenPayment, form, codeSessionFailed)
	if err != nil {
		return "", err
	}

	var answer struct {
		Status string `json:"status"`
		Token  string `json:"token"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return "", coreerrors.Wrap(err, coreerrors.KindUnavailable, codeSessionFailed,
			"PayTR's answer could not be read as JSON")
	}
	if answer.Status != payTRSuccess || answer.Token == "" {
		// PayTR's own reason is carried: it is the only thing that
		// distinguishes a wrong signature from a disabled merchant account, and
		// the two need entirely different repairs.
		return "", coreerrors.Unavailable(codeSessionFailed,
			"PayTR refused to open the payment: %s", answer.Reason)
	}

	return answer.Token, nil
}

// Authorize answers whether the money is held, from what the callback recorded.
//
// # Why this reads a table instead of calling PayTR
//
// The contract asks the provider, given only a session id, whether the money is
// held — and expects it to know. A gateway that can be DRIVEN answers by making
// an API call. PayTR cannot be driven: the customer pays inside PayTR's iframe
// and PayTR reports the outcome by posting back, once, at a moment of its
// choosing. The answer to this question is therefore whatever the callback
// said, and it has to survive a restart between the two.
//
// A payment PayTR has not reported on is NOT an error and NOT a failure. It is
// Unavailable — "ask again" — because that is exactly the state: the customer
// may still be typing their card number. The checkout asks
// [provider.InspectSession] first and does not start while the answer is
// pending (ADR 0438), so this answer is met only by a caller that skipped that
// question.
func (p *provider) Authorize(ctx context.Context, sessionID string) (coreprovider.AuthResult, error) {
	rec, err := p.store.get(ctx, sessionID)
	if err != nil {
		return coreprovider.AuthResult{}, err
	}

	switch rec.Status {
	case statusSuccess:
		// A payment given back, wholly or in part, holds nothing an order can
		// be placed on: the only refund before an order is a cancel, which
		// returns all of it (ADR 0438). And a report of nothing paid is not a
		// hold of nothing — the payment module reads zero as "all of it".
		if rec.RefundedAmount > 0 || shopsPart(rec) <= 0 {
			return coreprovider.AuthResult{Status: coreprovider.SessionFailed},
				coreerrors.Invalid(codeNotAuthorized,
					"the PayTR payment holds nothing to authorize: %d paid, %d refunded",
					rec.PaidAmount, rec.RefundedAmount)
		}

		// PayTR takes the money at the moment it authorizes; there is no
		// separate hold. The status returned is Authorized rather than Captured
		// because the core's saga expects to capture next, and reporting
		// Captured here would make it skip a step it uses for its own
		// bookkeeping. What Capture then does is documented on that method.
		//
		// The amount held is the SHOP's part of what was paid (see
		// [shopsPart]): a customer paying in installments pays more than the
		// session asked, and reporting that would be a hold above the session,
		// which the payment module refuses as a contract violation and the
		// saga answers with a full refund (ADR 0438).
		data, err := paidData(rec)
		if err != nil {
			return coreprovider.AuthResult{}, err
		}

		return coreprovider.AuthResult{
			Status:           coreprovider.SessionAuthorized,
			AuthorizedAmount: shopsPart(rec),
			Data:             data,
		}, nil

	case statusFailed:
		return coreprovider.AuthResult{Status: coreprovider.SessionFailed},
			coreerrors.Invalid(codeNotAuthorized,
				"PayTR reported the payment as failed: %s", rec.FailureReason)

	default:
		return coreprovider.AuthResult{Status: coreprovider.SessionPending},
			coreerrors.Unavailable(codeNotAuthorized,
				"PayTR has not reported on this payment yet; the customer may not have finished")
	}
}

// Capture records the collection that PayTR already performed.
//
// # There is nothing to call
//
// PayTR has no separate capture: the money is taken at the moment the payment
// succeeds, which is what the callback reported. This method therefore verifies
// rather than acts — it confirms the payment really is successful and that the
// amount being captured is not more than was paid.
//
// Returning success without checking would be the tempting shortcut and it is
// the dangerous one: a capture for an amount larger than the customer paid
// would be recorded by the core as collected, and the shortfall would surface
// as an accounting difference weeks later.
func (p *provider) Capture(ctx context.Context, sessionID string, amount int64) error {
	rec, err := p.store.get(ctx, sessionID)
	if err != nil {
		return err
	}
	if rec.Status != statusSuccess {
		return coreerrors.Invalid(codeNotAuthorized,
			"the payment cannot be captured because PayTR did not report it as successful")
	}
	// What can be captured is the shop's part less what was given back; zero
	// asks for all of it, and all of nothing is refused too.
	left := shopsPart(rec) - rec.RefundedAmount
	if left <= 0 || amount > left {
		return coreerrors.Invalid(codeAmountMismatch,
			"the capture is larger than what PayTR collected for the order: %d requested, %d paid "+
				"of %d asked, %d refunded", amount, rec.PaidAmount, rec.Amount, rec.RefundedAmount)
	}

	return nil
}

// shopsPart is the shop's part of what PayTR reported the customer paid: what
// was paid, up to what the session asked.
//
// # The installment difference is not the shop's
//
// PayTR's callback signs total_amount, the customer's total, and when the
// customer pays in installments that total carries the installment difference
// on top of the order (PayTR documents it so; payment_amount, the order's own
// figure, is not signed and is not read). The difference is the cost of the
// installments, collected from the customer by PayTR, and the shop sold the
// order for the session's amount. So the session is authorized and captured for
// that amount, the order and the collection say so, and a refund through gobit
// gives back at most that amount; the row keeps what PayTR said was paid, and
// [provider.Authorize] puts both figures on the session (ADR 0438).
//
// Paid below the session's amount, the part is what was paid, and the full
// payment rule refuses the order and refunds it.
func shopsPart(rec payment) int64 {
	return min(rec.PaidAmount, rec.Amount)
}

// paidData is the data a reported payment puts on its session: what PayTR said
// was paid, and the installment difference when there is one.
func paidData(rec payment) (json.RawMessage, error) {
	paid := map[string]int64{DataKeyPaidAmount: rec.PaidAmount}
	if difference := rec.PaidAmount - rec.Amount; difference > 0 {
		paid[DataKeyInstallmentDifference] = difference
	}

	data, err := json.Marshal(paid)
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInternal, codeSessionFailed,
			"the reported payment could not be encoded")
	}

	return data, nil
}

// Refund sends money back through PayTR.
//
// The amount is in MAJOR units here and in minor units at get-token; the two
// formats live in one integration and the conversion is [majorUnits]. Sending
// the wrong one is off by a factor of a hundred and is ACCEPTED.
func (p *provider) Refund(ctx context.Context, sessionID string, amount int64) error {
	rec, err := p.store.get(ctx, sessionID)
	if err != nil {
		return err
	}
	if rec.Status != statusSuccess {
		return coreerrors.Invalid(codeRefundFailed,
			"a payment PayTR did not report as successful cannot be refunded")
	}

	// What can be refunded is the shop's part of the payment, never the
	// installment difference: the shop did not take it (see [shopsPart]).
	if amount <= 0 {
		amount = shopsPart(rec)
	}
	if remaining := shopsPart(rec) - rec.RefundedAmount; amount > remaining {
		// PayTR has no "how much is left to refund" query, so this check is the
		// only thing standing between a retried compensation and sending the
		// money back twice.
		return coreerrors.Invalid(codeAmountMismatch,
			"the refund is larger than what is left: %d requested, %d remaining", amount, remaining)
	}

	signed := refundInput{
		MerchantID:   p.cfg.MerchantID,
		MerchantOID:  sessionID,
		ReturnAmount: majorUnits(amount),
	}

	form := url.Values{}
	form.Set("merchant_id", signed.MerchantID)
	form.Set("merchant_oid", signed.MerchantOID)
	form.Set("return_amount", signed.ReturnAmount)
	form.Set("paytr_token", refundSignature(signed, p.cfg.MerchantKey, p.cfg.MerchantSalt))

	body, err := p.post(ctx, pathRefund, form, codeRefundFailed)
	if err != nil {
		return err
	}

	var answer struct {
		Status string `json:"status"`
		ErrNo  int    `json:"err_no"`
		ErrMsg string `json:"err_msg"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return coreerrors.Wrap(err, coreerrors.KindUnavailable, codeRefundFailed,
			"PayTR's refund answer could not be read as JSON")
	}
	if answer.Status != payTRSuccess {
		return coreerrors.Unavailable(codeRefundFailed,
			"PayTR refused the refund (%d): %s", answer.ErrNo, answer.ErrMsg)
	}

	// The ledger is written AFTER PayTR confirms. Writing it first would mean a
	// failed refund counted against the remaining balance, so a legitimate
	// retry would be refused for exceeding it.
	return p.store.addRefund(ctx, sessionID, amount)
}

// Cancel is the saga's compensation, and for PayTR it is a REFUND or nothing.
//
// # Why it does not fail
//
// The compensation must be idempotent — that is the saga's working condition,
// not a preference. A payment PayTR never reported on has nothing to undo, and
// saying so with an error would make the compensation retry forever on a
// checkout the customer simply abandoned.
//
// A payment that DID succeed is money already taken, so undoing it means
// sending it back. That is the same rule the fulfillment module states for a
// delivered shipment: some things cannot be canceled, only reversed.
func (p *provider) Cancel(ctx context.Context, sessionID string) error {
	rec, err := p.store.get(ctx, sessionID)
	if err != nil {
		if coreerrors.IsNotFound(err) {
			// A session that was never opened has nothing to undo. The
			// compensation succeeded by definition.
			return nil
		}

		return err
	}

	switch rec.Status {
	case statusSuccess:
		if rec.RefundedAmount >= shopsPart(rec) {
			// Already fully returned. A second compensation is a no-op rather
			// than a second refund.
			return nil
		}

		return p.Refund(ctx, sessionID, shopsPart(rec)-rec.RefundedAmount)

	case statusFailed, statusPending:
		// Nothing was taken, so nothing is undone. This is a SUCCESSFUL
		// compensation.
		return nil

	default:
		return coreerrors.Internal(codeCannotCancel,
			"the payment is in an unrecognized state: %q", rec.Status)
	}
}

// post sends a signed form to PayTR and returns the body.
func (p *provider) post(ctx context.Context, path string, form url.Values, code string) ([]byte, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, requestTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.cfg.BaseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInternal, code,
			"the PayTR request could not be built")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindUnavailable, code,
			"PayTR could not be reached")
	}
	defer resp.Body.Close() //nolint:errcheck // the outcome is decided by the body

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindUnavailable, code,
			"PayTR's answer could not be read")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, coreerrors.Unavailable(code,
			"PayTR answered with status %d", resp.StatusCode)
	}

	return body, nil
}

// normalizeOID reduces a reference to the alphabet PayTR accepts.
//
// PayTR takes letters and digits only in an order id. The value that comes back
// in the callback is this one, so it — not the caller's original — is the
// session id, which is why the normalization happens once and its result is
// returned rather than being redone on every comparison.
func normalizeOID(reference string) string {
	var b strings.Builder
	for _, r := range reference {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		}
	}

	return b.String()
}

// encodeBasket base64 encodes the basket PayTR displays.
//
// An absent basket becomes an empty JSON array rather than an error: the basket
// is what the customer SEES on PayTR's page, and a payment with an unhelpful
// summary is better than a payment that cannot be started.
func encodeBasket(raw any) string {
	if raw == nil {
		return base64.StdEncoding.EncodeToString([]byte("[]"))
	}
	if s, ok := raw.(string); ok {
		return base64.StdEncoding.EncodeToString([]byte(s))
	}

	encoded, err := json.Marshal(raw)
	if err != nil {
		return base64.StdEncoding.EncodeToString([]byte("[]"))
	}

	return base64.StdEncoding.EncodeToString(encoded)
}

// stringOr reads a string out of free-form data, with a fallback.
func stringOr(raw any, fallback string) string {
	if s, ok := raw.(string); ok && strings.TrimSpace(s) != "" {
		return s
	}
	if n, ok := raw.(int64); ok {
		return strconv.FormatInt(n, 10)
	}

	return fallback
}

// payTRCurrency maps an ISO 4217 code to what PayTR calls it.
//
// PayTR uses "TL" rather than "TRY" and rejects the ISO code. Everything else
// passes through: the mapping is one exception, not a table, and inventing a
// table would mean guessing at codes nobody has tried.
func payTRCurrency(code string) string {
	if strings.EqualFold(code, "TRY") {
		return "TL"
	}

	return strings.ToUpper(code)
}

// InspectSession returns what PayTR's callback recorded, in the core's neutral
// shape.
//
// # This is the one provider where the two ledgers are genuinely independent
//
// For most gateways an inspection is an API call. Here it reads this plugin's
// own table — and that is not a shortcut, it is the honest answer: PayTR
// reports the outcome ONCE, by posting back, and that report is the only
// statement PayTR ever makes about the payment. The row is not a cache of
// PayTR's ledger; it IS what PayTR said.
//
// The divergence reconciliation looks for is therefore between the PAYMENT
// MODULE's collection and this row, and those two really are written by
// different transactions at different moments: the callback lands whenever
// PayTR chooses, and the module's capture happens inside a transaction that can
// roll back after the money moved.
func (p *provider) InspectSession(
	ctx context.Context, sessionID string,
) (coreprovider.SessionInspection, error) {
	rec, err := p.store.get(ctx, sessionID)
	if err != nil {
		return coreprovider.SessionInspection{}, err
	}

	inspection := coreprovider.SessionInspection{
		RefundedAmount: rec.RefundedAmount,
	}

	switch rec.Status {
	case statusSuccess:
		// PayTR takes the money at the moment it authorizes; there is no
		// separate hold, so a successful payment is both authorized AND
		// captured. Reporting only one of the two would make the reconciler
		// read every PayTR payment as a divergence. The amount is the shop's
		// part, the one the module's ledger holds (see [shopsPart]). A payment
		// given back whole is reported canceled, its amounts kept: the money
		// went back, and a completion asking about it must not proceed as if
		// it were held (ADR 0438).
		inspection.Status = coreprovider.SessionCaptured
		if rec.RefundedAmount >= shopsPart(rec) {
			inspection.Status = coreprovider.SessionCanceled
		}
		inspection.AuthorizedAmount = shopsPart(rec)
		inspection.CapturedAmount = shopsPart(rec)
	case statusFailed:
		inspection.Status = coreprovider.SessionFailed
	default:
		inspection.Status = coreprovider.SessionPending
	}

	return inspection, nil
}
