package app

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus/outbox"
)

// deadLetterFixture is the instant every report below is rendered at.
var deadLetterFixture = time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

// fakeDeadLetters is the store as this command uses it.
//
// It records what was asked of it, because half of what these tests check is
// that the command passes ONE id — the narrowing is the guard (see
// [parseDeadLetterAction]), and a future edit that widened it to a slice would
// still make every "did the row change?" assertion pass.
type fakeDeadLetters struct {
	// remaining is the pile the act path re-reads after the verb has run.
	remaining outbox.DeadLetterReport
	affected  int64
	redriven  []string
	discarded []string
}

// DeadLetters answers with the pile as it stands after the verb.
func (f *fakeDeadLetters) DeadLetters(_ context.Context, _ int32) (outbox.DeadLetterReport, error) {
	return f.remaining, nil
}

// Redrive records the ids and reports the configured row count.
func (f *fakeDeadLetters) Redrive(_ context.Context, ids ...string) (int64, error) {
	f.redriven = append(f.redriven, ids...)

	return f.affected, nil
}

// Discard records the ids and reports the configured row count.
func (f *fakeDeadLetters) Discard(_ context.Context, ids ...string) (int64, error) {
	f.discarded = append(f.discarded, ids...)

	return f.affected, nil
}

// letter builds one dead letter for the report tests.
func letter(id, name, cause string, attempts int64) outbox.DeadLetter {
	return outbox.DeadLetter{
		ID:             id,
		Name:           name,
		Attempts:       attempts,
		LastError:      cause,
		CreatedAt:      deadLetterFixture.Add(-8 * time.Hour),
		DeadLetteredAt: deadLetterFixture.Add(-4 * time.Hour),
	}
}

// renderDeadLetters prints one report.
func renderDeadLetters(t *testing.T, report outbox.DeadLetterReport, limit int32) string {
	t.Helper()

	var buf strings.Builder
	require.NoError(t, writeDeadLetters(&buf, report, limit, deadLetterFixture))

	return buf.String()
}

// TestTheListingCarriesEnoughToDECIDE is the whole reason the pile is printed
// rather than counted.
//
// `gobit jobs` already says HOW MANY were given up on. What it cannot say is
// whether the operator should redrive or discard, and that decision needs four
// things on the page: which event it was, how hard the relay tried, what the
// receiver said, and when it stopped. A listing missing any one of them sends
// the reader to psql, which is where they already were.
func TestTheListingCarriesEnoughToDECIDE(t *testing.T) {
	t.Parallel()

	out := renderDeadLetters(t, outbox.DeadLetterReport{
		Count: 1,
		Oldest: []outbox.DeadLetter{
			letter("evt_order_1", "order.placed", "dial tcp 10.0.0.5:443: connection refused", 10),
		},
	}, defaultDeadLetterLimit)

	assert.Contains(t, out, "evt_order_1", "the id is what both verbs take")
	assert.Contains(t, out, "order.placed", "the event name is how a human recognizes the promise")
	assert.Contains(t, out, "attempts=10", "how hard it tried decides redrive against discard")
	assert.Contains(t, out, "dial tcp 10.0.0.5:443: connection refused",
		"WHY it died is the question asked second, and the row is the only place it lives")
	assert.Contains(t, out, "after 4h0m0s of trying",
		"the distance between the two instants IS the length of the unkept promise")
	assert.Contains(t, out, "4h0m0s ago", "how long ago it died is a different incident from how long it tried")
}

// TestTheListingNamesBothWaysOut keeps the surface from being a dead end.
//
// An operator who can see the pile and cannot act on it is exactly where this
// repository was before this command existed. The two verbs are printed WITH
// their confirmation, because a line the reader has to reconstruct from the
// help text is a line they will get wrong once.
func TestTheListingNamesBothWaysOut(t *testing.T) {
	t.Parallel()

	out := renderDeadLetters(t, outbox.DeadLetterReport{
		Count:  1,
		Oldest: []outbox.DeadLetter{letter("evt_1", "order.placed", "boom", 10)},
	}, defaultDeadLetterLimit)

	assert.Contains(t, out, deadLettersCommand+" "+cmdRedrive+" <event-id> -"+flagConfirm)
	assert.Contains(t, out, deadLettersCommand+" "+cmdDiscard+" <event-id> -"+flagConfirm)
	assert.Contains(t, out, "cannot be undone", "the destructive verb says so where it is offered")
}

