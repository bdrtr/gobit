package app

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/workflow"
	"github.com/bdrtr/gobit/internal/core/workflow/pgstore"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// recordingLister is a [stuckLister] that answers with a canned page and keeps
// the filter it was given.
//
// Keeping the filter is the point: it is the only way to prove a flag reached
// the query instead of being parsed, printed and dropped — the failure this
// repository has paid for twice, with the pool's MaxConns and the idempotency
// budget.
type recordingLister struct {
	page pgstore.StuckPage
	got  pgstore.StuckFilter
}

func (l *recordingLister) Stuck(_ context.Context, filter pgstore.StuckFilter) (pgstore.StuckPage, error) {
	l.got = filter
	l.page.Limit = filter.Limit

	return l.page, nil
}

// stuckFixture builds a page holding the two classes the listing exists for.
func stuckFixture(now time.Time) pgstore.StuckPage {
	reservations := json.RawMessage(
		`{"reservations":[{"line_item_id":"li_1","reservation_id":"resv_A","location_id":"loc_ist"}]}`)

	return pgstore.StuckPage{
		StaleBefore: now.Add(-10 * time.Minute),
		Executions: []*workflow.Execution{
			{
				ID: "wfx_ABANDONED", Workflow: "complete_cart",
				Status:         workflow.StatusRunning,
				IdempotencyKey: "checkout:cart_1",
				Input:          json.RawMessage(`{"cart_id":"cart_1"}`),
				UpdatedAt:      now.Add(-3 * time.Hour),
				Steps: []workflow.StepRecord{
					{Index: 0, Name: "reserve_inventory", Status: workflow.StepInvoked, Output: reservations},
				},
			},
			{
				ID: "wfx_CLOSED", Workflow: "complete_cart",
				Status:         workflow.StatusCompensationFailed,
				IdempotencyKey: "checkout:cart_2",
				Input:          json.RawMessage(`{"cart_id":"cart_2"}`),
				Failure:        "stock could not be released",
				UpdatedAt:      now.Add(-2 * time.Hour),
				Steps: []workflow.StepRecord{
					{Index: 0, Name: "reserve_inventory", Status: workflow.StepCompensated, Output: reservations},
					{Index: 1, Name: "create_order", Status: workflow.StepCompensationFailed,
						Output: json.RawMessage(`{"order_id":"ord_9"}`)},
				},
			},
		},
	}
}

// TestStuckOutputAnswersBothOperatorQuestions checks the page an operator
// actually reads.
//
// README's known limit names two questions with no surface: which executions
// are stuck, and what is still held. The second one is the harder half — the
// reservation ids live inside a step's JSON output, and only for the steps
// whose side effect was never undone.
func TestStuckOutputAnswersBothOperatorQuestions(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	page := stuckFixture(now)
	page.Limit = 50

	out := &strings.Builder{}
	require.NoError(t, writeStuck(out, page, pgstore.StuckFilter{
		StaleAfter: 10 * time.Minute, Limit: 50,
	}, now))
	text := out.String()

	assert.Contains(t, text, "READ ONLY", "the promise the design rests on must be on the page")
	assert.Contains(t, text, "wfx_ABANDONED")
	assert.Contains(t, text, "wfx_CLOSED")
	assert.Contains(t, text, "cart_1", "the cart is in the input and nowhere else")
	assert.Contains(t, text, "idle=3h0m0s")
	assert.Contains(t, text, "2 execution(s), 2 held step(s).")

	// The abandoned record must SAY it is abandoned. It reached the page for a
	// different reason than the closed one and needs a different reaction: the
	// engine closed the second and logged ERROR, while nothing at all has
	// reported the first.
	assert.Contains(t, text, "running (abandoned; nothing has reported this one)")

	// Held is a per-STEP fact, not a per-execution one. The closed execution
	// has a compensated step whose output still names a reservation that was
	// already released; marking it held would send an operator to free stock
	// that is not held.
	assert.Contains(t, text, "HELD step 0 reserve_inventory (invoked)")
	assert.Contains(t, text, "HELD step 1 create_order (compensation_failed)")
	assert.NotContains(t, text, "HELD step 0 reserve_inventory (compensated)")

	// A held step's output is printed; a released step's is not, because it
	// would read as a list of things to go undo.
	assert.Contains(t, text, `"reservation_id":"resv_A"`)
	assert.Contains(t, text, `{"order_id":"ord_9"}`)
	assert.Equal(t, 1, strings.Count(text, `"reservation_id":"resv_A"`),
		"the released copy of the same output was printed too")
}

