//go:build integration

package e2e

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The web-push plugin's routes, as its module binds them.
const (
	pushKeyPath           = "/store/v1/webpush/vapid-key"
	pushSubscribePath     = "/store/v1/webpush/subscribe"
	pushUnsubscribePath   = "/store/v1/webpush/unsubscribe"
	pushUnbindPath        = "/store/v1/webpush/unbind"
	pushSubscriptionsPath = "/admin/v1/webpush/subscriptions"
	pushBroadcastPath     = "/admin/v1/webpush/broadcast"
)

// TestTheWebPushRoutesAnswerWhatTheyDescribe drives every route the web-push
// plugin binds on the ground and compares each answer with its description
// (D291).
//
// The plugin's seven routes entered /openapi.json without a body until it stood
// on this ground, and the four that answer with one answered outside the data
// envelope. Each answer is now read under data, its record compared with the
// served schema in both directions, and the values it carried at the top level
// before are read there too, since a client of the old shape still reads them.
func TestTheWebPushRoutesAnswerWhatTheyDescribe(t *testing.T) {
	_, doc := schemaDocument(t)

	var pushed atomic.Int32
	pushService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pushed.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(pushService.Close)

	seq := fixtureCounter.Add(1)
	customerID := fmt.Sprintf("e2e-push-customer-%d", seq)
	endpoint := fmt.Sprintf("%s/push/%d", pushService.URL, seq)

	t.Run("GET "+pushKeyPath, func(t *testing.T) {
		rec := storeRequest(t, pushKeyPath, publishableKey)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		var answer struct {
			Data struct {
				PublicKey string `json:"public_key"`
			} `json:"data"`
			PublicKey string `json:"public_key"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &answer), rec.Body.String())
		require.NotEmpty(t, answer.Data.PublicKey, "the key is not under data: %s", rec.Body.String())
		assert.Equal(t, answer.Data.PublicKey, answer.PublicKey, "the deprecated key is not the key")
		assertRecordIsDescribed(t, doc, http.MethodGet, pushKeyPath, "200", responseFields(t, rec.Body.Bytes()))
	})

	var deviceID string
	t.Run("POST "+pushSubscribePath, func(t *testing.T) {
		rec := storefrontRequest(t, http.MethodPost, pushSubscribePath, pushSubscription(t, endpoint, customerID))
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

		var answer struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &answer), rec.Body.String())
		require.NotEmpty(t, answer.Data.ID, "the device is not named under data: %s", rec.Body.String())
		assert.Equal(t, answer.Data.ID, answer.ID, "the deprecated id is not the device's")
		assert.NotContains(t, rec.Body.String(), endpoint, "the endpoint is a capability and is not echoed")
		assertRecordIsDescribed(t, doc, http.MethodPost, pushSubscribePath, "201", responseFields(t, rec.Body.Bytes()))
		deviceID = answer.Data.ID
	})
	require.NotEmpty(t, deviceID, "no device was stored; the routes below have nothing to act on")

	t.Run("GET "+pushSubscriptionsPath, func(t *testing.T) {
		devices, fields := pushDevicesOf(t, customerID)
		require.Len(t, devices, 1, "the customer's device is not listed")
		assert.Equal(t, deviceID, devices[0])
		assertRecordIsDescribed(t, doc, http.MethodGet, pushSubscriptionsPath, "200", fields)
	})

	t.Run("POST "+pushBroadcastPath, func(t *testing.T) {
		rec, err := adminRequestWithBody(http.MethodPost, pushBroadcastPath, map[string]any{
			"event": "order.placed",
			"data":  map[string]string{"display_id": "1"},
		})
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		type counts struct {
			Attempted int `json:"attempted"`
			Delivered int `json:"delivered"`
			Removed   int `json:"removed"`
			Failed    int `json:"failed"`
		}
		var answer struct {
			Data counts `json:"data"`
			counts
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &answer), rec.Body.String())
		assert.Positive(t, answer.Data.Delivered, "the device was not pushed to: %s", rec.Body.String())
		assert.Equal(t, answer.Data, answer.counts, "the deprecated counts are not the counts")
		assert.Positive(t, pushed.Load(), "the push service heard nothing")
		assertRecordIsDescribed(t, doc, http.MethodPost, pushBroadcastPath, "200", responseFields(t, rec.Body.Bytes()))
	})

	t.Run("POST "+pushUnbindPath, func(t *testing.T) {
		rec := storefrontRequest(t, http.MethodPost, pushUnbindPath, fmt.Sprintf(`{"endpoint":%q}`, endpoint))
		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
		devices, _ := pushDevicesOf(t, customerID)
		assert.Empty(t, devices, "the device is still bound to the customer")
	})

	t.Run("POST "+pushUnsubscribePath, func(t *testing.T) {
		rec := storefrontRequest(t, http.MethodPost, pushUnsubscribePath, fmt.Sprintf(`{"endpoint":%q}`, endpoint))
		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	})

	t.Run("DELETE "+pushSubscriptionsPath+"/{id}", func(t *testing.T) {
		rec := storefrontRequest(t, http.MethodPost, pushSubscribePath, pushSubscription(t, endpoint, customerID))
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		id, ok := storefrontData(t, rec)["id"].(string)
		require.True(t, ok, rec.Body.String())

		deleted, err := adminRequestWithBody(http.MethodDelete, pushSubscriptionsPath+"/"+id, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, deleted.Code, deleted.Body.String())
		devices, _ := pushDevicesOf(t, customerID)
		assert.Empty(t, devices, "the deleted device is still listed")
	})
}

// pushSubscription is the body a browser's PushSubscription.toJSON() gives,
// with fresh keys, claimed for the customer.
func pushSubscription(t *testing.T, endpoint, customerID string) string {
	t.Helper()

	key, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	auth := make([]byte, 16)
	_, err = rand.Read(auth)
	require.NoError(t, err)

	body, err := json.Marshal(map[string]any{
		"endpoint": endpoint,
		"keys": map[string]string{
			"p256dh": base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
			"auth":   base64.RawURLEncoding.EncodeToString(auth),
		},
		"customer_id": customerID,
	})
	require.NoError(t, err)

	return string(body)
}

// pushDevicesOf lists the customer's devices from the admin surface, and
// answers their ids and the fields the first one carries.
func pushDevicesOf(t *testing.T, customerID string) (ids, fields []string) {
	t.Helper()

	rec, err := adminRequestWithBody(http.MethodGet, pushSubscriptionsPath+"?customer_id="+customerID, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var answer struct {
		Data          []map[string]json.RawMessage `json:"data"`
		Subscriptions []map[string]json.RawMessage `json:"subscriptions"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &answer), rec.Body.String())
	require.NotNil(t, answer.Data, "the list is not under data: %s", rec.Body.String())
	assert.Equal(t, answer.Data, answer.Subscriptions, "the deprecated list is not the list")

	for _, device := range answer.Data {
		var id string
		require.NoError(t, json.Unmarshal(device["id"], &id))
		ids = append(ids, id)
	}
	if len(answer.Data) > 0 {
		fields = keysOf(answer.Data[0])
	}

	return ids, fields
}

// assertRecordIsDescribed compares the fields an answer's record carries with
// the record its description serves, in both directions.
func assertRecordIsDescribed(t *testing.T, doc map[string]any, method, pattern, code string, actual []string) {
	t.Helper()

	fields, required := recordFields(t, doc, method, pattern, code)
	mustBeSubset(t, actual, fields, "every field the server writes must be in the schema")
	mustBeSubset(t, required, actual, "every field the schema calls required must be in the response")
}
