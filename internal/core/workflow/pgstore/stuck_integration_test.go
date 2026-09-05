//go:build integration

// The listing of half-done executions, proven against a real PostgreSQL.
//
// None of it can be shown without a server: the two-class WHERE, the LEFT JOIN
// fold across several executions, the LIMIT+1 truncation probe and the
// staleness cutoff taken from the database clock are all statements about what
// PostgreSQL does with these rows.
//
// The file is INSIDE the package because the query text and the fold are
// unexported and because the read-only claim is checked by snapshotting the
// tables around a call.
package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// testLease stands in for the lease a workflow declares.
//
// The real checkout saga declares ten minutes (checkout.ExecutionLease); core
// cannot import that package, and the number does not matter here — what
// matters is that the cutoff is a caller's value and that the boundary falls
// where the caller put it.
const testLease = 10 * time.Minute

// reserveStepOutput is the shape the checkout saga actually writes.
//
// It is copied from the reservationRef struct in internal/workflows/checkout so
// that the assertion below reads what an operator would read: the reservation
// ids and the locations that hold the stock. Core does not import the workflow
// tree, so the shape travels as JSON, which is exactly how it travels in
// production.
const reserveStepOutput = `{"reservations":[` +
	`{"line_item_id":"li_1","reservation_id":"resv_A","location_id":"loc_ist"},` +
	`{"line_item_id":"li_2","reservation_id":"resv_B","location_id":"loc_ank"}]}`

// stuckReader builds the listing reader over the shared test pool.
func stuckReader() *Reader { return NewReader(testPool) }

// stuckWorkflowName derives a workflow name unique to the calling test.
//
// Every test in this file shares one database, and the listing's widest form
// reads across all workflows; without a per-test name one test's fixture would
// show up in another's page and the assertions would pass or fail depending on
// the order the suite happened to run in.
func stuckWorkflowName(t *testing.T) string {
	t.Helper()

	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	if len(name) > maxNameLen {
		name = name[:maxNameLen]
	}

	return name
}

// seedExecution writes one execution with its steps and returns its id.
//
// The status is written through UpdateStatus rather than Create so the record
// goes through the same path production takes; a record forced straight into a
// terminal state would not prove the listing sees what the engine writes.
func seedExecution(
	ctx context.Context,
	t *testing.T,
	name, key string,
	status workflow.Status,
	steps []workflow.StepRecord,
) string {
	t.Helper()

	writer := New(testPool, nil)
	exec := &workflow.Execution{
		Workflow:       name,
		IdempotencyKey: key,
		Input:          json.RawMessage(`{"cart_id":"cart_` + key + `"}`),
	}
	require.NoError(t, writer.Create(ctx, exec))

	for i := range steps {
		require.NoError(t, writer.AppendStep(ctx, exec.ID, steps[i]))
	}
	if status != workflow.StatusRunning {
		require.NoError(t, writer.UpdateStatus(ctx, exec.ID, status, nil, "test"))
	}

	return exec.ID
}

// backdate moves an execution's updated_at into the past.
//
// Every write path stamps updated_at with now(), which is the correct
// production behavior and makes "untouched for ten minutes" impossible to
// produce by waiting. The test therefore edits the column directly; that is a
// statement about the FIXTURE, not about the code under test, which never
// writes.
func backdate(ctx context.Context, t *testing.T, id string, age time.Duration) {
	t.Helper()

	tag, err := testPool.Pool().Exec(ctx,
		`UPDATE workflow_executions SET updated_at = now() - $2::interval WHERE id = $1`,
		id, age.String())
	require.NoError(t, err)
	require.EqualValues(t, 1, tag.RowsAffected(), "fixture did not backdate %s", id)
}

