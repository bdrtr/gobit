package paymentpaytr

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
)

// PayTR's request signatures.
//
// Every request that moves money carries one, and a wrong signature is refused
// with a message that does not name the field that was wrong. The formulas are
// therefore in ONE file, with the reference vectors in hash_test.go beside
// them: a field order learned from reading a call site is a field order that
// drifts.
//
// # The rule, and its one exception
//
// Almost every PayTR signature is
//
//	base64(HMAC-SHA256(<body> + merchant_salt, merchant_key))
//
// The CALLBACK is not. It puts the salt INSIDE the body, immediately after the
// order id, and appends nothing:
//
//	base64(HMAC-SHA256(merchant_oid + merchant_salt + status + total_amount, merchant_key))
//
// That asymmetry is the trap this file exists to contain. Applying the general
// rule to the callback produces a verifier that rejects every genuine
// notification — and PayTR retries a callback it believes was not
// acknowledged, so what an operator sees is "PayTR keeps calling and no payment
// ever completes", with a signature mismatch logged that reads like an attack.

// getTokenInput is the body of the request that opens a payment.
//
// The fields are in SIGNATURE ORDER, and the order is the whole point: they are
// concatenated with no separator, so swapping any two produces a valid-looking
// signature that PayTR refuses.
type getTokenInput struct {
	MerchantID     string
	UserIP         string
	MerchantOID    string
	Email          string
	PaymentAmount  string
	UserBasket     string
	NoInstallment  string
	MaxInstallment string
	Currency       string
	TestMode       string
}

// getTokenSignature signs the request that opens a payment.
func getTokenSignature(in getTokenInput, merchantKey, merchantSalt string) string {
	body := in.MerchantID + in.UserIP + in.MerchantOID + in.Email +
		in.PaymentAmount + in.UserBasket + in.NoInstallment + in.MaxInstallment +
		in.Currency + in.TestMode

	return signWithAppendedSalt(body, merchantKey, merchantSalt)
}

// callbackInput is what PayTR posts back when a payment finishes.
type callbackInput struct {
	MerchantOID string
	Status      string
	TotalAmount string
}

// callbackSignature verifies a notification from PayTR.
//
// The salt sits INSIDE the body here rather than being appended. It is the one
// place in the integration where the general rule does not apply, and it is
// written out separately for that reason instead of reusing
// [signWithAppendedSalt] with a rearranged body — a shared helper would invite
// the next reader to "fix" the inconsistency.
func callbackSignature(in callbackInput, merchantKey, merchantSalt string) string {
	body := in.MerchantOID + merchantSalt + in.Status + in.TotalAmount

	mac := hmac.New(sha256.New, []byte(merchantKey))
	mac.Write([]byte(body))

	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// refundInput is the body of a refund request.
type refundInput struct {
	MerchantID   string
	MerchantOID  string
	ReturnAmount string
}

// refundSignature signs a refund.
func refundSignature(in refundInput, merchantKey, merchantSalt string) string {
	return signWithAppendedSalt(
		in.MerchantID+in.MerchantOID+in.ReturnAmount, merchantKey, merchantSalt)
}

// signWithAppendedSalt is PayTR's general signature form.
func signWithAppendedSalt(body, merchantKey, merchantSalt string) string {
	mac := hmac.New(sha256.New, []byte(merchantKey))
	mac.Write([]byte(body + merchantSalt))

	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// signaturesMatch compares two signatures in constant time.
//
// A byte-by-byte comparison that returns early leaks, through timing, how much
// of a guessed signature was right — which turns forging a callback from
// impossible into merely expensive. The callback is the message that tells this
// system a payment succeeded, so it is exactly the message worth forging.
func signaturesMatch(expected, received string) bool {
	return hmac.Equal([]byte(expected), []byte(received))
}

// minorUnits renders an amount the way get-token wants it: the minor unit
// integer, unchanged.
func minorUnits(amount int64) string {
	return strconv.FormatInt(amount, 10)
}

// majorUnits renders an amount the way the REFUND endpoint wants it: major
// units with exactly two decimals.
//
// # Two formats in one integration
//
// get-token takes "10000" for one hundred lira; the refund takes "100.00" for
// the same amount. Sending one where the other belongs is off by a factor of a
// hundred, is ACCEPTED by PayTR, and refunds a hundred times too much or too
// little.
//
// The conversion is done with integer arithmetic rather than by dividing into a
// float. Money never passes through a float in this repository (plan Section
// 8), and the reason applies with full force here: the result of this function
// is the amount a customer gets back.
func majorUnits(amount int64) string {
	negative := amount < 0
	if negative {
		amount = -amount
	}

	whole := amount / 100
	fraction := amount % 100

	var b strings.Builder
	if negative {
		b.WriteByte('-')
	}
	b.WriteString(strconv.FormatInt(whole, 10))
	b.WriteByte('.')
	if fraction < 10 {
		b.WriteByte('0')
	}
	b.WriteString(strconv.FormatInt(fraction, 10))

	return b.String()
}
