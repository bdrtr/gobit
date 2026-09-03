package provider

import (
	"context"
	"time"
)

// This file is the contract of an ERROR REPORTER — the component that tells an
// outside collector (Sentry and its kind) that a request failed.
//
// # Why it is here and not in a module
//
// For the same reason as the other four contracts in this package: the concrete
// reporter lives in a plugin, the plugin may import no module (Principle 2.4),
// and the code that PRODUCES the failures is the core itself. A contract in a
// module would make error reporting depend on a commerce module being installed.
//
// # How it differs from the other four
//
// The other providers are SELECTED per transaction: a payment goes to the
// provider whose ID the order names, and picking the wrong one is a bug the
// checkout can see. A reporter is not selected, it is INSTALLED — there is at
// most one, nothing chooses it, and no request outcome depends on it. That is
// why [ErrorReporter] does not embed [Provider]: an ID that nothing selects by
// would be a promise of a lookup that does not exist. The reporter still names
// itself, for the startup log and for the operator who has to know which
// collector the process is talking to.

// ErrorEvent is one failure, as much of it as the core is willing to send.
//
// # It carries strings, never the error
//
// The type deliberately holds no `error`. A reporter handed the real error
// could walk the chain and ship whatever it found there — a driver message with
// a connection string, a query with its parameters bound in — and the core's
// decision about what may leave the process would be advice rather than a rule.
// What a reporter cannot receive, it cannot send.
//
// The fields are filled by the core's reporting policy; see the
// internal/core/errorreport package for what each one is allowed to contain.
type ErrorEvent struct {
	// Time is when the failure was logged.
	Time time.Time
	// Message is the log message. It is a LITERAL from gobit's own source —
	// "request ended with a server error", "the handler panicked" — so it
	// carries no runtime data at all.
	Message string
	// Code is the stable machine code of the failure ("product_not_found").
	//
	// It is the FINGERPRINT a collector should group by. It is better than a
	// stack trace for that job: a stack changes when a function moves and the
	// same failure then appears as a new issue, while a code is written into
	// the wire contract and does not move.
	Code string
	// Kind is the error class ("internal", "unavailable", …).
	Kind string
	// Detail is the typed error's own Message field, which
	// [github.com/bdrtr/gobit/internal/core/errors.Error] documents as
	// containing NO sensitive data. That documented promise is the whole reason
	// this field may cross the process boundary while the wrapped chain
	// underneath it may not.
	Detail string
	// Stack is the stack trace, and it is set for a PANIC only.
	//
	// An ordinary error has no useful stack here: it was returned, not thrown,
	// and by the time the log line is written the frames that produced it are
	// gone. A fabricated stack pointing at the logging call would be worse than
	// none, because it looks like an answer.
	Stack string
	// RequestID correlates the report with the log line and with the response
	// the client received.
	RequestID string
	// TraceID and SpanID bind the report to its trace when telemetry is on.
	TraceID string
	SpanID  string
	// Attrs are the remaining log attributes that passed the core's allow
	// list. Values are rendered as strings.
	Attrs map[string]string
	// Redacted names the attribute keys that were REMOVED. The keys travel and
	// the values do not: an operator has to be able to see that something was
	// dropped, or a missing field looks like a field that was never set.
	Redacted []string
	// Suppressed is how many identical failures were dropped by the rate limit
	// since the last report of this code. Zero means none.
	Suppressed int
}

// ErrorReporter sends failures to an outside collector.
type ErrorReporter interface {
	// ID names the reporter for the startup log ("sentry").
	ID() string

	// Report hands one failure over. It RETURNS NOTHING, and that is the
	// contract, not an omission.
	//
	// A reporter exists to observe failures; a reporter that could fail a
	// request would make the observation of an outage into a second outage. Any
	// error it meets — a refused connection, a rejected payload, a full queue —
	// it handles itself, and the only honest thing it can do about it is log.
	//
	// It MUST NOT block. The caller is inside a log handler, which is inside a
	// request; a synchronous HTTP call to a collector would add that
	// collector's latency to every failing request, and its outage to ours.
	Report(ctx context.Context, event ErrorEvent)

	// Close flushes what is queued and releases the reporter.
	//
	// It is called during shutdown and it is the ONE place a reporter may
	// block: the reports of the failures that happened just before the process
	// stopped are the ones most worth having, and they are exactly the ones an
	// unflushed queue loses.
	Close(ctx context.Context) error
}
