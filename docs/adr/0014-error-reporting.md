# ADR 0014 — Failures are reported to an outside collector through a core contract, and the core decides what may leave

- **Status:** Accepted
- **Date:** 2026-09-03
- **Related:** ADR 0001 (resolution by name), ADR 0007 (observability does not
  gate correctness), ADR 0012 (working language)

## Context

gobit logs its failures well. `corehttp.WriteError` writes one line for every
server error, `corehttp.Recoverer` writes one for every panic, and an arch test
keeps both doors single. What logging does not do is tell anybody. A log line
sits in a file or a log service until somebody goes looking, and the person who
would go looking is usually the one who has not yet been told there is a
problem.

The expensive parts of a good error-reporting integration were already in place
before this decision:

- **One door.** Response bodies only leave through `WriteError`/`WriteJSON`, and
  panics only through `Recoverer`. Three hooks cover every failure; a reporting
  call did not have to be sprinkled through the modules.
- **A stable fingerprint.** Every typed error carries a machine `Code`
  (`product_not_found`). It is a better grouping key than a stack trace, which
  is what collectors reach for by default: a stack moves when a function is
  renamed and the same failure reappears as a brand new issue, while a code is
  part of the wire contract and does not move.
- **Correlation.** `request_id` is in the response header AND in the error body.
  When a customer quotes the id they were shown, the incident is one search away.
- **Trace linkage.** W3C TraceContext is set up, so `trace_id`/`span_id` bind a
  report to its trace.
- **Release and environment.** `version` comes from ldflags and `APP_ENV` from
  configuration, which is what a collector's regression tracking keys off.

So the question this ADR answers is not "how do we call Sentry". It is **what
is reported, and what must never leave the process** — a question that has to be
answered in the core, because a process holding customers' addresses and orders
is about to start talking to a service in somebody else's datacenter.

## Decision

**1. The contract lives in the core; the implementation lives in a plugin.**
`provider.ErrorReporter` sits beside the four commerce provider contracts, for
the same reason they are there: the concrete reporter is in a plugin, a plugin
may import no module (Principle 2.4), and the code that PRODUCES failures is the
core itself. `plugins/errorsentry` is the first implementation and `plugins/errorotlp` the
second; the core holds ONE reporter, so an installation picks between them.

It does NOT embed `provider.Provider`. The other four providers are SELECTED per
transaction — a payment goes to the provider the order names — while a reporter
is INSTALLED: there is at most one and nothing chooses it. An `ID()` that nothing
looks up by would promise a lookup that does not exist, so the reporter names
itself for the startup log and for no other purpose.

**2. The feed is the LOG, through a `slog.Handler` wrapper.** Every failure in
this repository already logs, so wrapping the handler covers all three doors at
once and adds no obligation to the code that fails. A reporting call somebody has
to remember to make is a report somebody does not get.

The wrapper is installed through `logger.Options.Middleware`, a function field.
The logger package therefore still knows nothing about reporting — the package
everything depends on did not gain a dependency on a collector integration.

**3. The reporter never receives the error.** `provider.ErrorEvent` holds strings
only. A reporter handed the real error could walk the chain and ship whatever it
found — a driver message with a connection string, a query with its parameters
bound in — and the core's decision about what may leave would be advice rather
than a rule. **What a reporter cannot receive, it cannot send.**

**4. Attributes travel by ALLOW LIST, and the redacted key NAMES travel too.** A
deny list is a list of the leaks somebody already thought of; attributes are added
by whoever is debugging something, which is exactly the moment nobody is thinking
about a collector. The default list holds correlation handles and the request's
shape. It deliberately holds **no business identifier** — `user_id`, `cart_id`,
`order_id` and their kind are logged all over this repository and every one of
them points at a particular person's records. An installation that wants them in
its collector adds them itself, in a diff, with a reviewer.

The names of what was dropped still travel. A report that silently omitted them
would be indistinguishable from one where the field was never set, and an
operator would draw conclusions from an absence the policy created.

**5. Two kinds of free text may leave, and both have a written promise behind
them.** The log message, which is a literal in gobit's own source, and
`errors.Error.Message`, whose godoc says it must contain no sensitive data. The
wrapped chain underneath it stays in the process. This is the one place where a
promise the repository already made — made for the HTTP response body — is being
relied on for a second purpose, and it is written down here so that weakening it
is understood to break two things.

**6. Reports are rate limited PER CODE, and the suppressed count rides the next
report.** An outage does not produce one failure, it produces every failure at
once. An overall limit would fill with whichever endpoint is busiest and hide the
rest; grouping by code sends a few of EACH distinct failure, which is the set an
operator needs. The count of what was dropped travels so a burst that overflowed
does not look like a burst that did not happen.

