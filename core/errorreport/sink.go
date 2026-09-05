// Package errorreport carries gobit's failures to an outside collector.
//
// # The shape of it
//
// The contract is in [github.com/bdrtr/gobit/core/provider], the
// concrete reporter is in a plugin, and this package is the part in between:
// it decides WHAT may be sent ([Policy]), HOW OFTEN ([Limiter]), and it feeds
// on the logging that already happens ([Handler]).
//
// # Why it feeds on the log
//
// The alternative is a reporting call at every failure site. This repository
// has three places a request can fail through and all three already log:
// corehttp.WriteError writes the server-error line, corehttp.Recoverer writes
// the panic line, and everything else reaches slog.ErrorContext directly.
// Wrapping the log handler covers all three with one hook and adds no
// obligation to the code that fails — a call somebody could forget to make is
// a report nobody gets.
//
// # What it deliberately does not do
//
// It does not decide whether an error is worth reporting by its Kind. Every
// record at or above the configured level is a candidate, because "which
// failures matter" is a question about an installation and not about a
// framework, and a framework that answered it would be answering it wrong for
// somebody.
package errorreport

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/provider"
)

// CodeAlreadyBound reports a second attempt to bind a reporter.
const CodeAlreadyBound = "error_reporter_already_bound"

// Sink is the single point the core reports failures through.
//
// # Why the indirection exists
//
// The logger is built FIRST, before the configuration is fully applied and long
// before the plugins are installed — everything after it needs something to log
// to. The reporter arrives with a plugin. The sink is the seam: the log handler
// is wired to the sink at startup, the sink is empty, and binding it later
// makes every subsequent failure report without rebuilding the logger.
//
// An unbound sink DROPS. That is the normal case — most installations run
// without a collector — and it is the opposite of the choice adminui.Ring makes
// for its guard, where unbound REJECTS. The difference is what the component
// is for: a guard that is not wired must not let requests through, and a
// reporter that is not wired must not stop them.
type Sink struct {
	// bound holds the reporter. atomic.Pointer rather than a mutex because it
	// is READ on every failing request and written exactly once.
	bound atomic.Pointer[provider.ErrorReporter]
	// broken is set when the reporter panicked. See [Sink.Report].
	broken atomic.Bool
}

// NewSink builds an empty sink.
func NewSink() *Sink { return &Sink{} }

// Bind installs the reporter. It may be called ONCE.
//
// A second call is an error rather than a replacement: two plugins each
// believing they own the reporting is a configuration mistake, and silently
// letting the second win would send the failures somewhere the operator who
// installed the first one is not looking.
func (s *Sink) Bind(reporter provider.ErrorReporter) error {
	if reporter == nil {
		return coreerrors.Invalid(CodeAlreadyBound, "the error reporter is nil")
	}
	if !s.bound.CompareAndSwap(nil, &reporter) {
		return coreerrors.Conflict(CodeAlreadyBound,
			"an error reporter is already bound (%s); there can be only one",
			(*s.bound.Load()).ID())
	}

	return nil
}

// Reporter returns the bound reporter, or nil.
func (s *Sink) Reporter() provider.ErrorReporter {
	held := s.bound.Load()
	if held == nil {
		return nil
	}

	return *held
}

// Report hands one event to the reporter, if there is one.
//
// # The panic guard is not defensive decoration
//
// This call happens inside a log handler, which is inside a request, which is
// inside corehttp.Recoverer. A reporter that panics would be recovered there,
// the recovery would LOG the panic, the log would come back through this
// handler, and the reporter would panic again — a loop that turns one bad
// plugin into a process that does nothing else.
//
// So a panicking reporter is switched OFF for the lifetime of the process and
// said so on stderr, once. Stderr rather than the logger for the same reason:
// the logger is the thing that got us here.
func (s *Sink) Report(ctx context.Context, event provider.ErrorEvent) {
	if s.broken.Load() {
		return
	}
	reporter := s.Reporter()
	if reporter == nil {
		return
	}

	defer func() {
		rec := recover()
		if rec == nil {
			return
		}
		if s.broken.CompareAndSwap(false, true) {
			fmt.Fprintf(os.Stderr,
				"gobit: the error reporter %q panicked and was switched off: %v\n",
				reporter.ID(), rec)
		}
	}()

	reporter.Report(ctx, event)
}

// Close releases the reporter. It is safe on an unbound sink.
func (s *Sink) Close(ctx context.Context) error {
	reporter := s.Reporter()
	if reporter == nil {
		return nil
	}

	return reporter.Close(ctx)
}
