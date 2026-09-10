//go:build integration

// The tests in this file need a real PostgreSQL instance (and therefore
// Docker); they are separated behind the `integration` tag so that `make test`
// stays fast. To run them: make test-integration
//
// # What only a real store can witness here
//
// The unit tests in complete_cart_test.go drive the saga FORWARD with an
// in-process engine: every step's Invoke runs, and the state the compensations
// read is the live map one Invoke handed to the next. Restore is never called
// on that path, because nothing was ever lost.
//
// What is exercised here is the other path: the process DIED. The map is gone
// and the only thing left is what Postgres holds — a running execution row, a
// handful of step rows with their JSON output, and an updated_at old enough for
// the engine to call the record abandoned. Every claim below therefore needs a
// record that survived a process, which is exactly what a fake cannot be: the
// step output has to make the round trip through JSONB, the lease has to be a
// real timestamp the store compares against, and the claim that closes the
// record has to be the store's own conditional UPDATE.
//
// This is the crash-recovery path of the money-and-stock saga. If a Restore
// mis-reads its own record, recovery either releases nothing (stock reserved
// forever, invisible to everyone) or — at the capture step — decides the card
// was never charged and rolls back a paid order, charging the customer a second
// time when they retry.
//
// # Why there is no TestMain here
//
// The package already has one (fakes_test.go), and a test binary may hold only
// one. The container is therefore started LAZILY on the first test that needs it
// and torn down through the shutdown hook that TestMain runs afterwards.

package checkout

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
	"github.com/bdrtr/gobit/internal/core/workflow/pgstore"
)

// recoveryPostgresImage is the image the recovery tests run against.
const recoveryPostgresImage = "postgres:16-alpine"

var (
	// recoveryDBOnce guards the one-time container startup.
	recoveryDBOnce sync.Once
	// recoveryPool is the pool every recovery test shares.
	recoveryPool *db.Pool
	// recoveryDBErr is the startup failure, reported to every test that asks.
	recoveryDBErr error
)

// recoveryStore brings up the shared Postgres and returns a workflow store on
// it.
//
// The startup happens at most once per test binary; the teardown is handed to
// the package's TestMain (see the file comment).
func recoveryStore(t *testing.T) workflow.Store {
	t.Helper()

	recoveryDBOnce.Do(func() { recoveryPool, recoveryDBErr = startRecoveryPostgres() })
	require.NoError(t, recoveryDBErr, "the recovery tests need a real database")

	return pgstore.New(recoveryPool, slog.New(slog.DiscardHandler))
}

// startRecoveryPostgres opens the container, applies the workflow schema and
// returns the pool.
func startRecoveryPostgres() (*db.Pool, error) {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, recoveryPostgresImage,
		tcpostgres.WithDatabase("gobit_test"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, fmt.Errorf("the postgres container could not be started: %w", err)
	}
	integrationShutdown = func() { _ = testcontainers.TerminateContainer(ctr) }

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, fmt.Errorf("the connection address could not be obtained: %w", err)
	}

	pool, err := db.New(ctx, db.DefaultConfig(dsn), nil)
	if err != nil {
		return nil, fmt.Errorf("the connection pool could not be opened: %w", err)
	}

	terminate := integrationShutdown
	integrationShutdown = func() {
		pool.Close()
		terminate()
	}

	if err := db.Migrate(ctx, dsn, pgstore.Migrations(), pgstore.MigrationOwner); err != nil {
		return nil, fmt.Errorf("the workflow schema could not be applied: %w", err)
	}

	return pool, nil
}

// durableHarness is the package's fake-module harness wired to a DURABLE engine.
//
// The fakes are the same ones the unit tests use — this file does not test the
// modules, it tests what the saga does with a record — but the engine is a real
// one over Postgres, because an in-memory engine cannot hold a record that
// outlives the process that wrote it.
func durableHarness(t *testing.T) (*harness, workflow.Store) {
	t.Helper()

	store := recoveryStore(t)
	h := newHarness(t)

	wf, err := New(Deps{
		Carts:       h.carts,
		Totals:      h.totals,
		Inventory:   h.inventory,
		Fulfillment: h.fulfillment,
		Orders:      h.orders,
		Payments:    h.payments,
		Links:       h.links,
		Catalog:     h.catalog,
		Executor:    workflow.New(store, slog.New(slog.DiscardHandler)),
		Logger:      slog.New(slog.DiscardHandler),
	})
	require.NoError(t, err)
	h.wf = wf

	return h, store
}