// TestStuckOutputRefusesToUnderstateTheIncident is the no-silent-cap check.
//
// A page that stopped at its limit and did not say so would tell an operator
// the incident is smaller than it is, and the operator would go release the
// reservations they were shown and believe they were done.
func TestStuckOutputRefusesToUnderstateTheIncident(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	filter := pgstore.StuckFilter{StaleAfter: time.Minute, Limit: 2}

	full := stuckFixture(now)
	full.Limit = 2
	full.Truncated = true
	out := &strings.Builder{}
	require.NoError(t, writeStuck(out, full, filter, now))
	assert.Contains(t, out.String(), "THE LIST IS INCOMPLETE")
	assert.Contains(t, out.String(), "-limit=2")

	complete := stuckFixture(now)
	complete.Limit = 2
	quiet := &strings.Builder{}
	require.NoError(t, writeStuck(quiet, complete, filter, now))
	assert.NotContains(t, quiet.String(), "THE LIST IS INCOMPLETE",
		"nothing was cut off; claiming otherwise would send an operator hunting for "+
			"records that do not exist")
}

// TestStuckOutputStatesItsBoundsEvenWhenEmpty keeps the quiet answer honest.
//
// "Nothing is stuck" and "nothing is stuck THAT I LOOKED FOR" are different
// answers, and an operator during an incident has to be able to tell them
// apart: a stale-after longer than the outage hides every record.
func TestStuckOutputStatesItsBoundsEvenWhenEmpty(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	filter := pgstore.StuckFilter{Workflow: "", StaleAfter: 90 * time.Minute, Limit: 7}
	page := pgstore.StuckPage{Limit: 7, StaleBefore: now.Add(-90 * time.Minute)}

	out := &strings.Builder{}
	require.NoError(t, writeStuck(out, page, filter, now))
	text := out.String()

	assert.Contains(t, text, "0 execution(s), 0 held step(s).")
	assert.Contains(t, text, "stale-after=1h30m0s")
	assert.Contains(t, text, "limit=7")
	assert.Contains(t, text, "workflow=<all>",
		"an empty filter printed as nothing reads like a failed lookup")
	assert.Contains(t, text, "2026-09-03T10:30:00Z",
		"the cutoff that was applied has to be readable; it decides whether a missing "+
			"record was simply too young")
}

// TestStuckFlagsReachTheQuery proves the knobs are not decoration.
//
// The filter is read back from the place it LANDS — the lister that runs the
// query — and not from the line that prints it. Both of this repository's
// last-round failures of this class looked correct in the log and had never
// reached the thing they configured.
func TestStuckFlagsReachTheQuery(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

	filter, err := parseStuckFlags([]string{
		"-workflow", "complete_cart", "-stale-after", "45m", "-limit", "3",
	})
	require.NoError(t, err)

	lister := &recordingLister{page: pgstore.StuckPage{StaleBefore: now.Add(-45 * time.Minute)}}
	out := &strings.Builder{}
	require.NoError(t, listStuck(context.Background(), lister, out, filter, now))

	assert.Equal(t, "complete_cart", lister.got.Workflow)
	assert.Equal(t, 45*time.Minute, lister.got.StaleAfter)
	assert.Equal(t, 3, lister.got.Limit)
	assert.Contains(t, out.String(), "workflow=complete_cart  stale-after=45m0s  limit=3",
		"the header must echo the values that reached the query, not a second copy")
}

// TestStuckDefaultCutoffCannotNameALiveSaga is the safety property of the
// default, checked as an inequality rather than a copy of a constant.
//
// A cutoff shorter than the workflow's own lease lists sagas that are still
// running. An operator who then releases their stock by hand double-frees
// inventory the live compensation is about to release — the failure
// [workflow.WithLease] spends its whole godoc avoiding. The default therefore
// has to be at LEAST the lease; equality is not the requirement, safety is.
func TestStuckDefaultCutoffCannotNameALiveSaga(t *testing.T) {
	t.Parallel()

	filter, err := parseStuckFlags(nil)
	require.NoError(t, err)

	assert.GreaterOrEqual(t, filter.StaleAfter, checkoutwf.ExecutionLease,
		"the default cutoff (%s) is shorter than the checkout saga's lease (%s); the "+
			"listing would name executions that are still running",
		filter.StaleAfter, checkoutwf.ExecutionLease)
	assert.Positive(t, filter.Limit,
		"a non-positive default would be rejected by the store and the command would "+
			"never print anything at all")
	assert.Empty(t, filter.Workflow, "with no -workflow the listing must be the widest one")
}

