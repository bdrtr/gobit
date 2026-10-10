package webhook_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/core/webhook"
)

// The signature is the only credential a receiver has. These tests are about
// what a forged or shifted message must NOT be able to do with it.

// TestASignatureMatchesAnIndependentImplementation is the reference vector.
//
// It recomputes the MAC from the scheme as it is WRITTEN in this package's
// documentation, with the standard library and no call into [webhook.Sign].
// That is the point: a test that called Sign twice would agree with any change
// to Sign, including one that dropped a field from the signed string.
func TestASignatureMatchesAnIndependentImplementation(t *testing.T) {
	t.Parallel()

	fields := webhook.SignedFields{
		Timestamp:  "1757116800",
		DeliveryID: "whd_0J8K",
		Event:      "order.placed",
		Attempt:    "3",
		Body:       []byte(`{"id":"whd_0J8K"}`),
	}

	// The scheme, spelled out: len(part) + ":" + part for each of the six
	// parts, in this order, HMAC-SHA256, standard base64, "v1=" in front.
	var expected strings.Builder
	for _, part := range []string{
		"v1", fields.Timestamp, fields.DeliveryID, fields.Event, fields.Attempt,
		string(fields.Body),
	} {
		expected.WriteString(strconv.Itoa(len(part)) + ":" + part)
	}
	mac := hmac.New(sha256.New, []byte("s3cr3t"))
	mac.Write([]byte(expected.String()))
	want := "v1=" + base64.StdEncoding.EncodeToString(mac.Sum(nil))

	assert.Equal(t, want, webhook.Sign("s3cr3t", fields),
		"the signature no longer matches the scheme its own documentation describes; a "+
			"receiver implementing it from the prose would reject every delivery")
}

// TestTheLengthPrefixKeepsTwoMessagesApart is the reason the join is not a
// concatenation.
//
// Without a length prefix, moving a colon-bearing boundary between two adjacent
// fields produces the SAME signed string, so one signature would be valid for
// two different messages. That is how a body gets moved onto another event's
// signature.
func TestTheLengthPrefixKeepsTwoMessagesApart(t *testing.T) {
	t.Parallel()

	const secret = "s3cr3t"

	first := webhook.SignedFields{
		Timestamp: "1", DeliveryID: "whd_A", Event: "order.placed", Attempt: "1",
		Body: []byte("x"),
	}
	// The same characters, the boundary moved one place. A plain concatenation
	// makes these two identical.
	second := webhook.SignedFields{
		Timestamp: "1", DeliveryID: "whd_", Event: "Aorder.placed", Attempt: "1",
		Body: []byte("x"),
	}

	assert.NotEqual(t, webhook.Sign(secret, first), webhook.Sign(secret, second),
		"two different messages produced one signature: the length prefix is gone, and "+
			"with it the guarantee that a signature belongs to exactly one message")
}

// TestEverySignedFieldChangesTheSignature is the audit of the field set.
//
// A field that is in the header and not in the MAC is a field an attacker can
// rewrite in flight while the signature still verifies. The attempt number is
// the easy one to forget, and rewriting it is how a replay is disguised as a
// first delivery.
func TestEverySignedFieldChangesTheSignature(t *testing.T) {
	t.Parallel()

	const secret = "s3cr3t"
	base := webhook.SignedFields{
		Timestamp: "1757116800", DeliveryID: "whd_0J8K", Event: "order.placed",
		Attempt: "1", Body: []byte(`{"a":1}`),
	}
	signature := webhook.Sign(secret, base)

	mutations := map[string]webhook.SignedFields{
		"timestamp":   {Timestamp: "1757116801", DeliveryID: base.DeliveryID, Event: base.Event, Attempt: base.Attempt, Body: base.Body},
		"delivery id": {Timestamp: base.Timestamp, DeliveryID: "whd_OTHER", Event: base.Event, Attempt: base.Attempt, Body: base.Body},
		"event":       {Timestamp: base.Timestamp, DeliveryID: base.DeliveryID, Event: "product.created", Attempt: base.Attempt, Body: base.Body},
		"attempt":     {Timestamp: base.Timestamp, DeliveryID: base.DeliveryID, Event: base.Event, Attempt: "2", Body: base.Body},
		"body":        {Timestamp: base.Timestamp, DeliveryID: base.DeliveryID, Event: base.Event, Attempt: base.Attempt, Body: []byte(`{"a":2}`)},
	}

	for name, mutated := range mutations {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.False(t, webhook.VerifySignature(secret, signature, mutated),
				"the %s can be changed without breaking the signature, so it is not "+
					"covered by the MAC and a receiver believing it is believing an "+
					"attacker", name)
		})
	}
}

// TestTheWrongSecretDoesNotVerify is the whole point of the secret being
// per-receiver.
//
// Two receivers hold two keys, so a delivery captured on the way to one cannot
// be replayed at the other. A shared installation-wide secret would make every
// registered receiver able to forge deliveries to every other.
func TestTheWrongSecretDoesNotVerify(t *testing.T) {
	t.Parallel()

	fields := webhook.SignedFields{
		Timestamp: "1", DeliveryID: "whd_A", Event: "order.placed", Attempt: "1",
		Body: []byte("{}"),
	}

	signature := webhook.Sign("first-receivers-secret", fields)

	assert.False(t, webhook.VerifySignature("second-receivers-secret", signature, fields),
		"a delivery signed for one receiver verified at another; the secrets are not "+
			"actually separating them")
	assert.True(t, webhook.VerifySignature("first-receivers-secret", signature, fields))
}

// TestAnEmptySecretVerifiesNothing is the receiver that lost its configuration.
//
// The empty string is a key anybody can sign under, and gobit never issues it,
// so a receiver calling with one has a variable missing from its deployment.
// A forgery signed under that key is the delivery an attacker sends it, and it
// has to be refused, as must a delivery gobit really signed: under an empty
// secret there is nothing to tell the two apart with.
func TestAnEmptySecretVerifiesNothing(t *testing.T) {
	t.Parallel()

	forged := webhook.SignedFields{
		Timestamp: "1757116800", DeliveryID: "whd_FORGED", Event: "order.placed", Attempt: "1",
		Body: []byte(`{"event":"order.placed","data":{"order_id":"ord_none"}}`),
	}

	assert.False(t, webhook.VerifySignature("", webhook.Sign("", forged), forged),
		"a delivery signed by an attacker under the empty key verified under an empty "+
			"secret; a receiver missing its secret would accept every forgery")
	assert.False(t, webhook.VerifySignature("", webhook.Sign("s3cr3t", forged), forged),
		"an empty secret verified a signature it cannot have checked")
}

// TestASignatureUnderAKeyTheAttackerKnowsDoesNotVerify is the forgery the
// shared secret exists to stop: a delivery signed under any key but the
// receiver's, the empty one included, is refused by the receiver's secret.
func TestASignatureUnderAKeyTheAttackerKnowsDoesNotVerify(t *testing.T) {
	t.Parallel()

	forged := webhook.SignedFields{
		Timestamp: "1757116800", DeliveryID: "whd_FORGED", Event: "order.placed", Attempt: "1",
		Body: []byte(`{"event":"order.placed"}`),
	}

	for _, key := range []string{"", "the attacker's key", "s3cr3t "} {
		assert.False(t, webhook.VerifySignature("s3cr3t", webhook.Sign(key, forged), forged),
			"a delivery signed under %q verified under the receiver's secret", key)
	}
}
