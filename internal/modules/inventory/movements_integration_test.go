//go:build integration

// The movement ledger against a real PostgreSQL (ADR 0068).
//
// The unit tests prove the DECISIONS on a fake store. What can only be proved
// here is the ground they stand on: that the ledger row and the count it
// explains really do commit together, that the schema refuses the rows the
// decision says cannot exist, and that the listing SEEKS rather than walking —
// a claim this repository has shipped once as a godoc and had to withdraw.
package inventory_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/repository"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// newRepo builds a repository over the shared test pool.
//
// The store is reached directly in this file rather than through the service,
// because two of the properties under test — the transaction refusal and the
// CHECK constraints — are ones the service is built never to reach.
func newRepo() *repository.Repository { return repository.New(testPool.Pool()) }

// TestTheLedgerAndTheCountCommitTogether is the drift half of ADR 0068 on real
// SQL.
//
// The decision keeps stocked_quantity authoritative and lets the ledger explain
// it, and the ONLY thing holding the two together is that they are written in
// one transaction. Here the transaction is failed deliberately after both
// writes: if the movement survived, the ledger would report units that never
// left and the column would say otherwise.
func TestTheLedgerAndTheCountCommitTogether(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	repo := newRepo()
	item, loc := addItem(ctx, t, svc), addLocation(ctx, t, svc)
	_, err := svc.SetInventoryLevel(ctx, item.ID, loc.ID, 10)
	require.NoError(t, err)

	before := movementCount(ctx, t, item.ID)

	boom := errors.Internal("boom", "the transaction is failed on purpose")
	err = repo.WithTx(ctx, func(txCtx context.Context) error {
		level, lockErr := repo.LockInventoryLevel(txCtx, item.ID, loc.ID)
		require.NoError(t, lockErr)

		_, updateErr := repo.UpdateInventoryLevelQuantities(txCtx, level.ID, 4, 0)
		require.NoError(t, updateErr)

		_, appendErr := repo.AppendMovement(txCtx, models.Movement{
			ID:              models.NewMovementID(),
			InventoryItemID: item.ID,
			LocationID:      loc.ID,
			Reason:          models.MovementAdjustment,
			Delta:           -6,
			StockedAfter:    4,
		})
		require.NoError(t, appendErr)

		return boom
	})
	require.ErrorIs(t, err, boom)

	assert.Equal(t, before, movementCount(ctx, t, item.ID),
		"the movement has to roll back with the count it explains")

	levels, err := svc.ListInventoryLevels(ctx, item.ID)
	require.NoError(t, err)
	require.Len(t, levels, 1)
	assert.Equal(t, int64(10), levels[0].StockedQuantity,
		"the level has to roll back too, or this test proved nothing")
}

