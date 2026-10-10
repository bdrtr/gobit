package webhook_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/bdrtr/gobit/core/webhook"
)

// ExampleVerifySignature is a receiver's handler: the raw body is read first,
// the signature is checked over it with the headers as they traveled, and
// only then is the body decoded. A real receiver also refuses a timestamp far
// from now and drops a delivery id it has already handled; both are left out
// here.
func ExampleVerifySignature() {
	// The secret POST /admin/v1/webhooks/ returned when the receiver was
	// registered, and returns nowhere else.
	const secret = "the receiver's secret"

	receiver := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "unreadable", http.StatusBadRequest)

			return
		}

		if !webhook.VerifySignature(secret, r.Header.Get(webhook.HeaderSignature), webhook.SignedFields{
			Timestamp:  r.Header.Get(webhook.HeaderTimestamp),
			DeliveryID: r.Header.Get(webhook.HeaderDelivery),
			Event:      r.Header.Get(webhook.HeaderEvent),
			Attempt:    r.Header.Get(webhook.HeaderAttempt),
			Body:       body,
		}) {
			http.Error(w, "not from gobit", http.StatusUnauthorized)

			return
		}

		var delivery webhook.Delivery
		if err := json.Unmarshal(body, &delivery); err != nil {
			http.Error(w, "unparsable", http.StatusBadRequest)

			return
		}

		fmt.Println(delivery.Event, delivery.Data["order_id"], "withheld:", strings.Join(delivery.Redacted, ","))
		w.WriteHeader(http.StatusNoContent)
	})

	// A delivery as gobit sends it.
	body := []byte(`{"id":"whd_0J8K","event":"order.placed","event_id":"order.placed:ord_1",` +
		`"occurred_at":"2026-10-10T12:00:00Z","attempt":1,"data":{"order_id":"ord_1"},` +
		`"redacted":["customer_id"]}`)
	signature := webhook.Sign(secret, webhook.SignedFields{
		Timestamp: "1760097600", DeliveryID: "whd_0J8K", Event: "order.placed", Attempt: "1",
		Body: body,
	})
	fmt.Println(signature)

	send := func(attempt string) int {
		request := httptest.NewRequest(http.MethodPost, "/hooks/gobit", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(webhook.HeaderEvent, "order.placed")
		request.Header.Set(webhook.HeaderDelivery, "whd_0J8K")
		request.Header.Set(webhook.HeaderAttempt, attempt)
		request.Header.Set(webhook.HeaderTimestamp, "1760097600")
		request.Header.Set(webhook.HeaderSignature, signature)

		recorder := httptest.NewRecorder()
		receiver.ServeHTTP(recorder, request)

		return recorder.Code
	}

	fmt.Println(send("1"))
	// The same request with its attempt number rewritten on the way.
	fmt.Println(send("2"))
	// Output:
	// v1=K4pCL4MWDLhkLtqMhJbsUBzVRihp5k2uXcGho79SIaA=
	// order.placed ord_1 withheld: customer_id
	// 204
	// 401
}