// recoveryPlan is the saga input an abandoned record carries.
//
// It matches the two-line cart the unit-test fakes describe (see
// defaultSnapshot and defaultTotals), so that a compensation reading the plan
// finds the same world the forward path would have.
func recoveryPlan() *checkoutPlan {
	return &checkoutPlan{
		CartID:            testCartID,
		RegionID:          testRegionID,
		CustomerID:        testCustomerID,
		Email:             "customer@example.com",
		CurrencyCode:      testCurrency,
		Revision:          testRevision,
		LocationID:        testLocationID,
		PaymentProviderID: testProviderID,
		Amount:            testAmount,
		Subtotal:          2500,
		TaxTotal:          500,
		Lines: []planLine{
			{
				LineItemID: testLineA, VariantID: testVariantA, InventoryItemID: testItemA,
				Title: testTitleA, Quantity: 2, UnitPrice: 1000,
				Subtotal: 2000, TaxTotal: 400, Total: 2400,
			},
			{
				LineItemID: testLineB, VariantID: testVariantB, InventoryItemID: testItemB,
				Title: testTitleB, Quantity: 1, UnitPrice: 500,
				Subtotal: 500, TaxTotal: 100, Total: 600,
			},
		},
	}
}

// abandonedCheckout writes a running checkout execution with the given step
// records and winds its lease back.
//
// Winding time back is REQUIRED and cannot be done through the store surface:
// AppendStep stamps the execution's updated_at, so "has step records AND is
// stale" is a state only a crash produces in production. The interval is an
// hour, comfortably past [ExecutionLease].
//
// It returns the execution id and the record's input, which is what the
// recovery path decodes the workflow definition back out of.
func abandonedCheckout(
	ctx context.Context, t *testing.T, store workflow.Store, records ...workflow.StepRecord,
) (string, json.RawMessage) {
	t.Helper()

	input, err := json.Marshal(recoveryPlan())
	require.NoError(t, err)

	exec := &workflow.Execution{
		Workflow: WorkflowName,
		// The key is per-test: the tests share one database and the store
		// enforces (workflow, idempotency_key) uniqueness.
		IdempotencyKey: IdempotencyKeyPrefix + t.Name(),
		Status:         workflow.StatusRunning,
		Input:          input,
	}
	require.NoError(t, store.Create(ctx, exec))

	for i := range records {
		require.NoError(t, store.AppendStep(ctx, exec.ID, records[i]))
	}

	_, err = recoveryPool.Pool().Exec(ctx,
		`UPDATE workflow_executions SET updated_at = now() - interval '1 hour' WHERE id = $1`, exec.ID)
	require.NoError(t, err)

	return exec.ID, input
}

// runRecovery rebuilds the workflow definition from the record and drives the
// engine's recovery over it.
//
// The definition is built by the PRODUCTION path ([Workflows.RecoveryWorkflow]),
// not assembled in the test: recovery compares the record's step names against
// the definition's, so a test that built its own step list would keep passing
// on the day the two drifted apart.
func runRecovery(ctx context.Context, t *testing.T, h *harness, input json.RawMessage, executionID string) error {
	t.Helper()

	wf, err := h.wf.RecoveryWorkflow(input)
	require.NoError(t, err)

	recoverer, ok := h.wf.executor.(workflow.Recoverer)
	require.True(t, ok, "the durable engine must offer the on-demand recovery capability")

	return recoverer.Recover(ctx, wf, executionID, RecoveryOptions()...)
}

// invokedStep builds the record of a step that finished successfully.
//
// The output is marshaled from the step's OWN output type, which is what the
// engine persists on the live path; a hand-written JSON literal would let a
// field rename pass unnoticed on both sides.
func invokedStep(t *testing.T, index int, name string, output any) workflow.StepRecord {
	t.Helper()

	body, err := json.Marshal(output)
	require.NoError(t, err)

	return workflow.StepRecord{
		Name: name, Index: index, Status: workflow.StepInvoked, Attempts: 1, Output: body,
	}
}