// TestStuckRejectsAPositionalArgument keeps a typo from widening the listing.
//
// "gobit stuck complete_cart" is the shape an operator reaches for; accepting
// and ignoring it would print every workflow while the operator believed they
// had narrowed to one.
func TestStuckRejectsAPositionalArgument(t *testing.T) {
	t.Parallel()

	_, err := parseStuckFlags([]string{"complete_cart"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "complete_cart")
}

// TestStuckHelpIsNotAFailure keeps `-h` from looking like a crash.
//
// The flag package answers help with an error, and passing it on would print
// "fatal: flag: help requested" and exit non-zero — under an operator's first
// attempt to find out what the command does, during an incident. It also proves
// help costs nothing: the run returns before any configuration is read, so it
// works on a machine that cannot reach the database at all.
func TestStuckHelpIsNotAFailure(t *testing.T) {
	t.Parallel()

	out := &strings.Builder{}
	require.NoError(t, runStuck([]string{"-h"}, out, Options{}))
	assert.Empty(t, out.String(), "usage belongs on stderr; stdout is the listing")
}

// TestStuckIsRoutedByTheDispatcherAndUnknownVerbsFail covers both branches
// that do not start a server.
//
// The unknown verb must FAIL. Falling through to serve is the silent failure: a
// typo would start a listener, the operator would wait for output that never
// arrives, and in a production container it would raise a second server against
// the live database.
func TestStuckIsRoutedByTheDispatcherAndUnknownVerbsFail(t *testing.T) {
	t.Parallel()

	// A flag only the stuck flag set knows about proves the routing without
	// opening a database: the parse fails before any configuration is read.
	err := Main([]string{stuckCommand, "-not-a-flag"}, io.Discard, Options{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not-a-flag")

	err = Main([]string{"stcuk"}, io.Discard, Options{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stcuk")
}

// TestTheBinaryDispatchesRatherThanServingDirectly is the wiring invariant.
//
// A subcommand main never reaches is this repository's most expensive bug class
// — the capability that compiles, passes its own tests and exists in no
// installation. It cannot be caught by running main (that starts a server), so
// the rule is stated where it can be read: main's body reaches the entry point.
//
// Since ADR 0027 the invariant spans two packages. main lives in cmd/server and
// is fifteen lines; what it calls is the published facade, whose Main hands over
// to the dispatcher in THIS package. So the check reads the binary's source, not
// this package's — and it reads it from here, next to the subcommands whose
// deadness it is guarding against.
func TestTheBinaryDispatchesRatherThanServingDirectly(t *testing.T) {
	t.Parallel()

	fset := gotoken.NewFileSet()
	found, called := false, false

	entries, err := os.ReadDir(binaryDir)
	require.NoError(t, err, "%s could not be read", binaryDir)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, filepath.Join(binaryDir, entry.Name()), nil,
			parser.SkipObjectResolution)
		require.NoError(t, parseErr)

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "main" || fn.Recv != nil {
				continue
			}
			found = true
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, isCall := n.(*ast.CallExpr)
				if !isCall {
					return true
				}
				if sel, isSel := call.Fun.(*ast.SelectorExpr); isSel && sel.Sel.Name == entryPointName {
					called = true
				}

				return true
			})
		}
	}

	require.True(t, found,
		"no main function was found in %s; this check is BLIND.\n"+
			"The binary moved or was renamed: point binaryDir at it, because a dispatch "+
			"nobody reads is a dispatch nobody checks.", binaryDir)
	assert.True(t, called,
		"main does not reach the facade's %s.\n"+
			"Every subcommand would then be dead code: it would compile, its own tests "+
			"would pass, and no installation would have it.", entryPointName)
}

// binaryDir is where the shipped binary's main lives, relative to this package.
const binaryDir = "../../cmd/server"