// TestTheListingSaysThePayloadWasWITHHELD draws the line between "not printed"
// and "not there".
//
// An operator deciding whether anybody is owed the event will look for its
// data. The store leaves the payload out on purpose — it can carry an address
// or a phone number and this listing gets pasted into incident channels — and
// silence about that reads as "the event is empty", which would argue for
// discarding it.
func TestTheListingSaysThePayloadWasWITHHELD(t *testing.T) {
	t.Parallel()

	out := renderDeadLetters(t, outbox.DeadLetterReport{
		Count:  1,
		Oldest: []outbox.DeadLetter{letter("evt_1", "order.placed", "boom", 10)},
	}, defaultDeadLetterLimit)

	assert.Contains(t, out, "The payloads are NOT printed")
	assert.Contains(t, out, "a redrive loses nothing")
}

// TestATruncatedListingSaysTheCountIsThePile keeps the page from being read as
// the incident.
//
// The count comes from a window function over the whole filtered set, so it is
// the pile and not the sample; a report that printed fifty rows and said
// nothing about the rest would describe a smaller outage than the real one.
func TestATruncatedListingSaysTheCountIsThePile(t *testing.T) {
	t.Parallel()

	out := renderDeadLetters(t, outbox.DeadLetterReport{
		Count:  2000,
		Oldest: []outbox.DeadLetter{letter("evt_1", "order.placed", "boom", 10)},
	}, 1)

	assert.Contains(t, out, "2000 dead letter(s) in the outbox")
	assert.Contains(t, out, "THE LIST IS INCOMPLETE: 1 of 2000 printed")

	full := renderDeadLetters(t, outbox.DeadLetterReport{
		Count:  1,
		Oldest: []outbox.DeadLetter{letter("evt_1", "order.placed", "boom", 10)},
	}, defaultDeadLetterLimit)
	assert.NotContains(t, full, "INCOMPLETE",
		"a complete page must not carry the warning; a warning on every listing is a warning nobody reads")
}

// TestAnEmptyPileSaysNothingIsOwed is the healthy installation's answer.
//
// It has to be a sentence rather than an empty page: a command that printed a
// header and nothing else looks like a command that failed to reach the
// database, which during an incident is the worse of the two readings.
func TestAnEmptyPileSaysNothingIsOwed(t *testing.T) {
	t.Parallel()

	out := renderDeadLetters(t, outbox.DeadLetterReport{}, defaultDeadLetterLimit)

	assert.Contains(t, out, "the pile is EMPTY")
	assert.NotContains(t, out, "INCOMPLETE")
	assert.NotContains(t, out, cmdDiscard,
		"there is nothing to discard, so the destructive verb is not advertised")
}

// TestAnEmptyLastErrorIsNotABlank keeps a legitimately empty column from
// reading as a failed lookup.
func TestAnEmptyLastErrorIsNotABlank(t *testing.T) {
	t.Parallel()

	out := renderDeadLetters(t, outbox.DeadLetterReport{
		Count:  1,
		Oldest: []outbox.DeadLetter{letter("evt_1", "order.placed", "", 10)},
	}, defaultDeadLetterLimit)

	assert.Contains(t, out, "last error: <none recorded>")
}

// TestTheDeadLetterVerbsNeedTheEventIDRepeated is the operator-facing safety
// property, and it is cheap enough to run on every commit.
//
// Both verbs act on a value copied out of a listing where one line up or down
// is another customer's promise. Discard deletes that promise and its payload;
// redrive publishes a real message to a real person and erases the record of
// the death. A run without the repeat does NOTHING.
func TestTheDeadLetterVerbsNeedTheEventIDRepeated(t *testing.T) {
	t.Parallel()

	tests := map[string][]string{
		"no arguments at all":       {},
		"only the id":               {"evt_1"},
		"a confirmation for nobody": {"-" + flagConfirm, "evt_1"},
		"another event's id":        {"evt_1", "-" + flagConfirm, "evt_2"},
		"an empty confirmation":     {"evt_1", "-" + flagConfirm, ""},
		"the flags before the id":   {"-" + flagConfirm, "evt_1", "evt_1"},
	}

	for _, verb := range []string{cmdRedrive, cmdDiscard} {
		for name, args := range tests {
			t.Run(verb+"/"+name, func(t *testing.T) {
				t.Parallel()

				_, err := parseDeadLetterAction(verb, args)

				require.Error(t, err, "nothing may run without the id repeated")
				assert.True(t, coreerrors.IsInvalid(err), "error: %v", err)
				assert.Equal(t, codeDeadLetterRefused, coreerrors.CodeOf(err))
			})
		}
	}
}

