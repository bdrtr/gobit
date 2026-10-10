package webhookout

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/webhook"
)

// perAttemptTimeout bounds one POST.
//
// Ten seconds, and it bounds the request rather than the receiver's work: a
// receiver that needs longer than ten seconds to acknowledge a webhook is doing
// the work inline, which is what the delivery id and the retry exist to let it
// avoid. It is also what keeps a pass's budget arithmetic honest — see
// [maxRun].
const perAttemptTimeout = 10 * time.Second

// concurrency is how many deliveries one process has in flight at once.
//
// Eight, the same number webpush's fan-out settled on, and the reason is the
// same shape: the limit is about the receivers rather than about this process.
// A pass that opens a hundred connections to one receiver is a burst that
// receiver will rate limit as a whole, and then the failure is the sender's own
// doing rather than the receiver's.
//
// It is one semaphore per process, [webhookModule.slots], which the worker and
// the minute job's pass share (ADR 0436). An installation of N processes can
// therefore have 8N in flight, where one job once had eight; a receiver that
// cannot take that sets max_per_minute.
const concurrency = 8

// maxBodyRead bounds how much of a receiver's answer is read.
//
// The body is read only to put its first line into last_error, and it is a
// third party's bytes. Four kilobytes is more than any status text and small
// enough that a receiver answering a megabyte of HTML cannot make the sender's
// memory its problem.
const maxBodyRead = 4 << 10

// userAgent identifies the sender to a receiver's logs.
//
// The version is the SIGNATURE scheme's, not gobit's, and that is deliberate: a
// receiver's operator reading their access log wants to know which verification
// rule applies, and gobit's own version tells them nothing they can act on.
const userAgent = "gobit-webhooks/1"

// codeSendFailed reports that a delivery attempt did not land.
const codeSendFailed = "webhookout_send_failed"

// attemptResult is what happened to one POST.
type attemptResult struct {
	// Status is the HTTP status, or 0 when no answer arrived at all.
	Status int
	// Err is why it failed; nil when the receiver accepted it.
	Err error
}

// ok reports whether the receiver accepted the delivery.
func (r attemptResult) ok() bool { return r.Err == nil }

// poster makes one attempt; [sender] is the real one and a unit test drives a
// pass with a fake.
type poster interface {
	send(ctx context.Context, d delivery) attemptResult
}

// sender makes one signed POST.
type sender struct {
	client *http.Client
	now    func() time.Time
}

// newSender builds a sender over its own client.
//
// The client is the plugin's own rather than a shared one, because the timeout
// that belongs on it is this plugin's decision: a client shared with, say, a
// payment provider would carry whichever deadline was set last.
//
// # It follows a redirect only when nothing is lost by it (D272)
//
// Go's client follows every redirect by default, and for a webhook most of
// them are wrong. A 301, 302 or 303 turns the signed POST into a GET with no
// body, and when the address it points at answers 2xx the delivery is counted
// as delivered while the receiver saw nothing. A 307 or 308 keeps the method,
// the body and the X-Gobit headers, so the signature still verifies where it
// lands, and a receiver behind a framework that adds a trailing slash answers
// one on every delivery; those are followed, but only to an address that
// passes the rule a registered URL passes ([validateURL]: https unless the
// host is loopback), so an answer cannot send the body somewhere registration
// would have refused, and at most [maxRedirects] times. Anything else is
// returned as it came, and [send] counts it as the failed attempt it is.
func newSender() *sender {
	return &sender{
		client: &http.Client{
			Timeout:       perAttemptTimeout,
			CheckRedirect: followRedirect,
		},
		now: time.Now,
	}
}

// maxRedirects is how many 307 and 308 answers one attempt follows.
//
// Three. A trailing slash is one hop and a host that moved is another; a chain
// longer than that is a loop or a misconfiguration the operator should be
// shown, and every hop spends the same ten seconds of [perAttemptTimeout].
const maxRedirects = 3

// followRedirect is the client's CheckRedirect: it follows a redirect when
// [redirectRefusal] has nothing against it, and otherwise hands the answer
// back to [send] as it came.
func followRedirect(next *http.Request, via []*http.Request) error {
	if redirectRefusal(next.Response.StatusCode, next.URL, len(via)) != "" {
		return http.ErrUseLastResponse
	}

	return nil
}

