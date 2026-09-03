package errorotlp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
)

// The reporter's fixed numbers.
const (
	// queueDepth is how many events wait for the sender. A full queue DROPS
	// rather than blocking: blocking would put the collector's latency on the
	// request path, which is the one thing
	// [coreprovider.ErrorReporter.Report] promises never to do.
	queueDepth = 256
	// sendTimeout bounds one POST.
	sendTimeout = 5 * time.Second
	// severityError is OpenTelemetry's severity number for ERROR. The log
	// record model has no "level" string that means anything on its own; the
	// NUMBER is what a collector filters and routes on, and the text beside it
	// is for humans.
	severityError = 17
	// scopeName names the instrumentation scope the records come from. It is
	// the only place in the payload that says which part of gobit produced
	// them.
	scopeName = "gobit/errorreport"
)

// codeSendFailed reports that a report could not be delivered.
const codeSendFailed = "otlp_send_failed"

// resource describes the process the reports come from.
//
// It is sent on EVERY request rather than negotiated once: OTLP has no session,
// so a collector that received a resource-less batch would have to guess which
// service it belonged to.
type resource struct {
	service     string
	environment string
	release     string
}

// Reporter posts gobit's failures to an OpenTelemetry collector.
//
// The lifecycle is the one ADR 0014 requires and it is deliberately the same as
// the Sentry reporter's: one sender goroutine, a bounded queue that drops rather
// than blocking, no retries, and a Close that flushes. Writing it twice is what
// showed it belongs to the CONTRACT rather than to Sentry — see the ADR.
type Reporter struct {
	endpoint string
	headers  map[string]string
	res      resource
	client   *http.Client

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
func newReporter(endpoint string, headers map[string]string, res resource, onSendError func(error)) *Reporter {
	if onSendError == nil {
		onSendError = func(error) {}
	}

	r := &Reporter{
		endpoint:    endpoint,
		headers:     headers,
		res:         res,
		client:      &http.Client{Timeout: sendTimeout},
		queue:       make(chan coreprovider.ErrorEvent, queueDepth),
		done:        make(chan struct{}),
		onSendError: onSendError,
	}

	go r.run()

	return r
}

// ID names the reporter for the startup log.
func (r *Reporter) ID() string { return ProviderID }

// Report queues one failure. It never blocks and never fails.
//
// The caller's context is DELIBERATELY dropped. It belongs to the request that
// failed, and that request is finished — often canceled, which is why it failed
// — by the time the sender picks the event up.
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

// Close flushes the queue and stops the sender.
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

// run drains the queue on a single goroutine.
func (r *Reporter) run() {
	defer close(r.done)

	for event := range r.queue {
		if err := r.send(event); err != nil {
			r.onSendError(err)
		}
	}
}

// send POSTs one record and never retries.
func (r *Reporter) send(event coreprovider.ErrorEvent) error {
	body, err := json.Marshal(r.payload(event))
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindInternal, codeSendFailed,
			"the report could not be encoded")
	}

	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindInternal, codeSendFailed,
			"the report request could not be built")
	}
	req.Header.Set("Content-Type", "application/json")
	for name, value := range r.headers {
		req.Header.Set(name, value)
	}

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

// payload builds one OTLP/HTTP JSON logs request.
//
// # Where the fields go, and what had to change shape
//
// The log record has four fields of its own — time, severity, body, trace
// binding — and everything else is an attribute. So:
//
//   - Message becomes the BODY. It is the literal from gobit's source, which is
//     what a body is meant to be: the same failure produces the same body.
//   - Code becomes two attributes. error.code is gobit's own name for it, and
//     exception.type is the OpenTelemetry convention a collector's error views
//     read. The duplication is deliberate: a collector that knows nothing about
//     gobit still groups the reports correctly, and one that does can query the
//     name it already knows.
//   - Stack becomes exception.stacktrace and is present for PANICS only. An
//     error's report has no stack at all, and the OTel model has no complaint
//     about that — unlike a collector whose issue view is built out of frames.
//   - Redacted travels as a joined string. The names of the dropped keys are
//     part of the report (ADR 0014, decision 4) and an attribute value is a
//     scalar here.
func (r *Reporter) payload(event coreprovider.ErrorEvent) map[string]any {
	attrs := make([]map[string]any, 0, len(event.Attrs)+10)
	add := func(key, value string) {
		if value == "" {
			return
		}

		attrs = append(attrs, stringAttr(key, value))
	}

	add("error.code", event.Code)
	add("exception.type", event.Code)
	add("error.kind", event.Kind)
	add("error.detail", event.Detail)
	add("exception.stacktrace", event.Stack)
	add("request.id", event.RequestID)

	if len(event.Redacted) > 0 {
		add("error.redacted", strings.Join(event.Redacted, ", "))
	}
	if event.Suppressed > 0 {
		add("error.suppressed", strconv.Itoa(event.Suppressed))
	}
	if dropped := r.dropped.Swap(0); dropped > 0 {
		add("error.dropped_by_full_queue", strconv.FormatInt(dropped, 10))
	}

	for key, value := range event.Attrs {
		add(key, value)
	}

	record := map[string]any{
		"timeUnixNano":   strconv.FormatInt(event.Time.UTC().UnixNano(), 10),
		"severityNumber": severityError,
		"severityText":   "ERROR",
		"body":           map[string]any{"stringValue": event.Message},
		"attributes":     attrs,
	}
	// The trace binding is the record's OWN field, not an attribute: it is what
	// makes the collector show the report on the trace that produced it. When
	// telemetry is off there is no trace and the fields stay absent — a zeroed
	// trace id would point at a trace that does not exist.
	if event.TraceID != "" {
		record["traceId"] = event.TraceID
		record["spanId"] = event.SpanID
	}

	return map[string]any{
		"resourceLogs": []map[string]any{{
			"resource": map[string]any{"attributes": r.resourceAttrs()},
			"scopeLogs": []map[string]any{{
				"scope":      map[string]any{"name": scopeName},
				"logRecords": []map[string]any{record},
			}},
		}},
	}
}

// resourceAttrs describes the process the reports come from.
func (r *Reporter) resourceAttrs() []map[string]any {
	attrs := []map[string]any{stringAttr("service.name", r.res.service)}
	if r.res.environment != "" {
		attrs = append(attrs, stringAttr("deployment.environment", r.res.environment))
	}
	if r.res.release != "" {
		attrs = append(attrs, stringAttr("service.version", r.res.release))
	}

	return attrs
}

// stringAttr renders one OTLP key/value pair.
//
// Every value gobit reports is already a string ([coreprovider.ErrorEvent]
// renders them that way), so the union OTLP allows for a value has exactly one
// arm here.
func stringAttr(key, value string) map[string]any {
	return map[string]any{"key": key, "value": map[string]any{"stringValue": value}}
}
