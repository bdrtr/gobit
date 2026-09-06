// Package outboxrelay publishes the events that business transactions promised.
//
// # What it is for
//
// A module commits its work and then publishes. Between those two moments the
// process can die, and the work then exists while the event never happened.
// The outbox closes that window by writing the event INSIDE the transaction
// (core/eventbus/outbox); this job is the other half — the thing that
// turns a written row into a published event.
//
// # Why it is a job and not a goroutine
//
// A goroutine would restart with the process and would have to decide, on its
// own, how often to look and what to do about an event it could not send. The
// scheduler already answers both: an occurrence elected by a row, liveness by
// an advisory lock, a run recorded so `gobit jobs` can say whether it happened
// (ADR 0019). A relay that nobody can see the last run of is a relay nobody can
// trust.
//
// # It ACTS, and that is not a contradiction of ADR 0017
//
// ADR 0017 refuses SCHEDULED COMPENSATION: undoing work unwatched. This job
// undoes nothing. It delivers a message the business transaction already
// decided to send — the decision was made and committed by a person's request;
// all that was missing was the delivery.
//
// # The two ways delivery machinery betrays its promise
//
// Both are prevented, and each by a different mechanism, so it is worth saying
// which is which:
//
//   - RETRYING FOREVER. A payload the receiver will never accept, re-sent every
//     minute for the life of the installation — and, because the pass reads the
//     oldest rows up to its limit, filling every batch so that the healthy
//     events behind it are never attempted at all. The ceiling in
//     [github.com/bdrtr/gobit/core/eventbus/outbox.Policy] prevents this: the row
//     is given up on and leaves the relay's query.
//   - DROPPING SILENTLY. An event the relay stops trying, with nobody told. The
//     dead letter prevents this, and only because it is READ: this job asks for
//     the pile on every pass and, when it is not empty, FAILS. A job that
//     reported "ok" while promised events sat undelivered would be the
//     write-only ledger this repository has already built once, in audit_log.
//
// The failure is not cosmetic and it does not clear itself. It stands until a
// human redrives the events or discards them, which is the intended cost: the
// alarm is the feature.
//
// # The pile still FAILS the run, and that was re-decided rather than inherited
//
// The failure used to be the ONLY channel out of here: a run's detail was
// recorded only alongside an error, so a healthy pass could say nothing. That
// is no longer true — [job.Report] gives a successful run a line — which turns
// "the pile fails the run" from a consequence of the machinery into a choice,
// and a choice has to be made rather than kept by accident.
//
// It still fails. Three reasons, in the order they matter:
//
//   - OUTCOME is the column an operator scans and DETAIL is the one they read
//     afterwards. A standing fault demoted to a detail is a fault behind one
//     more step, and this one is undelivered messages somebody is waiting for.
//   - `gobit deadletters` is documented as the off switch for an alarm that
//     STANDS until a human acts (see internal/app/deadletters.go). An alarm
//     that reads "ok" needs no off switch, and the command's whole argument for
//     existing goes with it.
//   - Anything watching this job's outcome — a person during an incident, or a
//     check built on the listing — would stop firing on the day the change
//     shipped, silently, which is the failure mode this repository pays the
//     most for.
//
// What the new channel carries here is the pass's THROUGHPUT, which is a
// different fact and had nowhere to go before. An idle minute, a relay keeping
// up, and a relay that has filled its limit every minute for an hour with a
// growing backlog behind it were three different states printed as the same
// blank cell.
package outboxrelay

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/eventbus/outbox"
	"github.com/bdrtr/gobit/internal/core/job"
)

// Name is the job's name. It is the advisory lock's input and the primary key
// of its history, so it is a contract.
const Name = "outbox-relay"

// Every is how often the relay runs.
//
// A minute, and this is the one job in the repository where a short interval is
// right: what waits here is a message somebody is expecting — a confirmation
// mail for an order that has already been paid for. The saga-watch and
// reconciliation jobs report things that have ALREADY been wrong for a while,
// so finding them sooner changes nothing; here the delay IS the damage.
//
// It is not shorter than a minute because the relay is not the primary path.
// The bus is still published to directly in the same request; this is what
// catches what that missed.
//
// It is also the floor under the retry backoff. The store's first delay is one
// minute for exactly this reason: a shorter one would be rounded up to the next
// pass anyway, and a policy whose smallest step the scheduler cannot express
// would document a schedule the installation does not actually follow.
const Every = time.Minute

// MaxRun bounds one pass.
//
// It has to stay UNDER [Every], and the registry refuses a job where it does
// not: a pass that can outlast its own interval could never catch up, and the
// backlog would grow while every run looked healthy.
//
// Forty-five seconds against a limit of 200 events is generous — the work per
// event is one bus publish and one row update — and what it really bounds is a
// bus that has stopped answering, which is exactly when the pass must end
// rather than hold its rows locked into the next one.
const MaxRun = 45 * time.Second

