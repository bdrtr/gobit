//go:build integration

package e2e

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
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

	seq := fixtureCounter.Add(1)
	customerID := fmt.Sprintf("e2e-push-customer-%d", seq)
	endpoint := groundPushService.endpoint(fmt.Sprintf("described-%d", seq))

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
		body, _ := pushSubscription(t, endpoint, customerID)
		rec := identifiedStorefrontRequest(t, customerID, http.MethodPost, pushSubscribePath, body)
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
		assert.NotEmpty(t, groundPushService.received(endpoint), "the push service heard nothing")
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
		body, _ := pushSubscription(t, endpoint, customerID)
		rec := identifiedStorefrontRequest(t, customerID, http.MethodPost, pushSubscribePath, body)
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
// with fresh keys, naming the customer to bind the device to; the device is
// what reads the pushes sent to it.
func pushSubscription(t *testing.T, endpoint, customerID string) (string, pushDevice) {
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

	return string(body), pushDevice{key: key, auth: auth}
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

// groundPushService is the push service the ground lists in
// WEBPUSH_PUSH_SERVICES, started before the installation opens.
var groundPushService *fakePushService

// fakePushService answers a push as a push service does and keeps what each
// endpoint was sent. An endpoint under /redirect answers 307 to the address
// its "to" parameter names, which a sender following redirects would reach.
type fakePushService struct {
	*httptest.Server

	mu     sync.Mutex
	bodies map[string][][]byte
}

// newFakePushService starts the fake.
func newFakePushService() *fakePushService {
	f := &fakePushService{bodies: map[string][][]byte{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.bodies[r.URL.Path] = append(f.bodies[r.URL.Path], body)
		f.mu.Unlock()

		if strings.HasPrefix(r.URL.Path, "/redirect/") {
			w.Header().Set("Location", r.URL.Query().Get("to"))
			w.WriteHeader(http.StatusTemporaryRedirect)

			return
		}
		w.WriteHeader(http.StatusCreated)
	}))

	return f
}

// endpoint is a device's endpoint at the fake.
func (f *fakePushService) endpoint(device string) string { return f.URL + "/push/" + device }

// received is what the endpoint was sent so far.
func (f *fakePushService) received(endpoint string) [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.bodies[strings.TrimPrefix(endpoint, f.URL)])
}

// pushDevice is a browser's half of a subscription: what decrypts its pushes.
type pushDevice struct {
	key  *ecdh.PrivateKey
	auth []byte
}

// open decrypts one push the way a browser does (RFC 8291, aes128gcm with one
// record), written here from the RFC rather than from the plugin.
func (d pushDevice) open(t *testing.T, body []byte) string {
	t.Helper()

	const saltLength, headerFixed = 16, 21
	require.Greater(t, len(body), headerFixed, "the push is shorter than its header")
	salt := body[:saltLength]
	keyLength := int(body[saltLength+4])
	require.Greater(t, len(body), headerFixed+keyLength)
	serverPublic := body[headerFixed : headerFixed+keyLength]

	peer, err := ecdh.P256().NewPublicKey(serverPublic)
	require.NoError(t, err)
	shared, err := d.key.ECDH(peer)
	require.NoError(t, err)

	keyInfo := slices.Concat([]byte("WebPush: info\x00"), d.key.PublicKey().Bytes(), serverPublic)
	ikm, err := hkdf.Key(sha256.New, shared, d.auth, string(keyInfo), 32)
	require.NoError(t, err)
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	require.NoError(t, err)
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	require.NoError(t, err)

	block, err := aes.NewCipher(cek)
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	plain, err := gcm.Open(nil, nonce, body[headerFixed+keyLength:], nil)
	require.NoError(t, err, "the device cannot open the push")

	plain = bytes.TrimRight(plain, "\x00")
	require.True(t, bytes.HasSuffix(plain, []byte{2}), "the last record carries no delimiter")

	return string(plain[:len(plain)-1])
}