// TestAMovementOutsideATransactionIsRefusedByTheRepository drives the guard
// against a real pool.
//
// The unit test proves the refusal with a nil pool, which shows it happens
// before any query. This one shows the same call reaching a WORKING database
// and still being refused — that the guard is not something the driver happens
// to do.
func TestAMovementOutsideATransactionIsRefusedByTheRepository(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	repo := newRepo()
	item, loc := addItem(ctx, t, svc), addLocation(ctx, t, svc)

	_, err := repo.AppendMovement(ctx, models.Movement{
		ID:              models.NewMovementID(),
		InventoryItemID: item.ID,
		LocationID:      loc.ID,
		Reason:          models.MovementAdjustment,
		Delta:           1,
		StockedAfter:    1,
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInternal, errors.KindOf(err))
	assert.Equal(t, 0, movementCount(ctx, t, item.ID),
		"a refused append must not have written anything")
}

// TestTheSchemaRefusesAMovementThatCannotBeTrue is the last line of defence.
//
// Every one of these is refused in Go before it reaches the database. That is
// exactly why they are checked here: an intervention made directly in SQL, or a
// future writer that forgets a rule, meets the same refusals — which is what
// makes the CHECKs the definition of the row rather than a duplicate of the
// service's validation.
func TestTheSchemaRefusesAMovementThatCannotBeTrue(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item, loc := addItem(ctx, t, svc), addLocation(ctx, t, svc)
	_, err := svc.SetInventoryLevel(ctx, item.ID, loc.ID, 10)
	require.NoError(t, err)

	reservation, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 1,
	})
	require.NoError(t, err)

	cases := []struct {
		name          string
		reason        string
		delta         int64
		after         int64
		reservationID *string
		constraint    string
	}{
		{
			name: "a movement of nothing", reason: "adjustment", delta: 0, after: 10,
			constraint: "inventory_movements_delta_nonzero",
		},
		{
			name: "a count below zero", reason: "adjustment", delta: -20, after: -10,
			constraint: "inventory_movements_after_nonneg",
		},
		{
			name: "a reason nobody defined", reason: "transfer", delta: 1, after: 11,
			constraint: "inventory_movements_reason_valid",
		},
		{
			name: "a sale that ADDS units", reason: "sale", delta: 1, after: 11,
			reservationID: &reservation.ID,
			constraint:    "inventory_movements_sale_deducts",
		},
		{
			name: "a return that removes units", reason: "return_restock", delta: -1, after: 9,
			constraint: "inventory_movements_restock_adds",
		},
		{
			name: "a sale naming no reservation", reason: "sale", delta: -1, after: 9,
			constraint: "inventory_movements_sale_names_its_reservation",
		},
		{
			name: "an adjustment naming a reservation", reason: "adjustment", delta: 1, after: 11,
			reservationID: &reservation.ID,
			constraint:    "inventory_movements_sale_names_its_reservation",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := testPool.Pool().Exec(ctx,
				`INSERT INTO inventory_movements
                     (id, inventory_item_id, location_id, reservation_id, reason, delta, stocked_after)
                 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				models.NewMovementID(), item.ID, loc.ID, tc.reservationID,
				tc.reason, tc.delta, tc.after)

			require.Error(t, err, "the schema has to refuse this row")
			assert.Contains(t, err.Error(), tc.constraint,
				"the refusal has to come from the constraint that means it")
		})
	}
}

// TestAConfirmedReservationLeavesASaleThatNamesIt is the fact B7 says the audit
// log can never hold, taken end to end on real SQL.
//
// The reservation_id is a foreign key inside this module, so the row is not
// merely a string that looks like an id: the database refuses one that names no
// reservation.
func TestAConfirmedReservationLeavesASaleThatNamesIt(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item, loc := addItem(ctx, t, svc), addLocation(ctx, t, svc)
	_, err := svc.SetInventoryLevel(ctx, item.ID, loc.ID, 10)
	require.NoError(t, err)

	reservation, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 3,
	})
	require.NoError(t, err)

	afterReserve, err := svc.ListMovements(ctx, service.ListMovementsInput{InventoryItemID: item.ID})
	require.NoError(t, err)
	require.Len(t, afterReserve, 1, "a reservation is not a movement; only the opening count is here")

	require.NoError(t, svc.ConfirmReservation(ctx, reservation.ID))

	got, err := svc.ListMovements(ctx, service.ListMovementsInput{InventoryItemID: item.ID})
	require.NoError(t, err)
	require.Len(t, got, 2)

	sale := got[0]
	assert.Equal(t, models.MovementSale, sale.Reason)
	assert.Equal(t, int64(-3), sale.Delta)
	assert.Equal(t, int64(7), sale.StockedAfter)
	assert.Equal(t, reservation.ID, sale.ReservationID)

	// The newest row's stocked_after is what the column reads, which is the
	// property that makes a drift visible from ONE row.
	levels, err := svc.ListInventoryLevels(ctx, item.ID)
	require.NoError(t, err)
	require.Len(t, levels, 1)
	assert.Equal(t, levels[0].StockedQuantity, sale.StockedAfter)
}

// TestTwoMovementsInOneTransactionArePagedApart is why the id is in the index
// and in the keyset.
//
// created_at comes from now(), which is transaction START, so two movements
// written in one transaction share it EXACTLY. With the moment alone as the
// position, a page boundary landing between them would drop one or repeat it —
// and this is not a rare shape: it is what every confirm does when a level is
// opened and sold in the same call.
func TestTwoMovementsInOneTransactionArePagedApart(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	repo := newRepo()
	item, loc := addItem(ctx, t, svc), addLocation(ctx, t, svc)
	_, err := svc.SetInventoryLevel(ctx, item.ID, loc.ID, 10)
	require.NoError(t, err)

	require.NoError(t, repo.WithTx(ctx, func(txCtx context.Context) error {
		for _, delta := range []int64{1, 2} {
			if _, err := repo.AppendMovement(txCtx, models.Movement{
				ID:              models.NewMovementID(),
				InventoryItemID: item.ID,
				LocationID:      loc.ID,
				Reason:          models.MovementAdjustment,
				Delta:           delta,
				StockedAfter:    10 + delta,
			}); err != nil {
				return err
			}
		}

		return nil
	}))

	first, err := svc.ListMovements(ctx, service.ListMovementsInput{
		InventoryItemID: item.ID, Limit: 1,
	})
	require.NoError(t, err)
	require.Len(t, first, 1)

	second, err := svc.ListMovements(ctx, service.ListMovementsInput{
		InventoryItemID: item.ID, Limit: 1,
		After: cursorOf(first[0]),
	})
	require.NoError(t, err)
	require.Len(t, second, 1)

	assert.NotEqual(t, first[0].ID, second[0].ID,
		"the second page must not repeat the row the first one ended on")
	assert.True(t, first[0].CreatedAt.Equal(second[0].CreatedAt),
		"the fixture is only meaningful while the two share a moment")
}

// TestTheLedgerListingSeeksRatherThanWalks reads the PLAN.
//
// A cursor that does not reach the index buys nothing: it walks the same rows
// offset walks and merely spells the position differently. The shape being
// guarded is specific — written as "$n IS NULL OR (created_at, id) < (...)" the
// bound is an Index Cond for five executions and a Filter on the sixth, when
// Postgres switches to a generic plan, so a query that measured well would walk
// the whole index in production with nothing in the code having changed.
//
// The statement is retyped here rather than exported from the repository, which
// is the compromise the product module's cursor test already makes: what is
// being measured is the SHAPE of the predicate, and the shape is visible in
// both copies.
//
// The table is FILLED first, and that is not decoration. On an empty relation
// every plan costs nothing and the planner will take an index scan whatever the
// predicate looks like, so a plan read there says only that an index exists. The
// rows and the ANALYZE that follows them are what make a sequential scan a real
// alternative and the assertion a measurement.
func TestTheLedgerListingSeeksRatherThanWalks(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item, loc := addItem(ctx, t, svc), addLocation(ctx, t, svc)
	_, err := svc.SetInventoryLevel(ctx, item.ID, loc.ID, 1)
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO inventory_movements
             (id, inventory_item_id, location_id, reason, delta, stocked_after, created_at)
         SELECT 'invmov_PLAN_' || lpad(n::text, 6, '0'), $1, $2, 'adjustment', 1, n,
                now() - (n || ' seconds')::interval
         FROM generate_series(1, 5000) AS n`, item.ID, loc.ID)
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx, `ANALYZE inventory_movements`)
	require.NoError(t, err)

	const explain = `EXPLAIN (FORMAT TEXT)
SELECT id FROM inventory_movements
WHERE inventory_item_id = $1::text
  AND ($2::text IS NULL OR location_id = $2::text)
  AND (created_at, id) < (
    COALESCE($3::timestamptz, 'infinity'::timestamptz),
    COALESCE($4::text, '')
  )
ORDER BY created_at DESC, id DESC
LIMIT 10`

	rows, err := testPool.Pool().Query(ctx, explain, item.ID, nil, nil, nil)
	require.NoError(t, err)

	var plan strings.Builder

	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan.WriteString(line)
		plan.WriteString("\n")
	}

	require.NoError(t, rows.Err())
	rows.Close()

	assert.Contains(t, plan.String(), "inventory_movements_item_idx",
		"the listing has to reach the index built for it.\nplan:\n%s", plan.String())
	assert.Contains(t, plan.String(), "Index Cond",
		"the item and the keyset bound have to become INDEX CONDITIONS; as a Filter "+
			"the seek walks every row it was supposed to skip.\nplan:\n%s", plan.String())
	assert.NotContains(t, plan.String(), "Seq Scan",
		"the listing must not fall back to a sequential scan.\nplan:\n%s", plan.String())
}

