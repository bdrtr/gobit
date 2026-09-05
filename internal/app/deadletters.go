package app

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus/outbox"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/jobs/outboxrelay"
)

// deadLettersCommand is the operator surface for the events the outbox relay
// has given up on.
//
// # The gap it closes, and why the gap was the expensive kind
//
// The relay gives up after its ceiling and writes the row down: the instant,
// the attempt count and the last error stay on it, the relay reads the pile on
// every pass, and a non-empty pile FAILS the run — which is what puts it in
// `gobit jobs`. That half was built. The other half was not:
// [outbox.Store.Redrive] and [outbox.Store.Discard] existed, were tested
// against a real database, and had NO caller outside those tests — no command,
// no route, nothing. Measured rather than assumed: grep for either name across
// every non-test .go file in the repository finds only the godocs that promise
// them.
//
// That is this repository's most expensive recurring defect wearing its most
// dangerous costume. A capability with no consumer is usually merely dead; this
// one was load-bearing. The whole design of the dead letter rests on the failed
// job standing until a human acts, and the only way to act was to write Go or
// raw SQL against event_outbox. An alarm whose off switch is a hand-written
// DELETE is an alarm that gets muted at the alerting layer instead, and then
// the next outage is silent.
//
// # Why a subcommand, and not an endpoint or a panel screen
//
// The same three-way argument [stuckCommand] makes, and it lands in the same
// place for the same reasons. The table is CORE's, so no module's api package
// owns it; ADR 0011 keeps the panel out of surfaces the framework's API does
// not already offer; and a command needs no new identity surface, because it
// runs with the server's own environment and reaches the database with the
// credential psql already needs.
//
// One reason is stronger here than it was there. This surface is reached FROM
// `gobit jobs`, during an incident, when the failing thing may well be the
// admin API itself. A read surface that shares a failure domain with the
// outage is not a read surface.
//
// # Why the bare verb LISTS instead of demanding a subcommand
//
// `migrate` refuses to run without one, because it has no safe default: the
// two things it could mean are "report" and "roll back", and guessing between
// them is not a thing a program may do. This noun does have a safe default.
// Listing changes nothing, it is the only thing an operator ever wants first,
// and the operator arriving here is copying a word out of a FAILED job row
// while something is broken. Making them type a second word to see the pile
// buys nothing and costs a round trip at the worst moment.
const deadLettersCommand = "deadletters"

// The verbs that ACT on a dead letter. Listing has no verb; see
// [deadLettersCommand].
const (
	// cmdRedrive puts the event back in the queue.
	cmdRedrive = "redrive"
	// cmdDiscard deletes it for good. It is the destructive one.
	cmdDiscard = "discard"
)

// flagLimit bounds how much of the pile is printed.
const flagLimit = "limit"

// codeDeadLetterRefused is returned when NOTHING was touched: the id was not
// repeated, the flags did not parse, or the id names no dead letter.
//
// It is separate from a failure code for the reason [codeRollbackRefused] is —
// "nothing happened" and "it broke halfway" call for opposite next steps, and
// here the second one can mean a promise was deleted.
const codeDeadLetterRefused = "cli_dead_letter_refused"

// defaultDeadLetterLimit is how much of the pile is printed when the operator
// gives no -limit.
//
// Fifty, and it is a number about the READER rather than the database. The
// store's query computes the pile's size with a window function evaluated
// before LIMIT, so the whole pile is walked whatever the page size is:
// measured on PostgreSQL 16 against a 42,300-row event_outbox holding a
// 2,000-letter pile, best of five, -limit 1 cost 0.690 ms, -limit 50 cost
// 0.690 ms and -limit 500 cost 0.757 ms. What does move the number is the PILE
// — the same query over a 50,000-letter pile cost 17.3 ms — and no flag on
// this command changes that.
//
// So raising -limit is free and the closing line says so. Fifty is simply
// where a human stops reading, and the report states the full count on its
// first line precisely so that the page never has to carry it.
const defaultDeadLetterLimit = 50

// deadLetterReader is the read half of the store, declared on the consumer's
// side (ADR 0001).
//
// Everything below the query is formatting, and formatting is what an operator
// actually reads during an incident; naming the one method here is what lets
// the report be exercised without a database.
type deadLetterReader interface {
	DeadLetters(ctx context.Context, limit int32) (outbox.DeadLetterReport, error)
}

