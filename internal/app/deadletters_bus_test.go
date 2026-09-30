package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
)

// fakeBusPile is the bus pile without Redis.
type fakeBusPile struct {
	report   eventbus.RedisDeadLetterReport
	done     bool
	redriven []string
	dropped  []string
}

func (f *fakeBusPile) Read(_ context.Context, _ int64) (eventbus.RedisDeadLetterReport, error) {
	return f.report, nil
}

func (f *fakeBusPile) Redrive(_ context.Context, id string) (bool, error) {
	f.redriven = append(f.redriven, id)
	return f.done, nil
}

func (f *fakeBusPile) Discard(_ context.Context, id string) (bool, error) {
	f.dropped = append(f.dropped, id)
	return f.done, nil
}

// TestTheBusPileIsListedAfterTheOutboxs is ADR 0273 on the operator's verb:
// each kept message with its event, where it came from, who held it last and
// when it was given up on, the payload withheld, and both ways out.
func TestTheBusPileIsListedAfterTheOutboxs(t *testing.T) {
	t.Parallel()

	report := eventbus.RedisDeadLetterReport{Count: 3, Oldest: []eventbus.RedisDeadLetter{{
		ID: "1727000000000-0", EventID: "evt_poison", EventName: "order.placed",
		Stream: "shop:events:order.placed", MessageID: "1726999000000-0",
		Consumer: "web-7-1234", Deliveries: 3, DroppedAt: deadLetterFixture.Add(-2 * time.Hour),
	}}}
	out := &strings.Builder{}

	require.NoError(t, writeBusDeadLetters("gobit", out, report, deadLetterFixture))

	text := out.String()
	for _, want := range []string{
		"delivered events the Redis bus KEPT", "READ ONLY",
		"3 kept message(s); 1 printed",
		"1727000000000-0  order.placed  deliveries=3",
		"evt_poison (stream shop:events:order.placed, entry 1726999000000-0)",
		"held last:  web-7-1234", "(2h0m0s ago)",
		"THE LIST IS INCOMPLETE: 1 of 3",
		"The payloads are NOT printed",
		"gobit deadletters redrive <letter-id> -confirm <letter-id>",
		"gobit deadletters discard <letter-id> -confirm <letter-id>",
	} {
		assert.Contains(t, text, want)
	}

	empty := &strings.Builder{}
	require.NoError(t, writeBusDeadLetters("gobit", empty, eventbus.RedisDeadLetterReport{}, deadLetterFixture))
	assert.Contains(t, empty.String(), "the bus's pile is EMPTY")
}

// TestABusLetterIsActedOnByItsStreamID: the verb reaches the letter it names
// and says what is left; a letter that is not there is an error, not a quiet
// success.
func TestABusLetterIsActedOnByItsStreamID(t *testing.T) {
	t.Parallel()

	pile := &fakeBusPile{done: true, report: eventbus.RedisDeadLetterReport{Count: 1}}
	out := &strings.Builder{}
	err := actOnBusDeadLetter(context.Background(), "gobit", pile, out,
		deadLetterAction{verb: cmdRedrive, eventID: "1727000000000-0"})
	require.NoError(t, err)
	assert.Equal(t, []string{"1727000000000-0"}, pile.redriven)
	assert.Contains(t, out.String(), "is back on its stream")
	assert.Contains(t, out.String(), "1 kept message(s) are still waiting")

	drained := &fakeBusPile{done: true}
	out.Reset()
	require.NoError(t, actOnBusDeadLetter(context.Background(), "gobit", drained, out,
		deadLetterAction{verb: cmdDiscard, eventID: "1727000000000-0"}))
	assert.Equal(t, []string{"1727000000000-0"}, drained.dropped)
	assert.Contains(t, out.String(), "the bus's pile is now EMPTY")

	missing := &fakeBusPile{}
	err = actOnBusDeadLetter(context.Background(), "gobit", missing, out,
		deadLetterAction{verb: cmdDiscard, eventID: "1727000000000-0"})
	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "error: %v", err)

	err = actOnBusDeadLetter(context.Background(), "gobit", nil, out,
		deadLetterAction{verb: cmdRedrive, eventID: "1727000000000-0"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "EVENT_BUS is not redis")
}

// TestAStreamIDIsNoEventID: the two piles' ids cannot be mistaken for each
// other, which is what lets one verb take both.
func TestAStreamIDIsNoEventID(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"1727000000000-0", "0-1"} {
		assert.True(t, streamEntryID.MatchString(id), id)
	}
	for _, id := range []string{"evt_06GF7WR4MFC72YV6QBQD0AD7XG", "1727000000000", "a-1", "1-2-3", ""} {
		assert.False(t, streamEntryID.MatchString(id), id)
	}
}
