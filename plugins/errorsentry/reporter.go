package errorsentry

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
)

// The reporter's fixed numbers.
const (
	// queueDepth is how many events wait for the sender.
	//
	// The queue is bounded and a full one DROPS. The alternative — blocking the
	// caller — would put a collector's latency on the request path, which is
	// the one thing [coreprovider.ErrorReporter.Report] promises never to do.
	// The core's rate limit means a full queue here needs many DISTINCT codes
	// at once, which is an outage, and during an outage a few hundred examples
	// answer every question a few thousand would.
	queueDepth = 256
	// sendTimeout bounds one POST.
	sendTimeout = 5 * time.Second
	// The Sentry protocol version this client speaks.
	sentryVersion = "7"
	// clientName identifies gobit in the auth header.
	clientName = "gobit"
)

// codeSendFailed reports that a report could not be delivered.
const codeSendFailed = "sentry_send_failed"

// Reporter posts gobit's failures to Sentry.
//
// # Everything happens on one goroutine
//
// Report only enqueues. A single sender drains the queue, which means the
// collector sees the events in the order they happened and gobit opens exactly
// one connection to it no matter how many requests fail at once. A goroutine
// per event would answer an outage — every request failing — by starting a
// thousand outbound connections at the moment the process is least able to
// afford them.
//
// # It never retries
//
// A failed POST is logged by the sender and dropped. Retrying would mean a
// process already in trouble spending its remaining capacity talking to a
// collector, and a collector that is down is usually down for longer than a
// retry loop is willing to wait.
type Reporter struct {
	dsn    dsn
	client *http.Client
	// environment and release describe the deployment; both may be empty.
	environment string
	release     string

	queue chan coreprovider.ErrorEvent
	done  chan struct{}

	// mu guards closed against a Report racing Close. It is an RWMutex because
	// Report only READS the flag and must not serialize with other reports.
	mu     sync.RWMutex
	closed bool

	// dropped counts the events a full queue refused. The count rides along on
	// the next event that fits, so a burst that overflowed does not look like a
	// burst that did not happen.
	dropped atomic.Int64

	// onSendError is called when a POST fails. It is a field so the tests can
	// observe a failure without reading a log.
	onSendError func(error)
}

// The reporter satisfies the core's contract.
var _ coreprovider.ErrorReporter = (*Reporter)(nil)

// newReporter builds a reporter and starts its sender.
func newReporter(d dsn, environment, release string, onSendError func(error)) *Reporter {
	if onSendError == nil {
		onSendError = func(error) {}
	}

	r := &Reporter{
		dsn:         d,
		client:      &http.Client{Timeout: sendTimeout},
		environment: environment,
		release:     release,
		queue:       make(chan coreprovider.ErrorEvent, queueDepth),
		done:        make(chan struct{}),
		onSendError: onSendError,
	}
	go r.run()

	return r
}

// ID names the reporter.
func (r *Reporter) ID() string { return ProviderID }

// Report queues one failure. It never blocks and never fails.
//
// The caller's context is DELIBERATELY dropped. It belongs to the request that
// failed, and that request is finished — often canceled, which is why it failed
// — by the time the sender picks the event up. Carrying it would cancel every
// report at the moment the thing worth reporting ended.
func (r *Reporter) Report(_ context.Context, event coreprovider.ErrorEvent) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.closed {
		return
	}

	select {
	case r.queue <- event:
	default:
		r.dropped.Add(1)
	}
}

// Close stops accepting events and waits for the queue to drain.
//
// This is the one call that blocks, and it is the reason it exists: the reports
// of the failures that happened just before the process stopped are the ones
// most worth having, and they are exactly the ones an unflushed queue loses.
// The wait is bounded by the caller's context — a collector that stopped
// answering must not hold the shutdown open.
func (r *Reporter) Close(ctx context.Context) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()

		return nil
	}
	r.closed = true
	close(r.queue)
	r.mu.Unlock()

	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return coreerrors.Wrap(ctx.Err(), coreerrors.KindUnavailable, codeSendFailed,
			"the queued error reports could not be flushed before the deadline")
	}
}

// run drains the queue until it is closed.
func (r *Reporter) run() {
	defer close(r.done)

	for event := range r.queue {
		if err := r.send(event); err != nil {
			r.onSendError(err)
		}
	}
}

