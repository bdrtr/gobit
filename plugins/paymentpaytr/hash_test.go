package paymentpaytr

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// PayTR's three signatures decide whether money moves, and a wrong one is
// refused by PayTR with a message that does not name the field that was wrong.
// Worse, two of the three follow one rule and the third follows another — the
// callback puts the salt INSIDE the body while the rest append it — so a
// reader who learns the pattern from two of them writes the third wrong.
//
// The expected values below are NOT this package's output. They come from the
// locked vectors of a working PayTR integration, computed there from an
// independent HMAC-SHA256 against fixed credentials. That is what makes them
// evidence rather than a restatement of this code.

// The fixed credentials the vectors were computed against.
const (
	vecMerchantID  = "123456"
	vecMerchantKey = "TESTKEY"
	vecSalt        = "TESTSALT"
)

// TestTheGetTokenSignatureMatchesTheVector pins the field order of the request
// that opens a payment.
//
// Ten fields concatenated with no separator: any two of them swapped produces a
// different signature and an error from PayTR that says only that the token is
// invalid.
func TestTheGetTokenSignatureMatchesTheVector(t *testing.T) {
	got := getTokenSignature(getTokenInput{
		MerchantID:     vecMerchantID,
		UserIP:         "1.2.3.4",
		MerchantOID:    "ORDER1",
		Email:          "a@b.com",
		PaymentAmount:  "10000",
		UserBasket:     "W10=",
		NoInstallment:  "0",
		MaxInstallment: "0",
		Currency:       "TL",
		TestMode:       "1",
	}, vecMerchantKey, vecSalt)

	assert.Equal(t, "Ie9bXfkWlQC2AhLsZedqeqi80MorpL4bNUgZpmzO9bo=", got,
		"the get-token signature does not match the reference vector; the field order or the "+
			"salt position is wrong and PayTR will refuse every payment")
}

// TestTheCallbackSignatureMatchesTheVector pins the ASYMMETRIC one.
//
// The callback puts merchant_salt INSIDE the body, right after the order id,
// instead of appending it the way every other PayTR signature does. Applying
// the usual rule here produces a verifier that rejects every genuine callback —
// and PayTR retries a callback it believes was not acknowledged, so the visible
// symptom is "PayTR keeps calling us and no payment ever completes".
func TestTheCallbackSignatureMatchesTheVector(t *testing.T) {
	got := callbackSignature(callbackInput{
		MerchantOID: "ORDER1",
		Status:      "success",
		TotalAmount: "10000",
	}, vecMerchantKey, vecSalt)

	assert.Equal(t, "T+6GfkKNzrJ/vkpvCGEu24AAxbbrZTtcxalMYYB29dM=", got,
		"the callback signature does not match the reference vector; if the salt was appended "+
			"instead of placed after the order id, every genuine callback is rejected")
}

// TestTheCallbackSignatureIsNotTheAppendedForm proves the asymmetry is real
// rather than a coincidence of these inputs.
//
// Without this, a future edit that "tidied up" the callback to match the other
// two would still pass the vector test only if the vector happened to agree —
// it does not, and this states so.
func TestTheCallbackSignatureIsNotTheAppendedForm(t *testing.T) {
	in := callbackInput{MerchantOID: "ORDER1", Status: "success", TotalAmount: "10000"}

	correct := callbackSignature(in, vecMerchantKey, vecSalt)
	appended := signWithAppendedSalt(
		in.MerchantOID+in.Status+in.TotalAmount, vecMerchantKey, vecSalt)

	assert.NotEqual(t, appended, correct,
		"the callback signature must NOT be the appended-salt form the other requests use")
}

// TestTheRefundSignatureMatchesTheVector pins the refund request.
func TestTheRefundSignatureMatchesTheVector(t *testing.T) {
	got := refundSignature(refundInput{
		MerchantID:   vecMerchantID,
		MerchantOID:  "ORDER1",
		ReturnAmount: "50.00",
	}, vecMerchantKey, vecSalt)

	assert.Equal(t, "9qUqdodrBR2hFt5qnBXpniwnsDe+nPgQRL3tJKT++N8=", got,
		"the refund signature does not match the reference vector")
}

// TestAChangedFieldChangesEverySignature proves the signatures actually cover
// what they are supposed to cover.
//
// Without it the vector tests would still pass if a field were dropped from the
// body entirely, as long as the vector's value for that field happened to be
// the one omitted.
func TestAChangedFieldChangesEverySignature(t *testing.T) {
	base := getTokenInput{
		MerchantID: vecMerchantID, UserIP: "1.2.3.4", MerchantOID: "ORDER1",
		Email: "a@b.com", PaymentAmount: "10000", UserBasket: "W10=",
		NoInstallment: "0", MaxInstallment: "0", Currency: "TL", TestMode: "1",
	}
	reference := getTokenSignature(base, vecMerchantKey, vecSalt)

	for name, mutate := range map[string]func(*getTokenInput){
		"the amount":    func(i *getTokenInput) { i.PaymentAmount = "10001" },
		"the order id":  func(i *getTokenInput) { i.MerchantOID = "ORDER2" },
		"the basket":    func(i *getTokenInput) { i.UserBasket = "W1td" },
		"the currency":  func(i *getTokenInput) { i.Currency = "USD" },
		"the client ip": func(i *getTokenInput) { i.UserIP = "5.6.7.8" },
		"the email":     func(i *getTokenInput) { i.Email = "c@d.com" },
		"the test mode": func(i *getTokenInput) { i.TestMode = "0" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)

			assert.NotEqual(t, reference, getTokenSignature(changed, vecMerchantKey, vecSalt),
				"%s is not covered by the signature; PayTR would accept a request whose %s was altered",
				name, name)
		})
	}
}

// TestTheSaltAndTheKeyPlayDifferentRoles proves the two secrets are not
// interchangeable.
//
// Swapping them is an easy mistake — both are opaque strings from the same
// dashboard page — and it produces a signature that is well formed and always
// refused.
func TestTheSaltAndTheKeyPlayDifferentRoles(t *testing.T) {
	in := refundInput{MerchantID: vecMerchantID, MerchantOID: "ORDER1", ReturnAmount: "50.00"}

	correct := refundSignature(in, vecMerchantKey, vecSalt)
	swapped := refundSignature(in, vecSalt, vecMerchantKey)

	assert.NotEqual(t, swapped, correct,
		"the merchant key signs and the salt is signed; swapping them has to change the result")
}

// TestTheAmountIsFormattedTheWayPayTRExpects pins the two different amount
// formats in one integration.
//
// get-token takes MINOR units as an integer string ("10000" is 100.00 TL) while
// the refund takes MAJOR units with two decimals ("50.00"). Sending one where
// the other belongs is off by a factor of a hundred, is accepted by PayTR, and
// refunds a hundred times too much or too little.
func TestTheAmountIsFormattedTheWayPayTRExpects(t *testing.T) {
	assert.Equal(t, "10000", minorUnits(10000),
		"get-token takes the minor unit integer unchanged")
	assert.Equal(t, "100.00", majorUnits(10000),
		"the refund takes major units with two decimals")
	assert.Equal(t, "0.05", majorUnits(5))
	assert.Equal(t, "0.00", majorUnits(0))
	assert.Equal(t, "12345678.90", majorUnits(1234567890))
}
