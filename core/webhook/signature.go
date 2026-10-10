package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
)

// SignedFields are the values that go into the MAC, in order.
//
// It is a struct rather than five arguments because the order IS the scheme: a
// caller that transposes two positional strings produces a signature no
// receiver can reproduce, and the mismatch surfaces at the receiver as "gobit
// is sending garbage" rather than as a bug on the side that made it.
//
// Every field is the exact text of the header it names, so a receiver fills
// this in straight from the request with no conversion at all.
type SignedFields struct {
	// Timestamp is the [HeaderTimestamp] value.
	Timestamp string
	// DeliveryID is the [HeaderDelivery] value.
	DeliveryID string
	// Event is the [HeaderEvent] value.
	Event string
	// Attempt is the [HeaderAttempt] value.
	Attempt string
	// Body is the exact bytes of the request body.
	Body []byte
}

// signingString builds the length-prefixed string the MAC is taken over.
//
// Every part is written as its byte length, a colon and the part itself. The
// body goes in last and unmodified; nothing here re-encodes it.
func signingString(f SignedFields) string {
	parts := []string{
		SignatureVersion,
		f.Timestamp,
		f.DeliveryID,
		f.Event,
		f.Attempt,
		string(f.Body),
	}

	var b strings.Builder
	for _, part := range parts {
		b.WriteString(strconv.Itoa(len(part)))
		b.WriteString(":")
		b.WriteString(part)
	}

	return b.String()
}

// Sign returns the value of the [HeaderSignature] header.
//
// The sender calls it on every attempt; a receiver needs it only to test its
// own handler with a delivery it built.
func Sign(secret string, f SignedFields) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingString(f)))

	return SignatureVersion + "=" + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// VerifySignature reports whether a header value was produced by signing these
// fields with this secret.
//
// It is published, together with [Sign] and [SignedFields], because the
// receiver of a gobit webhook is very often another Go service written by the
// same team. Handing them this function is what stops the check being
// re-derived from the package documentation — and a check re-derived slightly
// wrong is one that accepts everything, which is indistinguishable from
// working.
//
// The comparison is constant time. The signature is the only credential a
// receiver has, and an early-returning comparison leaks how much of a guess was
// right, which turns forging a delivery from impossible into merely expensive.
//
// An EMPTY secret verifies nothing. HMAC is defined over an empty key, so the
// empty string is a key everybody knows, and gobit never issues it: a receiver
// that passes one has lost its configuration — a variable missing from one
// deployment — and accepting what anybody can sign under it would turn that
// mistake into a door. Refusing is the answer a check re-derived wrongly would
// not give.
//
// It checks the MAC and nothing else: rejecting a stale timestamp and dropping
// a delivery id already seen are the receiver's (the package documentation's
// steps 4 and 5).
func VerifySignature(secret, header string, f SignedFields) bool {
	if secret == "" {
		return false
	}

	return hmac.Equal([]byte(Sign(secret, f)), []byte(header))
}