// invokedStepRaw builds the record of a successful step whose output is given
// verbatim, for the cases where the point IS the shape of the JSON.
func invokedStepRaw(index int, name, output string) workflow.StepRecord {
	return workflow.StepRecord{
		Name: name, Index: index, Status: workflow.StepInvoked, Attempts: 1,
		Output: json.RawMessage(output),
	}
}

// stepIndex returns the position a step holds in the saga.
//
// The builders below used to carry the position as a LITERAL, and that made a
// step inserted into the saga a silent rewrite of every fixture after it: the
// record would name the right step at the wrong index, and recovery would
// disagree with a definition the test believed it was reproducing. The number is
// read off the production step list instead (ADR 0109 inserted redeem_promotions
// at position one, and every literal below it was wrong that day).
func stepIndex(t *testing.T, name string) int {
	t.Helper()

	var w Workflows
	for i, step := range w.sagaSteps(&checkoutPlan{}) {
		if step.Name() == name {
			return i
		}
	}
	t.Fatalf("the saga has no step called %q", name)

	return -1
}

// reserveRecord is the row the stock step leaves behind: two lines reserved out
// of the declared warehouse.
//
// This and the builders below it are the record of a checkout that got all the
// way through; each test takes the PREFIX its scenario ends at, because the
// prefix is the only thing that distinguishes one crash from another.
func reserveRecord(t *testing.T) workflow.StepRecord {
	t.Helper()

	return invokedStep(t, stepIndex(t, StepReserveInventory), StepReserveInventory, reserveOutput{Reservations: []reservationRef{
		{LineItemID: testLineA, ReservationID: "res_" + testLineA, LocationID: testLocationID},
		{LineItemID: testLineB, ReservationID: "res_" + testLineB, LocationID: testLocationID},
	}})
}

// orderRecord is the row the order step leaves behind: the placed order's id.
func orderRecord(t *testing.T) workflow.StepRecord {
	t.Helper()

	return invokedStep(t, stepIndex(t, StepCreateOrder), StepCreateOrder, createOrderOutput{OrderID: testOrderID})
}

// authorizeRecord is the row the authorization step leaves behind: the payment
// collection and the session the hold sits on.
func authorizeRecord(t *testing.T) workflow.StepRecord {
	t.Helper()

	return invokedStep(t, stepIndex(t, StepAuthorizePayment), StepAuthorizePayment, authorizeOutput{
		CollectionID: testCollectionID, SessionID: testSessionID,
		Status: "authorized", Authorized: testAmount,
	})
}

// captureRecord is the row the PIVOT leaves behind, and the most consequential
// one in the file: its mere existence is what tells recovery the card may have
// been charged.
func captureRecord(t *testing.T) workflow.StepRecord {
	t.Helper()

	return invokedStep(t, stepIndex(t, StepCapturePayment), StepCapturePayment, captureOutput{
		PaymentID: testPaymentID, Captured: testAmount,
	})
}

// clearCartRecord is the row the closing bookkeeping step leaves behind. Its
// content is never read; only its presence matters (see
// TestTheBookkeepingStepDoesNotTurnRecoveryIntoManualWork).
func clearCartRecord(t *testing.T) workflow.StepRecord {
	t.Helper()

	return invokedStep(t, stepIndex(t, StepClearCart), StepClearCart, CompleteCartResult{
		CartID: testCartID, OrderID: testOrderID, PaymentID: testPaymentID,
		CurrencyCode: testCurrency, Amount: testAmount,
	})
}

// redeemRecord is the row the coupon step leaves behind.
//
// It is EMPTY, and that is a real outcome rather than a placeholder: the recovery
// plan carries no promotion, so the step spent nothing and has nothing to give
// back (see [redeemPromotionsStep.Restore]).
func redeemRecord(t *testing.T) workflow.StepRecord {
	t.Helper()

	return invokedStep(t, stepIndex(t, StepRedeemPromotions), StepRedeemPromotions, redeemOutput{})
}

