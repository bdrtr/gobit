package webhookout

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
)

// The signature scheme, written out so a receiver can implement the check
// without reading Go.
//
// # What travels
//
//	POST <the registered URL>
//	Content-Type:      application/json
//	User-Agent:        gobit-webhooks/1
//	X-Gobit-Event:     order.placed
//	X-Gobit-Delivery:  whd_0J8K…          (stable across every retry)
//	X-Gobit-Attempt:   3                  (1 on the first try)
//	X-Gobit-Timestamp: 1757116800         (unix seconds of THIS attempt)
//	X-Gobit-Signature: v1=<standard base64 of the MAC>
//
// # What is signed
//
// Not the body alone. Six values are joined and the join is LENGTH-PREFIXED —
// each part is written as `len(part) + ":" + part`, with nothing between the
// parts:
//
//	SignatureVersion, X-Gobit-Timestamp, X-Gobit-Delivery,
//	X-Gobit-Event, X-Gobit-Attempt, <the raw body bytes>
//
// The MAC is HMAC-SHA256 over that string, keyed with the endpoint's secret,
// and the header carries it as `v1=` followed by standard base64.
//
// The length prefix is the same rule the INBOUND callback ring uses when it
// builds a replay key (ADR 0028, core/http/callback_guard.go): without it a
// value containing the separator can make two different messages produce one
// signed string, and here that would let a body be moved onto another event's
// signature. Nothing in the current field set contains a colon, which is
// exactly why the rule has to be in the code rather than in the assumption.
//
// # Every signed field is signed AS THE TEXT THAT TRAVELS
//
// The timestamp and the attempt number are numbers, and they are signed as the
// header strings rather than as integers. It is not a shortcut: "03" and "3"
// are one number and two different byte strings, so a side that parses before
// verifying has already changed what was signed. Parsing is the receiver's
// business after the MAC matches, never before.
//
// # How a receiver checks it
//
//  1. Read the raw body BEFORE parsing it. A MAC over re-serialized JSON is a
//     MAC over a different byte string; key ordering and number formatting are
//     not stable across languages.
//  2. Rebuild the signed string from the four headers and the body.
//  3. Compute HMAC-SHA256 with the shared secret and compare in CONSTANT TIME
//     against the whole header value. A comparison that returns early leaks, by
//     timing, how much of a guessed signature was right.
//  4. Reject a X-Gobit-Timestamp further than a few minutes from now. This is
//     affordable because the timestamp belongs to the ATTEMPT, not to the
//     event: a delivery retried six hours later is signed six hours later, so a
//     tight window never rejects a legitimate retry.
//  5. Treat X-Gobit-Delivery as the idempotency key. It does not change between
//     retries, so a receiver that answered 200 and lost the answer sees the
//     same id again.
//
// # Why this and not something else
//
// The primitive is the one the repository already has on the inbound side —
// HMAC-SHA256 with a shared secret, compared with [hmac.Equal] — rather than a
// signature over an asymmetric key. Inventing a second convention for the
// outbound direction would mean an installation reasoning about two schemes,
// and the value of the second one is a key distribution problem this repository
// has no answer for.
//
// Step 4 is the one thing this side does that the inbound ring could not. ADR
// 0028 records that it has "no freshness window ... it can only be required of
// a provider that SIGNS a timestamp, and PayTR does not". Here gobit IS the
// provider, so it signs one, and the gap that decision had to leave open is
// closed in the direction where closing it was available.

// SignatureVersion prefixes the header value and is the FIRST signed field.
//
// It is signed rather than merely written so that a second scheme can never be
// downgraded into this one: a receiver that accepts both would otherwise verify
// a v2 message against the v1 rule and find it valid.
const SignatureVersion = "v1"

// The header names a receiver reads.
const (
	// HeaderEvent carries the topic ("order.placed").
	HeaderEvent = "X-Gobit-Event"
	// HeaderDelivery carries the delivery id. It is STABLE across retries and
	// is what a receiver deduplicates on.
	HeaderDelivery = "X-Gobit-Delivery"
	// HeaderAttempt carries the 1-based attempt number.
	HeaderAttempt = "X-Gobit-Attempt"
	// HeaderTimestamp carries the unix seconds of this attempt.
	HeaderTimestamp = "X-Gobit-Timestamp"
	// HeaderSignature carries the MAC, as "v1=<base64>".
	HeaderSignature = "X-Gobit-Signature"
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
func Sign(secret string, f SignedFields) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingString(f)))

	return SignatureVersion + "=" + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// VerifySignature reports whether a header value was produced by signing these
// fields with this secret.
//
// It is EXPORTED, together with [Sign] and [SignedFields], because the receiver
// of a gobit webhook is very often another Go service written by the same team.
// Handing them this function is what stops the check being re-derived from the
// prose above — and a check re-derived slightly wrong is one that accepts
// everything, which is indistinguishable from working.
//
// The comparison is constant time. The signature is the only credential a
// receiver has, and an early-returning comparison leaks how much of a guess was
// right, which turns forging a delivery from impossible into merely expensive.
func VerifySignature(secret, header string, f SignedFields) bool {
	return hmac.Equal([]byte(Sign(secret, f)), []byte(header))
}
