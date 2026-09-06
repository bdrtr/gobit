package webhookout

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// TestTheRetryWindowOutlastsAReceiverBeingDownForADay is the ladder's claim,
// computed rather than asserted.
//
// The number in the documentation is the reason the ceiling is not the outbox's
// ten attempts: a webhook goes to a third party that can be down for a working
// day, and the outbox's four hours and three minutes is inside a single night.
// A sentence saying "more than a day" with constants that produce eleven hours
// would be exactly the kind of unmeasured claim this repository has had to
// correct before.
func TestTheRetryWindowOutlastsAReceiverBeingDownForADay(t *testing.T) {
	t.Parallel()

	window := deliveryWindow()

	assert.Greater(t, window, 24*time.Hour,
		"the retry window is %s. A receiver that broke on Friday evening has to still be "+
			"owed its delivery on Saturday; anything under a day gives up inside one "+
			"night's outage.", window)

	// The other side of the same bound. A window that reached a week would mean
	// a genuinely decommissioned receiver keeps the delivery job failing — that
	// is, the alarm ringing — for a week before a human is even allowed to
	// discard the row.
	assert.Less(t, window, 48*time.Hour,
		"the retry window is %s, which is long enough that a receiver that has genuinely "+
			"gone away holds a pending delivery for two days before anyone is told", window)
}

// TestTheLadderDoublesAndThenStops is the schedule itself.
//
// It is written out rather than computed a second way, because a test that
// recomputed the doubling would agree with any bug in the doubling.
func TestTheLadderDoublesAndThenStops(t *testing.T) {
	t.Parallel()

	minutes := []int{1, 2, 4, 8, 16, 32, 64, 128, 256, 360, 360, 360, 360}
	require.Len(t, minutes, int(maxAttempts),
		"the expected schedule has to have one entry per allowed attempt")

	schedule := deliverySchedule()
	require.Len(t, schedule, len(minutes))

	for i, want := range minutes {
		assert.Equal(t, time.Duration(want)*time.Minute, schedule[i],
			"the wait after failure %d changed", i+1)
	}
}

// TestAnAttemptCountBelowOneDoesNotPanic guards the error path.
//
// A shift by a negative amount panics, and a panic in the sender's failure path
// would turn a receiver outage into a dead job — the delivery pass would stop
// running at exactly the moment it is needed.
func TestAnAttemptCountBelowOneDoesNotPanic(t *testing.T) {
	t.Parallel()

	assert.Equal(t, firstDelay, delayAfter(0))
	assert.Equal(t, firstDelay, delayAfter(-5))
	assert.Equal(t, maxDelay, delayAfter(1_000_000),
		"a very large attempt count must saturate at the cap rather than overflow into a "+
			"NEGATIVE duration, which would make the row due in the past — no backoff at "+
			"all, at the attempt count where the wait matters most")
}

// TestACustomerIDNeverLeavesTheInstallation is the payload boundary.
//
// `GET /store/v1/customers/{id}` is unauthenticated by decision (ADR 0008), so
// a customer id is a bearer token for that customer's name, email address and
// saved addresses. Putting it in a webhook body hands that to a third party
// standing, for every order.
func TestACustomerIDNeverLeavesTheInstallation(t *testing.T) {
	t.Parallel()

	payload, redacted := redact(map[string]any{
		"order_id":    "ord_1",
		"customer_id": "cus_secret",
		"total":       "12300",
	})

	assert.NotContains(t, payload, "customer_id",
		"the customer id is in the outbound payload; it is a bearer token for an "+
			"unauthenticated storefront endpoint that returns name, email and addresses")
	assert.Equal(t, []string{"customer_id"}, redacted,
		"the removal has to be VISIBLE in the body: a receiver that simply sees no "+
			"customer_id cannot tell a guest order from a withheld field, and will build a "+
			"guest branch that fires on every order")
	assert.Equal(t, "ord_1", payload["order_id"], "nothing else may be dropped")
	assert.Equal(t, "12300", payload["total"])
}

// TestRedactDoesNotMutateTheEventTheOtherSubscribersSee is the reentrancy rule.
//
// The bus hands the same payload to every handler. A subscriber that deleted a
// key from it would silently change what the notification module and searchpg
// receive, and the symptom would appear in THEIR behavior, not here.
func TestRedactDoesNotMutateTheEventTheOtherSubscribersSee(t *testing.T) {
	t.Parallel()

	original := map[string]any{"order_id": "ord_1", "customer_id": "cus_1"}

	redact(original)

	assert.Equal(t, "cus_1", original["customer_id"],
		"redact mutated the event's own map; every other subscriber on the bus now sees "+
			"a payload this plugin edited")
}

// TestATopicNobodyPublishesIsRefusedAtRegistration is the visible half of the
// name-based subscription.
//
// A receiver registered for a topic gobit does not publish would sit in the
// table looking correct and receive nothing forever. The only moment an
// integrator is present to be told is the registration request.
func TestATopicNobodyPublishesIsRefusedAtRegistration(t *testing.T) {
	t.Parallel()

	_, err := validateTopics([]string{"order.shipped"})
	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err))
	assert.Contains(t, err.Error(), "order.placed",
		"the refusal has to name the supported topics; \"invalid topic\" sends the "+
			"integrator to a support ticket to find out what they are")

	_, err = validateTopics(nil)
	require.Error(t, err, "a receiver with no topics is registered, visible, and can never "+
		"be delivered to")

	topics, err := validateTopics([]string{"order.placed", "order.placed", " product.created "})
	require.NoError(t, err)
	assert.Equal(t, []string{"order.placed", "product.created"}, topics,
		"a repeated topic must collapse rather than register twice, and surrounding "+
			"space must not make one name into two")
}

// TestAPlaintextDestinationIsRefusedUnlessItIsLoopback is the confidentiality
// half.
//
// The signature proves who SENT a delivery and nothing about who else read it.
// A body carrying order ids and totals over http is readable by everything on
// the path, and the receiver has no way to notice.
func TestAPlaintextDestinationIsRefusedUnlessItIsLoopback(t *testing.T) {
	t.Parallel()

	for _, target := range []string{
		"http://receiver.example/hook",
		"ftp://receiver.example/hook",
		"receiver.example/hook",
		"",
	} {
		_, err := validateURL(target)
		assert.Error(t, err, "%q was accepted as a webhook destination", target)
	}

	for _, target := range []string{
		"https://receiver.example/hook",
		"http://127.0.0.1:8080/hook",
		"http://localhost:8080/hook",
	} {
		_, err := validateURL(target)
		assert.NoError(t, err, "%q is a destination this sender must accept", target)
	}
}

// TestALongReceiverAnswerCannotFillTheColumn bounds attacker-influenced text.
//
// last_error goes into an admin listing and comes from a third party's body.
func TestALongReceiverAnswerCannotFillTheColumn(t *testing.T) {
	t.Parallel()

	long := make([]byte, 5000)
	for i := range long {
		long[i] = 'x'
	}

	stored := truncateError(string(long))
	assert.LessOrEqual(t, len(stored), maxStoredError+len("…"))
	assert.Equal(t, "one line", truncateError("  one\nline  "),
		"a newline in a receiver's answer must not turn one log record into two")
}