// finalStatus reads the execution's terminal state back out of the database.
func finalStatus(ctx context.Context, t *testing.T, store workflow.Store, executionID string) *workflow.Execution {
	t.Helper()

	exec, err := store.Get(ctx, executionID)
	require.NoError(t, err)

	return exec
}

// assertNothingWasUndone asserts that no compensation touched the world.
//
// The three calls are the saga's only reversible side effects, and every test
// that expects a REFUSAL has the same obligation: a refusal that quietly
// released half the work would be worse than no recovery at all.
func assertNothingWasUndone(t *testing.T, h *harness) {
	t.Helper()

	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineA),
		"a refused recovery must not release stock: the record it refused to trust is the same record that says which stock")
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineB),
		"a refused recovery must not release stock")
	assert.Equal(t, 0, h.rec.count("order:cancel"),
		"a refused recovery must not cancel the order")
	assert.Equal(t, 0, h.rec.count("payment:cancel"),
		"a refused recovery must not touch the customer's card")
}

// TestAbandonedStockIsReleasedFromTheRecordALONE proves the whole point of
// Restore: an execution whose process died releases its stock and cancels its
// order using nothing but what Postgres kept.
//
// This is the ordinary abandonment. The process reserved two lines, placed the
// order, and was killed before it could authorize. Nothing in memory survived —
// not the reservation identifiers, not the order id — and nobody is coming back
// to release that stock: the customer closed the tab. Without a working Restore
// the reservation is invisible forever; the goods sit unsellable and no report
// names them, because from the outside the row looks exactly like a saga that
// is still running.
//
// The record is deliberately cut BEFORE the authorization: with an authorize
// record and no capture record the engine refuses recovery outright (that is
// the claim of TestRecoveryWillNotAssumeAnUnrecordedCaptureNeverRan), so this is
// the longest prefix on which compensation is actually allowed to run.
func TestAbandonedStockIsReleasedFromTheRecordALONE(t *testing.T) {
	ctx := context.Background()
	h, store := durableHarness(t)

	executionID, input := abandonedCheckout(ctx, t, store, reserveRecord(t), redeemRecord(t), orderRecord(t))

	require.NoError(t, runRecovery(ctx, t, h, input, executionID))

	// reserveInventoryStep.Restore put the reservation trail back: both lines,
	// by their real identifiers.
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA),
		"the reservation of the first line was rebuilt from the record and released")
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineB),
		"the reservation of the second line was rebuilt from the record and released")

	// createOrderStep.Restore put the order id back; without it the order would
	// stay 'pending' with nothing behind it.
	assert.Equal(t, []string{testOrderID}, h.orders.canceled,
		"the abandoned order must be canceled, and by the id the record carries")

	exec := finalStatus(ctx, t, store, executionID)
	assert.Equal(t, workflow.StatusFailed, exec.Status,
		"compensation completed in full, and that state is what 'the work was done and undone' means")

	// Only a real store can witness this: moving to failed RELEASES the
	// idempotency key, so the customer can pay for the same cart again. Were the
	// key held, the abandoned cart would be unbuyable forever.
	_, err := store.FindByIdempotencyKey(ctx, WorkflowName, IdempotencyKeyPrefix+t.Name())
	assert.True(t, errors.IsNotFound(err),
		"a compensated execution must give its key back; otherwise the customer can never buy that cart: %v", err)
}