// redirectRefusal says why a redirect is not followed, or "" when it is.
//
// hop is the redirect's place in the attempt, 1 for the first, which is the
// count of requests already made when the client asks.
func redirectRefusal(status int, target *url.URL, hop int) string {
	switch {
	case status != http.StatusTemporaryRedirect && status != http.StatusPermanentRedirect:
		return "only a 307 or a 308 keeps the POST and its body"
	case target == nil:
		return "it names no address"
	case hop > maxRedirects:
		return fmt.Sprintf("an attempt follows at most %d redirects", maxRedirects)
	}

	if _, err := validateURL(target.String()); err != nil {
		return "the address fails the rule a registered URL passes, https unless the host is loopback"
	}

	return ""
}

// redirectsFollowed counts the redirects the client followed to reach an answer.
func redirectsFollowed(resp *http.Response) int {
	followed := 0
	for req := resp.Request; req != nil && req.Response != nil; req = req.Response.Request {
		followed++
	}

	return followed
}

// send makes one attempt and reports what happened.
//
// # The body is the published envelope, and nothing else
//
// The request is a [webhook.Delivery], marshaled, signed with [webhook.Sign]
// and headed with the [webhook.HeaderEvent] family: the names a receiver
// compiles against are the names this function writes, so the two cannot part
// (ADR 0435).
//
// Its Data is the event's payload as the bus carried it, MINUS the redacted
// fields (see [redactedFields]). Nothing is added to it. The sender does not
// read the order, the product or the customer, and it could not: a plugin may
// not import a module (ADR 0001), so enriching the body would mean a new
// dependency for this plugin and a second definition of "what an order looks
// like" that nothing keeps in step with the first.
//
// That constraint turns out to be the right policy anyway. The bus's payloads
// were already chosen to carry no personal data because the bus is durable; a
// webhook body is the same data one trust boundary further out, sitting in a
// third party's logs. Widening it here would widen it there.
//
// # Which answers count as delivered
//
// Any 2xx. Nothing narrower, because a receiver answering 202 for "queued" is
// the shape this sender wants to encourage, and one answering 204 is a receiver
// that had nothing to say.
//
// A 307 or 308 to an address registration would accept is followed, at most
// [maxRedirects] times, and the answer at its end decides (see [newSender]).
// Any other 3xx is a failure like any other. Its error LEADS with the status
// and the absolute address it pointed at, and does not repeat the delivery's
// URL, which the listing already shows: last_error keeps two hundred bytes,
// and the address is what the operator needs to register the receiver where
// it moved.
//
// Everything else is a failure that will be retried, INCLUDING 4xx. That is the
// deliberate half: a 404 usually means the URL is wrong and no number of
// retries will fix it, and treating it as fatal would be the obvious
// optimization. It is refused because the two cases are indistinguishable from
// here — a receiver that is being redeployed answers 404 for a minute, and a
// sender that gave up on it would drop the event with the operator told nothing
// until they read a listing. The retry ladder is bounded anyway, so the cost of
// being wrong in this direction is thirteen attempts and a dead letter that
// says 404 in it.
func (s *sender) send(ctx context.Context, d delivery) attemptResult {
	attempt := d.Attempts + 1

	// The published envelope promises "data" as an object and "redacted" as an
	// array on every delivery. Both columns are NOT NULL and scan to an empty
	// map and an empty list, so a nil here is a row this sender did not read
	// from the table; it is still written as {} and [] rather than null,
	// because the promise is the type's and not the table's.
	data, redacted := d.Payload, d.Redacted
	if data == nil {
		data = map[string]any{}
	}
	if redacted == nil {
		redacted = []string{}
	}

	payload, err := json.Marshal(webhook.Delivery{
		ID:         d.ID,
		Event:      d.EventName,
		EventID:    d.EventID,
		OccurredAt: d.OccurredAt.UTC(),
		Attempt:    attempt,
		Data:       data,
		Redacted:   redacted,
	})
	if err != nil {
		// Not reachable with the values this struct holds; a payload that
		// cannot be marshaled would have failed the jsonb write already. It is
		// still a failure rather than a panic: a panic here takes the pass down
		// and with it every other delivery in the batch.
		return attemptResult{Err: coreerrors.Wrap(err, coreerrors.KindInternal, codeSendFailed,
			"the delivery body could not be encoded")}
	}

	ctx, cancel := context.WithTimeout(ctx, perAttemptTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.URL, bytes.NewReader(payload))
	if err != nil {
		return attemptResult{Err: coreerrors.Wrap(err, coreerrors.KindInvalid, codeSendFailed,
			"the delivery request could not be built for %s", d.URL)}
	}

	timestamp := strconv.FormatInt(s.now().UTC().Unix(), 10)
	attemptText := strconv.FormatInt(attempt, 10)
	signature := webhook.Sign(d.Secret, webhook.SignedFields{
		Timestamp:  timestamp,
		DeliveryID: d.ID,
		Event:      d.EventName,
		Attempt:    attemptText,
		Body:       payload,
	})

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set(webhook.HeaderEvent, d.EventName)
	req.Header.Set(webhook.HeaderDelivery, d.ID)
	req.Header.Set(webhook.HeaderAttempt, attemptText)
	req.Header.Set(webhook.HeaderTimestamp, timestamp)
	req.Header.Set(webhook.HeaderSignature, signature)
	req.ContentLength = int64(len(payload))

	resp, err := s.client.Do(req)
	if err != nil {
		return attemptResult{Err: coreerrors.Wrap(err, coreerrors.KindUnavailable, codeSendFailed,
			"%s could not be reached", d.URL)}
	}
	defer func() { _ = resp.Body.Close() }()

	answer, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyRead))

	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return attemptResult{Status: resp.StatusCode}
	}

	if resp.StatusCode < http.StatusBadRequest && resp.StatusCode >= http.StatusMultipleChoices {
		target, _ := resp.Location()
		where := ""
		if target != nil {
			where = target.String()
		}

		return attemptResult{
			Status: resp.StatusCode,
			Err: coreerrors.Unavailable(codeSendFailed, "redirect %d to %q not followed: %s",
				resp.StatusCode, where, redirectRefusal(resp.StatusCode, target, redirectsFollowed(resp)+1)),
		}
	}

	return attemptResult{
		Status: resp.StatusCode,
		Err: coreerrors.Unavailable(codeSendFailed,
			"%s answered %d: %s", d.URL, resp.StatusCode, firstLine(answer)),
	}
}

