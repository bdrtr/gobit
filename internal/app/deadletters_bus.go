package app

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/jobs/outboxrelay"
)

// The event bus's own pile, on the same verb as the outbox's (ADR 0273).
//
// The outbox relay gives up on an event it could not PUBLISH; the Redis bus
// gives up on a message it published and every consumer that took it died on.
// The two are one question for an operator — "what did we promise and not
// deliver" — so `deadletters` lists both and acts on either, told apart by the
// id: an outbox letter is an event id, a bus letter is a stream entry id
// ("1727000000000-0"), and neither can be the other.

// streamEntryID is the shape of a Redis stream entry id.
var streamEntryID = regexp.MustCompile(`^\d+-\d+$`)

// busDeadLetterStore is the bus pile's surface, declared on the consumer's
// side (ADR 0001) so the report and the verbs are testable without Redis.
type busDeadLetterStore interface {
	Read(ctx context.Context, limit int64) (eventbus.RedisDeadLetterReport, error)
	Redrive(ctx context.Context, id string) (bool, error)
	Discard(ctx context.Context, id string) (bool, error)
}

// redisPile is the bus pile of an installation, over its own client and the
// namespace its server's bus uses.
type redisPile struct {
	client *redis.Client
	cfg    eventbus.RedisConfig
}

func (p redisPile) Read(ctx context.Context, limit int64) (eventbus.RedisDeadLetterReport, error) {
	return eventbus.ReadRedisDeadLetters(ctx, p.client, p.cfg, limit)
}

func (p redisPile) Redrive(ctx context.Context, id string) (bool, error) {
	return eventbus.RedriveRedisDeadLetter(ctx, p.client, p.cfg, id)
}

func (p redisPile) Discard(ctx context.Context, id string) (bool, error) {
	return eventbus.DiscardRedisDeadLetter(ctx, p.client, p.cfg, id)
}

// openBusPile opens the installation's Redis when its bus is Redis; nil means
// the bus keeps no pile (the in-memory bus drops nothing it could keep).
func openBusPile(ctx context.Context, cfg config.Config) (busDeadLetterStore, func(), error) {
	if cfg.EventBus != config.BackendRedis {
		return nil, func() {}, nil
	}

	opt, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, nil, errors.Wrap(err, errors.KindInvalid, "redis_url_invalid",
			"REDIS_URL could not be parsed")
	}
	client := redis.NewClient(opt)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, nil, errors.Wrap(err, errors.KindUnavailable, "redis_unreachable",
			"Redis could not be reached (%s); the bus's dead letters cannot be read", opt.Addr)
	}

	return redisPile{client: client, cfg: busConfig(cfg)}, func() { _ = client.Close() }, nil
}

// listBusDeadLetters prints the bus's pile after the outbox's.
func listBusDeadLetters(
	ctx context.Context, name string, pile busDeadLetterStore, out io.Writer, limit int64, now time.Time,
) error {
	report, err := pile.Read(ctx, limit)
	if err != nil {
		return err
	}

	return writeBusDeadLetters(name, out, report, now)
}

// writeBusDeadLetters prints the bus's pile the way the outbox's is printed:
// how many, then which, then what to do about one.
func writeBusDeadLetters(name string, out io.Writer, report eventbus.RedisDeadLetterReport, now time.Time) error {
	buf := &strings.Builder{}

	fmt.Fprintf(buf,
		"\n%s %s: delivered events the Redis bus KEPT after they emptied every consumer "+
			"that took them. READ ONLY — nothing below was changed.\n",
		name, deadLettersCommand)
	if report.Empty() {
		fmt.Fprintf(buf, "the bus's pile is EMPTY: no message has killed the consumers that took it.\n")
		_, err := io.WriteString(out, buf.String())

		return err
	}

	fmt.Fprintf(buf, "%d kept message(s); %d printed, oldest first (-%s).\n\n",
		report.Count, len(report.Oldest), flagLimit)
	for i := range report.Oldest {
		letter := &report.Oldest[i]
		fmt.Fprintf(buf, "%s  %s  deliveries=%d\n", letter.ID, letter.EventName, letter.Deliveries)
		fmt.Fprintf(buf, "  event:      %s (stream %s, entry %s)\n", letter.EventID, letter.Stream, letter.MessageID)
		fmt.Fprintf(buf, "  held last:  %s\n", letter.Consumer)
		fmt.Fprintf(buf, "  given up:   %s (%s ago)\n\n",
			letter.DroppedAt.Format(time.RFC3339), now.Sub(letter.DroppedAt).Truncate(time.Second))
	}
	if int64(len(report.Oldest)) < report.Count {
		fmt.Fprintf(buf, "THE LIST IS INCOMPLETE: %d of %d printed. Raise -%s to see the rest.\n",
			len(report.Oldest), report.Count, flagLimit)
	}
	fmt.Fprintf(buf,
		"The payloads are NOT printed; the stream still has them, so a redrive loses nothing.\n"+
			"Once the handler is fixed, put one back on its stream:\n"+
			"  %s %s %s <letter-id> -%s <letter-id>\n"+
			"Or delete it for good:\n"+
			"  %s %s %s <letter-id> -%s <letter-id>\n",
		name, deadLettersCommand, cmdRedrive, flagConfirm,
		name, deadLettersCommand, cmdDiscard, flagConfirm)

	_, err := io.WriteString(out, buf.String())

	return err
}

// actOnBusDeadLetter runs one verb against one bus letter and reports what
// changed and what is left.
func actOnBusDeadLetter(
	ctx context.Context, name string, pile busDeadLetterStore, out io.Writer, action deadLetterAction,
) error {
	if pile == nil {
		return errors.Invalid(codeDeadLetterRefused,
			"%s is a stream entry id, and this installation's bus keeps no dead letters "+
				"(EVENT_BUS is not redis); `%s %s` lists what there is",
			action.eventID, name, deadLettersCommand)
	}

	var (
		done bool
		err  error
	)
	switch action.verb {
	case cmdRedrive:
		done, err = pile.Redrive(ctx, action.eventID)
	case cmdDiscard:
		done, err = pile.Discard(ctx, action.eventID)
	default:
		return errors.Internal(codeDeadLetterRefused, "%s is not a %s verb", action.verb, deadLettersCommand)
	}
	if err != nil {
		return err
	}
	if !done {
		return errors.NotFound(codeDeadLetterRefused,
			"%s is not a letter the bus keeps, so NOTHING was changed: either the id is wrong "+
				"or somebody has already handled it. `%s %s` says which.",
			action.eventID, name, deadLettersCommand)
	}

	report, err := pile.Read(ctx, 1)
	if err != nil {
		return err
	}

	buf := &strings.Builder{}
	if action.verb == cmdRedrive {
		fmt.Fprintf(buf, "done: %s is back on its stream as a new message; the group reads it as any other.\n",
			action.eventID)
	} else {
		fmt.Fprintf(buf, "done: %s is gone, with its payload; nobody is owed it any more.\n", action.eventID)
	}
	if report.Empty() {
		fmt.Fprintf(buf, "the bus's pile is now EMPTY; once the outbox's is too, the next %s pass "+
			"records a success.\n", outboxrelay.Name)
	} else {
		fmt.Fprintf(buf, "%d kept message(s) are still waiting; the %s job keeps FAILING until the "+
			"pile is empty.\n", report.Count, outboxrelay.Name)
	}

	return writeReport(out, buf.String())
}
