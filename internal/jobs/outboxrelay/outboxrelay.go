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
package outboxrelay

import (
	"context"
	"log/slog"
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
const limit = 200

// codeRelayFailed reports that the pass could not be made.
const codeRelayFailed = "outbox_relay_failed"

// relay is the narrow surface this job needs.
//
// It is declared HERE so the job depends on the one method it calls and a test
// can supply it without a database.
type relay interface {
	Relay(
		ctx context.Context, limit int32, publish func(context.Context, outbox.Pending) error,
	) (published, failed int, err error)
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

// run relays one batch.
func run(ctx context.Context, r relay, bus publisher, log *slog.Logger) error {
	published, failed, err := r.Relay(ctx, limit,
		func(ctx context.Context, event outbox.Pending) error {
			return bus.Publish(ctx, event.Event())
		})
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeRelayFailed,
			"the outbox could not be relayed")
	}

	if published == 0 && failed == 0 {
		// DEBUG, not INFO. A healthy installation runs this every minute
		// forever, and a line that never changes is a line nobody reads.
		log.DebugContext(ctx, "the outbox is empty")

		return nil
	}

	if failed > 0 {
		// ERROR: every one of these is a message somebody is waiting for. The
		// row keeps its attempt count, so a permanently failing event stops
		// looking like a slow one.
		log.ErrorContext(ctx, "some promised events could not be published; they stay in the "+
			"outbox and are retried, but a repeated failure needs a human",
			"failed", failed, "published", published)
	}

	if published > 0 {
		log.InfoContext(ctx, "promised events were published", "published", published)
	}

	if published+failed == limit {
		log.WarnContext(ctx, "the relay filled its limit, so there is a backlog; the next pass "+
			"is a minute away", "limit", limit)
	}

	return nil
}
