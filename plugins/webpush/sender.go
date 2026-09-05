package webpush

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// ttlSeconds is how long a push service holds an undelivered message.
//
// The header is MANDATORY: without it a push service answers 400, and the
// message that never left looks from here like a message that was sent.
//
// Four hours rather than days: the messages this plugin sends are about
// something that just happened, and one delivered two days later is worse than
// one not delivered at all.
const ttlSeconds = 4 * 60 * 60

// fanOutLimit bounds how many devices are pushed to at once.
//
// The plugin owns its own budget — it is not running inside another module's
// deadline — so the limit is about the push services rather than about time: a
// broadcast that opens a thousand connections to one service gets rate limited
// as a whole, and then the failure is the sender's own doing.
const fanOutLimit = 8

// perDeviceTimeout bounds one push.
//
// A push service that accepts the connection and stops answering must not hold
// the fan-out; the alternative is one wedged device stalling every other
// device's message behind it.
const perDeviceTimeout = 10 * time.Second

// Error codes.
const (
	codeSendFailed = "webpush_send_failed"
	codeBadKey     = "webpush_subscription_key_invalid"
)

// sender pushes one message to one device.
type sender struct {
	client  *http.Client
	key     *ecdsa.PrivateKey
	subject string
	now     func() time.Time
}

// outcome is what happened to one device.
type outcome int

const (
	// outcomeDelivered means the push service accepted the message.
	outcomeDelivered outcome = iota
	// outcomeGone means the subscription is dead and its row must be removed.
	outcomeGone
	// outcomeFailed means something else went wrong; the row STAYS.
	outcomeFailed
)

// send pushes one encrypted message and reports what to do with the row.
//
// # Which statuses delete a row, and which must never
//
// 404 and 410 delete: the push service is telling us this subscription no
// longer exists, and that is the only authoritative source for that fact — a
// browser that revokes permission tells nobody else.
//
// 401 and 403 must NEVER delete, and this is the sharpest rule in the plugin.
// They mean the token was refused, which happens when the VAPID key is rotated
// or a clock drifts — conditions that affect EVERY row at once and are
// repairable. Deleting on them wipes the entire device registry the afternoon
// somebody rotates a key, and nothing on the server can put it back: only the
// browsers can, one visit at a time.
//
// 429 and 5xx do not delete either; they are the push service's problem and it
// will be over.
func (s *sender) send(ctx context.Context, sub subscription, body []byte, topic string) (outcome, error) {
	ctx, cancel := context.WithTimeout(ctx, perDeviceTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return outcomeFailed, coreerrors.Wrap(err, coreerrors.KindInvalid, codeSendFailed,
			"the push request could not be built")
	}

	authorization, err := vapidHeader(sub.Endpoint, s.key, s.subject, s.now())
	if err != nil {
		return outcomeFailed, err
	}

	req.Header.Set("Authorization", authorization)
	// Both of the next two are mandatory. Content-Encoding tells the browser
	// which decryption to run; without it the message arrives and the service
	// worker sees nothing it can open. TTL is required by RFC 8030 and its
	// absence is a 400.
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", strconv.Itoa(ttlSeconds))
	if topic != "" {
		// Topic lets the push service COLLAPSE an undelivered duplicate. The
		// event bus delivers at least once, so the same order can produce two
		// pushes; without this the customer sees the notification twice.
		req.Header.Set("Topic", topic)
	}
	req.ContentLength = int64(len(body))

	resp, err := s.client.Do(req)
	if err != nil {
		return outcomeFailed, coreerrors.Wrap(err, coreerrors.KindUnavailable, codeSendFailed,
			"the push service could not be reached")
	}
	defer resp.Body.Close() //nolint:errcheck // the outcome is decided by the status

	// The body is drained so the connection is pooled rather than closed; a
	// fan-out otherwise pays a TLS handshake per device.
	detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return outcomeDelivered, nil

	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return outcomeGone, nil

	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return outcomeFailed, coreerrors.Unavailable(codeSendFailed,
			"the push service refused the token (status %d); the subscription was KEPT because "+
				"a refused token is a key or clock fault affecting every device, not a dead "+
				"subscription: %s", resp.StatusCode, string(detail))

	case resp.StatusCode == http.StatusTooManyRequests:
		return outcomeFailed, coreerrors.Unavailable(codeSendFailed,
			"the push service is rate limiting (status 429)")

	default:
		return outcomeFailed, coreerrors.Unavailable(codeSendFailed,
			"the push service refused the message with status %d: %s", resp.StatusCode, string(detail))
	}
}

// fanOutResult counts what a fan-out did.
//
// Counts rather than a boolean, and that is the point: a fan-out has no single
// truth value, and reducing it to one is exactly the lie that kept push out of
// the notification ledger (ADR 0018). "3 delivered, 1 gone, 2 failed" is a
// sentence an operator can act on; "sent" is not.
type fanOutResult struct {
	Attempted int
	Delivered int
	Gone      int
	Failed    int
}