// step builds a step record with both timestamps set.
func step(index int, name string, status workflow.StepStatus, output string) workflow.StepRecord {
	now := time.Now().UTC()
	rec := workflow.StepRecord{
		Name: name, Index: index, Status: status,
		Attempts: 1, StartedAt: now, EndedAt: now,
	}
	if output != "" {
		rec.Output = json.RawMessage(output)
	}

	return rec
}

// ids returns the execution ids of a page, in page order.
func ids(page StuckPage) []string {
	out := make([]string, len(page.Executions))
	for i, exec := range page.Executions {
		out[i] = exec.ID
	}

	return out
}

// TestStuckListsBothClassesAndNothingElse is the measurement the whole surface
// exists for.
//
// It builds the six records an incident actually produces and asserts which of
// them a human has to look at. The two that must be listed are NOT the same
// kind of problem:
//
//   - compensation_failed: the engine already closed it and logged ERROR. It is
//     findable today, in psql, if you know the status string.
//   - running-and-stale-with-held-work: NOTHING has noticed it. The move to
//     compensation_failed only happens when someone retries the same
//     idempotency key, and a shopper who never comes back never triggers it.
//     This record holds stock forever and appears in no log line at all.
//
// The second class is why the listing is not "WHERE status =
// 'compensation_failed'", and the test measures that difference directly.
func TestStuckListsBothClassesAndNothingElse(t *testing.T) {
	ctx := context.Background()
	name := stuckWorkflowName(t)

	closed := seedExecution(ctx, t, name, "closed", workflow.StatusCompensationFailed, []workflow.StepRecord{
		step(0, "reserve_inventory", workflow.StepCompensationFailed, reserveStepOutput),
		step(1, "create_order", workflow.StepFailed, ""),
	})
	unnoticed := seedExecution(ctx, t, name, "unnoticed", workflow.StatusRunning, []workflow.StepRecord{
		step(0, "reserve_inventory", workflow.StepInvoked, reserveStepOutput),
	})
	nothingHeld := seedExecution(ctx, t, name, "nothing-held", workflow.StatusRunning, []workflow.StepRecord{
		step(0, "reserve_inventory", workflow.StepFailed, ""),
	})
	live := seedExecution(ctx, t, name, "live", workflow.StatusRunning, []workflow.StepRecord{
		step(0, "reserve_inventory", workflow.StepInvoked, reserveStepOutput),
	})
	done := seedExecution(ctx, t, name, "done", workflow.StatusCompleted, []workflow.StepRecord{
		step(0, "reserve_inventory", workflow.StepInvoked, reserveStepOutput),
	})
	rolledBack := seedExecution(ctx, t, name, "rolled-back", workflow.StatusFailed, []workflow.StepRecord{
		step(0, "reserve_inventory", workflow.StepCompensated, reserveStepOutput),
	})

	// The two listed records are made old, and made old in a KNOWN order: the
	// page must put the oldest reservation first.
	backdate(ctx, t, unnoticed, 3*time.Hour)
	backdate(ctx, t, closed, 2*time.Hour)
	backdate(ctx, t, nothingHeld, 3*time.Hour)

	page, err := stuckReader().Stuck(ctx, StuckFilter{Workflow: name, StaleAfter: testLease, Limit: 10})
	require.NoError(t, err)

	assert.Equal(t, []string{unnoticed, closed}, ids(page),
		"expected exactly the two records that need a human, oldest first")
	assert.False(t, page.Truncated)
	for _, unwanted := range []string{nothingHeld, live, done, rolledBack} {
		assert.NotContains(t, ids(page), unwanted)
	}

	// What is HELD has to be readable, or the listing answers only half the
	// question. The reservation ids live in the step output and the saga keeps
	// them there after compensation precisely for this moment.
	held := 0
	for _, exec := range page.Executions {
		for _, rec := range exec.Steps {
			if !rec.Status.Held() {
				continue
			}
			held++
			assert.JSONEq(t, reserveStepOutput, string(rec.Output),
				"%s step %d: the reserved stock is not readable from the record",
				exec.ID, rec.Index)
		}
	}
	assert.Equal(t, 2, held, "expected one held step in each listed execution")

	// The cart is answerable too: it is in the execution input, not in any
	// column, which is the other thing an operator has to know today.
	assert.JSONEq(t, `{"cart_id":"cart_unnoticed"}`, string(page.Executions[0].Input))

	// The measurement: what today's psql one-liner would have returned.
	var naive int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM workflow_executions WHERE workflow = $1 AND status = 'compensation_failed'`,
		name).Scan(&naive))
	assert.Equal(t, 1, naive)
	assert.Len(t, page.Executions, 2,
		"the status-only query finds %d of %d; the missing one is the class nothing "+
			"else reports", naive, len(page.Executions))
}

// TestStuckSeesEveryHeldStatusOnARunningRecord closes a gap a mutation found.
//
// The held filter is a LIST, and the fixture above only ever exercises the
// 'invoked' entry: dropping 'compensation_failed' from
// [workflow.HeldStepStatuses] left every other test in this file green. The
// state it would have hidden is a real one and it is the worst one — the saga
// started to compensate, a Compensate FAILED (its step is written as
// compensation_failed) and the process died before the terminal status could be
// written. The record stays 'running' with a side effect that was neither
// undone nor reported.
//
// So the test drives one running record per held status and requires each to be
// listed on its own.
//
// The statuses are written out LITERALLY and not read from
// [workflow.HeldStepStatuses]. That is not carelessness, it is the point:
// ranging over the list under test makes the test go quiet together with it —
// measured, the shortened list left this very test green because it had also
// shortened the loop. A status added later is caught by the source-parsing
// check in the engine's own package, not here.
func TestStuckSeesEveryHeldStatusOnARunningRecord(t *testing.T) {
	ctx := context.Background()

	for _, status := range []workflow.StepStatus{
		workflow.StepInvoked,
		workflow.StepCompensationFailed,
	} {
		t.Run(string(status), func(t *testing.T) {
			name := stuckWorkflowName(t)
			id := seedExecution(ctx, t, name, "held-"+string(status), workflow.StatusRunning,
				[]workflow.StepRecord{step(0, "reserve_inventory", status, reserveStepOutput)})
			backdate(ctx, t, id, time.Hour)

			page, err := stuckReader().Stuck(ctx,
				StuckFilter{Workflow: name, StaleAfter: testLease, Limit: 10})
			require.NoError(t, err)
			assert.Equal(t, []string{id}, ids(page),
				"a running record whose only held step is %q was not listed; its side "+
					"effect is in the world and nothing else reports it", status)
		})
	}
}

// TestStuckStaleAfterDecidesTheBoundary proves the knob reaches the query.
//
// The cutoff is asserted by BEHAVIOR at both sides of the boundary, not by
// comparing a constant to another constant: a record one minute short of the
// cutoff must stay out and a record one minute past it must come in. A cutoff
// that never reached the SQL would put both on the same side and an equality
// assertion would not have noticed.
func TestStuckStaleAfterDecidesTheBoundary(t *testing.T) {
	ctx := context.Background()
	name := stuckWorkflowName(t)

	young := seedExecution(ctx, t, name, "young", workflow.StatusRunning, []workflow.StepRecord{
		step(0, "reserve_inventory", workflow.StepInvoked, reserveStepOutput),
	})
	old := seedExecution(ctx, t, name, "old", workflow.StatusRunning, []workflow.StepRecord{
		step(0, "reserve_inventory", workflow.StepInvoked, reserveStepOutput),
	})
	backdate(ctx, t, young, testLease-time.Minute)
	backdate(ctx, t, old, testLease+time.Minute)

	page, err := stuckReader().Stuck(ctx, StuckFilter{Workflow: name, StaleAfter: testLease, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []string{old}, ids(page))
	assert.WithinDuration(t, time.Now().UTC().Add(-testLease), page.StaleBefore, time.Minute,
		"the reported cutoff must be the one that was applied; an operator reads it to "+
			"decide whether a missing record was simply too young")

	// The reported cutoff and the SELECTING cutoff are the same value, and the
	// assertion above cannot see that on its own: in a test the caller and the
	// database share one host, so a build that filtered on one clock and
	// reported the other would still land inside a minute. What IS observable is
	// the relation the header claims — every listed running record must actually
	// sit on the old side of the printed instant.
	for _, exec := range page.Executions {
		if exec.Status != workflow.StatusRunning {
			continue
		}
		assert.True(t, exec.UpdatedAt.Before(page.StaleBefore),
			"%s is listed as abandoned but was updated at %s, which is NOT before the "+
				"cutoff the header reports (%s); the instant that chose the rows and the "+
				"instant printed with them have come apart",
			exec.ID, exec.UpdatedAt.Format(time.RFC3339Nano), page.StaleBefore.Format(time.RFC3339Nano))
	}

	// Widening the cutoff must pull the young record in. If the value never
	// reached the query, this second call would return the same page.
	wider, err := stuckReader().Stuck(ctx, StuckFilter{
		Workflow: name, StaleAfter: testLease - 2*time.Minute, Limit: 10,
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{old, young}, ids(wider))
}

// TestStuckLimitBoundsThePageAndSaysSo checks the cap by behavior.
//
// Both halves matter. A limit that did not bound would read an incident-sized
// table; a limit that bounded SILENTLY would tell an operator the incident is
// smaller than it is, which is the failure mode this repository calls a silent
// cap. The truncation flag is the operator-visible half.
func TestStuckLimitBoundsThePageAndSaysSo(t *testing.T) {
	ctx := context.Background()
	name := stuckWorkflowName(t)

	// FOUR records against a limit of two, and the count is load-bearing. The
	// query fetches limit+1 rows to detect truncation, so with three records
	// and a limit of two the inner LIMIT never actually drops anything and the
	// inner ORDER BY stops mattering — measured: reversing it left this test
	// green. Four records make the inner LIMIT discard one, which is the only
	// arrangement in which the page's ordering can be wrong.
	const seeded = 4
	for i := range seeded {
		id := seedExecution(ctx, t, name, fmt.Sprintf("record-%d", i), workflow.StatusCompensationFailed,
			[]workflow.StepRecord{step(0, "reserve_inventory", workflow.StepCompensationFailed, reserveStepOutput)})
		backdate(ctx, t, id, time.Duration(seeded-i)*time.Hour)
	}

	limited, err := stuckReader().Stuck(ctx, StuckFilter{Workflow: name, StaleAfter: testLease, Limit: 2})
	require.NoError(t, err)
	assert.Len(t, limited.Executions, 2)
	assert.True(t, limited.Truncated, "the page is full and there is more; that must be said")
	assert.Equal(t, 2, limited.Limit)

	full, err := stuckReader().Stuck(ctx, StuckFilter{Workflow: name, StaleAfter: testLease, Limit: seeded})
	require.NoError(t, err)
	assert.Len(t, full.Executions, seeded)
	assert.False(t, full.Truncated, "exactly four matched; nothing was cut off")

	// The page must be the OLDEST records, not an arbitrary two: the oldest
	// reservation has been held longest and is the one an operator should reach
	// first. A page chosen from the other end of the ordering would hand them
	// the least urgent rows while claiming the rest were merely cut off.
	assert.Equal(t, ids(full)[:2], ids(limited))
}

// TestStuckBreaksTimestampTiesByID keeps a page reproducible.
//
// Two executions can share an updated_at to the microsecond — a single UPDATE
// touching both, or simply two writes inside one transaction — and updated_at
// alone then leaves the order unspecified. An operator who runs the command
// twice and gets the rows shuffled cannot tell a new record from a reordered
// one, and with a limit in play the SAME records may not even be on both pages.
//
// The ids are supplied out of order on purpose. Generated ids are time-sorted,
// so an insert-ordered result would look sorted whether or not anything sorted
// it, and the check would prove nothing.
func TestStuckBreaksTimestampTiesByID(t *testing.T) {
	ctx := context.Background()
	name := stuckWorkflowName(t)

	writer := New(testPool, nil)
	for _, id := range []string{"wfx_tie_C", "wfx_tie_A", "wfx_tie_B"} {
		exec := &workflow.Execution{ID: id, Workflow: name, IdempotencyKey: id}
		require.NoError(t, writer.Create(ctx, exec))
		require.NoError(t, writer.AppendStep(ctx, id,
			step(0, "reserve_inventory", workflow.StepCompensationFailed, reserveStepOutput)))
		require.NoError(t, writer.UpdateStatus(ctx, id, workflow.StatusCompensationFailed, nil, "test"))
	}

	// ONE statement, so now() is evaluated once and all three land on the exact
	// same instant. Three separate updates would each get their own now().
	tag, err := testPool.Pool().Exec(ctx,
		`UPDATE workflow_executions SET updated_at = now() - interval '1 hour' WHERE workflow = $1`,
		name)
	require.NoError(t, err)
	require.EqualValues(t, 3, tag.RowsAffected())

	page, err := stuckReader().Stuck(ctx, StuckFilter{Workflow: name, StaleAfter: testLease, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []string{"wfx_tie_A", "wfx_tie_B", "wfx_tie_C"}, ids(page),
		"executions sharing an updated_at came back in an order the query does not fix; "+
			"two runs of the command could disagree about which records exist")

	// The display order above is not enough, and a mutation proved it: dropping
	// the tiebreaker from the INNER ordering left it green, because the outer
	// ordering re-sorts whatever arrives. What the inner ordering decides is
	// WHICH executions are on the page at all, so the check has to look at the
	// selection — and that is only visible when the limit actually cuts.
	fetched, _, err := stuckReader().probe(ctx, name,
		StuckFilter{Workflow: name, StaleAfter: testLease, Limit: 1})
	require.NoError(t, err)
	assert.Equal(t, []string{"wfx_tie_A", "wfx_tie_B"}, ids(StuckPage{Executions: fetched}),
		"with three executions on the same instant and room for two, the page must be "+
			"the same two every time; otherwise a second run silently swaps a record in "+
			"and out of the operator's list")
}

// TestStuckLimitIsEnforcedByTheDatabase closes a gap a mutation found.
//
// The page is cut in Go, so replacing the query's LIMIT parameter with a huge
// constant leaves every caller-visible result identical — measured, that
// mutation survived every other test in this file. What it changes is invisible
// from the outside and is the whole point of the bound: the database would hand
// back the entire matching set, which at incident scale is the unbounded read
// the limit exists to prevent.
//
// So the bound is measured where it is applied, by counting what the query
// returns before anything trims it.
func TestStuckLimitIsEnforcedByTheDatabase(t *testing.T) {
	ctx := context.Background()
	name := stuckWorkflowName(t)

	const matching = 6
	for i := range matching {
		id := seedExecution(ctx, t, name, fmt.Sprintf("record-%d", i), workflow.StatusCompensationFailed,
			[]workflow.StepRecord{step(0, "reserve_inventory", workflow.StepCompensationFailed, reserveStepOutput)})
		backdate(ctx, t, id, time.Duration(matching-i)*time.Hour)
	}

	filter := StuckFilter{Workflow: name, StaleAfter: testLease, Limit: 2}

	fetched, _, err := stuckReader().probe(ctx, name, filter)
	require.NoError(t, err)
	assert.Len(t, fetched, filter.Limit+1,
		"the query returned %d executions for a limit of %d. It must return exactly one "+
			"more than asked — one is the truncation probe, and anything beyond that is "+
			"the database reading rows nobody will look at.", len(fetched), filter.Limit)
}

// TestStuckRejectsFiltersThatWouldMislead proves the two bounds are required.
//
// A zero StaleAfter is the dangerous one: it would list sagas that are still
// running, and an operator who releases their stock by hand double-frees
// inventory the live compensation is about to release. Defaulting it here was
// rejected because the safe value is the workflow's lease and this package does
// not know which workflow is being listed.
func TestStuckRejectsFiltersThatWouldMislead(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		label  string
		filter StuckFilter
	}{
		{"stale-after zero", StuckFilter{StaleAfter: 0, Limit: 10}},
		{"stale-after negative", StuckFilter{StaleAfter: -time.Minute, Limit: 10}},
		{"limit zero", StuckFilter{StaleAfter: testLease, Limit: 0}},
		{"limit negative", StuckFilter{StaleAfter: testLease, Limit: -1}},
		{"blank workflow", StuckFilter{Workflow: "   ", StaleAfter: testLease, Limit: 10}},
	} {
		t.Run(tc.label, func(t *testing.T) {
			_, err := stuckReader().Stuck(ctx, tc.filter)
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}
}

// TestStuckWritesNothing is the promise the whole design rests on.
//
// The listing exists because releasing a reservation by hand is dangerous while
// a saga may still be running; a listing that quietly touched a row — an
// updated_at bumped, an idempotency key dropped — would put the caller back in
// the danger it was built to avoid. The check is a digest of both tables taken
// around a call that returns rows, so it fails if ANY column of ANY row moved.
func TestStuckWritesNothing(t *testing.T) {
	ctx := context.Background()
	name := stuckWorkflowName(t)

	id := seedExecution(ctx, t, name, "untouched", workflow.StatusCompensationFailed,
		[]workflow.StepRecord{step(0, "reserve_inventory", workflow.StepCompensationFailed, reserveStepOutput)})
	backdate(ctx, t, id, time.Hour)

	before := tableDigest(ctx, t)

	page, err := stuckReader().Stuck(ctx, StuckFilter{Workflow: name, StaleAfter: testLease, Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Executions, 1, "the digest would be meaningless over an empty read")

	assert.Equal(t, before, tableDigest(ctx, t),
		"the listing changed a row. Reading is safe at any moment; writing is not, and "+
			"this surface must never have to decide which moment it is")
}

// tableDigest is a content hash of both execution tables.
//
// Every column is included: a check that only compared statuses would miss an
// updated_at bump, and an updated_at bump is precisely what would make the
// engine treat a live saga as abandoned.
func tableDigest(ctx context.Context, t *testing.T) string {
	t.Helper()

	var digest string
	require.NoError(t, testPool.Pool().QueryRow(ctx, `
SELECT md5(string_agg(line, '|' ORDER BY line))
FROM (
	SELECT e::text AS line FROM workflow_executions e
	UNION ALL
	SELECT s::text FROM workflow_execution_steps s
) AS both_tables`).Scan(&digest))

	return digest
}

// TestStuckWorkflowFilterNarrowsTheListing keeps the name filter honest.
//
// An installation runs several workflows and an incident is usually about one
// of them. A filter that was accepted and then ignored would show the operator
// a page full of records from flows they did not ask about, and the record they
// came for would be somewhere in it.
func TestStuckWorkflowFilterNarrowsTheListing(t *testing.T) {
	ctx := context.Background()
	mine := stuckWorkflowName(t)
	other := mine + "_other"

	kept := seedExecution(ctx, t, mine, "mine", workflow.StatusCompensationFailed,
		[]workflow.StepRecord{step(0, "reserve_inventory", workflow.StepCompensationFailed, reserveStepOutput)})
	dropped := seedExecution(ctx, t, other, "other", workflow.StatusCompensationFailed,
		[]workflow.StepRecord{step(0, "reserve_inventory", workflow.StepCompensationFailed, reserveStepOutput)})

	page, err := stuckReader().Stuck(ctx, StuckFilter{Workflow: mine, StaleAfter: testLease, Limit: 50})
	require.NoError(t, err)
	assert.Equal(t, []string{kept}, ids(page))

	// An empty name means every workflow, and that is the useful default during
	// an incident: the operator does not yet know which flow broke.
	all, err := stuckReader().Stuck(ctx, StuckFilter{StaleAfter: testLease, Limit: 500})
	require.NoError(t, err)
	assert.Contains(t, ids(all), kept)
	assert.Contains(t, ids(all), dropped)
}

// TestStuckFoldsStepsPerExecution guards the boundary between two executions.
//
// The join returns one row per step, so the fold decides where one execution
// ends and the next begins. Getting that wrong would hand an operator another
// cart's reservation ids under this cart's identity — and the page would still
// look plausible.
func TestStuckFoldsStepsPerExecution(t *testing.T) {
	ctx := context.Background()
	name := stuckWorkflowName(t)

	first := seedExecution(ctx, t, name, "first", workflow.StatusCompensationFailed, []workflow.StepRecord{
		step(0, "reserve_inventory", workflow.StepCompensationFailed, `{"reservations":["resv_1"]}`),
		step(1, "create_order", workflow.StepFailed, ""),
		step(2, "authorize_payment", workflow.StepInvoked, `{"payment_id":"pay_1"}`),
	})
	second := seedExecution(ctx, t, name, "second", workflow.StatusCompensationFailed, []workflow.StepRecord{
		step(0, "reserve_inventory", workflow.StepCompensationFailed, `{"reservations":["resv_2"]}`),
	})
	backdate(ctx, t, first, 2*time.Hour)
	backdate(ctx, t, second, time.Hour)

	page, err := stuckReader().Stuck(ctx, StuckFilter{Workflow: name, StaleAfter: testLease, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{first, second}, ids(page))

	require.Len(t, page.Executions[0].Steps, 3)
	require.Len(t, page.Executions[1].Steps, 1)
	for i, rec := range page.Executions[0].Steps {
		assert.Equal(t, i, rec.Index, "steps must arrive in index order")
	}
	assert.JSONEq(t, `{"reservations":["resv_1"]}`, string(page.Executions[0].Steps[0].Output))
	assert.JSONEq(t, `{"reservations":["resv_2"]}`, string(page.Executions[1].Steps[0].Output))
}

// TestStuckListsAClosedRecordWithNothingHeld draws the asymmetry.
//
// compensation_failed is listed unconditionally, even with no held step left,
// because the status IS the engine's statement that a human is needed. The
// running class is not: a stale running record with nothing held holds no
// inventory and the engine repairs it itself on the next attempt, so listing it
// would fill an incident page with rows that need no action.
func TestStuckListsAClosedRecordWithNothingHeld(t *testing.T) {
	ctx := context.Background()
	name := stuckWorkflowName(t)

	closed := seedExecution(ctx, t, name, "closed-empty", workflow.StatusCompensationFailed,
		[]workflow.StepRecord{step(0, "reserve_inventory", workflow.StepCompensated, reserveStepOutput)})
	backdate(ctx, t, closed, time.Hour)

	page, err := stuckReader().Stuck(ctx, StuckFilter{Workflow: name, StaleAfter: testLease, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []string{closed}, ids(page))
}

// TestStuckReportsAMissingPool keeps the failure typed.
//
// A reader wired without a pool must not panic on the first call during an
// incident; it must say what is missing, in the same error class the store
// uses.
func TestStuckReportsAMissingPool(t *testing.T) {
	_, err := NewReader(nil).Stuck(context.Background(),
		StuckFilter{StaleAfter: testLease, Limit: 10})
	require.Error(t, err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err))
	assert.Equal(t, CodeUnavailable, errors.CodeOf(err))
}
