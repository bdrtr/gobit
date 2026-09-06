//go:build integration

package e2e

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/plugins/webhookout"
)

// This file proves end to end that an outbound webhook works in the real
// system:
//
//	a real cart becomes a real order, the real order module publishes
//	"order.placed", the plugin's subscriber enqueues a delivery, the plugin's
//	own registered JOB sends it, and a receiver holding nothing but the secret
//	the admin API issued verifies the signature.
//
// Every link is real: the real checkout workflow, the real order module, the
// real event bus, the plugin's real PostgreSQL queue, the real HTTP request and
// the real admin surface behind the production guard stack.
//
// # Why this is IN ADDITION to the plugin's own integration test
//
// A plugin may import no module (ADR 0001), so inside plugins/webhookout the
// event that starts everything is one the test hands to the subscriber itself.
// That test can prove the transport and the queue; it cannot prove that the
// event the ORDER MODULE really publishes is the event the plugin really
// receives. If order renamed a payload field, or published a name this plugin
// does not forward, that test would stay green and every installation would
// deliver nothing. This is the only place the divergence can be seen.
//
// # Why the job is invoked rather than waited for
//
// The scheduler is not running in this harness, and starting it would make the
// test wait a minute for an occurrence. The job is taken from the plugin host —
// the same value the composition root drains — and its Run is called directly.
// That is the production body, reached through the production registration; the
// only thing skipped is the clock.

// webhookReceiver is a test server standing in for a customer's endpoint.
type webhookReceiver struct {
	server *httptest.Server

	mu       sync.Mutex
	secret   string
	requests []webhookDelivery
}

// webhookDelivery is one delivery as the receiver saw it.
type webhookDelivery struct {
	event    string
	attempt  string
	verified bool
	body     map[string]any
}

// newWebhookReceiver starts a receiver that verifies every signature.
//
// The verification is written out with the standard library, from the rule in
// the plugin's signature.go documentation. Calling the plugin's own
// VerifySignature would make this agree with any change to the scheme — the
// documented rule is the contract a customer implements against, and this is
// the thing standing in for that customer.
func newWebhookReceiver(t *testing.T) *webhookReceiver {
	t.Helper()

	r := &webhookReceiver{}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		payload, _ := io.ReadAll(req.Body)

		var parsed map[string]any
		_ = json.Unmarshal(payload, &parsed)

		r.mu.Lock()
		var signed strings.Builder
		for _, part := range []string{
			"v1",
			req.Header.Get(webhookout.HeaderTimestamp),
			req.Header.Get(webhookout.HeaderDelivery),
			req.Header.Get(webhookout.HeaderEvent),
			req.Header.Get(webhookout.HeaderAttempt),
			string(payload),
		} {
			signed.WriteString(strconv.Itoa(len(part)) + ":" + part)
		}
		mac := hmac.New(sha256.New, []byte(r.secret))
		mac.Write([]byte(signed.String()))
		want := "v1=" + base64.StdEncoding.EncodeToString(mac.Sum(nil))

		r.requests = append(r.requests, webhookDelivery{
			event:    req.Header.Get(webhookout.HeaderEvent),
			attempt:  req.Header.Get(webhookout.HeaderAttempt),
			verified: hmac.Equal([]byte(want), []byte(req.Header.Get(webhookout.HeaderSignature))),
			body:     parsed,
		})
		r.mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(r.server.Close)

	return r
}

// withSecret tells the receiver which key to verify with.
func (r *webhookReceiver) withSecret(secret string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.secret = secret
}

// deliveries returns what has arrived so far.
func (r *webhookReceiver) deliveries() []webhookDelivery {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]webhookDelivery(nil), r.requests...)
}

