package webhookout

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/webhook"
)

// TestTheSenderWritesThePublishedEnvelope is ADR 0435 at the one function that
// writes a delivery, with no database in front of it.
//
// The integration test reaches the sender through the table, whose NOT NULL
// columns never hand it a nil payload or a nil list, so the half of the promise
// that is the sender's own — "data" an object and "redacted" an array on every
// delivery, whatever the row held — is only reachable here. The body is
// compared as text, so a key renamed or dropped on the sender's side fails too,
// and the headers are checked with core/webhook's verifier, which is what a
// receiver in Go calls.
func TestTheSenderWritesThePublishedEnvelope(t *testing.T) {
	t.Parallel()

	var (
		mu      sync.Mutex
		headers http.Header
		body    []byte
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		read, _ := io.ReadAll(r.Body)

		mu.Lock()
		headers, body = r.Header.Clone(), read
		mu.Unlock()

		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	s := newSender()
	s.now = func() time.Time { return time.Unix(1760097600, 0) }

	result := s.send(t.Context(), delivery{
		ID: "whd_1", URL: server.URL, Secret: "s3cr3t",
		EventID: "product.deleted:prd_1", EventName: topicProductDeleted,
		OccurredAt: time.Date(2026, 10, 10, 15, 0, 0, 0, time.FixedZone("TRT", 3*60*60)),
		Payload:    nil, Redacted: nil, Attempts: 0,
	})
	require.True(t, result.ok(), "the receiver answered 204 and the attempt failed: %v", result.Err)

	mu.Lock()
	defer mu.Unlock()

	assert.Equal(t, `{"id":"whd_1","event":"product.deleted","event_id":"product.deleted:prd_1",`+
		`"occurred_at":"2026-10-10T12:00:00Z","attempt":1,"data":{},"redacted":[]}`, string(body),
		"the sender wrote something other than the published envelope; a nil payload or list "+
			"has to travel as {} and [], which is what the document promises a receiver")

	assert.Equal(t, "application/json", headers.Get("Content-Type"))
	assert.Equal(t, topicProductDeleted, headers.Get(webhook.HeaderEvent))
	assert.Equal(t, "whd_1", headers.Get(webhook.HeaderDelivery))
	assert.Equal(t, "1", headers.Get(webhook.HeaderAttempt))
	assert.Equal(t, "1760097600", headers.Get(webhook.HeaderTimestamp))
	assert.True(t, webhook.VerifySignature("s3cr3t", headers.Get(webhook.HeaderSignature), webhook.SignedFields{
		Timestamp:  headers.Get(webhook.HeaderTimestamp),
		DeliveryID: headers.Get(webhook.HeaderDelivery),
		Event:      headers.Get(webhook.HeaderEvent),
		Attempt:    headers.Get(webhook.HeaderAttempt),
		Body:       body,
	}), "the delivery does not verify with core/webhook's own check")
}

// redirectDelivery is the delivery the redirect tests send to url.
func redirectDelivery(url string) delivery {
	return delivery{
		ID: "whd_1", URL: url, Secret: "s3cr3t",
		EventID: "order.placed:ord_1", EventName: topicOrderPlaced,
		OccurredAt: time.Now(), Payload: map[string]any{"order_id": "ord_1"},
	}
}

// TestARedirectThatKeepsThePostIsFollowed is the half of D272 that keeps a
// receiver working: a 307 or 308 keeps the method, the body and the headers,
// so it is followed, a relative address included (a framework adding a
// trailing slash answers one on every delivery), and what arrives at the end
// verifies with the receiver's secret.
func TestARedirectThatKeepsThePostIsFollowed(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			var (
				mu      sync.Mutex
				method  string
				headers http.Header
				body    []byte
			)
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/hooks" {
					http.Redirect(w, r, "/hooks/", status)

					return
				}
				read, _ := io.ReadAll(r.Body)
				mu.Lock()
				method, headers, body = r.Method, r.Header.Clone(), read
				mu.Unlock()
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(receiver.Close)

			result := newSender().send(t.Context(), redirectDelivery(receiver.URL+"/hooks"))

			require.True(t, result.ok(), "a %d to an address registration accepts was not followed: %v", status, result.Err)
			assert.Equal(t, http.StatusNoContent, result.Status, "the answer at the end of the redirect decides")

			mu.Lock()
			defer mu.Unlock()
			assert.Equal(t, http.MethodPost, method)
			assert.Contains(t, string(body), `"order_id":"ord_1"`, "the body did not survive the redirect")
			assert.True(t, webhook.VerifySignature("s3cr3t", headers.Get(webhook.HeaderSignature), webhook.SignedFields{
				Timestamp:  headers.Get(webhook.HeaderTimestamp),
				DeliveryID: headers.Get(webhook.HeaderDelivery),
				Event:      headers.Get(webhook.HeaderEvent),
				Attempt:    headers.Get(webhook.HeaderAttempt),
				Body:       body,
			}), "the delivery that arrived after a %d does not verify; the headers or the body were lost on the way", status)
		})
	}
}