// TestTheDeadLetterRefusalSaysWhatIsAtSTAKE keeps the two verbs from sharing
// one sentence.
//
// The refusal is the last thing an operator reads before repeating the id, so
// it is the last chance to say which of the two they are holding. They are not
// the same risk: one deletes a promise, the other sends a message.
func TestTheDeadLetterRefusalSaysWhatIsAtSTAKE(t *testing.T) {
	t.Parallel()

	_, discardErr := parseDeadLetterAction(cmdDiscard, []string{"evt_1"})
	require.Error(t, discardErr)
	assert.Contains(t, discardErr.Error(), "DELETES")
	assert.Contains(t, discardErr.Error(), "nothing can bring it back")

	_, redriveErr := parseDeadLetterAction(cmdRedrive, []string{"evt_1"})
	require.Error(t, redriveErr)
	assert.Contains(t, redriveErr.Error(), "publishes the event again")
	assert.Contains(t, redriveErr.Error(), "erases the record of its death")
}

// TestTheDeadLetterVerbsAcceptTheRepeatedID draws the other side of the
// boundary: the one shape that runs.
func TestTheDeadLetterVerbsAcceptTheRepeatedID(t *testing.T) {
	t.Parallel()

	for _, verb := range []string{cmdRedrive, cmdDiscard} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()

			action, err := parseDeadLetterAction(verb, []string{"evt_1", "-" + flagConfirm, "evt_1"})

			require.NoError(t, err)
			assert.Equal(t, "evt_1", action.eventID)
			assert.Equal(t, verb, action.verb)
		})
	}
}

// TestADeadLetterVerbRefusesALeftoverArgument keeps a mistyped command from
// being read as a valid one.
//
// `deadletters discard evt_1 -confirm evt_1 evt_2` is somebody trying to
// discard two events. Taking the first and dropping the second silently would
// leave the operator believing both promises were closed — and the second one
// would sit in the pile keeping the relay job red, which reads as a NEW
// failure.
func TestADeadLetterVerbRefusesALeftoverArgument(t *testing.T) {
	t.Parallel()

	_, err := parseDeadLetterAction(cmdDiscard,
		[]string{"evt_1", "-" + flagConfirm, "evt_1", "evt_2"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "evt_2")
	assert.Contains(t, err.Error(), "ONE event id")
}

// TestTheVerbPassesExactlyONEID pins the narrowing that IS the guard.
//
// The store's methods are variadic and the confirmation names one id. If this
// ever handed them a slice, the confirmation would be authorizing rows it never
// named — and every other test here would still pass, because they only check
// that the row changed.
func TestTheVerbPassesExactlyONEID(t *testing.T) {
	t.Parallel()

	store := &fakeDeadLetters{affected: 1}
	require.NoError(t, actOnDeadLetter(t.Context(), store, io.Discard,
		deadLetterAction{verb: cmdDiscard, eventID: "evt_1"}))

	assert.Equal(t, []string{"evt_1"}, store.discarded)
	assert.Empty(t, store.redriven, "the discard verb must not redrive")
}

// TestTheOutcomeSaysWhetherTheALARMWillClear is why the act path re-reads the
// pile.
//
// The operator arrived from a FAILED `gobit jobs` row. "One event was
// discarded" does not answer their question; whether that row goes green does,
// and finding out with a second command is a second command during an incident.
func TestTheOutcomeSaysWhetherTheALARMWillClear(t *testing.T) {
	t.Parallel()

	emptied := &fakeDeadLetters{affected: 1}
	var cleared strings.Builder
	require.NoError(t, actOnDeadLetter(t.Context(), emptied, &cleared,
		deadLetterAction{verb: cmdRedrive, eventID: "evt_1"}))
	assert.Contains(t, cleared.String(), "the pile is now EMPTY")
	assert.Contains(t, cleared.String(), "evt_1 is back in the queue")

	standing := &fakeDeadLetters{affected: 1, remaining: outbox.DeadLetterReport{Count: 41}}
	var remaining strings.Builder
	require.NoError(t, actOnDeadLetter(t.Context(), standing, &remaining,
		deadLetterAction{verb: cmdDiscard, eventID: "evt_1"}))
	assert.Contains(t, remaining.String(), "41 dead letter(s) are still waiting")
	assert.Contains(t, remaining.String(), "keeps FAILING")
}

// TestAVerbThatChangedNOTHINGIsAnError is the operator's most likely mistake,
// and it must not print "done".
//
// The store refuses anything that is not already dead, so zero rows means the
// id named nothing this verb may touch. Reporting success would tell an
// operator that a promise was closed when the row is still sitting in the pile.
func TestAVerbThatChangedNOTHINGIsAnError(t *testing.T) {
	t.Parallel()

	store := &fakeDeadLetters{affected: 0}
	var out strings.Builder

	err := actOnDeadLetter(t.Context(), store, &out,
		deadLetterAction{verb: cmdDiscard, eventID: "evt_typo"})

	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "error: %v", err)
	assert.Contains(t, err.Error(), "NOTHING was changed")
	assert.Empty(t, out.String(), "nothing happened, so nothing is reported as having happened")
}