// TestRecoveryWillNotAssumeAnUnrecordedCaptureNeverRan is the double-charge
// guard, exercised on a real record.
//
// The record stops at the authorization: the last thing on disk is "the hold was
// taken", and the capture step has no row at all. Two very different worlds
// produce that record — the process died BEFORE it ever entered the capture, or
// it died INSIDE it, after the provider had taken the money and before anything
// could be written down. The records cannot tell them apart, and nothing else
// can either.
//
// Guessing "it never ran" is what costs a customer their money: recovery would
// release the stock, cancel the order and hand the idempotency key back, and the
// customer, seeing a failed checkout, would pay a second time for goods they had
// already been charged for. So the capture step declares BlocksRecovery and the
// engine stops at the boundary, leaves everything standing and asks for a human.
func TestRecoveryWillNotAssumeAnUnrecordedCaptureNeverRan(t *testing.T) {
	ctx := context.Background()
	h, store := durableHarness(t)

	executionID, input := abandonedCheckout(ctx, t, store,
		reserveRecord(t), redeemRecord(t), orderRecord(t), authorizeRecord(t))

	err := runRecovery(ctx, t, h, input, executionID)

	// The world is checked BEFORE the error: a recovery that guessed wrong does
	// its damage whether or not it also reports one, and that damage is what
	// this test is about.
	assertNothingWasUndone(t, h)

	require.Error(t, err, "an unrecorded capture cannot be recovered automatically")
	assert.True(t, hasCode(err, workflow.CodeRecoveryFailed),
		"the refusal has to be reported as a recovery refusal, not as a failed compensation: %v", err)

	exec := finalStatus(ctx, t, store, executionID)
	assert.Equal(t, workflow.StatusCompensationFailed, exec.Status,
		"a record a human has to answer for must be in the state monitoring counts first")

	// The key stays HELD, and that is the point: a released key invites the
	// second charge this whole test exists to prevent.
	found, ferr := store.FindByIdempotencyKey(ctx, WorkflowName, IdempotencyKeyPrefix+t.Name())
	require.NoError(t, ferr,
		"the key of an execution waiting for a human must not be released; a new attempt would land on a card that may already be charged")
	assert.Equal(t, executionID, found.ID)
}

// TestARecordedCaptureStopsRecoveryFromRollingBackAPaidOrder proves that the
// saga's pivot guard survives the death of the process that set it.
//
// On the live path the guard is a flag in the shared map, set before the capture
// call (see sharedCaptureAttempted). That map died with the process. All that
// remains is the capture step's row, and capturePaymentStep.Restore's whole job
// is to read "this row exists" as "the money may have gone" and put the flag
// back.
//
// If it does not, every compensation below it goes to work on a paid order: the
// hold is canceled, the order is canceled, and the goods a paying customer owns
// are released back onto the shelf. The unit tests prove that guard on the
// forward path; only a record can prove it on the path where the flag itself had
// to be reconstructed.
func TestARecordedCaptureStopsRecoveryFromRollingBackAPaidOrder(t *testing.T) {
	ctx := context.Background()
	h, store := durableHarness(t)

	executionID, input := abandonedCheckout(ctx, t, store,
		reserveRecord(t), redeemRecord(t), orderRecord(t), authorizeRecord(t), captureRecord(t))

	err := runRecovery(ctx, t, h, input, executionID)

	// Nothing was undone: the flag Restore rebuilt closed every compensation
	// below the pivot. It is asserted BEFORE the error, because a recovery that
	// forgot the flag has already emptied the shelf by the time it reports
	// anything at all.
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineA),
		"the goods of a paid order must not go back on the shelf; otherwise the same stock is sold twice")
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineB),
		"the goods of a paid order must not go back on the shelf")
	assert.Empty(t, h.orders.canceled,
		"a paid order is not canceled: the customer would lose both the money and the order")
	assert.Equal(t, 0, h.rec.count("payment:cancel"),
		"the hold behind a captured payment must not be canceled")

	require.Error(t, err, "a captured saga cannot be rolled back; it needs a refund flow and a human")
	assert.True(t, hasCode(err, CodeCaptureIrreversible),
		"the reason has to name the capture: a refund is a separate flow started by hand: %v", err)

	exec := finalStatus(ctx, t, store, executionID)
	assert.Equal(t, workflow.StatusCompensationFailed, exec.Status,
		"a captured saga that cannot be undone is exactly what compensation_failed means")
}

