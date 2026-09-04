package webpush

import (
	"context"
	"crypto/ecdh"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// maxBodyBytes bounds a request body.
//
// A subscription is a few hundred bytes; 8 KiB is generous for it and still far
// too small to be a way of filling memory.
const maxBodyBytes = 8 << 10

// broadcastPageLimit bounds one page of the admin listing.
const broadcastPageLimit = 100

// Error codes.
const (
	codeInvalidRequest = "webpush_invalid_request"
)

// subscribeRequest is what a browser posts after
// PushManager.subscribe() resolves.
//
// The field names mirror the browser's own PushSubscription.toJSON() output, so
// a storefront can post it without reshaping — a reshape is a place where the
// keys get transposed and nothing notices until decryption fails.
type subscribeRequest struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256DH string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	// CustomerID is the storefront's CLAIM about who is signed in. It is
	// believed, and ADR 0008 governs why: gobit offers no surface that verifies
	// a customer's identity, and a half-built identity layer is more dangerous
	// than none. The admin surface exists so an operator can undo a bad claim.
	CustomerID string `json:"customer_id"`
	// Locale selects the template; empty means the default.
	Locale string `json:"locale"`
}

// endpointRequest is the body of unsubscribe and unbind.
type endpointRequest struct {
	Endpoint string `json:"endpoint"`
}

// broadcastRequest is the body of the admin broadcast.
type broadcastRequest struct {
	// Event names the template to render.
	Event string `json:"event"`
	// Data fills the template. Every value is a string, for the same reason the
	// event payload's are.
	Data map[string]string `json:"data"`
}

// handleVAPIDKey serves the application server key.
//
// # Why the key is served rather than configured on both sides
//
// A storefront has to pass the public key to PushManager.subscribe(). If it
// were configured separately, the two copies would eventually disagree — and
// the failure is the quiet kind: existing subscriptions keep working while
// every NEW one is minted against a key the server does not hold, so the
// symptom is "push works for old users only", weeks later.
//
// Serving it from the private key makes the two impossible to separate.
func (m *webpushModule) handleVAPIDKey(w http.ResponseWriter, r *http.Request) {
	corehttp.WriteJSON(r.Context(), w, http.StatusOK, map[string]string{
		"public_key": m.opts.publicKey,
	})
}

