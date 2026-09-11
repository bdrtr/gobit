//go:build integration

// This file is English because ADR 0012 makes language a property of the FILE
// and every new file is English; inventory_integration_test.go beside it stays
// Turkish and lends its helpers.
//
// The tests here run against a real PostgreSQL; to run them:
// make test-integration
//
// What they cover is the schema's half of one decision: a promise carries WHY
// it was made, and the ledger row it produces says the same thing. The unit
// tests prove the mapping against a fake, and a fake cannot disagree with a
// CHECK it does not have.
package inventory_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/repository"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// replacementService builds a service on the real store.
//
// The Turkish-named helpers beside this file do the same thing, and they are
// deliberately NOT called: the language ratchet reads a file's identifiers as
// well as its prose (ADR 0012), so an English file that borrowed them would
// count as Turkish debt it does not carry.
func replacementService(t *testing.T) *service.Service {
	t.Helper()

	return service.New(repository.New(testPool.Pool()), nil)
}

// stockedAt creates an item and a location and puts quantity units in it.
func stockedAt(
	ctx context.Context, t *testing.T, svc *service.Service, quantity int64,
) (models.InventoryItem, models.StockLocation) {
	t.Helper()

	item, err := svc.CreateInventoryItem(ctx, service.CreateInventoryItemInput{
		SKU:   "SKU-" + models.NewInventoryItemID(),
		Title: t.Name(),
	})
	require.NoError(t, err)

	location, err := svc.CreateStockLocation(ctx, service.CreateStockLocationInput{
		Name:        "Warehouse " + t.Name(),
		City:        "Istanbul",
		CountryCode: "TR",
	})
	require.NoError(t, err)

	_, err = svc.SetInventoryLevel(ctx, item.ID, location.ID, quantity)
	require.NoError(t, err)

	return item, location
}

// heldForAClaim sets four units aside for goods being sent to settle a claim.
func heldForAClaim(
	ctx context.Context, t *testing.T, svc *service.Service,
) (models.Reservation, models.InventoryItem) {
	t.Helper()

	item, loc := stockedAt(ctx, t, svc, 10)
	reservation, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID,
		LocationID:      loc.ID,
		Quantity:        4,
		Purpose:         models.PurposeReplacement,
	})
	require.NoError(t, err)

	return reservation, item
}

// TestGoodsSentToSettleAClaimAreTheirOwnLedgerReason is the sentence the column
// was added for, read back off the real row.
func TestGoodsSentToSettleAClaimAreTheirOwnLedgerReason(t *testing.T) {
	ctx := context.Background()
	svc := replacementService(t)

	reservation, item := heldForAClaim(ctx, t, svc)
	require.NoError(t, svc.ConfirmReservation(ctx, reservation.ID, noSaleOrder))

	var (
		reason        string
		delta         int64
		stockedAfter  int64
		reservationID *string
	)
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT reason, delta, stocked_after, reservation_id
           FROM inventory_movements WHERE inventory_item_id = $1`,
		item.ID).Scan(&reason, &delta, &stockedAfter, &reservationID))

	assert.Equal(t, "replacement", reason,
		"nobody paid for these units, and the ledger is where that is said")
	assert.Equal(t, int64(-4), delta)
	assert.Equal(t, int64(6), stockedAfter)
	require.NotNil(t, reservationID)
	assert.Equal(t, reservation.ID, *reservationID,
		"units that leave against a promise name the promise")

	var purpose string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT purpose FROM inventory_reservations WHERE id = $1`,
		reservation.ID).Scan(&purpose))
	assert.Equal(t, "replacement", purpose,
		"the purpose is written ON THE PROMISE, which is what the confirm reads back")
}

// TestAPromiseWrittenBeforeTheColumnIsASale pins the default the migration
// chose.
//
// It is not a convenience: the checkout saga was the module's only caller, so
// 'sale' is what every existing row really was.
func TestAPromiseWrittenBeforeTheColumnIsASale(t *testing.T) {
	ctx := context.Background()
	svc := replacementService(t)

	item, loc := stockedAt(ctx, t, svc, 5)
	reservation, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 1,
	})
	require.NoError(t, err)

	var purpose string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT purpose FROM inventory_reservations WHERE id = $1`,
		reservation.ID).Scan(&purpose))
	assert.Equal(t, "sale", purpose)
}

// TestTheSchemaRefusesAPurposeNobodyDefined keeps the column's vocabulary
// closed, the way the reason's has been since 000004.
func TestTheSchemaRefusesAPurposeNobodyDefined(t *testing.T) {
	ctx := context.Background()
	svc := replacementService(t)

	reservation, _ := heldForAClaim(ctx, t, svc)

	_, err := testPool.Pool().Exec(ctx,
		`UPDATE inventory_reservations SET purpose = 'gift' WHERE id = $1`, reservation.ID)

	require.Error(t, err, "a word no code writes must not be writable")
	assert.Contains(t, err.Error(), "inventory_reservations_purpose_valid")
}

// TestAReplacementMovementCannotAddUnits holds the sign that is part of what
// the reason MEANS.
//
// Goods coming back are a return; this reason is the going-out half, and a
// positive delta under it would be a restock wearing the wrong name.
func TestAReplacementMovementCannotAddUnits(t *testing.T) {
	ctx := context.Background()
	svc := replacementService(t)

	reservation, item := heldForAClaim(ctx, t, svc)
	require.NoError(t, svc.ConfirmReservation(ctx, reservation.ID, noSaleOrder))

	_, err := testPool.Pool().Exec(ctx,
		`UPDATE inventory_movements SET delta = 4 WHERE inventory_item_id = $1`, item.ID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "inventory_movements_replacement_deducts")
}

// TestUnitsLeavingAgainstAPromiseNameIt is the equivalence 000004 wrote for the
// sale, widened rather than loosened.
//
// Both directions are checked: a going-out reason cannot lose its promise, and
// a reason that moves no promised units cannot acquire one.
func TestUnitsLeavingAgainstAPromiseNameIt(t *testing.T) {
	ctx := context.Background()
	svc := replacementService(t)

	reservation, item := heldForAClaim(ctx, t, svc)
	require.NoError(t, svc.ConfirmReservation(ctx, reservation.ID, noSaleOrder))

	_, err := testPool.Pool().Exec(ctx,
		`UPDATE inventory_movements SET reservation_id = NULL WHERE inventory_item_id = $1`,
		item.ID)
	require.Error(t, err, "goods that left against a promise cannot stop naming it")
	assert.Contains(t, err.Error(), "inventory_movements_promise_names_its_reservation")

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE inventory_movements SET reason = 'adjustment' WHERE inventory_item_id = $1`,
		item.ID)
	require.Error(t, err, "a warehouse correction has no promise to name")
	assert.Contains(t, err.Error(), "inventory_movements_promise_names_its_reservation")
}

// noSaleOrder is the empty order a REPLACEMENT confirmation names.
//
// Goods settling a claim leave against the claim, not against an order, and the
// movement's reference exists so a canceled ORDER line can find the shelf its
// units left (ADR 0134). Passing an order here would put a reference on a reason
// that has nothing to point at, which the service refuses — it is how this file
// found the pairing check.
const noSaleOrder = ""
