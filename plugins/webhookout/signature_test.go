package webhookout

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The signing scheme's own tests moved with it to core/webhook (ADR 0435).
// What stays is the half this plugin owns: the key it mints for each receiver.

// TestTheSecretIsLongEnoughToBeOne checks the minted key.
//
// A short key still produces a valid-looking HMAC, so nothing at run time would
// complain; the only place this can be caught is here.
func TestTheSecretIsLongEnoughToBeOne(t *testing.T) {
	t.Parallel()

	secret, err := newSecret()
	require.NoError(t, err)

	decoded, err := base64.RawURLEncoding.DecodeString(secret)
	require.NoError(t, err, "the secret has to survive being pasted into a .env file, which "+
		"is why it is base64url")
	assert.Len(t, decoded, secretBytes,
		"the signing key is shorter than the hash it keys; it would be the weakest part "+
			"of a scheme whose only credential it is")
}