**7. The log is written FIRST and reporting can never cost the log line.** The
log is the durable record; the report is a courtesy to a service elsewhere. The
wrapped handler runs before the sink and its error is what `Handle` returns.

**8. A reporter that panics is switched off for the life of the process.** The
loop it prevents is real: reporting runs inside a log handler, inside a request,
inside the panic recoverer. A panic here is recovered up there, the recovery
LOGS, and the log comes straight back in. The reporter is disabled and said so on
stderr — stderr, because the logger is the thing that got us there.

**9. A delivery failure is logged BELOW the reporting floor.** The sender has to
say when a POST fails, and saying it at ERROR would put the complaint back
through the reporting handler: send fails, log, report, send fails, log again.
The rate limit caps the spiral rather than letting it run forever, but capping it
means the budget for genuinely unclassified failures is spent on the collector
complaining about itself, during the exact incident somebody is trying to read.

**10. The reporter is bound between plugin Install and module Bootstrap.** The
modules come up in that gap — migrations, schema checks, provider verification —
and a reporter bound later would watch every one of those failures go by
unreported, in the one phase where a failure means the process is about to exit.
It is the only registration a plugin makes that is not queued to Start, and it can
be: there is no module for it to wait for.

**11. No retries.** A failed POST is logged and dropped. Retrying means a process
already in trouble spending its remaining capacity talking to a collector, and a
collector that is down is usually down for longer than a retry loop will wait.

**13. A record whose producer marks it as already reported is skipped, before
the rate limit.** This one was found by running the whole thing against a real
collector, and no unit test would have surfaced it: a 5xx is logged TWICE — once
by the code that produced it, carrying the machine code, and once by the access
log as a summary, carrying none — and both records are at ERROR. Reporting both
doubles the volume and, worse, files every server error in the application under
`unclassified`, which is the bucket that has to stay empty enough for a genuinely
unclassified failure to be visible in it. It also spends that bucket's rate limit
on failures that are reported properly a line earlier.

The marker is set by the PRODUCER (`errorreport.KeyAlreadyReported`, set by the
access-log middleware for a 5xx) rather than inferred by the reporting handler.
Detecting the access log by its shape — "has a status and no error" — would be
one package guessing about another package's log format, and this repository has
already been broken once by exactly that coupling. The marker is also a
STATEMENT: it appears in the log, so setting it on a line that was not already
reported is a bug even though nothing would act on it.

**12. Reporting is never a reason a request fails.** `Report` returns nothing,
must not block, and handles its own errors. A component that exists to observe
failures must not be able to turn the observation of an outage into a second
outage.

## Consequences

- Error reporting is off unless a plugin provides a reporter, and off costs one
  atomic load per failing request.
- A useful attribute stays out of the reports until somebody adds it to the allow
  list. That cost is paid on purpose.
- The collector shows a code and a safe sentence, not a stack. For an ordinary
  error there is no useful stack to show anyway — it was returned, not thrown —
  and for a panic the trace travels as text.
- `core/errorreport` is the second core package the log handler passes
  through. It is on the path of every failing request, so its cost is one map
  copy and one allow-list check per failure, and nothing at all below the level.
- The reports carry no method or path. The access-log line has them and it is
  the line that is skipped; the diagnostic line does not log them. An operator
  goes from the report's `request_id` to the access log for the endpoint, which
  is the same division as the point below.
- Anyone reading a report needs the log too: the report carries the fingerprint
  and the correlation handle, and the full text stays where the access controls
  already are. That is the intended division, not a gap.

## What this cannot express

- **Whether the safe message is actually safe.** Decision 5 leans on a godoc
  promise. Nothing mechanically checks that a caller did not interpolate an email
  address into `errors.Invalid(...)`, and a check that could would need to know
  what the values mean.
- **A panic value under a caller's control.** `panic(x)` puts `x` in the report.
  Every panic this repository can produce carries a runtime string; a
  library that panics with request data would not.
- **Sampling by traffic.** The limit is per code per window, not a percentage. A
  high-cardinality code space would defeat it; the bound on the limiter's table
  keeps that from becoming a memory leak, not from becoming noise.
- **Ordering across processes.** Reports are sent in the order one process
  produced them; a collector merging several instances sees whatever arrives.

## What the second reporter showed

This ADR named the one thing that could test it: "reopen when a second reporter
is written." `plugins/errorotlp` is that second implementation, and it was
chosen for the model furthest from the first. Sentry has ISSUES — a report
carries a fingerprint and the collector groups by it. The OpenTelemetry log
model has no issue, no grouping key and no deduplication: a record is a
timestamp, a severity, a body and attributes, and everything else has to survive
as an attribute or not at all.