// deadLetterStore adds the two verbs that write.
//
// It embeds the reader because the act path needs BOTH: it changes one row and
// then re-reads the pile's size, since "will the `gobit jobs` alarm clear?" is
// the actual question the operator came with, and a report that answered only
// "one row changed" would leave them running the command again to find out.
type deadLetterStore interface {
	deadLetterReader
	Redrive(ctx context.Context, ids ...string) (int64, error)
	Discard(ctx context.Context, ids ...string) (int64, error)
}

// runDeadLetters routes the noun's verbs.
//
// A verb is only a verb when it does not start with "-", so `gobit
// deadletters -limit 10` reaches the listing rather than being read as a
// subcommand named "-limit".
func runDeadLetters(args []string, out io.Writer, opts Options) error {
	verb := ""
	rest := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		verb, rest = args[0], args[1:]
	}

	switch verb {
	case "":
		return runDeadLetterList(rest, out)
	case cmdRedrive, cmdDiscard:
		return runDeadLetterAction(verb, rest, out)
	default:
		if err := writeReport(out, usageText(opts.version())); err != nil {
			return err
		}

		return errors.Invalid(codeUsage,
			"%s has no %q verb; it LISTS with no verb at all, and acts with %s or %s",
			deadLettersCommand, verb, cmdRedrive, cmdDiscard)
	}
}

