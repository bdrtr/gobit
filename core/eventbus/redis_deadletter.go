package eventbus

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/bdrtr/gobit/core/errors"
)

// CodeDeadLetterFailed reports that the Redis bus's dead letters could not be
// read or changed (ADR 0273).
const CodeDeadLetterFailed = "eventbus_dead_letter_failed"

// RedisDeadLetter is one message the Redis bus gave up on: it emptied every
// consumer that took it, so the bus kept it in [RedisConfig.DeadLetterStream]
// instead of handing it to another (ADR 0273).
//
// The payload is deliberately absent, for the reason the outbox's dead letter
// leaves it out: a dead letter is read by a human during an incident and
// printed somewhere, and an event's data can carry a name or an address. The
// stream still holds it, so a redrive puts back what was published.
type RedisDeadLetter struct {
	// ID is the letter's entry in the dead-letter stream; it is what a redrive
	// or a discard names.
	ID string
	// EventID and EventName identify the event.
	EventID   string
	EventName string
	// Stream is the event's stream and MessageID its entry there.
	Stream    string
	MessageID string
	// Consumer is the consumer that held it last, and Deliveries how many
	// times it was handed over.
	Consumer   string
	Deliveries int64
	// DroppedAt is when the bus gave up on it.
	DroppedAt time.Time
}

// RedisDeadLetterReport is the pile and the oldest part of it: Count is the
// whole pile whatever the limit, so the report says how much as well as what.
type RedisDeadLetterReport struct {
	Count  int64
	Oldest []RedisDeadLetter
}

// Empty reports whether there is nothing for a human to look at.
func (r RedisDeadLetterReport) Empty() bool { return r.Count == 0 }

// ReadRedisDeadLetters reads how many dead letters the bus keeps and the
// oldest of them, at most limit (ADR 0273).
func ReadRedisDeadLetters(
	ctx context.Context, client *redis.Client, cfg RedisConfig, limit int64,
) (RedisDeadLetterReport, error) {
	key := cfg.DeadLetterStream()

	count, err := client.XLen(ctx, key).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return RedisDeadLetterReport{}, errors.Wrap(err, errors.KindUnavailable, CodeDeadLetterFailed,
			"the dead letters in %q could not be counted", key)
	}
	if count == 0 || limit <= 0 {
		return RedisDeadLetterReport{Count: count}, nil
	}

	msgs, err := client.XRangeN(ctx, key, rangeStart, rangeEnd, limit).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return RedisDeadLetterReport{}, errors.Wrap(err, errors.KindUnavailable, CodeDeadLetterFailed,
			"the dead letters in %q could not be read", key)
	}

	report := RedisDeadLetterReport{Count: count, Oldest: make([]RedisDeadLetter, 0, len(msgs))}
	for _, msg := range msgs {
		report.Oldest = append(report.Oldest, deadLetterOf(msg))
	}

	return report, nil
}

// deadLetterOf reads one entry of the dead-letter stream; a field it cannot
// read stays at its zero value rather than hiding the letter.
func deadLetterOf(msg redis.XMessage) RedisDeadLetter {
	letter := RedisDeadLetter{
		ID:        msg.ID,
		EventID:   stringField(msg.Values, fieldID),
		EventName: stringField(msg.Values, fieldName),
		Stream:    stringField(msg.Values, fieldStream),
		MessageID: stringField(msg.Values, fieldMessageID),
		Consumer:  stringField(msg.Values, fieldConsumer),
	}
	letter.Deliveries, _ = strconv.ParseInt(stringField(msg.Values, fieldDeliveries), 10, 64)
	letter.DroppedAt, _ = time.Parse(time.RFC3339Nano, stringField(msg.Values, fieldDroppedAt))

	return letter
}

// RedriveRedisDeadLetter puts one dead letter's message back on its stream,
// as it was published, and removes the letter; false means there was no such
// letter (ADR 0273).
//
// The two writes are one transaction, so a letter is never both redriven and
// still in the pile. The message comes back as a NEW entry: the group reads it
// as it reads any message, and a consumer that still dies on it hands it back
// to the pile after the same three deliveries.
func RedriveRedisDeadLetter(ctx context.Context, client *redis.Client, cfg RedisConfig, id string) (bool, error) {
	key := cfg.DeadLetterStream()

	msgs, err := client.XRange(ctx, key, id, id).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return false, errors.Wrap(err, errors.KindUnavailable, CodeDeadLetterFailed,
			"the dead letter %s could not be read from %q", id, key)
	}
	if len(msgs) == 0 {
		return false, nil
	}
	stream := stringField(msgs[0].Values, fieldStream)
	if stream == "" {
		return false, errors.Invalid(CodeDeadLetterFailed,
			"the dead letter %s names no stream to go back to", id)
	}

	values := make(map[string]any, 4)
	for _, field := range []string{fieldID, fieldName, fieldOccurredAt, fieldData} {
		values[field] = stringField(msgs[0].Values, field)
	}
	args := &redis.XAddArgs{Stream: stream, Values: values}
	if maxLen := cfg.withDefaults().MaxLen; maxLen > 0 {
		args.MaxLen, args.Approx = maxLen, true
	}

	if _, err := client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.XAdd(ctx, args)
		pipe.XDel(ctx, key, id)

		return nil
	}); err != nil {
		return false, errors.Wrap(err, errors.KindUnavailable, CodeDeadLetterFailed,
			"the dead letter %s could not be put back on %q", id, stream)
	}

	return true, nil
}

// DiscardRedisDeadLetter deletes one dead letter for good; false means there
// was no such letter (ADR 0273).
func DiscardRedisDeadLetter(ctx context.Context, client *redis.Client, cfg RedisConfig, id string) (bool, error) {
	key := cfg.DeadLetterStream()

	deleted, err := client.XDel(ctx, key, id).Result()
	if err != nil {
		return false, errors.Wrap(err, errors.KindUnavailable, CodeDeadLetterFailed,
			"the dead letter %s could not be deleted from %q", id, key)
	}

	return deleted > 0, nil
}