// firstLine is what of a receiver's answer goes into last_error.
func firstLine(answer []byte) string {
	text := string(bytes.TrimSpace(answer))
	if text == "" {
		return "(empty body)"
	}
	if cut, _, found := bytes.Cut([]byte(text), []byte("\n")); found {
		return string(bytes.TrimSpace(cut))
	}

	return text
}

// passResult is what one delivery pass did.
//
// A pass that delivered nothing and failed everything is not the same as an
// empty one, and a caller that could not tell them apart would log "nothing to
// do" during an outage. The dead letters are carried as IDS because the moment
// a delivery is given up on is the only moment anything knows which one it was:
// the log line is the record.
type passResult struct {
	// Delivered is how many receivers accepted.
	Delivered int
	// Failed is how many did not. It INCLUDES the ones that crossed the ceiling
	// in this pass — those failed and were then given up on.
	Failed int
	// DeadLettered are the delivery ids the sender stopped trying, this pass.
	DeadLettered []string
	// Claimed is how many rows the pass took.
	Claimed int
	// Skipped is how many claimed rows were never attempted because the pass
	// ran out of budget or was told to stop. They are released at the end of
	// the pass, so the next claim takes them (D274). It is reported because a
	// pass that silently attempted half its batch looks identical to a quiet
	// one.
	Skipped int
}