// runDeadLetterList prints the pile.
//
// It reuses [config.Load], so it needs the SAME environment as the server —
// which is the point: run inside the running container, it is configured
// already and cannot be pointed at the wrong database by accident. Nothing
// here migrates, opens Redis, or starts a listener.
//
// The context is built HERE rather than handed down, from the same signals the
// server watches: an operator who hits Ctrl-C during an incident expects the
// query to stop, and a background context would leave it running until the
// database answered. This is the shape [runStuck] uses, for the same reasons.
func runDeadLetterList(args []string, out io.Writer) error {
	limit, err := parseDeadLetterListFlags(args)
	switch {
	case errors.Is(err, flag.ErrHelp):
		// The flag set has already printed the usage. Asking what a command
		// does is not a failure, and returning the error here would answer the
		// question with a non-zero exit and the word "fatal".
		return nil
	case err != nil:
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, closeStore, err := openOutboxStore(ctx)
	if err != nil {
		return err
	}
	defer closeStore()

	return listDeadLetters(ctx, store, out, limit, time.Now().UTC())
}

// runDeadLetterAction redrives or discards ONE named event.
func runDeadLetterAction(verb string, args []string, out io.Writer) error {
	action, err := parseDeadLetterAction(verb, args)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, closeStore, err := openOutboxStore(ctx)
	if err != nil {
		return err
	}
	defer closeStore()

	return actOnDeadLetter(ctx, store, out, action)
}

// openOutboxStore opens the installation's own pool and builds the store over
// it.
//
// # Why a pool and not the whole application
//
// [runJobs] and [runRecover] call [openApplication] because what they need is
// built from modules: a job's dependency is a module service, a compensation
// chain is the checkout flow's own functions. Nothing here is. event_outbox is
// a CORE table and the three methods used against it are plain SQL, so booting
// every commerce module to read it would buy nothing — and would cost the one
// property this command needs most. An operator reaches it while something is
// already broken, and a module whose bootstrap fails would then take the
// dead-letter listing down with it. The lighter path keeps the pile readable
// in exactly the installation that cannot start.
//
// The store is built with [outbox.NewStore], so it carries the default retry
// policy. That is not a decision here: the policy is only read by the relay's
// own pass, and none of the three methods this file calls consults it.
//
// The pool's own log goes to STDERR, not stdout: stdout is the operator's data
// and a log line landing in the middle of it would break the first grep. The
// level is Warn so that the startup checks which warn about THIS database
// still get to speak.
func openOutboxStore(ctx context.Context) (*outbox.Store, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	pool, err := db.New(ctx, dbConfig(cfg), log)
	if err != nil {
		return nil, nil, err
	}

	return outbox.NewStore(pool.Pool()), pool.Close, nil
}

// listDeadLetters reads one page and prints it.
//
// The split from [runDeadLetterList] is what makes the output testable:
// everything above it is the environment, everything here is what the operator
// sees.
func listDeadLetters(
	ctx context.Context, reader deadLetterReader, out io.Writer, limit int32, now time.Time,
) error {
	report, err := reader.DeadLetters(ctx, limit)
	if err != nil {
		return err
	}

	return writeDeadLetters(out, report, limit, now)
}

// parseDeadLetterListFlags turns the command line into a page size.
func parseDeadLetterListFlags(args []string) (int32, error) {
	flags := flag.NewFlagSet(binaryName+" "+deadLettersCommand, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() {
		fmt.Fprintf(os.Stderr,
			"usage: %s %s [flags]\n\n"+
				"Lists the promised events the outbox relay has GIVEN UP on. This is the\n"+
				"pile that fails the %s job and keeps `%s %s` red.\n"+
				"READ ONLY: it redrives nothing and deletes nothing. Acting on one letter\n"+
				"is a separate verb that names it:\n"+
				"  %s %s %s <event-id> -%s <event-id>\n"+
				"  %s %s %s <event-id> -%s <event-id>\n\n"+
				"flags:\n",
			binaryName, deadLettersCommand,
			outboxrelay.Name, binaryName, jobsCommand,
			binaryName, deadLettersCommand, cmdRedrive, flagConfirm,
			binaryName, deadLettersCommand, cmdDiscard, flagConfirm)
		flags.PrintDefaults()
	}

	limit := flags.Int(flagLimit, defaultDeadLetterLimit,
		"how many dead letters to print, oldest first; the COUNT on the first line is "+
			"always the whole pile, and raising this is free because the query walks the "+
			"pile either way")

	if err := flags.Parse(args); err != nil {
		return 0, err
	}
	if flags.NArg() > 0 {
		flags.Usage()

		return 0, errors.Invalid(codeDeadLetterRefused,
			"%s %s takes no positional arguments, got %q; did you mean `%s %s %s %s -%s %s`?",
			binaryName, deadLettersCommand, flags.Arg(0),
			binaryName, deadLettersCommand, cmdRedrive, flags.Arg(0), flagConfirm, flags.Arg(0))
	}
	// The bound is the store's parameter type, not a policy. A number above it
	// cannot reach the query as itself, and a limit that silently became
	// something else would make the "showing N of M" line a lie.
	if *limit < 1 || *limit > math.MaxInt32 {
		return 0, errors.Invalid(codeDeadLetterRefused,
			"-%s must be between 1 and %d, got %d", flagLimit, math.MaxInt32, *limit)
	}

	return int32(*limit), nil
}

// deadLetterAction is the parsed command line of [cmdRedrive] or [cmdDiscard].
type deadLetterAction struct {
	verb    string
	eventID string
}

// parseDeadLetterAction turns the command line into ONE event id, and refuses
// everything that is not one.
//
// # Why a repeated id and not a -force flag
//
// The guard is the one `migrate down <owner> -confirm <owner>` and `recover
// <id> -confirm <id>` already use, and copying it is the smaller half of the
// reason. The larger half is that a boolean cannot express what has to be
// confirmed here.
//
// What goes wrong with these verbs is almost never "the operator did not mean
// to discard". It is "the operator meant a DIFFERENT id": the value is copied
// out of a listing where one line up or down is another customer's promise,
// out of a terminal, during an incident, at speed. A -force flag carries no
// information about the target, so it cannot catch that class of mistake at
// all — it would confirm the intent and wave through the wrong row.
//
// A flag is also a CONSTANT, which is the second half of the problem. The same
// six characters work for every invocation forever, so they migrate into a
// shell alias, a runbook line or a history entry, and after that they are
// never typed again — the confirmation stops being a decision and becomes
// punctuation. A repeated id cannot be pre-typed: it is different every time
// and it has to be read off the pile first, which is precisely the act being
// required.
//
// # Why REDRIVE is guarded too, when it deletes nothing
//
// Because it is not the read it looks like. It resets attempts to zero and
// clears dead_lettered_at, which erases the record of the death the operator
// was just shown — the relay's schema keeps that instant deliberately, so that
// raising the ceiling later cannot rewrite history a human has already been
// told. And it puts a real message back on the bus: a confirmation mail to a
// real customer, hours late, possibly for the second time. Both of those are
// side effects in the world, and neither is undone by running something else.
//
// # Why ONE id, and why there is no "discard everything"
//
// The store's two verbs are variadic and this command passes exactly one
// element into them. That narrowing IS the guard: a confirmation that names
// one id cannot authorize a list, and every list-shaped confirmation degrades
// into either a count — which says how many, never which, and goes stale
// between the listing and the run — or a second copy of the whole list, which
// nobody types.
//
// The question is not rhetorical, because the pile an outage produces is not
// five letters but two thousand, and a surface usable one row at a time is a
// surface that sends the operator back to psql. It was measured before it was
// decided, so the argument does not rest on cost: against PostgreSQL 16 a
// single discard is a primary-key delete at 0.047 ms and a single redrive is
// 0.183 ms, so two thousand of either is nothing the database notices. A bulk
// flag would buy the operator TYPING and nothing else. Three reasons say that
// typing is the feature:
//
//   - `-all` would be a one-keystroke mute for the only alarm in this
//     repository that cannot be muted by ignoring it. The dead letter's whole
//     design is that the relay job stays FAILED until a human acts (see
//     internal/jobs/outboxrelay), and a flag makes acting indistinguishable
//     from silencing — at which point the cheaper of the two is what happens at
//     3am.
//   - The row is the LAST copy. Discarding deletes the payload, and the only
//     other trace is a log line from the pass that gave up. Emptying the pile
//     without reading it destroys the answer to "what did we promise and not
//     deliver", which is the question the pile exists to answer.
//   - Naming an id means having read the event name and the last error printed
//     beside it. That reading is not overhead around the feature; it is the
//     feature.
//
// A bulk REDRIVE is the harder call and was refused too. "The receiver is
// back, put everything back" is a real and legitimate request, and redrive is
// the recoverable verb — but the pile after an outage is heterogeneous. Some
// letters died because a receiver was down and want redriving; others died
// because their payload is malformed, and redriving those spends another four
// hours of attempts to arrive back in exactly this pile. A verb that treats
// both alike hides the difference, and the confirmation shape has nothing
// honest to repeat for "all of them" either.
//
// What is left is honest rather than complete: an operator holding two
// thousand letters with one cause still writes a loop, and this command does
// not pretend otherwise. What it removes is the case that was actually
// stopping people — one event, one decision, one line — which is every dead
// letter that is not an outage.
func parseDeadLetterAction(verb string, args []string) (deadLetterAction, error) {
	// The id is required to be the FIRST argument, exactly as the owner is for
	// `migrate down` and the execution id is for `recover`: the flag package
	// stops at the first non-flag argument, so an id written after the flags
	// would be swallowed as a leftover.
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return deadLetterAction{}, errors.Invalid(codeDeadLetterRefused,
			"%s %s needs the event id as its FIRST argument "+
				"(%s %s %s <event-id> -%s <event-id>); find it with `%s %s`",
			deadLettersCommand, verb,
			binaryName, deadLettersCommand, verb, flagConfirm,
			binaryName, deadLettersCommand)
	}

	flags := flag.NewFlagSet(binaryName+" "+deadLettersCommand+" "+verb, flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	confirm := flags.String(flagConfirm, "",
		"repeat the event id; nothing runs without it")

	if err := flags.Parse(args[1:]); err != nil {
		return deadLetterAction{}, errors.Wrap(err, errors.KindInvalid, codeDeadLetterRefused,
			"the flags of %s %s could not be parsed", deadLettersCommand, verb)
	}
	if rest := flags.Args(); len(rest) > 0 {
		return deadLetterAction{}, errors.Invalid(codeDeadLetterRefused,
			"unexpected argument %q after the flags of %s %s; this verb takes ONE event id",
			rest[0], deadLettersCommand, verb)
	}

	id := args[0]
	if *confirm != id {
		return deadLetterAction{}, errors.Invalid(codeDeadLetterRefused,
			"%s: run it again with -%s %s to confirm", deadLetterStakes(verb), flagConfirm, id)
	}

	return deadLetterAction{verb: verb, eventID: id}, nil
}

// deadLetterStakes says what the verb is about to do, in the refusal itself.
//
// The two verbs are not the same risk and the refusal is the last place an
// operator reads before repeating the id, so it states which one they are
// holding rather than printing one sentence for both.
func deadLetterStakes(verb string) string {
	if verb == cmdDiscard {
		return "discarding DELETES the event and its payload for good, and nothing can bring " +
			"it back"
	}

	return "redriving publishes the event again and erases the record of its death " +
		"(the attempt count and the instant it was given up on)"
}

// actOnDeadLetter runs one verb against one id and reports what changed.
//
// The split from [runDeadLetterAction] is what makes it testable without a
// process: everything above it is the environment, everything here is the
// decision and the report.
func actOnDeadLetter(
	ctx context.Context, store deadLetterStore, out io.Writer, action deadLetterAction,
) error {
	var (
		affected int64
		err      error
	)

	switch action.verb {
	case cmdRedrive:
		affected, err = store.Redrive(ctx, action.eventID)
	case cmdDiscard:
		affected, err = store.Discard(ctx, action.eventID)
	default:
		// Unreachable through [runDeadLetters], which routes only the two
		// verbs. Stated rather than left to fall through as a silent success,
		// because a third verb added to the dispatch and not to this switch
		// would otherwise report "done" having done nothing.
		return errors.Internal(codeDeadLetterRefused,
			"%s is not a %s verb", action.verb, deadLettersCommand)
	}
	if err != nil {
		return err
	}

	// Zero rows is the operator's most likely mistake and it is an ERROR, not
	// a quiet success. The store refuses anything that is not already dead —
	// which is what protects a healthy pending row from a mistyped id — so a
	// count of zero means the id named nothing this verb may touch, and the
	// three reasons are worth spelling out because they call for different
	// next steps.
	if affected == 0 {
		return errors.NotFound(codeDeadLetterRefused,
			"%s is not a dead letter, so NOTHING was changed: either the id is wrong, or the "+
				"relay never gave up on that event (a pending or published row is deliberately "+
				"out of reach of both verbs), or somebody has already handled it. `%s %s` says "+
				"which.",
			action.eventID, binaryName, deadLettersCommand)
	}

	report, err := store.DeadLetters(ctx, 1)
	if err != nil {
		return err
	}

	return writeReport(out, deadLetterOutcomeText(action, report.Count))
}

// deadLetterOutcomeText renders what the verb did and what is left.
//
// The remaining count is on it because that is the question the operator
// actually arrived with. They came from a FAILED `gobit jobs` row; "one event
// was redriven" does not tell them whether that row will go green, and a
// second command to find out is a second command during an incident.
func deadLetterOutcomeText(action deadLetterAction, remaining int64) string {
	buf := &strings.Builder{}

	if action.verb == cmdRedrive {
		fmt.Fprintf(buf, "done: %s is back in the queue; the %s job retries it within %s.\n",
			action.eventID, outboxrelay.Name, outboxrelay.Every)
	} else {
		fmt.Fprintf(buf, "done: %s is gone, with its payload; nobody is owed it any more.\n",
			action.eventID)
	}

	if remaining == 0 {
		fmt.Fprintf(buf,
			"the pile is now EMPTY, so the next %s pass records a success and `%s %s` clears.\n",
			outboxrelay.Name, binaryName, jobsCommand)

		return buf.String()
	}

	fmt.Fprintf(buf,
		"%d dead letter(s) are still waiting; the %s job keeps FAILING until the pile is "+
			"empty. `%s %s` lists what is left.\n",
		remaining, outboxrelay.Name, binaryName, deadLettersCommand)

	return buf.String()
}

// writeDeadLetters prints a page in the shape an operator reads during an
// incident.
//
// The order answers the questions in the order they are asked: HOW MANY
// (whether anybody is woken up), then WHICH events and WHY each died, then
// WHAT to do about one. now is a parameter so the ages are reproducible in a
// test; taking it from the clock inside would make the output untestable line
// for line.
func writeDeadLetters(
	out io.Writer, report outbox.DeadLetterReport, limit int32, now time.Time,
) error {
	buf := &strings.Builder{}

	fmt.Fprintf(buf,
		"%s %s: promised events the outbox relay has GIVEN UP on. "+
			"READ ONLY — nothing below was changed.\n",
		binaryName, deadLettersCommand)

	if report.Empty() {
		fmt.Fprintf(buf,
			"the pile is EMPTY: nothing has been given up on, and the %s job has nothing "+
				"to report.\n", outboxrelay.Name)

		_, err := io.WriteString(out, buf.String())

		return err
	}

	fmt.Fprintf(buf, "%d dead letter(s) in the outbox; %d printed, oldest first (-%s %d).\n\n",
		report.Count, len(report.Oldest), flagLimit, limit)

	// Indexed rather than ranged by value: a DeadLetter carries two strings of
	// unbounded length and the loop only reads.
	for i := range report.Oldest {
		writeDeadLetter(buf, &report.Oldest[i], now)
	}

	writeDeadLetterFooter(buf, report, limit)

	_, err := io.WriteString(out, buf.String())

	return err
}

// writeDeadLetter prints one letter as a block rather than a table row.
//
// A table was the obvious alternative and it cannot hold this data: last_error
// is whatever the receiver said — a stack-shaped Go error, a provider's HTML
// error page — and a column that wide either wraps into unreadable porridge or
// truncates away the one field the operator opened the listing to read.
func writeDeadLetter(buf *strings.Builder, letter *outbox.DeadLetter, now time.Time) {
	fmt.Fprintf(buf, "%s  %s  attempts=%d\n", letter.ID, letter.Name, letter.Attempts)
	fmt.Fprintf(buf, "  promised:   %s\n", letter.CreatedAt.Format(time.RFC3339))
	// The two instants are printed with the distance between them because that
	// distance IS the unkept promise: how long the outbox tried before it
	// stopped. The age is printed as well, because "given up on four hours
	// ago" and "given up on last Tuesday" are different incidents.
	fmt.Fprintf(buf, "  given up:   %s (after %s of trying, %s ago)\n",
		letter.DeadLetteredAt.Format(time.RFC3339),
		letter.DeadLetteredAt.Sub(letter.CreatedAt).Truncate(time.Second),
		now.Sub(letter.DeadLetteredAt).Truncate(time.Second))
	fmt.Fprintf(buf, "  last error: %s\n\n", deadLetterErrorLabel(letter.LastError))
}

// deadLetterErrorLabel renders a last error that may legitimately be empty.
//
// Empty is possible — the column defaults to the empty string and a row could
// in principle be dead-lettered by a future writer that records no cause — and
// a blank after "last error:" reads like a failed lookup rather than like
// "nothing was written here".
func deadLetterErrorLabel(cause string) string {
	if cause == "" {
		return "<none recorded>"
	}

	return cause
}

// writeDeadLetterFooter prints what the page did not say and what to do next.
func writeDeadLetterFooter(buf *strings.Builder, report outbox.DeadLetterReport, limit int32) {
	// Said out loud, because an operator deciding whether anybody is owed this
	// event will look for the payload and has to know it was withheld rather
	// than lost. The store leaves it out on purpose: an event's data can carry
	// an address, a name or a phone number, and this listing gets pasted into
	// incident channels.
	fmt.Fprintf(buf,
		"The payloads are NOT printed: an event's data can carry an address or a phone "+
			"number, and this listing gets pasted around. The rows still have them, so a "+
			"redrive loses nothing.\n")

	if int64(len(report.Oldest)) < report.Count {
		fmt.Fprintf(buf,
			"THE LIST IS INCOMPLETE: %d of %d printed. Raise -%s to see the rest — it is "+
				"nearly free, because the count is computed over the whole pile either way.\n",
			len(report.Oldest), report.Count, flagLimit)
	}

	fmt.Fprintf(buf,
		"\nOnce the receiver is fixed, send one back to the queue:\n"+
			"  %s %s %s <event-id> -%s <event-id>\n"+
			"When nobody is owed the event, throw it away (this cannot be undone):\n"+
			"  %s %s %s <event-id> -%s <event-id>\n"+
			"Both take ONE id and both refuse anything that is not already dead. The %s job "+
			"keeps FAILING while this pile is not empty, and that is the design: see "+
			"internal/jobs/outboxrelay.\n",
		binaryName, deadLettersCommand, cmdRedrive, flagConfirm,
		binaryName, deadLettersCommand, cmdDiscard, flagConfirm,
		outboxrelay.Name)
}