// TestTheListingLimitIsBoundedByTheStoresParameter keeps the "showing N of M"
// line from being a lie.
//
// A number the query cannot carry as itself would arrive at the database as
// something else, and the page would then describe a different request than the
// one that was typed.
func TestTheListingLimitIsBoundedByTheStoresParameter(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{"0", "-3", "2147483648"} {
		_, err := parseDeadLetterListFlags([]string{"-" + flagLimit, bad})

		require.Error(t, err, "-%s %s must be refused", flagLimit, bad)
		assert.True(t, coreerrors.IsInvalid(err), "error: %v", err)
	}

	limit, err := parseDeadLetterListFlags(nil)
	require.NoError(t, err)
	assert.Equal(t, int32(defaultDeadLetterLimit), limit)
}

// TestTheListingRefusesAPositionalArgument catches the likeliest typo of all.
//
// `gobit deadletters evt_1` is somebody who means to act on evt_1 and has
// forgotten the verb. Printing the whole pile instead would look like the
// command worked.
func TestTheListingRefusesAPositionalArgument(t *testing.T) {
	t.Parallel()

	_, err := parseDeadLetterListFlags([]string{"evt_1"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), cmdRedrive,
		"the refusal has to name the verb the operator was reaching for")
}

// TestDeadLettersHelpIsNotAFailure keeps asking a question from exiting
// non-zero.
//
// Help costs nothing: the run returns before any configuration is read, so it
// works on a machine that cannot reach the database at all.
func TestDeadLettersHelpIsNotAFailure(t *testing.T) {
	t.Parallel()

	out := &strings.Builder{}
	require.NoError(t, runDeadLetterList([]string{"-h"}, out))
	assert.Empty(t, out.String(), "usage belongs on stderr; stdout is the listing")
}

// TestDeadLettersIsRoutedByTheDispatcher covers the branches that reach this
// command without opening a database.
//
// A subcommand main never reaches is this repository's most expensive bug
// class, and it is the exact class this whole command was written to close —
// so the routing is pinned rather than assumed. The unknown verb must FAIL:
// falling through would print the pile in answer to a request to change it.
func TestDeadLettersIsRoutedByTheDispatcher(t *testing.T) {
	t.Parallel()

	// A flag only this flag set knows about proves the routing without opening
	// a database: the parse fails before any configuration is read.
	err := Main([]string{deadLettersCommand, "-not-a-flag"}, io.Discard, Options{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not-a-flag")

	// A verb reaches the guard, and the guard refuses before any configuration
	// is read too.
	err = Main([]string{deadLettersCommand, cmdDiscard, "evt_1"}, io.Discard, Options{})
	require.Error(t, err)
	assert.Equal(t, codeDeadLetterRefused, coreerrors.CodeOf(err))

	err = Main([]string{deadLettersCommand, "redrivee", "evt_1"}, io.Discard, Options{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redrivee")
	assert.Equal(t, codeUsage, coreerrors.CodeOf(err))
}

// TestUsageTextNamesTheDeadLetterCommand keeps the help text honest: a command
// the binary answers but does not list is a command nobody finds during an
// incident — and this one is only ever looked for during an incident.
func TestUsageTextNamesTheDeadLetterCommand(t *testing.T) {
	t.Parallel()

	text := usageText("dev")

	for _, want := range []string{
		deadLettersCommand,
		deadLettersCommand + " " + cmdRedrive,
		deadLettersCommand + " " + cmdDiscard,
		"-" + flagLimit + " N",
	} {
		assert.Contains(t, text, want)
	}
}