// TestARedirectThatDropsThePostIsAFailedAttempt is the half of D272 that was
// the fault: a 301, 302 or 303 turns the signed POST into a GET with no body,
// which the address it points at can answer 200 to — a delivery counted as
// delivered that no receiver read. Each is a failed attempt, that address is
// never asked, and the recorded error, cut to what last_error keeps, still
// names it: it leads with the status and the address and does not repeat the
// delivery's own URL.
func TestARedirectThatDropsThePostIsAFailedAttempt(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			var (
				mu      sync.Mutex
				reached int
			)
			elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mu.Lock()
				reached++
				mu.Unlock()
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(elsewhere.Close)

			target := elsewhere.URL + "/api/v2/integrations/gobit/webhooks/deliveries/received-from-the-shop"
			moved := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target, status)
			}))
			t.Cleanup(moved.Close)

			result := newSender().send(t.Context(),
				redirectDelivery(moved.URL+"/api/v1/integrations/gobit/webhooks/deliveries/received-from-the-shop"))

			require.False(t, result.ok(), "a %d was counted as delivered", status)
			assert.Equal(t, status, result.Status)
			require.Error(t, result.Err)
			stored := truncateError(result.Err.Error())
			assert.Contains(t, stored, target,
				"the error last_error keeps does not name where the receiver pointed, so the operator "+
					"cannot register it: %s", stored)
			assert.Contains(t, stored, strconv.Itoa(status))

			mu.Lock()
			defer mu.Unlock()
			assert.Zero(t, reached, "the sender followed a %d", status)
		})
	}
}

// TestARedirectPastTheRegistrationRuleIsAFailedAttempt: a 307 keeps the body,
// so following it to an address registration would refuse — plain http to a
// host that is not loopback — would send the order data where the rule exists
// to keep it from going. It is a failed attempt naming why.
func TestARedirectPastTheRegistrationRuleIsAFailedAttempt(t *testing.T) {
	t.Parallel()

	const downgraded = "http://receiver.example/hooks/"
	moved := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, downgraded, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(moved.Close)

	result := newSender().send(t.Context(), redirectDelivery(moved.URL+"/hooks"))

	require.False(t, result.ok())
	assert.Equal(t, http.StatusTemporaryRedirect, result.Status,
		"the sender went on past the 307 instead of stopping at it")
	require.Error(t, result.Err)
	assert.Contains(t, result.Err.Error(), downgraded)
	assert.Contains(t, result.Err.Error(), "fails the rule a registered URL passes")
}

// TestARedirectChainStopsAtTheCap: a receiver answering 307 to itself is a
// loop, and an attempt follows at most maxRedirects of them before it fails,
// rather than Go's ten or forever.
func TestARedirectChainStopsAtTheCap(t *testing.T) {
	t.Parallel()

	var (
		mu       sync.Mutex
		requests int
	)
	loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		http.Redirect(w, r, "/again", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(loop.Close)

	result := newSender().send(t.Context(), redirectDelivery(loop.URL+"/hooks"))

	require.False(t, result.ok())
	assert.Equal(t, http.StatusTemporaryRedirect, result.Status)
	require.Error(t, result.Err)
	assert.Contains(t, result.Err.Error(), "at most "+strconv.Itoa(maxRedirects)+" redirects")

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, maxRedirects+1, requests, "the attempt and its %d followed redirects", maxRedirects)
}