// TestTheBookkeepingStepDoesNotTurnRecoveryIntoManualWork proves that the
// saga's last step being a no-op does not cost the chain its recoverability.
//
// The record here is a saga that did every step and then died before its final
// status could be written — a real and unremarkable crash, one write short of
// completed. Every row is present, including the closing bookkeeping step.
//
// That step restores nothing, because it shares nothing; the reason it
// implements the interface at all is that the engine treats a step which did
// work and cannot rebuild its state as grounds to refuse THE WHOLE CHAIN. Were
// it to drop the method, this record would stop being a decision about a
// captured payment and become an unexplained "cannot rebuild" instead — the same
// manual intervention, arrived at for the wrong reason and reported with the
// wrong message to whoever has to fix it.
func TestTheBookkeepingStepDoesNotTurnRecoveryIntoManualWork(t *testing.T) {
	ctx := context.Background()
	h, store := durableHarness(t)

	executionID, input := abandonedCheckout(ctx, t, store,
		reserveRecord(t), redeemRecord(t), orderRecord(t), authorizeRecord(t), captureRecord(t), clearCartRecord(t))

	err := runRecovery(ctx, t, h, input, executionID)
	require.Error(t, err)

	assert.True(t, hasCode(err, CodeCaptureIrreversible),
		"the chain must have RUN and stopped at the capture; a rebuild refusal would mean the last step blocked it: %v", err)
	assert.False(t, hasCode(err, workflow.CodeRecoveryFailed),
		"a step with nothing to restore must not make the record unrecoverable: %v", err)

	exec := finalStatus(ctx, t, store, executionID)
	assert.Equal(t, workflow.StatusCompensationFailed, exec.Status)
}

// TestARecordNamingNoReservationStopsRecovery proves that recovery refuses to
// call a compensation whose target it does not know.
//
// A reserve_inventory row whose output holds an empty reservation list is a
// record that contradicts itself: the step is written as invoked, so stock IS
// standing reserved somewhere, yet the record cannot say which. Decoding that
// into an empty slice and carrying on is the quiet failure: the compensation
// walks zero reservations, reports success, and the engine writes the execution
// as failed — the state that means "the work was done and UNDONE". The stock
// stays reserved forever and the record now claims otherwise, so no report and
// no operator will ever look at it again.
//
// The refusal turns that into the opposite outcome: compensation_failed, which
// is the queue a human actually reads.
func TestARecordNamingNoReservationStopsRecovery(t *testing.T) {
	ctx := context.Background()
	h, store := durableHarness(t)

	executionID, input := abandonedCheckout(ctx, t, store,
		invokedStep(t, stepIndex(t, StepReserveInventory), StepReserveInventory, reserveOutput{}),
		orderRecord(t),
	)

	err := runRecovery(ctx, t, h, input, executionID)

	assertNothingWasUndone(t, h)

	exec := finalStatus(ctx, t, store, executionID)
	assert.Equal(t, workflow.StatusCompensationFailed, exec.Status,
		"a record that cannot say what it reserved must land in the queue a human reads, NOT be written off as rolled back")

	// The key stays held: releasing it would let a second attempt reserve the
	// same goods again on top of the stock nobody can find.
	_, ferr := store.FindByIdempotencyKey(ctx, WorkflowName, IdempotencyKeyPrefix+t.Name())
	assert.NoError(t, ferr, "an execution waiting for a human keeps its key")

	require.Error(t, err)
	assert.True(t, hasCode(err, CodeSharedStateInvalid),
		"the refusal has to name the corrupt record, not a module fault: %v", err)
}

// TestARecordNamingNoOrderStopsRecovery is the order step's half of the same
// rule.
//
// An invoked create_order row with no order id means an order was placed and its
// identifier lost. Taking that as "no order" would leave an ORPHAN order
// standing while the execution is filed as cleanly rolled back — a customer's
// order that nobody will ship and nobody will find.
func TestARecordNamingNoOrderStopsRecovery(t *testing.T) {
	ctx := context.Background()
	h, store := durableHarness(t)

	executionID, input := abandonedCheckout(ctx, t, store,
		reserveRecord(t),
		redeemRecord(t),
		invokedStep(t, stepIndex(t, StepCreateOrder), StepCreateOrder, createOrderOutput{}),
	)

	err := runRecovery(ctx, t, h, input, executionID)

	assertNothingWasUndone(t, h)

	exec := finalStatus(ctx, t, store, executionID)
	assert.Equal(t, workflow.StatusCompensationFailed, exec.Status,
		"an order whose identifier was lost is manual work, not a completed rollback")

	require.Error(t, err)
	assert.True(t, hasCode(err, CodeSharedStateInvalid), "error: %v", err)
}