// deliverAll makes one pass over the claimed deliveries and returns the ones it
// did not attempt, for the caller to release.
//
// # The budget check is not an optimization
//
// ctx carries the pass's budget as a deadline, [maxRun] for either pass. A
// pass of [deliveryLimit] deliveries at [perAttemptTimeout] each, even eight
// at a time, can outlast it, and a request cut off by the pass deadline is
// recorded as a FAILURE against a receiver that may have been perfectly
// healthy — an attempt spent, and the dead letter one step nearer, for the
// sender's own scheduling. So a delivery is not started unless the remaining
// budget can hold a whole attempt.
//
// # stop is not ctx
//
// stop says "start nothing more" and nothing else. Both passes hand in their
// own context's Done and a ctx detached from its cancellation, so a shutdown
// stops the feed while the attempts in flight finish and write their outcome:
// an attempt cut off by shutdown would be a POST the receiver may have taken,
// recorded as nothing, and sent again after the restart.
//
// Every attempt takes one of slots first, the process's [concurrency].
func deliverAll(
	ctx context.Context, stop <-chan struct{}, slots chan struct{},
	p poster, q deliveryQueue, log *slog.Logger, claimed []delivery,
) (passResult, []delivery) {
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		result = passResult{Claimed: len(claimed)}
		left   []delivery
	)

	for i := range claimed {
		if !acquire(ctx, stop, slots) {
			left = append(left, claimed[i:]...)

			break
		}

		d := claimed[i]
		wg.Go(func() {
			defer func() { <-slots }()

			status, dead, err := deliverOne(ctx, p, q, d)

			mu.Lock()
			switch {
			case err != nil:
				result.Failed++
				if dead {
					result.DeadLettered = append(result.DeadLettered, d.ID)
				}
			default:
				result.Delivered++
			}
			mu.Unlock()

			if err != nil {
				log.WarnContext(ctx, "a webhook delivery attempt failed",
					"delivery_id", d.ID,
					"endpoint_id", d.EndpointID,
					"event", d.EventName,
					"attempt", d.Attempts+1,
					"status", status,
					"dead_lettered", dead,
					"error", err)
			}
		})
	}
	wg.Wait()

	result.Skipped = len(left)

	return result, left
}

// acquire takes one of slots when the pass may still start an attempt, and
// reports whether it did.
//
// The budget is read again once the slot is held: waiting for one, while the
// other pass holds all eight, is time the budget no longer has.
func acquire(ctx context.Context, stop <-chan struct{}, slots chan struct{}) bool {
	if stopped(stop) || !budgetAllows(ctx) {
		return false
	}

	select {
	case slots <- struct{}{}:
	case <-stop:
		return false
	case <-ctx.Done():
		return false
	}

	if stopped(stop) || !budgetAllows(ctx) {
		<-slots

		return false
	}

	return true
}

// stopped reports whether stop is closed.
func stopped(stop <-chan struct{}) bool {
	select {
	case <-stop:
		return true
	default:
		return false
	}
}

// budgetAllows reports whether the pass has room for one more whole attempt.
func budgetAllows(ctx context.Context) bool {
	deadline, ok := ctx.Deadline()
	if !ok {
		// No deadline means no budget to run out of. Both passes set one; a
		// caller that does not (a test, a manual run) is not second-guessed
		// into doing nothing.
		return true
	}

	return time.Until(deadline) > perAttemptTimeout
}

// outcomeTimeout bounds the write that records an attempt's outcome.
//
// The write runs on a context of its own, detached from the pass's budget: an
// attempt may end at the budget's last instant, and a 2xx the receiver sent
// that could not then be recorded would be sent again when the lease ends.
const outcomeTimeout = 5 * time.Second

// deliverOne sends one delivery and closes its row.
//
// It returns the status, whether this attempt was the last one allowed, and the
// failure. A failure to WRITE the outcome is returned as the failure too: a
// delivery that landed but whose row could not be closed is worse than one that
// did not land, because the receiver will get it again and nothing here knows.
func deliverOne(
	ctx context.Context, p poster, q deliveryQueue, d delivery,
) (status int, dead bool, err error) {
	result := p.send(ctx, d)

	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), outcomeTimeout)
	defer cancel()

	if result.ok() {
		if writeErr := q.markDelivered(writeCtx, d.ID, result.Status); writeErr != nil {
			return result.Status, false, writeErr
		}

		return result.Status, false, nil
	}

	delay := delayAfter(d.Attempts + 1)
	dead, writeErr := q.markFailed(writeCtx, d.ID, result.Err.Error(), result.Status, delay)
	if writeErr != nil {
		return result.Status, false, writeErr
	}

	return result.Status, dead, result.Err
}
