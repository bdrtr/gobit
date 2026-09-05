package outboxrelay_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/eventbus/outbox"
	"github.com/bdrtr/gobit/internal/jobs/outboxrelay"
)

// These tests cover the job's REPORTING, which is the half a database cannot
// prove: whether a dead letter reaches the operator. The delivery half — the
// backoff, the ceiling and the row leaving the queue — is proved against a real
// PostgreSQL in core/eventbus/outbox, because a fake that implements the same
// rule the code was written to only proves the fake agrees with itself.

// fakeRelay stands in for the outbox store.
type fakeRelay struct {
	pending   []outbox.Pending
	deadFrom  int
	report    outbox.DeadLetterReport
	err       error
	reportErr error

	gotLimit int32
	calls    int
}

// Relay hands every pending event to publish and reports the outcome.
//
// An event whose index is at or past deadFrom is treated as having crossed the
// ceiling on this pass, which is what the store's UPDATE decides in production.
func (f *fakeRelay) Relay(
	ctx context.Context, limit int32, publish func(context.Context, outbox.Pending) error,
) (outbox.RelayResult, error) {
	f.calls++
	f.gotLimit = limit
	if f.err != nil {
		return outbox.RelayResult{}, f.err
	}

	var result outbox.RelayResult
	for i, event := range f.pending {
		if publish(ctx, event) != nil {
			result.Failed++
			if f.deadFrom > 0 && i >= f.deadFrom-1 {
				result.DeadLettered = append(result.DeadLettered, event.ID)
			}

			continue
		}
		result.Published++
	}

	return result, nil
}

// DeadLetters returns the scripted pile.
func (f *fakeRelay) DeadLetters(_ context.Context, limit int32) (outbox.DeadLetterReport, error) {
	if f.reportErr != nil {
		return outbox.DeadLetterReport{}, f.reportErr
	}
	if int32(len(f.report.Oldest)) > limit {
		f.report.Oldest = f.report.Oldest[:limit]
	}

	return f.report, nil
}

// fakeBus stands in for the event bus.
type fakeBus struct {
	err       error
	published []eventbus.Event
}

// Publish records the event and applies the scripted behavior.
func (f *fakeBus) Publish(_ context.Context, e eventbus.Event) error {
	if f.err != nil {
		return f.err
	}
	f.published = append(f.published, e)

	return nil
}

// runRelay runs one pass and returns everything it logged.
func runRelay(t *testing.T, r *fakeRelay, bus *fakeBus) (string, error) {
	t.Helper()

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	err := outboxrelay.Definition(r, bus, log).Run(context.Background())

	return buf.String(), err
}

// TestAPromisedEventIsPublished is the delivery half of the outbox.
//
// The module writes the event with its work; without this it would sit in the
// table forever and the guarantee would be a table nobody read.
func TestAPromisedEventIsPublished(t *testing.T) {
	r := &fakeRelay{pending: []outbox.Pending{
		{ID: "order.placed:order_1", Name: "order.placed", Data: map[string]any{"order_id": "order_1"}},
	}}
	bus := &fakeBus{}

	out, err := runRelay(t, r, bus)
	require.NoError(t, err)

	require.Len(t, bus.published, 1)
	assert.Equal(t, "order.placed", bus.published[0].Name)
	assert.Equal(t, "order.placed:order_1", bus.published[0].ID,
		"the id has to survive: it is what makes the row and the direct publish ONE event")
	assert.Contains(t, out, "level=INFO")
}

// TestAnEmptyOutboxStaysQUIET keeps the minute-by-minute line out of the log.
//
// A relay that says something every minute is one whose lines nobody reads,
// which is how the minute that matters gets missed.
func TestAnEmptyOutboxStaysQUIET(t *testing.T) {
	out, err := runRelay(t, &fakeRelay{}, &fakeBus{})
	require.NoError(t, err)

	assert.Contains(t, out, "level=DEBUG")
	assert.NotContains(t, out, "level=INFO")
	assert.NotContains(t, out, "level=ERROR")
}

// TestAnEventThatCouldNotBeSentIsREPORTED is why the count is worth having.
//
// Every one of these is a message somebody is waiting for. Staying silent would
// make an outage look like a quiet minute.
func TestAnEventThatCouldNotBeSentIsREPORTED(t *testing.T) {
	r := &fakeRelay{pending: []outbox.Pending{
		{ID: "order.placed:order_1", Name: "order.placed"},
	}}
	bus := &fakeBus{err: errors.New("the bus is unreachable")}

	out, err := runRelay(t, r, bus)
	require.NoError(t, err, "a failed delivery is retried, not a failed run")

	assert.Contains(t, out, "level=ERROR")
	assert.Contains(t, out, "failed=1")
}