// TestARecordNamingNoSessionStopsRecovery is the authorization step's half.
//
// An invoked authorize_payment row with no collection or session identifier
// means a hold was taken on a customer's card and its handle lost. The refusal
// is what puts the record in front of a human; reading it as "nothing was
// authorized" would file the execution as clean while a hold sat on the card
// until the provider expired it.
//
// The capture row is present because it has to be: without it the engine refuses
// at the recovery boundary instead (see
// TestRecoveryWillNotAssumeAnUnrecordedCaptureNeverRan), and the test would pass
// without the authorization step ever being consulted.
func TestARecordNamingNoSessionStopsRecovery(t *testing.T) {
	ctx := context.Background()
	h, store := durableHarness(t)

	executionID, input := abandonedCheckout(ctx, t, store,
		reserveRecord(t), redeemRecord(t), orderRecord(t),
		invokedStep(t, stepIndex(t, StepAuthorizePayment), StepAuthorizePayment, authorizeOutput{Status: "authorized"}),
		captureRecord(t),
	)

	err := runRecovery(ctx, t, h, input, executionID)

	assertNothingWasUndone(t, h)

	require.Error(t, err)
	assert.True(t, hasCode(err, CodeSharedStateInvalid),
		"the record's own defect has to be the reported reason: %v", err)
	assert.True(t, hasCode(err, workflow.CodeRecoveryFailed),
		"the chain must be refused BEFORE it runs; reaching the capture's compensation would mean the corrupt row was accepted: %v", err)
	assert.False(t, hasCode(err, CodeCaptureIrreversible),
		"a corrupt authorization row must not be papered over by the pivot guard downstream: %v", err)

	exec := finalStatus(ctx, t, store, executionID)
	assert.Equal(t, workflow.StatusCompensationFailed, exec.Status)
}

// TestACaptureRowWhoseSHAPEChangedIsNotReadAsNoCapture is the most expensive
// decode failure in the system, and the one a deploy actually causes.
//
// The row is well-formed JSON that no longer matches the step's output type —
// payment_id arriving as a number instead of a string. That is what a schema
// change between two deploys looks like from the recovery side: the executions
// in flight when the new binary rolls out carry the OLD shape, and the new
// binary is the one that has to read them.
//
// Restore returns the decode error, and the whole chain is refused. Swallowing
// it — returning nil on a shape it could not read — would leave the pivot flag
// unset, which recovery reads as "the card was never charged": the paid order
// would be canceled, the stock released and the key handed back for the customer
// to pay a second time. A refusal costs a human ten minutes; that costs a
// chargeback.
func TestACaptureRowWhoseSHAPEChangedIsNotReadAsNoCapture(t *testing.T) {
	ctx := context.Background()
	h, store := durableHarness(t)

	executionID, input := abandonedCheckout(ctx, t, store,
		reserveRecord(t), redeemRecord(t), orderRecord(t), authorizeRecord(t),
		invokedStepRaw(stepIndex(t, StepCapturePayment), StepCapturePayment, `{"payment_id": 7, "captured": 3000}`),
	)

	err := runRecovery(ctx, t, h, input, executionID)

	// The world first: a Restore that swallowed the decode error leaves the pivot
	// flag unset, and the chain below it empties the shelf and cancels a paid
	// order before anything is reported.
	assertNothingWasUndone(t, h)

	require.Error(t, err)
	assert.True(t, hasCode(err, CodeSharedStateInvalid),
		"an undecodable capture row is a corrupt record, and it has to be reported as one: %v", err)
	assert.True(t, hasCode(err, workflow.CodeRecoveryFailed),
		"the chain must be refused at the rebuild, before any compensation runs: %v", err)

	exec := finalStatus(ctx, t, store, executionID)
	assert.Equal(t, workflow.StatusCompensationFailed, exec.Status,
		"a capture row that cannot be read is exactly the case a human has to answer")
}
