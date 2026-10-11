//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreplugin "github.com/bdrtr/gobit/core/plugin"
	"github.com/bdrtr/gobit/plugins/webhookout"
)

// TestAParcelsDeliveryAndAReturnsMovesReachAWebhookReceiver is ADR 0437 end to
// end: an operator delivers a parcel over the admin API, the shopper opens a
// return at the storefront and the operator withdraws it, and a receiver that
// asked for the three topics gets each once, signed, carrying the fields the
// served document lists for it and no tracking number, reason or note.
func TestAParcelsDeliveryAndAReturnsMovesReachAWebhookReceiver(t *testing.T) {
	topics := []string{"fulfillment.delivered", "order.return_opened", "order.return_canceled"}
	receiver := newWebhookReceiver(t)
	_, secret := registerWebhook(t, receiver.server.URL, topics...)
	receiver.withSecret(secret)

	orderID, lineID, _, _, parcelID := shippedParcel(t, "E2E Aftersales Webhook")
	delivered, err := adminRequestWithBody(http.MethodPost, "/admin/v1/fulfillments/"+parcelID+"/deliver", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, delivered.Code, "body: %s", delivered.Body.String())

	requested := shopperOrderRequest(t, orderID, http.MethodPost, "/store/v1/orders/"+orderID+"/returns",
		`{"lines":[{"order_line_item_id":"`+lineID+`","quantity":1}],"reason":"too small"}`)
	require.Equal(t, http.StatusCreated, requested.Code, requested.Body.String())
	var opened afterSalesRecordResponse
	require.NoError(t, json.Unmarshal(requested.Body.Bytes(), &opened))
	withdrawn, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+orderID+"/returns/"+opened.Data.ID+"/cancel", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, withdrawn.Code, "body: %s", withdrawn.Body.String())

	// mine are the deliveries about this order's parcel and return; other
	// scenarios share the bus and the receiver hears only what it asked for.
	mine := func() []webhookDelivery {
		var out []webhookDelivery
		for _, delivery := range receiver.deliveries() {
			data, _ := delivery.body["data"].(map[string]any)
			if data["fulfillment_id"] == parcelID || data["order_id"] == orderID {
				out = append(out, delivery)
			}
		}

		return out
	}
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		if !assert.NoError(c, runWebhookJob(t)) {
			return
		}
		assert.Len(c, mine(), len(topics))
	}, 15*time.Second, 200*time.Millisecond,
		"the three moves did not all reach the receiver; got %d", len(mine()))

	_, doc := schemaDocument(t)
	webhooks := objectField(t, doc, "webhooks", "document")
	byTopic := map[string]webhookDelivery{}
	for _, delivery := range mine() {
		require.True(t, delivery.verified, "a %s delivery did not verify", delivery.event)
		_, twice := byTopic[delivery.event]
		require.False(t, twice, "%s arrived twice for one move", delivery.event)
		byTopic[delivery.event] = delivery

		data, ok := delivery.body["data"].(map[string]any)
		require.True(t, ok)
		assert.ElementsMatch(t, webhookout.TopicFields[delivery.event], keysOf(data),
			"%s carries %v; TopicFields says %v", delivery.event, keysOf(data), webhookout.TopicFields[delivery.event])
		for _, private := range []string{"tracking_number", "reason", "note"} {
			assert.NotContains(t, data, private, "%s carries %s", delivery.event, private)
		}

		post := objectField(t, objectField(t, webhooks, delivery.event, "webhooks"), "post", delivery.event)
		body := requestSchema(t, objectField(t, post, "requestBody", delivery.event), delivery.event+" requestBody")
		parts, ok := body["allOf"].([]any)
		require.True(t, ok)
		require.Len(t, parts, 2)
		narrowing, ok := parts[1].(map[string]any)
		require.True(t, ok)
		listed := keysOf(objectField(t, objectField(t,
			objectField(t, narrowing, "properties", "narrowing"), "data", "narrowing"), "properties", "data"))
		assert.ElementsMatch(t, listed, keysOf(data), "the served document lists other fields for %s", delivery.event)
	}
	require.ElementsMatch(t, topics, slices.Collect(maps.Keys(byTopic)), "one delivery per move")

	parcel, _ := byTopic["fulfillment.delivered"].body["data"].(map[string]any)
	assert.Equal(t, parcelID, parcel["fulfillment_id"])
	assert.Equal(t, orderID, parcel["reference"], "the order's own route opens the parcel for the order")
	assert.Empty(t, parcel["return_id"], "a parcel that went out brings no return back")
	for _, topic := range []string{"order.return_opened", "order.return_canceled"} {
		data, _ := byTopic[topic].body["data"].(map[string]any)
		assert.Equal(t, orderID, data["order_id"], topic)
		assert.Equal(t, opened.Data.ID, data["return_id"], topic)
	}
}

// TestAReturnsOpeningReachesAReceiverWithoutThePass is ADR 0437 under ADR
// 0436: with the plugin's worker running and its minute job never called, a
// return opened at the storefront reaches a receiver that asked for
// order.return_opened within seconds.
func TestAReturnsOpeningReachesAReceiverWithoutThePass(t *testing.T) {
	ctx := t.Context()

	var worker *coreplugin.Worker
	for i := range groundWorkers {
		if groundWorkers[i].PluginName() == webhookout.Name {
			worker = &groundWorkers[i]
		}
	}
	require.NotNil(t, worker, "the %s plugin registered no worker; a delivery waits for the minute job", webhookout.Name)

	receiver := newWebhookReceiver(t)
	_, secret := registerWebhook(t, receiver.server.URL, "order.return_opened")
	receiver.withSecret(secret)

	orderID, _, lineID := placedHappyOrder(t, "E2E Return Worker Product")

	// Started after the registration and the order, so its first pass has
	// nothing of this receiver's to send and the delivery below is the wake's.
	workerCtx, stopWorker := context.WithCancel(context.WithoutCancel(ctx))
	stopped := make(chan error, 1)
	go func() { stopped <- worker.Run(workerCtx) }()
	t.Cleanup(func() {
		stopWorker()
		assert.NoError(t, <-stopped)
	})

	requested := shopperOrderRequest(t, orderID, http.MethodPost, "/store/v1/orders/"+orderID+"/returns",
		`{"lines":[{"order_line_item_id":"`+lineID+`","quantity":1}]}`)
	require.Equal(t, http.StatusCreated, requested.Code, requested.Body.String())
	var opened afterSalesRecordResponse
	require.NoError(t, json.Unmarshal(requested.Body.Bytes(), &opened))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NotEmpty(c, receiver.deliveries())
	}, 5*time.Second, 50*time.Millisecond,
		"no order.return_opened delivery arrived within five seconds of the return being opened, "+
			"with the worker running and the minute job not called")

	got := receiver.deliveries()
	require.Len(t, got, 1)
	assert.True(t, got[0].verified)
	assert.Equal(t, "order.return_opened", got[0].event)
	data, ok := got[0].body["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, opened.Data.ID, data["return_id"])
	assert.Equal(t, orderID, data["order_id"])
}