// limit caps one pass.
//
// A cap that is hit is REPORTED as hit: a backlog of ten thousand events is a
// different fact from a quiet minute, and the next pass is a minute away, so
// nothing is stranded by it.
//
// The cap used to be reachable by rows that could never succeed: the pass took
// the oldest unpublished rows, and a limit's worth of permanently failing ones
// filled every batch forever. Backoff and the dead letter are what keep this
// number a throughput bound instead of a queue-length bomb.
const limit = 200

// deadLetterSample is how many dead letters the report carries.
//
// Five, not all of them. The report ends up on one line of `gobit jobs` and in
// one log record; a pile of two thousand printed in full would push the count —
// the number that decides whether anybody is woken up — off the screen. The
// count is always the whole pile; the sample is only what it looks like.
const deadLetterSample = 5

// codeRelayFailed reports that the pass could not be made.
const codeRelayFailed = "outbox_relay_failed"

// relay is the narrow surface this job needs.
//
// It is declared HERE so the job depends on the two methods it calls and a test
// can supply them without a database.
type relay interface {
	Relay(
		ctx context.Context, limit int32, publish func(context.Context, outbox.Pending) error,
	) (outbox.RelayResult, error)
	DeadLetters(ctx context.Context, limit int32) (outbox.DeadLetterReport, error)
}

// publisher is the bus the events go to.
type publisher interface {
	Publish(ctx context.Context, e eventbus.Event) error
}

// Definition builds the job.
func Definition(r relay, bus publisher, log *slog.Logger) job.Definition {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return job.Definition{
		Name:   Name,
		Every:  Every,
		MaxRun: MaxRun,
		Run:    func(ctx context.Context) error { return run(ctx, r, bus, log) },
	}
}

// run relays one batch and then reports whatever the relay has given up on.
//
// The two halves are in one job on purpose. A separate "dead letter watch" job
// would run on its own schedule, take its own lock and produce its own history
// row, and an operator would then have to correlate two listings to learn that
// the relay is fine and the events are not. The relay is the only thing that
// creates dead letters; it is the right thing to report them.
func run(ctx context.Context, r relay, bus publisher, log *slog.Logger) error {
	result, err := r.Relay(ctx, limit,
		func(ctx context.Context, event outbox.Pending) error {
			return bus.Publish(ctx, event.Event())
		})
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeRelayFailed,
			"the outbox could not be relayed")
	}

	reportPass(ctx, result, log)

	// The pass's own numbers, on the channel that reaches `gobit jobs`.
	//
	// It is reported BEFORE the dead-letter read, so a pass that then fails —
	// because the pile is not empty, or because the pile could not be read at
	// all — has already said what it relayed. The failure's own detail wins
	// over this line when it carries one (internal/core/job's Outcome.Detail),
	// which is why the pile's report is unaffected by this call; what it
	// rescues is the second case, where the relay worked and the READ broke,
	// and the listing used to show a bare error with no sign that anything was
	// delivered.
	job.Report(ctx, summarize(result))

	// Asked for on EVERY pass, including the ones that published nothing. A
	// pile that is only counted when something happens is one that goes
	// unnoticed during the quiet hour after the outage that filled it.
	deadLetters, err := r.DeadLetters(ctx, deadLetterSample)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeRelayFailed,
			"the outbox dead letters could not be read; the relay cannot say whether "+
				"anything has been given up on")
	}
	if deadLetters.Empty() {
		return nil
	}

	return deadLetterError{report: deadLetters}
}

// summarize renders one pass as the single line `gobit jobs` prints.
//
// It is deliberately NOT the log's content. The log gets the event ids, three
// severities and one record per condition, because a log is read forwards by
// somebody reconstructing an incident; this is one cell of a table, read by
// somebody deciding whether there IS an incident. So it carries the two counts
// and the one state that the counts alone cannot express — a filled batch,
// which is the difference between "the relay is working" and "the relay is
// working and losing ground".
func summarize(result outbox.RelayResult) string {
	if result.Published == 0 && result.Failed == 0 {
		// Said rather than left blank. A blank cell is what a job that has
		// never reported looks like, and "the outbox was empty" is a real
		// answer to "is the relay running".
		return "nothing to relay"
	}

	line := fmt.Sprintf("published %d, failed %d", result.Published, result.Failed)

	if given := len(result.DeadLettered); given > 0 {
		// The ones given up on IN THIS PASS, which is not the pile: the pile is
		// counted by the dead-letter report and printed by the failure. This
		// number says the pile grew just now.
		line += fmt.Sprintf(", %d newly given up on", given)
	}

	if result.Published+result.Failed == limit {
		line += fmt.Sprintf("; the limit of %d was filled, so there is a backlog", limit)
	}

	return line
}

