package errorreport

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
)

// Policy decides what a failure is allowed to carry out of the process.
//
// # Why it is an allow list
//
// A deny list is a list of the leaks somebody already thought of. Every new
// logged attribute is permitted by default under one and forbidden by default
// under the other, and attributes are added by whoever is debugging something
// at the time — which is exactly the moment nobody is thinking about a
// collector in another company's datacenter.
//
// The cost is real and is paid on purpose: a useful attribute stays out of the
// report until somebody adds it here, in a diff, with a reviewer.
//
// # Why the keys of what was dropped still travel
//
// [Redact] returns the removed KEYS. A report that silently omitted them would
// be indistinguishable from a report where the field was never set, and an
// operator would draw conclusions from an absence the policy created.
type Policy struct {
	// Allow are the attribute keys whose VALUES may leave. A key absent from
	// this list is redacted; its name still travels.
	Allow []string
}

// The attribute keys the default policy permits.
//
// Every entry is either a correlation handle or a description of the request's
// SHAPE. None of them is a business identifier, and that omission is the whole
// point: user_id, cart_id, order_id and their kind are logged all over this
// repository and every one of them points at a particular person's records. A
// report is a COPY leaving the building; an installation that wants those in
// its collector adds them here deliberately.
const (
	// KeyRequestID correlates the report with the log line and with the
	// response the client was given.
	KeyRequestID = "request_id"
	// KeyTraceID and KeySpanID bind the report to its trace.
	KeyTraceID = "trace_id"
	KeySpanID  = "span_id"
	// KeyCode is the failure's stable machine code.
	KeyCode = "code"
	// KeyStatus is the HTTP status that was written.
	KeyStatus = "status"
	// KeyMethod and KeyPath are the request's shape. The path is the ROUTED
	// path where one is available; a raw path can carry a handle or a slug
	// somebody typed.
	KeyMethod = "method"
	KeyPath   = "path"
	// KeyPanic is the value a recovered panic carried.
	//
	// It is permitted because a panic report without it says only "something
	// panicked", and the value is produced by the Go runtime in every case this
	// repository can produce. The caveat is written down rather than solved: a
	// deliberate panic(x) with x under a caller's control would put x here.
	KeyPanic = "panic"
	// KeyStack is the stack trace of a panic. It names files and functions,
	// never values.
	KeyStack = "stack"
	// KeyError is the logged error itself and is NEVER allowed through as an
	// attribute. It is named here so the exclusion is visible, and because the
	// handler reads it to extract the code, the kind and the safe message.
	KeyError = "error"
	// KeyAlreadyReported marks a record as a DUPLICATE of one that was already
	// reported, and a record carrying it true is skipped entirely.
	//
	// It exists for the access log. The transport writes one line per request
	// and raises it to ERROR for a 5xx, so every server error is logged TWICE:
	// once by the code that produced it, carrying the machine code, and once by
	// the middleware as a summary, carrying none. Reporting both doubles the
	// volume and — worse — files every 5xx in the application under
	// "unclassified", which is the bucket that has to stay empty enough for a
	// genuinely unclassified failure to be visible in it. It also spends that
	// bucket's rate limit on failures that are already reported properly
	// elsewhere.
	//
	// The marker is set by the PRODUCER, because only the producer knows its
	// line duplicates another one. Detecting the access log by its shape here —
	// "has a status and no error" — would be this package guessing about
	// another package's log format, which is the coupling that has already
	// broken this repository once.
	KeyAlreadyReported = "already_reported"
	// KeyModule, KeyPlugin, KeyProvider, KeyEvent, KeyWorkflow and KeyStep say
	// WHICH part of the system failed. All six carry names from gobit's own
	// source or from configuration.
	KeyModule   = "module"
	KeyPlugin   = "plugin"
	KeyProvider = "provider"
	KeyEvent    = "event"
	KeyWorkflow = "workflow"
	KeyStep     = "step"
)

// DefaultPolicy is the allow list an installation gets without deciding
// anything.
func DefaultPolicy() Policy {
	return Policy{Allow: []string{
		KeyRequestID, KeyTraceID, KeySpanID,
		KeyCode, KeyStatus, KeyMethod, KeyPath,
		KeyPanic, KeyStack,
		KeyModule, KeyPlugin, KeyProvider, KeyEvent, KeyWorkflow, KeyStep,
	}}
}

// Redact splits the attributes into the ones that may travel and the names of
// the ones that may not.
//
// The kept values are rendered as STRINGS. A collector's payload is JSON, JSON
// has a single number type, and an int64 identifier above 2^53 would arrive
// rounded — the same trap the notification payload documents. A string is the
// exact value on both sides.
func (p Policy) Redact(attrs map[string]any) (kept map[string]string, redacted []string) {
	kept = make(map[string]string, len(attrs))

	for key, value := range attrs {
		if key == KeyError || !slices.Contains(p.Allow, key) {
			redacted = append(redacted, key)

			continue
		}
		kept[key] = render(value)
	}

	// Both results are ordered so two reports of the same failure look the
	// same. Map order would make a collector's diff between two occurrences
	// noise rather than signal.
	sort.Strings(redacted)

	return kept, redacted
}

// render turns an attribute value into the string the report carries.
func render(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		// 'g' rather than 'f': a duration in milliseconds and a byte count do
		// not share a sensible fixed precision, and %v would print the same.
		return strconv.FormatFloat(v, 'g', -1, 64)
	case error:
		return v.Error()
	default:
		return fmt.Sprint(v)
	}
}
