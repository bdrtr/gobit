package webhookout

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/core/jobreport"
)

// reportedLine runs one pass's reporting and returns what reached the listing.
//
// It goes through [webhookModule.reportPass] rather than [summarize] directly,
// because the fault ADR 0069 closes is not "the line is wrong" — it is that the
// line never left the process. A test that called summarize would still pass
// with the Report call deleted.
func reportedLine(t *testing.T, result passResult) string {
	t.Helper()

	ctx := jobreport.WithReporter(t.Context())
	newModule(nil).reportPass(ctx, result)

	return jobreport.Detail(ctx)
}

// TestAPassThatDeliveredSaysSoInTheListing is this plugin's half of ADR 0069.
//
// The sender's whole job is invisible when it works. A pass that claimed twelve
// deliveries and sent them all left `gobit jobs` showing exactly what an empty
// minute showed — a blank DETAIL cell — because the only way a plugin's job
// could fill it was to FAIL, and this pass fails only for a dead-letter pile.
func TestAPassThatDeliveredSaysSoInTheListing(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "claimed 12, delivered 12, failed 0",
		reportedLine(t, passResult{Claimed: 12, Delivered: 12}),
		"a plugin's job has to be able to say something while SUCCEEDING; if this is "+
			"empty the sender is again indistinguishable from a sender with nothing to do")
}

// TestAnEmptyPassSaysNothingWasDueRatherThanNothingAtAll keeps the cell honest.
//
// A blank cell is what a job that has NEVER RUN looks like, and this job runs
// every minute. "nothing due" is a real answer to "is the sender running", and
// it is reported before the early return that skips the log on a quiet pass —
// which is the one line of this that is easy to get wrong.
func TestAnEmptyPassSaysNothingWasDueRatherThanNothingAtAll(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "nothing due", reportedLine(t, passResult{}))
}

// TestAFilledBatchIsReportedAsABacklog is the bound the retry ladder promised
// an operator could see.
//
// [delayAfter]'s ladder carries no jitter because [deliveryLimit] bounds the
// herd instead, and it calls that a bound "the operator can see in the job's
// report". A filled batch and a batch of ninety-nine produce the same two
// counts; this is the difference between "the sender is working" and "the
// sender is working and losing ground".
func TestAFilledBatchIsReportedAsABacklog(t *testing.T) {
	t.Parallel()

	line := reportedLine(t, passResult{Claimed: deliveryLimit, Delivered: deliveryLimit})

	assert.Contains(t, line, "so there is a backlog")
}

// TestTheClaimedRowsAPassNeverAttemptedAreCounted covers the state the two
// headline counts cannot express.
//
// A pass that ran out of budget attempted half its batch and looks, in
// "claimed/delivered/failed" alone, exactly like a pass whose second half all
// failed silently. The rows are leased rather than lost, and saying so is what
// stops the arithmetic reading as a hole.
func TestTheClaimedRowsAPassNeverAttemptedAreCounted(t *testing.T) {
	t.Parallel()

	line := reportedLine(t, passResult{Claimed: 10, Delivered: 6, Skipped: 4})

	assert.Contains(t, line, "4 left for the next pass")
}

// TestTheDeliveriesGivenUpOnInThisPassAreNamedAsNew separates the pile from the
// moment it grew.
//
// The pile itself is counted by the dead-letter read and printed by the
// failure. This number says the pile grew JUST NOW, which is the fact an
// operator correlates with an outage — and the only pass that can state it is
// the one that made them.
func TestTheDeliveriesGivenUpOnInThisPassAreNamedAsNew(t *testing.T) {
	t.Parallel()

	line := reportedLine(t, passResult{
		Claimed: 3, Failed: 3, DeadLettered: []string{"a", "b"},
	})

	assert.Contains(t, line, "2 newly given up on")
}