// TestAFailedPassIsAFailedRun keeps a relay that could not read from looking
// like one with nothing to do.
func TestAFailedPassIsAFailedRun(t *testing.T) {
	r := &fakeRelay{err: errors.New("the pool is closed")}

	_, err := runRelay(t, r, &fakeBus{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not be relayed")
}

// TestTheRelayIsBounded keeps one pass from trying to send a whole backlog.
func TestTheRelayIsBounded(t *testing.T) {
	r := &fakeRelay{}

	_, err := runRelay(t, r, &fakeBus{})
	require.NoError(t, err)

	assert.Positive(t, r.gotLimit)
}

// TestDefinitionRunsOftenEnoughToMatter pins the interval's intent.
//
// What waits in the outbox is a message somebody is expecting — a confirmation
// for an order already paid for — so here the delay IS the damage, unlike the
// reporting jobs whose subjects have already been wrong for a while.
func TestDefinitionRunsOftenEnoughToMatter(t *testing.T) {
	def := outboxrelay.Definition(&fakeRelay{}, &fakeBus{}, nil)

	assert.Equal(t, outboxrelay.Name, def.Name)
	assert.LessOrEqual(t, def.Every.Minutes(), 1.0)
	assert.Positive(t, def.MaxRun)
}

// TestTheMomentAnEventIsGivenUpOnIsLOGGED records which pass killed which row.
//
// The row keeps the attempt count and the last error, but nothing in the schema
// says WHEN the relay stopped — that is [outbox.DeadLetter]'s timestamp — or
// which ids went together. An operator reconstructing an incident reads the log
// forwards, and this is the line that appears at the moment of death.
func TestTheMomentAnEventIsGivenUpOnIsLOGGED(t *testing.T) {
	r := &fakeRelay{
		pending:  []outbox.Pending{{ID: "order.placed:order_9", Name: "order.placed"}},
		deadFrom: 1,
		report:   deadLetterReport(1),
	}
	bus := &fakeBus{err: errors.New("the bus is unreachable")}

	out, err := runRelay(t, r, bus)

	require.Error(t, err, "a standing dead letter is a failed run")
	assert.Contains(t, out, "given up on")
	assert.Contains(t, out, "order.placed:order_9")
}

// TestAStandingDeadLetterFAILSTheRun is the whole read surface.
//
// `gobit jobs` prints a DETAIL column, and internal/core/job's runner fills it
// only from an ERROR — a successful run records no detail at all. So a relay
// that returned nil while promised events sat undelivered would leave the
// operator's one listing saying "ok" with an empty detail, which is the
// write-only ledger this repository has already built once in audit_log.
func TestAStandingDeadLetterFAILSTheRun(t *testing.T) {
	r := &fakeRelay{report: deadLetterReport(3)}

	_, err := runRelay(t, r, &fakeBus{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "given up on")
	assert.Contains(t, err.Error(), "3 promised event")
}

// TestTheDetailReachesTheJobListing checks the interface the runner looks for.
//
// The link between this job and `gobit jobs` is a method NAME, resolved with
// errors.As inside the runner. A rename on either side compiles perfectly and
// silently empties the DETAIL column, which is the only place the pile is
// printed.
func TestTheDetailReachesTheJobListing(t *testing.T) {
	r := &fakeRelay{report: deadLetterReport(2)}

	_, err := runRelay(t, r, &fakeBus{})
	require.Error(t, err)

	var reporter interface{ JobDetail() string }
	require.ErrorAs(t, err, &reporter,
		"the runner reads the operator's line off this interface; without it the "+
			"listing shows a failed job with no reason")

	detail := reporter.JobDetail()
	assert.Contains(t, detail, "2 dead-lettered")
	assert.Contains(t, detail, "order.placed:order_1")
	assert.NotContains(t, detail, "\n", "Outcome.Detail is one line of a table")
}

// TestTheDeadLettersAreAskedForOnAQuietPass keeps the pile from hiding.
//
// The pile is created during the outage and read afterwards, when there is
// nothing left to publish. A relay that only looked when it had work would go
// silent in exactly the hour somebody needs to be told.
func TestTheDeadLettersAreAskedForOnAQuietPass(t *testing.T) {
	r := &fakeRelay{report: deadLetterReport(1)}

	out, err := runRelay(t, r, &fakeBus{})

	require.Error(t, err, "there was nothing to relay, and still something to say")
	assert.Contains(t, out, "the outbox is empty",
		"the pass itself was quiet; the failure comes from the pile, not the pass")
}

// TestAnUnreadableDeadLetterPileFailsLOUDLY refuses to guess.
//
// If the query behind the report breaks, the honest answer is "I cannot say
// whether anything has been given up on". Returning nil would report the one
// thing that is certainly wrong: that there is nothing to report.
func TestAnUnreadableDeadLetterPileFailsLOUDLY(t *testing.T) {
	r := &fakeRelay{reportErr: errors.New("the pool is closed")}

	_, err := runRelay(t, r, &fakeBus{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "dead letters could not be read")
}

// TestTheDetailSurvivesAMultiLineError keeps the listing's table intact.
//
// The last error came from a REMOTE system — a driver message, a wrapped chain,
// an HTTP body — and nothing here controls whether it has a newline in it. The
// detail is one cell of a tabwriter table, so a newline breaks the alignment of
// every row after it, in the one listing read during an incident.
func TestTheDetailSurvivesAMultiLineError(t *testing.T) {
	report := deadLetterReport(1)
	report.Oldest[0].LastError = "dial tcp: connection refused\n\tafter 3 retries\n"
	r := &fakeRelay{report: report}

	_, err := runRelay(t, r, &fakeBus{})
	require.Error(t, err)

	var reporter interface{ JobDetail() string }
	require.ErrorAs(t, err, &reporter)

	detail := reporter.JobDetail()
	assert.NotContains(t, detail, "\n")
	assert.NotContains(t, detail, "\t")
	assert.Contains(t, detail, "dial tcp: connection refused after 3 retries")
}

// deadLetterReport builds a pile of the given size with a sample of it.
func deadLetterReport(count int64) outbox.DeadLetterReport {
	return outbox.DeadLetterReport{
		Count: count,
		Oldest: []outbox.DeadLetter{{
			ID:             "order.placed:order_1",
			Name:           "order.placed",
			Attempts:       10,
			LastError:      "the bus is unreachable",
			CreatedAt:      time.Now().Add(-5 * time.Hour),
			DeadLetteredAt: time.Now().Add(-time.Hour),
		}},
	}
}