// TestAStrangerCannotPointTheSenderAtAnAddress is D292 on the production
// wiring (ADR 0440).
//
// Before it, the subscribe route stored any absolute http(s) endpoint for the
// publishable key alone, and an operator's broadcast POSTed to each: a local
// server that is no push service heard the POST, a 307 from a push service was
// followed to wherever it pointed, and the metadata address and the private
// network were tried. The endpoint is now admitted at a browser's push service
// or a listed one, and the sender follows no redirect.
func TestAStrangerCannotPointTheSenderAtAnAddress(t *testing.T) {
	var heard atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		heard.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(elsewhere.Close)

	for _, endpoint := range []string{
		elsewhere.URL + "/internal/admin/purge?all=1",
		"http://169.254.169.254/latest/meta-data/iam/",
		"http://[::ffff:127.0.0.1]:1/mapped",
		"http://10.0.0.1/private",
		"https://192.168.1.1/router",
		"https://fcm.googleapis.com.attacker.test/fcm/send/x",
		strings.Replace(groundPushService.URL, "http://", "https://", 1) + "/push/tls",
	} {
		body, _ := pushSubscription(t, endpoint, "")
		rec := storefrontRequest(t, http.MethodPost, pushSubscribePath, body)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "%s: %s", endpoint, rec.Body.String())
		assert.Equal(t, "webpush_endpoint_refused", errorCode(t, rec), endpoint)
		assert.NotContains(t, rec.Body.String(), "/internal/admin", "the refusal names no path")
	}

	seq := fixtureCounter.Add(1)
	redirecting := fmt.Sprintf("%s/redirect/%d?to=%s", groundPushService.URL, seq,
		url.QueryEscape(elsewhere.URL+"/after-redirect"))
	body, _ := pushSubscription(t, redirecting, "")
	rec := storefrontRequest(t, http.MethodPost, pushSubscribePath, body)
	require.Equal(t, http.StatusCreated, rec.Code, "an endpoint at a listed push service is stored: %s", rec.Body.String())

	broadcast, err := adminRequestWithBody(http.MethodPost, pushBroadcastPath, map[string]any{
		"event": "order.placed", "data": map[string]string{"display_id": "1"},
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, broadcast.Code, broadcast.Body.String())

	redirectingPath, _, _ := strings.Cut(redirecting, "?")
	require.NotEmpty(t, groundPushService.received(redirectingPath),
		"the listed push service was asked, so the redirect was answered")
	assert.Zero(t, heard.Load(), "no address a stranger named heard the sender, through a redirect or not")
}

// TestADeviceReceivesOnlyItsProvenCustomersOrders is D293 on the production
// wiring (ADR 0440).
//
// Before it, a stranger with the publishable key subscribed a device of their
// own under a customer's id, and the customer's next order pushed its display
// id and order id to that device. The subscription now binds a device only to
// the customer the installation's identity proves.
func TestADeviceReceivesOnlyItsProvenCustomersOrders(t *testing.T) {
	ctx := t.Context()

	victim, email := newCustomer(ctx, t)
	seq := fixtureCounter.Add(1)
	strangersDevice := groundPushService.endpoint(fmt.Sprintf("stranger-%d", seq))
	victimsDevice := groundPushService.endpoint(fmt.Sprintf("victim-%d", seq))

	body, _ := pushSubscription(t, strangersDevice, victim)
	unproven := storefrontRequest(t, http.MethodPost, pushSubscribePath, body)
	require.Equal(t, http.StatusUnauthorized, unproven.Code, unproven.Body.String())
	assert.Equal(t, corehttp.CodeIdentityRefused, errorCode(t, unproven))

	body, _ = pushSubscription(t, strangersDevice, victim)
	mismatched := identifiedStorefrontRequest(t, aStranger, http.MethodPost, pushSubscribePath, body)
	require.Equal(t, http.StatusForbidden, mismatched.Code, mismatched.Body.String())
	assert.Equal(t, corehttp.CodeIdentityMismatch, errorCode(t, mismatched))

	devices, _ := pushDevicesOf(t, victim)
	assert.Empty(t, devices, "no device is bound to the customer on another's word")

	body, device := pushSubscription(t, victimsDevice, victim)
	proven := identifiedStorefrontRequest(t, victim, http.MethodPost, pushSubscribePath, body)
	require.Equal(t, http.StatusCreated, proven.Code, proven.Body.String())

	variantID, _ := newStockedVariant(ctx, t, "E2E Push Bound Product", map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, happyInitialStock)
	cartID, _ := prepareCart(ctx, t, victim, variantID, happyQuantity)
	order, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: paymentmanual.ID,
		PaymentData:       paymentBehavior(t, paymentmanual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     happyTotal,
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool { return len(groundPushService.received(victimsDevice)) > 0 },
		15*time.Second, 50*time.Millisecond, "the customer's own device was not pushed the order")
	pushes := groundPushService.received(victimsDevice)
	require.Len(t, pushes, 1)
	assert.Contains(t, device.open(t, pushes[0]), order.OrderID, "the push is the customer's order")

	assert.Empty(t, groundPushService.received(strangersDevice), "the stranger's device heard nothing")
}
