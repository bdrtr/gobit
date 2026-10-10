package webhook_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/webhook"
)

// The names in this package are a promise twice over: once to the compiler of a
// receiver that names them, and once to every receiver that never imports Go
// and reads the same strings off the wire. The inventory in internal/arch reads
// the first — which NAMES exist — and cannot see the second, so a constant
// whose value changed under an unchanged name would leave every gate green and
// every receiver deaf. These tests pin the values, written out as literals, so
// that changing one is a diff to this file and not a side effect.

// TestTheWireNamesAreTheWrittenOnes pins the header names and the version.
//
// A receiver in another language has these strings in its source and nothing
// else; renaming X-Gobit-Event to X-Gobit-Topic would compile everywhere and be
// read by none of them.
func TestTheWireNamesAreTheWrittenOnes(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "v1", webhook.SignatureVersion,
		"the version is the first signed part and the header's prefix; changing it "+
			"changes every signature a receiver checks")

	for _, header := range []struct{ got, want string }{
		{webhook.HeaderEvent, "X-Gobit-Event"},
		{webhook.HeaderDelivery, "X-Gobit-Delivery"},
		{webhook.HeaderAttempt, "X-Gobit-Attempt"},
		{webhook.HeaderTimestamp, "X-Gobit-Timestamp"},
		{webhook.HeaderSignature, "X-Gobit-Signature"},
	} {
		assert.Equal(t, header.want, header.got,
			"a header a receiver reads by name changed its name; every receiver built "+
				"against the old one now reads an empty value")
	}
}

// TestADeliveryTravelsUnderItsSevenNames pins the body's bytes.
//
// The comparison is of the encoded TEXT, not of a decoded map, because the text
// is what a receiver signs over and parses: it fails on a renamed key, on a key
// that disappears when its value is empty, and on the keys changing order, and
// the last is the promise the type's godoc makes about the bytes the body had
// before it was published.
//
// The second case is the one omitempty would bite. A delivery with nothing
// withheld still says so, with an empty list, because "redacted" is how a
// receiver tells "not told" from "absent".
func TestADeliveryTravelsUnderItsSevenNames(t *testing.T) {
	t.Parallel()

	occurred := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

	for name, tc := range map[string]struct {
		delivery webhook.Delivery
		want     string
	}{
		"a field withheld": {
			delivery: webhook.Delivery{
				ID: "whd_1", Event: "order.placed", EventID: "order.placed:ord_1",
				OccurredAt: occurred, Attempt: 2,
				Data:     map[string]any{"order_id": "ord_1"},
				Redacted: []string{"customer_id"},
			},
			want: `{"id":"whd_1","event":"order.placed","event_id":"order.placed:ord_1",` +
				`"occurred_at":"2026-10-10T12:00:00Z","attempt":2,"data":{"order_id":"ord_1"},` +
				`"redacted":["customer_id"]}`,
		},
		"nothing withheld": {
			delivery: webhook.Delivery{
				ID: "whd_2", Event: "product.created", EventID: "product.created:prd_1",
				OccurredAt: occurred, Attempt: 1,
				Data:     map[string]any{},
				Redacted: []string{},
			},
			want: `{"id":"whd_2","event":"product.created","event_id":"product.created:prd_1",` +
				`"occurred_at":"2026-10-10T12:00:00Z","attempt":1,"data":{},"redacted":[]}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			raw, err := json.Marshal(tc.delivery)
			require.NoError(t, err)

			assert.Equal(t, tc.want, string(raw),
				"a delivery no longer travels as its seven names, in their order, every "+
					"one of them present; a receiver parsing the published shape reads "+
					"something else")
		})
	}
}
