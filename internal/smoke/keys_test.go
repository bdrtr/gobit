//go:build smoke

package smoke

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	authmodels "github.com/bdrtr/gobit/internal/modules/auth/models"
	authservice "github.com/bdrtr/gobit/internal/modules/auth/service"
)

// This file nails down the README's SETUP TRAP: a publishable key is created
// even without a channel (201) but always gets a 401 on the store surface; once
// a channel is attached later, the SAME key starts working.
//
// # Why a smoke scenario
//
// The trap is exactly where the developer who follows the documentation really
// falls: the key creation request looks successful, and the fault only surfaces
// on the SECOND request and on a different surface. The three pieces that make
// the decision also sit apart — the gate is held by the core's middleware, the
// channel list is resolved by the auth module's service, and the composition
// root wires the two to each other. No module test can see all three at once.
//
// # Why no test caught this until today
//
// No test in the repository expected the auth_no_sales_channel code; the
// channel-less key path ran on neither the unit, nor the integration, nor the
// end-to-end harness. internal/smoke's own helpers (client_test.go) ALWAYS
// create the key attached to a channel, which means the existing scenarios
// walked right past the trap.
//
// # The diagnostic code is NOT in the response but in the log
//
// The code the client sees is "unauthenticated", and that is deliberate: at the
// identity gate the detail behind a 401 is not handed to the caller. The reason
// ("which key, why") lives only in the server's log, and the scenario pins down
// exactly this distinction — which is also what the documentation says.

// TestPublishableKeyWithoutChannelIsRejectedByStorefront walks the README's
// publishable key paragraph in a real process.
//
// # Which mutations it catches
//
//   - rejecting the channel-less key at creation time (the creation step fails
//     while expecting 201): the documentation has to change that same day,
//     because the README says "a key is created even with an empty list",
//   - ACCEPTING the channel-less key on the store surface (the step expecting
//     401 fails): if the empty channel list is read downstream as "no filter",
//     the storefront opens the whole catalog — the gate's decision to fail
//     closed is exactly this,
//   - removing the attach-later endpoint or renaming the body field (the attach
//     step fails while expecting 200),
//   - returning the plaintext key on the read endpoints too (the redaction step
//     fails).
func TestPublishableKeyWithoutChannelIsRejectedByStorefront(t *testing.T) {
	cfg := baseSettings(scenarioDatabase(t), freePort(t))
	cfg["ADMIN_BOOTSTRAP_EMAIL"] = seedEmail
	cfg["ADMIN_BOOTSTRAP_PASSWORD"] = seedPassword

	s := startServer(t, cfg)
	s.waitForReady(startupTimeout)

	token := fetchToken(t, s, seedEmail, seedPassword)
	keyID, key := createKeyWithoutChannel(t, s, token)

	t.Run("plaintext key is returned only in the creation response", func(t *testing.T) {
		status, body := s.adminRequest(http.MethodGet, "/admin/v1/api-keys/"+keyID, token, nil)
		require.Equal(t, http.StatusOK, status, "the key could not be read; body: %s", body)

		view := zarfVerisi[struct {
			Redacted string `json:"redacted"`
		}](t, body)
		assert.NotEmpty(t, view.Redacted,
			"the read endpoint must carry the redacted representation; body: %s", body)
		assert.NotContains(t, body, key,
			"the plaintext key must be returned ONLY in the creation response; showing up "+
				"on a read endpoint falsifies the documentation's sentence "+
				"'if you lose it, the only way out is to revoke it'")
	})

	t.Run("channel-less key gets a 401 on the store surface", func(t *testing.T) {
		status, body := s.storefrontRequest(http.MethodGet, "/store/v1/products", key, nil)
		require.Equal(t, http.StatusUnauthorized, status,
			"a channel-less publishable key must not get into the store surface. A 200 "+
				"shows that the empty channel list was read as 'no filter' and that the "+
				"catalog was opened in full. body: %s", body)
		assert.Equal(t, corehttp.CodeUnauthenticated, errorCode(t, body),
			"the identity gate returns a single code; the detail is not handed to the "+
				"caller. body: %s", body)

		// The diagnostic code is looked up in the log, NOT in the response; this
		// is the place the documentation points at.
		s.waitForLog(authservice.CodeNoSalesChannel, logTimeout)
	})

	t.Run("channel is attached later and the SAME key works", func(t *testing.T) {
		channelID := openSalesChannel(t, s, token, "Later Attached Channel")

		status, body := s.adminRequest(http.MethodPost,
			"/admin/v1/api-keys/"+keyID+"/sales-channels", token,
			map[string]any{"sales_channel_id": channelID})
		require.Equal(t, http.StatusOK, status,
			"the key could not be attached to a channel later. The status code tells the "+
				"reason and all three were MEASURED: 404 means the route was never "+
				"mounted, 405 means the route is mounted only with another method (since "+
				"a GET sits on the same path, a dropped write endpoint yields 405 rather "+
				"than 404), and 422 means the body field was renamed (singular "+
				"sales_channel_id). body: %s", body)

		// The key is NOT RECREATED: the claim is that the very same plaintext
		// already in hand now passes. Fetching a new key would leave this step
		// green even in a world where the attach endpoint does nothing at all.
		status, body = s.storefrontRequest(http.MethodGet, "/store/v1/products", key, nil)
		assert.Equal(t, http.StatusOK, status,
			"after the channel is attached the same key must be able to get into the "+
				"store surface; body: %s", body)
	})
}

// createKeyWithoutChannel creates a publishable key that is NOT attached to any
// sales channel; it returns its id and its plaintext.
//
// The body carries the sales_channel_ids field as an EMPTY array rather than not
// carrying it at all: the README's example does send the field, and the trap is
// exactly the "I sent the field but left it empty" case. Not sending the field
// at all arrives at the same result, but that is not what the documentation
// writes down.
func createKeyWithoutChannel(t *testing.T, s *proc, token string) (keyID, key string) {
	t.Helper()

	status, body := s.adminRequest(http.MethodPost, "/admin/v1/api-keys", token, map[string]any{
		"type":              string(authmodels.APIKeyPublishable),
		"title":             "smoke channel-less key",
		"sales_channel_ids": []string{},
	})
	require.Equal(t, http.StatusCreated, status,
		"a channel-less publishable key MUST BE CREATABLE; had it been rejected at "+
			"creation time, the README's sentence 'a key is created even with an empty "+
			"list' would be wrong. body: %s", body)

	created := zarfVerisi[struct {
		APIKey struct {
			ID string `json:"id"`
		} `json:"api_key"`
		Key string `json:"key"`
	}](t, body)
	require.NotEmpty(t, created.APIKey.ID, "the response must carry the key's id; body: %s", body)
	require.NotEmpty(t, created.Key, "the response must carry the plaintext key; body: %s", body)

	return created.APIKey.ID, created.Key
}