// send posts one envelope.
//
// The context is built here rather than passed in, for the reason written on
// [Reporter.Report]: there is no live context to inherit from. The sender runs
// on its own goroutine, after the request is over.
func (r *Reporter) send(event coreprovider.ErrorEvent) error {
	id, err := eventID()
	if err != nil {
		return err
	}

	body, err := r.envelope(id, event)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.dsn.endpoint, bytes.NewReader(body))
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindInternal, codeSendFailed,
			"the report request could not be built")
	}
	req.Header.Set("Content-Type", "application/x-sentry-envelope")
	req.Header.Set("X-Sentry-Auth", strings.Join([]string{
		"Sentry sentry_version=" + sentryVersion,
		"sentry_client=" + clientName,
		"sentry_key=" + r.dsn.publicKey,
	}, ", "))

	resp, err := r.client.Do(req)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindUnavailable, codeSendFailed,
			"the collector could not be reached")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusBadRequest {
		return coreerrors.Unavailable(codeSendFailed,
			"the collector refused the report with status %d", resp.StatusCode)
	}

	return nil
}

// envelope builds Sentry's envelope: a header line, an item header line and the
// item itself, each on its own line.
func (r *Reporter) envelope(id string, event coreprovider.ErrorEvent) ([]byte, error) {
	payload, err := json.Marshal(r.payload(id, event))
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInternal, codeSendFailed,
			"the report could not be encoded")
	}

	header, err := json.Marshal(map[string]any{"event_id": id})
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInternal, codeSendFailed,
			"the envelope header could not be encoded")
	}

	item, err := json.Marshal(map[string]any{"type": "event", "length": len(payload)})
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInternal, codeSendFailed,
			"the envelope item header could not be encoded")
	}

	var out bytes.Buffer
	out.Write(header)
	out.WriteByte('\n')
	out.Write(item)
	out.WriteByte('\n')
	out.Write(payload)
	out.WriteByte('\n')

	return out.Bytes(), nil
}

// payload builds the event Sentry stores.
//
// # The fingerprint is the code, not the stack
//
// Sentry groups by a fingerprint and by default derives one from the stack
// trace. That is the wrong key here: a stack moves when a function is renamed
// and the same failure then appears as a brand new issue, while gobit's error
// codes are part of the wire contract and do not move. Pinning the fingerprint
// to the code makes "this failure, over time" a question the collector can
// answer.
//
// # The stack is an extra, not an exception
//
// A Go stack could be parsed into Sentry's exception frames and would render
// more prettily. It is left as text on purpose: the parse would be a guess
// about runtime.Stack's format, and a guess that goes wrong turns a panic
// report into an empty exception with no text anywhere.
func (r *Reporter) payload(id string, event coreprovider.ErrorEvent) map[string]any {
	extra := make(map[string]any, len(event.Attrs)+4)
	for key, value := range event.Attrs {
		extra[key] = value
	}
	if event.Detail != "" {
		extra["detail"] = event.Detail
	}
	if event.Stack != "" {
		extra["stack"] = event.Stack
	}
	if len(event.Redacted) > 0 {
		extra["redacted"] = strings.Join(event.Redacted, ", ")
	}
	if event.Suppressed > 0 {
		extra["suppressed"] = strconv.Itoa(event.Suppressed)
	}
	if dropped := r.dropped.Swap(0); dropped > 0 {
		extra["dropped_by_full_queue"] = strconv.FormatInt(dropped, 10)
	}

	tags := map[string]any{"code": event.Code, "kind": event.Kind}
	if event.RequestID != "" {
		tags["request_id"] = event.RequestID
	}

	payload := map[string]any{
		"event_id":    id,
		"timestamp":   event.Time.UTC().Format(time.RFC3339Nano),
		"platform":    "go",
		"level":       "error",
		"logger":      clientName,
		"fingerprint": []string{event.Code},
		"message":     map[string]any{"formatted": title(event)},
		"tags":        tags,
		"extra":       extra,
	}
	if r.environment != "" {
		payload["environment"] = r.environment
	}
	if r.release != "" {
		payload["release"] = r.release
	}
	if event.TraceID != "" {
		payload["contexts"] = map[string]any{"trace": map[string]any{
			"type": "trace", "trace_id": event.TraceID, "span_id": event.SpanID,
		}}
	}

	return payload
}

// title is the line the collector shows for the issue.
//
// Both halves are safe to send: the message is a literal from gobit's own
// source, and the detail is the typed error's own Message, which the errors
// package documents as carrying nothing sensitive.
func title(event coreprovider.ErrorEvent) string {
	if event.Detail == "" {
		return event.Message
	}

	return event.Message + " — " + event.Detail
}

// eventID produces the 32 hex characters Sentry identifies an event by.
func eventID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", coreerrors.Wrap(err, coreerrors.KindInternal, codeSendFailed,
			"an event id could not be generated")
	}

	return hex.EncodeToString(raw[:]), nil
}