**`ErrorEvent` survived it.** Every field found a home and nothing had to be
added to the contract. That is the first real evidence the shape is not merely
the shape Sentry wanted.

**The fingerprint turned out to be a CONVENTION, not a field.** Sentry gets a
`fingerprint` list; OTLP offers nothing of the kind, so the only thing a
collector's error view can group by is the semantic attribute
`exception.type`. gobit's `Code` goes there — the same value decision 3 calls
the fingerprint — and a collector that knows nothing about gobit then groups its
failures correctly. **A code is enough for a collector that groups by exception
type, and no stack is needed to get there.** That was the open question, and the
answer holds decision 3 up.

**The missing stack costs nothing in the second model either.** An ordinary
error carries no `exception.stacktrace` and OTLP has no complaint about it; the
attribute is simply absent, exactly as it is for a report Sentry receives. The
one case that has a stack — a panic — is the one case that sends it.

**Two attribute namespaces turned out to be Sentry's judgement, not the
event's.** Sentry splits `tags` (indexed, low cardinality) from `extra` (not
indexed), and the first reporter chooses which of gobit's fields go where. OTLP
has ONE attribute space and no such choice to make. The event handing over a
flat map of strings is therefore at the right level: each reporter decides
indexing, and neither decision leaked into the core.

**What the second model wanted and did not get: a severity NUMBER.** OTLP's
record has `severityNumber`, and the plugin fills it with a constant, ERROR.
That is honest today because reporting has a floor — only ERROR records are
reported — so there is exactly one severity to send. It is also the shape of the
next change: if the floor ever moves (reporting WARN, say), `ErrorEvent` would
need to carry the level, and no reporter can infer it from the fields it has.

**The duplication that appeared is the LIFECYCLE, not the payload.** The two
reporters share about ninety lines that are not about Sentry or OTLP at all: a
bounded queue that drops rather than blocking, one sender goroutine, no retries,
a count of what a full queue refused riding the next report, and a `Close` that
flushes. That is decisions 6, 7, 11 and 12 written out by hand, twice. It says
the decisions are right — a second implementation reached for the same shape —
and it says the core could carry them: a third reporter will write them a third
time. Extracting a helper is NOT done here, because doing it while writing the
second implementation would have made the second implementation prove itself
against a helper built from the first one.

## Rejected options

**A reporting call at every failure site.** Explicit, greppable, and wrong: it
adds an obligation to hundreds of call sites, and the one place somebody forgets
is the place nobody hears about.

**Hand the reporter the real error and let it decide.** This is what a
conventional integration does. It makes the confidentiality decision a property
of the plugin, so every plugin can get it wrong, and it can only be audited by
reading the plugin's source.

**Use the official Sentry SDK.** It brings retries, breadcrumbs and stack-frame
parsing, and it would add a dependency to `go.mod`. Most of what it offers is
built on the thing decision 3 refuses to hand it — the error and its stack — so
the parts that would justify the dependency are the parts that would be turned
off. The envelope protocol is a header line, an item header line and a JSON
object; writing it costs less than the dependency does.

**A deny list of sensitive keys.** It is a list of the leaks somebody already
thought of, and every new attribute is permitted by default.

**Report only `KindInternal`.** Tempting, because those are "our bugs". But
`KindUnavailable` is what an outage looks like and it is the class an operator
most wants pushed at them, and a framework deciding which failures matter would
decide it wrong for somebody.

**Sample a percentage of failures.** Percentages lose the rare failure, which is
the one worth seeing. A per-code cap keeps one of each.

**Send the reports synchronously.** It would put a collector's latency on the
request path and its outage inside ours.

## Reopening the decision

The original trigger has FIRED: `plugins/errorotlp` is the second reporter and
what it showed is written above. The shape held; the two things it surfaced are
the triggers that replace it.

Reopen when a THIRD reporter is written, and this time to move the lifecycle
into the core rather than to re-examine the event. Two hand-written copies of
the same queue-drop-flush loop are a coincidence; three are a missing helper,
and by then the shape it should have will have been demonstrated twice.

Reopen when the reporting FLOOR moves. Everything reported today is an ERROR
record, which is why `ErrorEvent` can leave severity implicit and why a reporter
whose protocol wants a severity number can fill it with a constant. Reporting a
second level would make that constant a lie, and the field belongs on the event
rather than in each reporter's guess.

Reopen decision 5 if a message is ever found carrying data it should not. The
answer would not be to patch the message: it would be to stop sending free text
and let the collector carry the code alone, with the log holding everything else.
