package webhookout

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/core/webhook"
)

// TestTheOldNamesAreTheNewOnes holds the deprecated names to what they promise:
// that a receiver built against them keeps working, unchanged, until it moves.
//
// A deprecated alias is a name nobody in this tree calls any more, which is
// exactly the code a mistake survives in. The worst one available is the
// verifier: an alias that accepted one forgery the new name refuses would
// leave every receiver still on the old name open while compiling cleanly. So
// the two verifiers are asked the SAME questions, from one table — a genuine
// delivery, a rewritten one, another receiver's secret, a forgery signed under
// the empty key, and an empty secret — and each must give the table's answer.
// A value built under the old type's name is handed to the new functions,
// which compiles only while the alias is the same type.
//
// It is an internal test on purpose. An outside package naming these would be
// flagged by the linter, which is the point of deprecating them; this package
// is where they are defined.
func TestTheOldNamesAreTheNewOnes(t *testing.T) {
	t.Parallel()

	for _, name := range []struct{ old, current string }{
		{SignatureVersion, webhook.SignatureVersion},
		{HeaderEvent, webhook.HeaderEvent},
		{HeaderDelivery, webhook.HeaderDelivery},
		{HeaderAttempt, webhook.HeaderAttempt},
		{HeaderTimestamp, webhook.HeaderTimestamp},
		{HeaderSignature, webhook.HeaderSignature},
	} {
		assert.Equal(t, name.current, name.old, "a deprecated name no longer means what it replaced")
	}

	const secret = "s3cr3t"
	fields := SignedFields{
		Timestamp: "1757116800", DeliveryID: "whd_0J8K", Event: "order.placed", Attempt: "1",
		Body: []byte(`{"id":"whd_0J8K"}`),
	}
	tampered := fields
	tampered.Attempt = "2"

	for _, tc := range []struct {
		name   string
		secret string
		header string
		fields SignedFields
		want   bool
	}{
		{"a genuine delivery", secret, webhook.Sign(secret, fields), fields, true},
		{"its attempt rewritten", secret, webhook.Sign(secret, fields), tampered, false},
		{"another receiver's secret", "another receiver's secret", webhook.Sign(secret, fields), fields, false},
		{"a forgery under the empty key", secret, webhook.Sign("", fields), fields, false},
		{"an empty secret and a forgery under it", "", webhook.Sign("", fields), fields, false},
		{"an empty secret and a genuine delivery", "", webhook.Sign(secret, fields), fields, false},
	} {
		assert.Equal(t, tc.want, webhook.VerifySignature(tc.secret, tc.header, tc.fields),
			"webhook.VerifySignature: %s", tc.name)
		assert.Equal(t, tc.want, VerifySignature(tc.secret, tc.header, tc.fields),
			"the old VerifySignature answers differently from the table on %s; a receiver "+
				"still on the old name is checked by another rule", tc.name)
		assert.Equal(t, webhook.Sign(tc.secret, tc.fields), Sign(tc.secret, tc.fields),
			"the old Sign signs differently from the new one on %s", tc.name)
	}
}
