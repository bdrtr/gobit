// Package webhook is the outbound webhook's wire: what gobit sends a receiver,
// and how the receiver proves gobit sent it.
//
// It is published (ADR 0435) because a receiver is a program outside this
// repository that has to NAME these values to compile: the headers it reads,
// the fields the signature covers, the function that checks it and the type
// the body decodes into. plugins/webhookout signs, heads and marshals every
// delivery with this package, so what is written here is what travels rather
// than a description of it. It imports the standard library alone; a
// receiver's module builds it without gobit's database, router or Redis code.
//
// The topics, and the payload fields each one carries, are NOT here. They are
// published the way an HTTP path is, in the served /openapi.json under
// "webhooks", because a receiver runs beside a gobit of some release: the
// document describes the server it is talking to, where a constant would
// describe the release it was compiled against.
//
// # What travels
//
//	POST <the registered URL>
//	Content-Type:      application/json
//	X-Gobit-Event:     order.placed
//	X-Gobit-Delivery:  whd_0J8K…          (stable across every retry)
//	X-Gobit-Attempt:   3                  (1 on the first try)
//	X-Gobit-Timestamp: 1757116800         (unix seconds of THIS attempt)
//	X-Gobit-Signature: v1=<standard base64 of the MAC>
//
// The body is a [Delivery], written as JSON.
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
//     timing, how much of a guessed signature was right. An EMPTY secret
//     verifies nothing: HMAC is defined over an empty key, so anybody can sign
//     under it, and gobit never issues one. A receiver whose secret is missing
//     refuses every delivery, and is better refusing to start.
//  4. Reject a X-Gobit-Timestamp further than a few minutes from now. This is
//     affordable because the timestamp belongs to the ATTEMPT, not to the
//     event: a delivery retried six hours later is signed six hours later, so a
//     tight window never rejects a legitimate retry.
//  5. Treat X-Gobit-Delivery as the idempotency key. It does not change between
//     retries, so a receiver that answered 200 and lost the answer sees the
//     same id again.
//
// [VerifySignature] is steps 2 and 3. Steps 4 and 5 stay the receiver's: the
// window is its own to choose, and the record of what it has seen is in its
// own store.
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
package webhook

import "time"

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

// Delivery is the JSON body of every delivery, and what a receiver decodes it
// into once [VerifySignature] has accepted the raw bytes.
//
// The sender marshals this type and nothing else, so its JSON names ARE the
// wire's. All seven are written on every delivery — none carries omitempty —
// and they are written in the order the fields are declared, which is the order
// the body had before the type was published: moving it here changed no byte a
// receiver had seen.
//
// What the payload under Data carries is per topic, and is published beside
// the paths: each topic's entry under "webhooks" in /openapi.json lists the
// fields its data may hold.
type Delivery struct {
	// ID is the delivery id, the same value as [HeaderDelivery]. It is the
	// receiver's idempotency key and it does not change between retries.
	ID string `json:"id"`
	// Event is the topic, the same value as [HeaderEvent].
	Event string `json:"event"`
	// EventID is the bus event's own id, for correlation with gobit's logs.
	EventID string `json:"event_id"`
	// OccurredAt is when the event happened, not when this attempt was made.
	OccurredAt time.Time `json:"occurred_at"`
	// Attempt is 1 on the first try, the same number as [HeaderAttempt].
	Attempt int64 `json:"attempt"`
	// Data is the event's payload as the bus carried it, minus the fields named
	// in Redacted and narrowed to the receiver's field list when it registered
	// one. Nothing is added to it.
	//
	// Its values are strings, the numeric ones and the timestamps too: the
	// bus's rule (docs/extending.md), which plugins/webhookout holds the
	// publisher of every topic it forwards to by type. The field stays
	// map[string]any because it is the bus's payload as stored and sent; a
	// conversion here would be a second place deciding what a value that is not
	// a string becomes.
	Data map[string]any `json:"data"`
	// Redacted names the payload fields that were withheld, and it is an array
	// on every delivery, empty when nothing was.
	//
	// It is in the body rather than omitted, and that is the whole point of
	// having it: a receiver that sees no customer_id cannot tell "this order
	// had no customer" from "we are not told". The list makes the difference
	// visible.
	Redacted []string `json:"redacted"`
}
