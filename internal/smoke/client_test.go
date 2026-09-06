//go:build smoke

package smoke

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	authmodels "github.com/bdrtr/gobit/internal/modules/auth/models"
)

// This file holds the HTTP helpers shared by the scenarios of the two surfaces
// that had never been run in a REAL PROCESS (the GraphQL storefront and B2B).
//
// [proc.request] in process_test.go is deliberately narrow: it builds a request
// with no body and knows Bearer as the only identity form. The surfaces here,
// on the other hand, send a JSON body, and on the storefront side the identity
// is NOT in Authorization but in the publishable key header. That is why these
// helpers were added; the pattern is the same and the request still goes out
// through [proc.send], which means there is NO second HTTP client and NO
// second harness.

// jsonRequest builds a JSON request with a body and sends it.
//
// If body is nil the request goes out without a body; the same helper being
// able to build both a POST and a GET makes it unnecessary for the caller to
// choose between two functions.
//
// The identity header comes from the caller, and that is deliberate: the admin
// surface wants Authorization, the storefront surface wants the publishable
// key header. Squeezing the two into a single "token" parameter would make it
// unreadable at the call site which surface is being addressed — and in this
// repository the whole of the guard rests on that distinction.
func (s *proc) jsonRequest(
	method, path string,
	body any,
	headers map[string]string,
) (code int, response string) {
	s.t.Helper()

	var raw io.Reader = http.NoBody
	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(s.t, err, "could not encode the request body: %s %s", method, path)
		raw = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(s.t.Context(), method, s.addr+path, raw)
	require.NoError(s.t, err, "could not build the request: %s %s", method, path)

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}

	return s.send(req)
}

// adminRequest makes a JSON request with a token against the admin surface.
func (s *proc) adminRequest(method, path, token string, body any) (code int, response string) {
	s.t.Helper()

	return s.jsonRequest(method, path, body, map[string]string{"Authorization": "Bearer " + token})
}

// storefrontRequest makes a JSON request with a publishable key against the
// storefront surface.
//
// If key is empty the header is NOT sent at all; the claim "is an
// identity-less request rejected" is exercised by not sending the header at
// all rather than by sending an empty one — that is the state a missing
// configuration produces in production.
func (s *proc) storefrontRequest(method, path, key string, body any) (code int, response string) {
	s.t.Helper()

	headers := map[string]string{}
	if key != "" {
		headers[corehttp.PublishableKeyHeader] = key
	}

	return s.jsonRequest(method, path, body, headers)
}

// zarfVerisi resolves the data field of a single response envelope into the
// target type.
//
// The envelope ({"data": …}) is the contract of plan Section 8 and the
// scenarios read it exactly as it comes off the wire; the modules' DTO types
// are NOT imported. The reason is the same as for fetchToken in
// startup_test.go: a shared Go type would leave the test green even if a field
// name changed — whereas the thing that changed is precisely the contract the
// client sees.
func zarfVerisi[T any](t *testing.T, response string) T {
	t.Helper()

	var envelope struct {
		Data T `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(response), &envelope),
		"could not decode the response envelope; body: %s", response)

	return envelope.Data
}

// errorCode returns the MACHINE code of an error envelope.
//
// The code is looked at, not the message: the message is free text and can
// change, whereas the code is the contract the client branches on (see
// core/http ErrorBody). The core's envelope TYPE is again not imported; the
// reasoning is in the zarfVerisi godoc.
func errorCode(t *testing.T, response string) string {
	t.Helper()

	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(response), &envelope),
		"could not decode the error envelope; body: %s", response)

	return envelope.Error.Code
}

// openSalesChannel opens a new sales channel and returns its id.
//
// The channel is the OPERATING CONDITION of the storefront surface: a
// publishable key represents a channel and the catalog filter rests on that
// channel. A storefront opened with a key that has no channel looks empty,
// which means skipping the setup step would turn the scenario into a test that
// is "green but proves nothing".
func openSalesChannel(t *testing.T, s *proc, token, name string) string {
	t.Helper()

	code, body := s.adminRequest(http.MethodPost, "/admin/v1/sales-channels", token,
		map[string]any{"name": name})
	require.Equal(t, http.StatusCreated, code, "could not open the sales channel; body: %s", body)

	channel := zarfVerisi[struct {
		ID string `json:"id"`
	}](t, body)
	require.NotEmpty(t, channel.ID, "the sales channel has to return an id; body: %s", body)

	return channel.ID
}

// fetchStorefrontKey creates a publishable key bound to the channel and
// returns its PLAIN text.
//
// The plain key is visible only in this response (see auth api
// adminCreateAPIKey); the scenario has no way other than holding on to it.
func fetchStorefrontKey(t *testing.T, s *proc, token, channelID string) string {
	t.Helper()

	code, body := s.adminRequest(http.MethodPost, "/admin/v1/api-keys", token, map[string]any{
		"type":              string(authmodels.APIKeyPublishable),
		"title":             "smoke storefront key",
		"sales_channel_ids": []string{channelID},
	})
	require.Equal(t, http.StatusCreated, code, "could not create the publishable key; body: %s", body)

	key := zarfVerisi[struct {
		Key string `json:"key"`
	}](t, body)
	require.NotEmpty(t, key.Key, "the response has to carry the plain key; body: %s", body)

	return key.Key
}

// setUpAdminHarness logs in with the seeded administrator and prepares the
// storefront identity.
//
// The three steps (login, channel, key) sit in one place because the latter
// two are both preconditions of the NEW surface, and letting them drift apart
// between the scenarios would mean two tests exercising different setups.
func setUpAdminHarness(t *testing.T, s *proc, channelName string) (token, channelID, storefrontKey string) {
	t.Helper()

	token = fetchToken(t, s, seedEmail, seedPassword)
	channelID = openSalesChannel(t, s, token, channelName)
	storefrontKey = fetchStorefrontKey(t, s, token, channelID)

	return token, channelID, storefrontKey
}