// registerWebhook registers a receiver over the ADMIN API and returns its id and
// secret.
//
// It goes through HTTP rather than through the store, and that is the point of
// the test: the registration surface is the thing that has to exist. A table
// filled by a test helper would prove nothing about whether an operator can
// fill it.
func registerWebhook(t *testing.T, target string, topics ...string) (id, secret string) {
	t.Helper()

	recorder, err := adminRequestWithBody(http.MethodPost, "/admin/v1/webhooks", map[string]any{
		"url":         target,
		"topics":      topics,
		"description": "end-to-end test receiver",
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, recorder.Code,
		"the receiver could not be registered; body: %s", recorder.Body.String())

	var envelope struct {
		Data struct {
			ID     string `json:"id"`
			Secret string `json:"secret"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope),
		"the create response could not be decoded; body: %s", recorder.Body.String())
	require.NotEmpty(t, envelope.Data.ID)
	require.NotEmpty(t, envelope.Data.Secret,
		"the secret has to be returned on creation; it is returned nowhere else, so a "+
			"registration that does not hand it over cannot be used at all")

	t.Cleanup(func() {
		// The receiver is removed so the orders other scenarios place do not
		// enqueue deliveries to a server that is already closed.
		_, _ = adminRequestWithBody(http.MethodDelete, "/admin/v1/webhooks/"+envelope.Data.ID, nil)
	})

	return envelope.Data.ID, envelope.Data.Secret
}

// runWebhookJob runs the plugin's registered delivery pass once.
func runWebhookJob(t *testing.T) error {
	t.Helper()

	for _, job := range pluginHost.Jobs() {
		if job.PluginName() != webhookout.Name {
			continue
		}

		ctx, cancel := context.WithTimeout(t.Context(), job.MaxRun)
		defer cancel()

		return job.Run(ctx)
	}

	t.Fatalf("the %s plugin registered no job; the delivery queue has no drain and every "+
		"delivery would stay pending forever", webhookout.Name)

	return nil
}

// TestARealOrderReachesARealWebhookReceiver is the whole chain.
func TestARealOrderReachesARealWebhookReceiver(t *testing.T) {
	ctx := t.Context()

	receiver := newWebhookReceiver(t)
	_, secret := registerWebhook(t, receiver.server.URL, "order.placed")
	receiver.withSecret(secret)

	orderID, _, _ := notificationOrder(ctx, t, "E2E Webhook Product")

	// The subscriber runs on the bus, which delivers in its own goroutine, so
	// the pass is repeated until the delivery has been enqueued and sent. It is
	// bounded: a chain that is broken never produces one.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		if !assert.NoError(c, runWebhookJob(t)) {
			return
		}
		assert.NotEmpty(c, receiver.deliveries())
	}, 15*time.Second, 200*time.Millisecond,
		"no webhook delivery arrived for order %s. The chain is order -> order.placed -> "+
			"the plugin's subscriber -> the delivery queue -> the plugin's job -> HTTP; "+
			"every link of it is production code here.", orderID)

	got := receiver.deliveries()
	require.Len(t, got, 1, "one order produced %d deliveries to one receiver", len(got))

	assert.True(t, got[0].verified,
		"the receiver could not verify the signature with the secret the admin API "+
			"issued it. Either the bytes signed are not the bytes sent, or the documented "+
			"scheme and the shipped one have parted company.")
	assert.Equal(t, "order.placed", got[0].event)
	assert.Equal(t, "1", got[0].attempt)

	data, ok := got[0].body["data"].(map[string]any)
	require.True(t, ok, "the body has to carry the event payload under \"data\"")
	assert.Equal(t, orderID, data["order_id"],
		"the delivery carries a different order than the one that was placed")

	// The payload the ORDER MODULE really publishes has to be the payload this
	// receiver really gets. These are the fields an integrator builds against,
	// and a rename on the order side would silently empty them.
	assert.NotEmpty(t, data["display_id"])
	assert.NotEmpty(t, data["total"])
	assert.NotEmpty(t, data["currency_code"])

	// And the boundary holds on the real event, which really does carry a
	// customer id: it is a bearer token for an unauthenticated storefront
	// endpoint, so it must not reach a third party.
	assert.NotContains(t, data, "customer_id",
		"the customer id reached the receiver. GET /store/v1/customers/{id} is "+
			"unauthenticated by decision, so the receiver now has standing access to that "+
			"customer's name, email address and every address they have saved.")
	redacted, _ := got[0].body["redacted"].([]any)
	assert.Contains(t, redacted, "customer_id",
		"the withholding has to be visible; a receiver that simply sees no customer_id "+
			"cannot tell a guest order from a withheld field")
}

// TestAWebhookReceiverIsNotSentATopicItDidNotAskFor is the filter, on the real
// bus.
//
// The receiver here asks for the product topics only. A real order is then
// placed, which publishes order.placed to the same bus and through the same
// subscriber. Nothing may arrive.
func TestAWebhookReceiverIsNotSentATopicItDidNotAskFor(t *testing.T) {
	ctx := t.Context()

	receiver := newWebhookReceiver(t)
	_, secret := registerWebhook(t, receiver.server.URL, "product.created")
	receiver.withSecret(secret)

	orderID, _, _ := notificationOrder(ctx, t, "E2E Webhook Unsubscribed Product")

	// The product fixture inside notificationOrder publishes product.created,
	// so this receiver DOES get something — which is what makes the assertion
	// below about the filter rather than about a sender that is simply broken.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		if !assert.NoError(c, runWebhookJob(t)) {
			return
		}
		assert.NotEmpty(c, receiver.deliveries())
	}, 15*time.Second, 200*time.Millisecond,
		"the receiver asked for product.created and the order fixture creates a product; "+
			"nothing arrived at all, so the chain is broken rather than filtering")

	for _, delivery := range receiver.deliveries() {
		assert.Equal(t, "product.created", delivery.event,
			"a receiver registered for product.created was sent a %q delivery (order %s)",
			delivery.event, orderID)
	}
}

// TestTheAdminSurfaceNeverShowsASecretAgain is the one-time secret, checked
// where an operator would look for it.
//
// The secret cannot be hashed — a MAC has to be computed with it on every
// attempt — so the only thing standing between it and a screen recording is
// that no listing returns it.
func TestTheAdminSurfaceNeverShowsASecretAgain(t *testing.T) {
	receiver := newWebhookReceiver(t)
	_, secret := registerWebhook(t, receiver.server.URL, "order.placed")

	recorder, err := adminRequestWithBody(http.MethodGet, "/admin/v1/webhooks", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, recorder.Code, "body: %s", recorder.Body.String())

	assert.NotContains(t, recorder.Body.String(), secret,
		"the receiver listing returned the signing secret. It would then be in every "+
			"log, proxy and screen recording that touched an admin response, and the only "+
			"thing that needs it is the sender.")

	// The listing does say which topics can be asked for, because that is the
	// question that brings an operator here.
	assert.Contains(t, recorder.Body.String(), "forwarded_topics")
}

// TestATopicGobitDoesNotPublishIsRefused is the visible half of a name-based
// subscription, over the real API.
func TestATopicGobitDoesNotPublishIsRefused(t *testing.T) {
	recorder, err := adminRequestWithBody(http.MethodPost, "/admin/v1/webhooks", map[string]any{
		"url":    "https://receiver.example/hook",
		"topics": []string{"order.shipped"},
	})
	require.NoError(t, err)

	assert.Equal(t, http.StatusUnprocessableEntity, recorder.Code,
		"a receiver was registered for a topic gobit does not publish. It would sit in "+
			"the table looking correct and receive nothing, forever, and this request was "+
			"the only moment anybody was present to be told; body: %s", recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), "order.placed",
		"the refusal has to name the topics that ARE supported")
}
