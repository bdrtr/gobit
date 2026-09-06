package webhookout

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// perAttemptTimeout bounds one POST.
//
// Ten seconds, and it bounds the request rather than the receiver's work: a
// receiver that needs longer than ten seconds to acknowledge a webhook is doing
// the work inline, which is what the delivery id and the retry exist to let it
// avoid. It is also what keeps a pass's budget arithmetic honest — see
// [maxRun].
const perAttemptTimeout = 10 * time.Second

// concurrency is how many deliveries are in flight at once.
//
// Eight, the same number webpush's fan-out settled on, and the reason is the
// same shape: the limit is about the receivers rather than about this process.
// A pass that opens a hundred connections to one receiver is a burst that
// receiver will rate limit as a whole, and then the failure is the sender's own
// doing rather than the receiver's.
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
func newSender() *sender {
	return &sender{
		client: &http.Client{Timeout: perAttemptTimeout},
		now:    time.Now,
	}
}

// body is the JSON envelope a receiver parses.
//
// # What it carries, and what it deliberately does not
//
// Data is the event's payload as the bus carried it, MINUS the redacted fields
// (see [redactedFields]). Nothing is added to it. The sender does not read the
// order, the product or the customer, and it could not: a plugin may not import
// a module (ADR 0001), so enriching the body would mean a new dependency for
// this plugin and a second definition of "what an order looks like" that
// nothing keeps in step with the first.
//
// That constraint turns out to be the right policy anyway. The bus's payloads
// were already chosen to carry no personal data because the bus is durable; a
// webhook body is the same data one trust boundary further out, sitting in a
// third party's logs. Widening it here would widen it there.
type body struct {
	// ID is the delivery id. It is the receiver's idempotency key and it does
	// not change between retries.
	ID string `json:"id"`
	// Event is the topic.
	Event string `json:"event"`
	// EventID is the bus event's own id, for correlation with gobit's logs.
	EventID string `json:"event_id"`
	// OccurredAt is when the event happened, not when this attempt was made.
	OccurredAt time.Time `json:"occurred_at"`
	// Attempt is 1 on the first try.
	Attempt int64 `json:"attempt"`
	// Data is the event payload, redacted.
	Data map[string]any `json:"data"`
	// Redacted names the fields that were removed.
	//
	// It is in the body rather than omitted, and that is the whole point of
	// having it: a receiver that sees no customer_id cannot tell "this order
	// had no customer" from "we are not told". The list makes the difference
	// visible, which is the same distinction `gobit deadletters` draws when it
	// says a payload was WITHHELD rather than absent.
	Redacted []string `json:"redacted"`
}

// send makes one attempt and reports what happened.
//
// # Which answers count as delivered
//
// Any 2xx. Nothing narrower, because a receiver answering 202 for "queued" is
// the shape this sender wants to encourage, and one answering 204 is a receiver
// that had nothing to say.
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

	payload, err := json.Marshal(body{
		ID:         d.ID,
		Event:      d.EventName,
		EventID:    d.EventID,
		OccurredAt: d.OccurredAt.UTC(),
		Attempt:    attempt,
		Data:       d.Payload,
		Redacted:   d.Redacted,
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
	signature := Sign(d.Secret, SignedFields{
		Timestamp:  timestamp,
		DeliveryID: d.ID,
		Event:      d.EventName,
		Attempt:    attemptText,
		Body:       payload,
	})

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set(HeaderEvent, d.EventName)
	req.Header.Set(HeaderDelivery, d.ID)
	req.Header.Set(HeaderAttempt, attemptText)
	req.Header.Set(HeaderTimestamp, timestamp)
	req.Header.Set(HeaderSignature, signature)
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
	// ran out of budget. They are leased, not lost: they come back when the
	// lease elapses. It is reported because a pass that silently attempted half
	// its batch looks identical to a quiet one.
	Skipped int
}

// deliverAll makes one pass over the claimed deliveries.
//
// # The budget check is not an optimization
//
// The job's context carries [maxRun] as a deadline. A pass of [deliveryLimit]
// deliveries at [perAttemptTimeout] each, even eight at a time, can outlast it,
// and a request cut off by the pass deadline is recorded as a FAILURE against
// a receiver that may have been perfectly healthy — an attempt spent, and the
// dead letter one step nearer, for the sender's own scheduling. So a delivery
// is not started unless the remaining budget can hold a whole attempt.
func deliverAll(
	ctx context.Context, s *sender, st *store, log *slog.Logger, claimed []delivery,
) passResult {
	var (
		mu     sync.Mutex
		result = passResult{Claimed: len(claimed)}
	)

	work := make(chan delivery)
	var wg sync.WaitGroup

	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for d := range work {
				status, dead, err := deliverOne(ctx, s, st, d)

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
			}
		}()
	}

	for i := range claimed {
		if !budgetAllows(ctx) {
			result.Skipped++

			continue
		}
		work <- claimed[i]
	}
	close(work)
	wg.Wait()

	return result
}

// budgetAllows reports whether the pass has room for one more whole attempt.
func budgetAllows(ctx context.Context) bool {
	deadline, ok := ctx.Deadline()
	if !ok {
		// No deadline means no budget to run out of. The job always sets one;
		// a caller that does not (a test, a manual run) is not second-guessed
		// into doing nothing.
		return true
	}

	return time.Until(deadline) > perAttemptTimeout
}

// deliverOne sends one delivery and closes its row.
//
// It returns the status, whether this attempt was the last one allowed, and the
// failure. A failure to WRITE the outcome is returned as the failure too: a
// delivery that landed but whose row could not be closed is worse than one that
// did not land, because the receiver will get it again and nothing here knows.
func deliverOne(
	ctx context.Context, s *sender, st *store, d delivery,
) (status int, dead bool, err error) {
	result := s.send(ctx, d)
	if result.ok() {
		if writeErr := st.markDelivered(ctx, d.ID, result.Status); writeErr != nil {
			return result.Status, false, writeErr
		}

		return result.Status, false, nil
	}

	delay := delayAfter(d.Attempts + 1)
	dead, writeErr := st.markFailed(ctx, d.ID, result.Err.Error(), result.Status, delay)
	if writeErr != nil {
		return result.Status, false, writeErr
	}

	return result.Status, dead, result.Err
}
