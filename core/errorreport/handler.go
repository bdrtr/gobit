package errorreport

import (
	"context"
	"log/slog"
	"strings"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/provider"
)

// unclassifiedCode is the fingerprint an error with no code reports under.
//
// It is a constant rather than an empty string so the collector groups these
// together under a name that says what they are, instead of under "" — which
// reads as a missing field rather than as a class of failure worth fixing.
const unclassifiedCode = "unclassified"

// Options configure the reporting handler.
type Options struct {
	// Level is the lowest level that gets reported. The zero value means
	// [slog.LevelError].
	Level slog.Level
	// Policy decides which attributes may travel. The zero value means
	// [DefaultPolicy].
	Policy Policy
	// Limiter caps how often one code is reported. Nil means a limiter with
	// the defaults.
	Limiter *Limiter
}

// Handler forwards the failures it sees to a [Sink] and passes every record on
// to the handler underneath.
//
// # Ordering
//
// The wrapped handler runs FIRST and its error is what Handle returns. Logging
// is the durable record; reporting is a courtesy to a service in another
// datacenter. A collector that was slow, broken or misconfigured must not be
// able to cost the operator the log line.
type Handler struct {
	next    slog.Handler
	sink    *Sink
	level   slog.Level
	policy  Policy
	limiter *Limiter

	// attrs are the attributes accumulated through WithAttrs, already
	// qualified with the groups that were open at the time.
	attrs map[string]any
	// groups are the groups open now; they qualify the attributes of the
	// records that arrive from here on.
	groups []string
}

// NewHandler wraps next so that failing records also reach the sink.
func NewHandler(next slog.Handler, sink *Sink, opts Options) *Handler {
	if opts.Level == 0 {
		opts.Level = slog.LevelError
	}
	if len(opts.Policy.Allow) == 0 {
		opts.Policy = DefaultPolicy()
	}
	if opts.Limiter == nil {
		opts.Limiter = NewLimiter(DefaultBurst, DefaultWindow, nil)
	}

	return &Handler{
		next: next, sink: sink,
		level: opts.Level, policy: opts.Policy, limiter: opts.Limiter,
		attrs: map[string]any{},
	}
}

// Middleware returns the wrapper in the shape logger.Options.Middleware takes.
func Middleware(sink *Sink, opts Options) func(slog.Handler) slog.Handler {
	return func(next slog.Handler) slog.Handler { return NewHandler(next, sink, opts) }
}

// Enabled defers to the handler underneath.
//
// It deliberately does NOT widen the level to include everything this handler
// would report. Reporting rides on logging: a level the operator turned off is
// a level they decided not to keep, and reporting it anyway would send a
// collector records that exist nowhere else.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

// WithAttrs returns a handler carrying the given attributes.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}

	clone := h.clone()
	clone.next = h.next.WithAttrs(attrs)
	for _, attr := range attrs {
		flatten(clone.attrs, h.groups, attr)
	}

	return clone
}

// WithGroup returns a handler whose later attributes are qualified by name.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	clone := h.clone()
	clone.next = h.next.WithGroup(name)
	clone.groups = append(append([]string{}, h.groups...), name)

	return clone
}

// clone copies the handler's own state; next is set by the caller.
func (h *Handler) clone() *Handler {
	attrs := make(map[string]any, len(h.attrs))
	for key, value := range h.attrs {
		attrs[key] = value
	}

	return &Handler{
		next: h.next, sink: h.sink,
		level: h.level, policy: h.policy, limiter: h.limiter,
		attrs: attrs, groups: h.groups,
	}
}

// Handle writes the record and, when it is a failure, reports it.
func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	err := h.next.Handle(ctx, record)

	if record.Level < h.level || h.sink == nil || h.sink.Reporter() == nil {
		return err
	}
	h.report(ctx, record)

	return err
}

// report turns the record into an event and hands it to the sink.
func (h *Handler) report(ctx context.Context, record slog.Record) {
	attrs := make(map[string]any, len(h.attrs)+record.NumAttrs())
	for key, value := range h.attrs {
		attrs[key] = value
	}
	record.Attrs(func(attr slog.Attr) bool {
		flatten(attrs, h.groups, attr)

		return true
	})

	// A record the producer marked as a duplicate is dropped before anything
	// else, including the rate limit: it must not spend a budget that belongs
	// to the failure it duplicates.
	if reported, _ := attrs[KeyAlreadyReported].(bool); reported {
		return
	}

	code, kind, detail := classify(attrs[KeyError])

	// The limiter is asked LAST, after the code is known, because the code is
	// what it groups by. Asking earlier would group every failure of a busy
	// endpoint together and hide the rare one among the common ones.
	allowed, suppressed := h.limiter.Allow(code)
	if !allowed {
		return
	}

	kept, redacted := h.policy.Redact(attrs)
	// The three correlation handles are lifted into their own fields and out of
	// the map. Leaving them in both places would make a collector show each one
	// twice, once as a tag and once as an ordinary attribute.
	requestID, traceID, spanID := kept[KeyRequestID], kept[KeyTraceID], kept[KeySpanID]
	delete(kept, KeyRequestID)
	delete(kept, KeyTraceID)
	delete(kept, KeySpanID)
	stack := kept[KeyStack]
	delete(kept, KeyStack)

	h.sink.Report(ctx, provider.ErrorEvent{
		Time:       record.Time,
		Message:    record.Message,
		Code:       code,
		Kind:       kind,
		Detail:     detail,
		Stack:      stack,
		RequestID:  requestID,
		TraceID:    traceID,
		SpanID:     spanID,
		Attrs:      kept,
		Redacted:   redacted,
		Suppressed: suppressed,
	})
}

// classify reads the fingerprint and the safe message out of a logged error.
//
// Only [coreerrors.Error.Message] is taken as the detail. The wrapped chain
// underneath it is left where it is: it is written by drivers and libraries
// that promise nothing about their contents, and it is where a connection
// string or a bound query parameter would be.
func classify(value any) (code, kind, detail string) {
	err, ok := value.(error)
	if !ok || err == nil {
		return unclassifiedCode, coreerrors.KindInternal.String(), ""
	}

	code = coreerrors.CodeOf(err)
	if code == "" {
		code = unclassifiedCode
	}
	kind = coreerrors.KindOf(err).String()

	var typed *coreerrors.Error
	if coreerrors.As(err, &typed) && typed != nil {
		detail = typed.Message
	}

	return code, kind, detail
}

// flatten writes one attribute into the map, qualifying it with the open groups
// and expanding a group attribute into its members.
func flatten(into map[string]any, groups []string, attr slog.Attr) {
	value := attr.Value.Resolve()

	if value.Kind() == slog.KindGroup {
		members := value.Group()
		// An empty group is dropped, which is slog's own rule for it.
		if attr.Key != "" {
			groups = append(append([]string{}, groups...), attr.Key)
		}
		for _, member := range members {
			flatten(into, groups, member)
		}

		return
	}
	if attr.Key == "" {
		return
	}

	into[qualify(groups, attr.Key)] = value.Any()
}

// qualify joins the open group names with the attribute's key.
func qualify(groups []string, key string) string {
	if len(groups) == 0 {
		return key
	}

	return strings.Join(groups, ".") + "." + key
}