// fanOut pushes one message to many devices and cleans up the dead ones.
func fanOut(
	ctx context.Context,
	s *sender,
	st *store,
	log *slog.Logger,
	devices []subscription,
	fingerprint string,
	render func(sub subscription) ([]byte, error),
	topic string,
) fanOutResult {
	var (
		mu     sync.Mutex
		result = fanOutResult{Attempted: len(devices)}
		wg     sync.WaitGroup
		slots  = make(chan struct{}, fanOutLimit)
	)

	for _, device := range devices {
		wg.Add(1)
		slots <- struct{}{}

		go func(sub subscription) {
			defer wg.Done()
			defer func() { <-slots }()

			out, err := deliver(ctx, s, st, log, sub, fingerprint, render, topic)

			mu.Lock()
			defer mu.Unlock()
			switch out {
			case outcomeDelivered:
				result.Delivered++
			case outcomeGone:
				result.Gone++
			case outcomeFailed:
				result.Failed++
				// The device id is logged, the endpoint is NOT: an endpoint is
				// a capability — anyone holding it can push to that browser —
				// and a log collector is a wider audience than this process.
				log.WarnContext(ctx, "a push could not be delivered",
					"subscription_id", sub.ID, "error", err)
			}
		}(device)
	}

	wg.Wait()

	return result
}

// deliver handles one device end to end, including the row cleanup.
func deliver(
	ctx context.Context,
	s *sender,
	st *store,
	log *slog.Logger,
	sub subscription,
	fingerprint string,
	render func(sub subscription) ([]byte, error),
	topic string,
) (outcome, error) {
	// A row minted under a signing key we no longer hold can only ever answer
	// 401, and 401 never deletes. Without this check the rotation leaves a
	// graveyard that grows and is invisible; with it the graveyard drains
	// itself the first time each row is used.
	if sub.Fingerprint != fingerprint {
		removeDeadRow(ctx, st, log, sub, "the subscription was minted under a different VAPID key")

		return outcomeGone, nil
	}

	uaPublic, authSecret, keyErr := parseSubscriptionKeys(sub)
	if keyErr != nil {
		// A row whose keys will not parse cannot ever be sent to. It was
		// validated at subscribe, so reaching here means the row was written by
		// something else or the column was edited; either way it is dead.
		//
		// The outcome is Gone rather than Failed, and the error is CARRIED
		// rather than dropped: "gone" is the right disposition for the row, and
		// the reason it is gone is what an operator needs in the log.
		removeDeadRow(ctx, st, log, sub, "the subscription's keys are unusable: "+keyErr.Error())

		return outcomeGone, keyErr
	}

	plaintext, err := render(sub)
	if err != nil {
		return outcomeFailed, err
	}

	body, err := encrypt(plaintext, uaPublic, authSecret)
	if err != nil {
		return outcomeFailed, err
	}

	out, err := s.send(ctx, sub, body, topic)
	if out == outcomeGone {
		removeDeadRow(ctx, st, log, sub, "the push service reported the subscription is gone")
	}

	return out, err
}

// removeDeadRow deletes a subscription that can never succeed again.
//
// The deletion runs on a context DETACHED from the caller's. A fan-out started
// by an event subscriber can have its context canceled at shutdown, and the
// cleanup is the one part that must still happen: a row the push service
// already called gone will be retried on every future order otherwise, forever.
func removeDeadRow(ctx context.Context, st *store, log *slog.Logger, sub subscription, why string) {
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), perDeviceTimeout)
	defer cancel()

	if err := st.deleteByID(detached, sub.ID); err != nil {
		log.WarnContext(ctx, "a dead push subscription could not be removed",
			"subscription_id", sub.ID, "reason", why, "error", err)

		return
	}

	log.InfoContext(ctx, "a dead push subscription was removed",
		"subscription_id", sub.ID, "reason", why)
}

// parseSubscriptionKeys turns the stored base64url values back into keys.
func parseSubscriptionKeys(sub subscription) (*ecdh.PublicKey, []byte, error) {
	rawPublic, err := decodeKey(sub.P256DH)
	if err != nil {
		return nil, nil, coreerrors.Invalid(codeBadKey, "the subscription's public key is not base64url")
	}
	uaPublic, err := ecdh.P256().NewPublicKey(rawPublic)
	if err != nil {
		return nil, nil, coreerrors.Wrap(err, coreerrors.KindInvalid, codeBadKey,
			"the subscription's public key is not a P-256 point")
	}

	authSecret, err := decodeKey(sub.Auth)
	if err != nil {
		return nil, nil, coreerrors.Invalid(codeBadKey, "the subscription's auth secret is not base64url")
	}
	if len(authSecret) != authSecretLength {
		return nil, nil, coreerrors.Invalid(codeBadKey,
			"the subscription's auth secret has to be %d bytes; it is %d",
			authSecretLength, len(authSecret))
	}

	return uaPublic, authSecret, nil
}