// TestTheLedgerHasNoOpeningBalance holds the answer to the question the row did
// not ask: a migration cannot invent history.
//
// The stock a shop already holds gets NO row when the table arrives, so the
// deltas of an item that predates the ledger do not sum to its count. What
// makes that harmless is stocked_after: the oldest movement names the balance
// the ledger inherited, and here that inherited balance is deliberately not
// zero.
func TestTheLedgerHasNoOpeningBalance(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item, loc := addItem(ctx, t, svc), addLocation(ctx, t, svc)
	_, err := svc.SetInventoryLevel(ctx, item.ID, loc.ID, 10)
	require.NoError(t, err)

	// Erase the ledger for this item, which is the state of every item that
	// existed before migration 000004 ran.
	_, err = testPool.Pool().Exec(ctx,
		`DELETE FROM inventory_movements WHERE inventory_item_id = $1`, item.ID)
	require.NoError(t, err)

	_, err = svc.AdjustInventory(ctx, item.ID, loc.ID, -4)
	require.NoError(t, err)

	got, err := svc.ListMovements(ctx, service.ListMovementsInput{InventoryItemID: item.ID})
	require.NoError(t, err)
	require.Len(t, got, 1)

	var sum int64
	for i := range got {
		sum += got[i].Delta
	}
	assert.Equal(t, int64(-4), sum)
	assert.Equal(t, int64(6), got[0].StockedAfter, "the row still names the resulting count")
	assert.Equal(t, int64(10), got[0].StockedAfter-got[0].Delta,
		"and the oldest row names the balance the ledger inherited")

	levels, err := svc.ListInventoryLevels(ctx, item.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(6), levels[0].StockedQuantity,
		"the count is authoritative, which is what makes the missing history survivable")
}

// cursorOf turns a row into the position the next page starts below.
func cursorOf(mv models.Movement) page.Cursor {
	return page.Cursor{Time: mv.CreatedAt, ID: mv.ID}
}

// movementCount counts the item's ledger rows straight from the table.
//
// It reads SQL rather than the listing, deliberately: a test of whether a row
// EXISTS must not be answered by the reader whose filters are part of what is
// being tested.
func movementCount(ctx context.Context, t *testing.T, itemID string) int {
	t.Helper()

	var count int
	err := testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM inventory_movements WHERE inventory_item_id = $1`, itemID).Scan(&count)
	require.NoError(t, err, "the ledger could not be counted")

	return count
}