// handleSubscribe stores a device.
//
// The keys are validated HERE and strictly. A malformed key stored now is a
// subscription that fails on every send forever, and the only place that can be
// reported to anybody who can fix it is this request.
func (m *webpushModule) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req subscribeRequest
	if err := decodeBody(r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	if _, err := audienceOf(req.Endpoint); err != nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
			"endpoint has to be an absolute http(s) URL"))

		return
	}

	if err := validateKeys(req.Keys.P256DH, req.Keys.Auth); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	locale, err := validateLocale(req.Locale)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	id, err := m.store.upsert(ctx, subscription{
		Endpoint:    req.Endpoint,
		P256DH:      req.Keys.P256DH,
		Auth:        req.Keys.Auth,
		CustomerID:  strings.TrimSpace(req.CustomerID),
		Locale:      locale,
		Fingerprint: m.opts.fingerprint,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	// The endpoint is NOT echoed back. It is a capability — whoever holds it
	// can push to that browser — and a response body travels through proxies
	// and browser caches that the request body does not.
	corehttp.WriteJSON(ctx, w, http.StatusCreated, map[string]string{"id": id})
}

// handleUnsubscribe removes a device.
//
// The caller proves nothing but possession of the endpoint, and that is the
// right bar: whoever holds the endpoint can already push to that browser, so
// requiring more to REMOVE it would protect nothing and would leave a browser
// unable to clean up after itself.
func (m *webpushModule) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	endpoint, err := readEndpoint(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	if err := m.store.deleteByEndpoint(ctx, endpoint); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// handleUnbind clears the customer binding and keeps the device.
//
// This is what a logout calls, and it exists because the upsert deliberately
// never downgrades a customer id to empty. Without it nothing could ever clear
// the binding, and a shared device would keep delivering the previous user's
// orders — to their lock screen — forever.
//
// It is POST rather than DELETE because the real call site is
// navigator.sendBeacon on logout, which cannot issue a DELETE.
func (m *webpushModule) handleUnbind(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	endpoint, err := readEndpoint(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	if err := m.store.unbind(ctx, endpoint); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// handleList is the operator's view of the device registry.
//
// It is the remediation path for the trust boundary ADR 0008 leaves open: a
// customer id is a claim, so a hostile claim binds an attacker's device to
// somebody else's orders, and an operator needs to be able to SEE that and
// remove it.
//
// The endpoint is not returned, for the reason given at subscribe.
func (m *webpushModule) handleList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customerID := r.URL.Query().Get("customer_id")

	var (
		devices []subscription
		err     error
	)
	if customerID != "" {
		devices, err = m.store.byCustomer(ctx, customerID)
	} else {
		devices, err = m.store.all(ctx, r.URL.Query().Get("after"), broadcastPageLimit)
	}
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	items := make([]map[string]string, 0, len(devices))
	for _, d := range devices {
		items = append(items, map[string]string{
			"id":          d.ID,
			"customer_id": d.CustomerID,
			"locale":      d.Locale,
			// The fingerprint is shown because it answers the one question a
			// shrinking audience raises: were these minted under the key we
			// still hold?
			"vapid_fingerprint": d.Fingerprint,
		})
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, map[string]any{"subscriptions": items})
}

// handleDelete removes one device by its row id.
func (m *webpushModule) handleDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := m.store.deleteByID(ctx, chi.URLParam(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// handleBroadcast pushes one message to every registered device.
//
// # Why this is not a notification
//
// A broadcast needs no customer identity at all, which makes it the one
// capability the notification module's routing could never serve: its
// destination is a single address, and a delivery row keyed by (template,
// reference) would collapse a fan-out to thousands of devices into one row
// claiming "sent".
//
// The response carries COUNTS rather than a status, for the same reason.
func (m *webpushModule) handleBroadcast(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req broadcastRequest
	if err := decodeBody(r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	if strings.TrimSpace(req.Event) == "" {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
			"event is required and has to name a loaded template"))

		return
	}

	// The payload is rendered ONCE before the walk, purely to fail fast: an
	// unknown template or an oversized body must be a 400 on this request
	// rather than a per-device error discovered after the table has been read.
	if _, err := m.opts.templates.render(req.Event, defaultLocale, req.Data); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	total := m.broadcast(ctx, req)

	corehttp.WriteJSON(ctx, w, http.StatusOK, map[string]int{
		"attempted": total.Attempted,
		"delivered": total.Delivered,
		"removed":   total.Gone,
		"failed":    total.Failed,
	})
}

// broadcast walks the registry a page at a time and pushes to each page.
//
// Paging by the primary key rather than an offset is deliberate: the send path
// DELETES dead rows while this walk is running, and an offset would silently
// skip live devices as the set shrinks underneath it.
func (m *webpushModule) broadcast(ctx context.Context, req broadcastRequest) fanOutResult {
	var total fanOutResult
	after := ""

	for {
		page, err := m.store.all(ctx, after, pageSize)
		if err != nil {
			m.opts.log.WarnContext(ctx, "the broadcast could not read a page of devices",
				"after", after, "error", err)

			return total
		}
		if len(page) == 0 {
			return total
		}

		result := fanOut(ctx, m.sender, m.store, m.opts.log, page, m.opts.fingerprint,
			func(sub subscription) ([]byte, error) {
				return m.opts.templates.render(req.Event, sub.Locale, req.Data)
			},
			topicFor(req.Event, after))

		total.Attempted += result.Attempted
		total.Delivered += result.Delivered
		total.Gone += result.Gone
		total.Failed += result.Failed

		after = page[len(page)-1].ID
	}
}

// readEndpoint reads and validates the endpoint out of a request body.
func readEndpoint(r *http.Request) (string, error) {
	var req endpointRequest
	if err := decodeBody(r, &req); err != nil {
		return "", err
	}
	if strings.TrimSpace(req.Endpoint) == "" {
		return "", coreerrors.Invalid(codeInvalidRequest, "endpoint is required")
	}

	return req.Endpoint, nil
}

// decodeBody reads a bounded JSON body and refuses an unknown field.
//
// Refusing unknown fields is what turns a renamed key into a 400 instead of a
// silently ignored value: a storefront posting "customerId" where the API wants
// "customer_id" would otherwise store every device unbound, and the symptom
// would be "push never works for signed-in users".
func decodeBody(r *http.Request, into any) error {
	body := io.LimitReader(r.Body, maxBodyBytes)

	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(into); err != nil {
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"the request body could not be read as JSON")
	}

	return nil
}

// validateKeys checks the two values the browser minted.
//
// Lenient decoding, strict validation: browsers differ on base64 padding, so
// both forms are accepted, but the decoded values must be a real P-256 point
// and a 16-byte secret. A row that fails this check can never be sent to.
func validateKeys(p256dh, auth string) error {
	rawPublic, err := decodeKey(p256dh)
	if err != nil {
		return coreerrors.Invalid(codeInvalidRequest, "keys.p256dh is not base64url")
	}
	if _, err := ecdh.P256().NewPublicKey(rawPublic); err != nil {
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"keys.p256dh is not a P-256 public key")
	}

	rawAuth, err := decodeKey(auth)
	if err != nil {
		return coreerrors.Invalid(codeInvalidRequest, "keys.auth is not base64url")
	}
	if len(rawAuth) != authSecretLength {
		return coreerrors.Invalid(codeInvalidRequest,
			"keys.auth has to be %d bytes; %d were given", authSecretLength, len(rawAuth))
	}

	return nil
}

// validateLocale bounds the locale to a language tag.
//
// It is validated rather than stored as free text because it selects a FILE:
// unbounded input here means an unbounded set of template lookups that all miss
// and fall back, which reads in production as "the translation never applies".
func validateLocale(locale string) (string, error) {
	locale = strings.ToLower(strings.TrimSpace(locale))
	if locale == "" {
		return "", nil
	}
	if !looksLikeLocale(locale) {
		return "", coreerrors.Invalid(codeInvalidRequest,
			"locale has to be a language tag such as %q or %q; %q was given", "tr", "en-gb", locale)
	}

	return locale, nil
}