// reportPass logs what the pass did.
func reportPass(ctx context.Context, result outbox.RelayResult, log *slog.Logger) {
	if result.Published == 0 && result.Failed == 0 {
		// DEBUG, not INFO. A healthy installation runs this every minute
		// forever, and a line that never changes is a line nobody reads.
		log.DebugContext(ctx, "the outbox is empty")

		return
	}

	if len(result.DeadLettered) > 0 {
		// The moment of death, with the ids, logged once. The row keeps the
		// last error and the attempt count, but nothing else records WHICH
		// pass gave up on it, and an operator reconstructing an incident reads
		// the log forwards.
		log.ErrorContext(ctx, "promised events have been given up on and will NOT be retried; "+
			"they are dead-lettered and need a human to redrive or discard them",
			"dead_lettered", len(result.DeadLettered),
			"event_ids", strings.Join(result.DeadLettered, ","))
	}

	if result.Failed > 0 {
		// ERROR: every one of these is a message somebody is waiting for. The
		// row keeps its attempt count and its next attempt is delayed, so a
		// permanently failing event stops looking like a slow one — and stops
		// occupying a slot in every batch.
		log.ErrorContext(ctx, "some promised events could not be published; they stay in the "+
			"outbox and are retried after a growing delay, but a repeated failure needs a human",
			"failed", result.Failed, "published", result.Published)
	}

	if result.Published > 0 {
		log.InfoContext(ctx, "promised events were published", "published", result.Published)
	}

	if result.Published+result.Failed == limit {
		log.WarnContext(ctx, "the relay filled its limit, so there is a backlog; the next pass "+
			"is a minute away", "limit", limit)
	}
}

// deadLetterError is a standing pile of dead letters, stated as a failed run.
//
// # Why a failure and not a log line
//
// Because a log line is not a read surface an operator visits. `gobit jobs` is
// — it is the first thing asked during an incident — and when this was written
// the runner filled its DETAIL column only from an error, so the choice was not
// a choice at all: it was "failure, or the pile is invisible to the one listing
// built to be looked at".
//
// It IS a choice now. A successful run can leave a line ([job.Report]), so
// "failure or detail" is a live question, and the package documentation answers
// it: still a failure, because OUTCOME is the column that gets scanned and
// `gobit deadletters` is the off switch for an alarm that stands. The reasoning
// moved there rather than being repeated here.
//
// # Why it does not clear itself
//
// It stands on every pass until the events are redriven or discarded, so the
// listing keeps saying so. That is deliberate and it is the cost of the design:
// a report that cleared after one pass would be gone by the time anybody typed
// the command. The escape hatches exist and are not promises — Redrive and
// Discard on the store.
type deadLetterError struct {
	report outbox.DeadLetterReport
}

// Error states the pile and names the oldest of it.
func (e deadLetterError) Error() string {
	return fmt.Sprintf(
		"%d promised event(s) have been given up on and are waiting for a human "+
			"(redrive them once the receiver is fixed, or discard them): %s",
		e.report.Count, describe(e.report.Oldest))
}

// JobDetail is the one line `gobit jobs` prints in its DETAIL column.
//
// It carries the event NAMES and ids rather than the payloads. The rule on
// Outcome.Detail is that it holds no personal data; an event id here is
// composed by the publisher from its own aggregate id (ADR 0023 requires the
// caller to supply it), which is the same identifier the error logs already
// print. The payload, which can hold an address or a phone number, never
// leaves the row.
func (e deadLetterError) JobDetail() string {
	return fmt.Sprintf("%d dead-lettered; oldest %s", e.report.Count, describe(e.report.Oldest))
}

// describe renders the sample on one line.
func describe(letters []outbox.DeadLetter) string {
	if len(letters) == 0 {
		// Reachable only if the count and the sample disagree, which the
		// store's single-query report makes impossible today. Saying so beats
		// printing an empty list that reads like "nothing is wrong".
		return "(no sample available)"
	}

	parts := make([]string, 0, len(letters))
	for _, letter := range letters {
		parts = append(parts, fmt.Sprintf("%s %s after %d attempts (%s)",
			letter.Name, letter.ID, letter.Attempts, oneLine(letter.LastError)))
	}

	return strings.Join(parts, "; ")
}

// oneLine flattens whatever the receiver said into a single line.
//
// The last error is a string a REMOTE system produced — a driver message, a
// wrapped chain, an HTTP body — and nothing in this repository controls whether
// it contains a newline. Outcome.Detail is one cell of a tabwriter table, so a
// newline there does not merely look bad: it breaks the alignment of every row
// after it, in the one listing an operator reads during an incident.
func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }
