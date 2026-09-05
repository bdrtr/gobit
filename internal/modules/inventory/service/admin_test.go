package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// This file exercises the module's ADMIN surface (ADR 0013).
//
// Its consumer is the panel, which cannot import this package: what binds them
// is the JSON schema, so the schema is asserted here BY NAME rather than by
// decoding into a struct declared in this file. A test that unmarshalled into
// its own struct would keep passing after a tag was renamed and the panel would
// silently read zeros.

// adminLevel is the shape the panel decodes. The tags are copied by hand from
// the panel's stockLevelRow on purpose: two hand-written copies disagreeing is
// exactly the failure this file is here to catch.
type adminLevel struct {
	LocationID        string `json:"location_id"`
	LocationName      string `json:"location_name"`
	StockedQuantity   int64  `json:"stocked_quantity"`
	ReservedQuantity  int64  `json:"reserved_quantity"`
	AvailableQuantity int64  `json:"available_quantity"`
}

// newAdmin builds a service with its admin surface over a fresh fake store.
//
// The service is constructed here rather than through the package's existing
// helper on purpose: that helper still carries a Turkish name, and this file is
// English (ADR 0012, decision 3 — language is a property of the FILE). Reaching
// for it would drag the old name into a translated file and the language check
// would fail, correctly.
func newAdmin(t *testing.T) (*service.AdminSurface, *service.Service, *fakeStore) {
	t.Helper()

	store := newFakeStore()
	svc := service.New(store, nil)

	return service.NewAdminSurface(svc), svc, store
}

// addLocation creates a location and returns its generated id.
func addLocation(t *testing.T, svc *service.Service, name string) string {
	t.Helper()

	loc, err := svc.CreateStockLocation(context.Background(),
		service.CreateStockLocationInput{Name: name})
	require.NoError(t, err)

	return loc.ID
}

// readLevels decodes the admin read.
func readLevels(t *testing.T, admin *service.AdminSurface, itemID string) []adminLevel {
	t.Helper()

	body, err := admin.StockLevelsJSON(context.Background(), itemID)
	require.NoError(t, err)

	var rows []adminLevel
	require.NoError(t, json.Unmarshal(body, &rows))

	return rows
}

// TestStockLevelsListEveryLocationIncludingTheEmptyOnes proves a warehouse that
// holds nothing is still offered to the form.
//
// This is the difference between a form that can stock a new warehouse and one
// that cannot: listing only the locations that already have a level would hide
// the location until it reached the state the operator is trying to create.
func TestStockLevelsListEveryLocationIncludingTheEmptyOnes(t *testing.T) {
	admin, svc, store := newAdmin(t)
	store.seedItem(itemID, "SKU-1")
	stocked := addLocation(t, svc, "Main warehouse")
	empty := addLocation(t, svc, "Overflow")
	store.seedLevel(itemID, stocked, 12, 0)

	rows := readLevels(t, admin, itemID)

	require.Len(t, rows, 2, "both locations must be offered")
	byID := map[string]adminLevel{}
	for _, row := range rows {
		byID[row.LocationID] = row
	}
	assert.Equal(t, int64(12), byID[stocked].StockedQuantity)
	assert.Equal(t, "Main warehouse", byID[stocked].LocationName)
	assert.Zero(t, byID[empty].StockedQuantity, "a location with no level reads as zero")
	assert.Equal(t, "Overflow", byID[empty].LocationName)
}

// TestStockLevelsCarryTheReservedQuantity proves the promised stock is visible.
//
// It is not decoration: [service.Service.SetInventoryLevel] refuses a count
// below the reserved quantity, and a form that showed only the physical number
// would make that refusal unexplainable.
func TestStockLevelsCarryTheReservedQuantity(t *testing.T) {
	admin, svc, store := newAdmin(t)
	store.seedItem(itemID, "SKU-1")
	loc := addLocation(t, svc, "Main warehouse")
	store.seedLevel(itemID, loc, 10, 4)

	rows := readLevels(t, admin, itemID)

	require.Len(t, rows, 1)
	assert.Equal(t, int64(10), rows[0].StockedQuantity)
	assert.Equal(t, int64(4), rows[0].ReservedQuantity)
	assert.Equal(t, int64(6), rows[0].AvailableQuantity,
		"the sellable quantity is derived, not stored")
}

// TestStockLevelsKeepTheServicesOwnOrder proves the admin read does not
// reshuffle the warehouses.
//
// The assertion compares against ListStockLocations rather than against a fixed
// order on purpose. The module already decides how locations are ordered, and
// the two orders disagree in practice — the repository returns the newest
// first while this package's fake sorts by id. A test that pinned either one
// would be asserting the FAKE, and an admin surface that quietly re-sorted the
// real list would slip past it.
func TestStockLevelsKeepTheServicesOwnOrder(t *testing.T) {
	admin, svc, store := newAdmin(t)
	store.seedItem(itemID, "SKU-1")
	for _, name := range []string{"A", "B", "C", "D", "E"} {
		addLocation(t, svc, name)
	}

	locations, _, err := svc.ListStockLocations(context.Background(), service.Page{Limit: 50})
	require.NoError(t, err)
	want := make([]string, 0, len(locations))
	for i := range locations {
		want = append(want, locations[i].ID)
	}

	rows := readLevels(t, admin, itemID)

	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, row.LocationID)
	}
	assert.Equal(t, want, got, "the panel shows the warehouses in the module's own order")
}

// TestTheStockSchemaIsTheNamesThePanelReads pins the wire contract.
//
// The panel decodes this JSON with its own struct; renaming a tag here would
// not break the build, it would make the number stop arriving. The field names
// are therefore asserted as TEXT.
func TestTheStockSchemaIsTheNamesThePanelReads(t *testing.T) {
	admin, svc, store := newAdmin(t)
	store.seedItem(itemID, "SKU-1")
	loc := addLocation(t, svc, "Main warehouse")
	store.seedLevel(itemID, loc, 7, 2)

	body, err := admin.StockLevelsJSON(context.Background(), itemID)
	require.NoError(t, err)

	var raw []map[string]any
	require.NoError(t, json.Unmarshal(body, &raw))
	require.Len(t, raw, 1)
	for _, field := range []string{
		"location_id", "location_name",
		"stocked_quantity", "reserved_quantity", "available_quantity",
	} {
		assert.Contains(t, raw[0], field, "the panel reads this field by name")
	}
	assert.Len(t, raw[0], 5, "an unannounced extra field means the schema drifted")
}

// TestStockLevelsRefuseAnUnknownItem proves the read does not invent an empty
// answer for an item that does not exist.
func TestStockLevelsRefuseAnUnknownItem(t *testing.T) {
	admin, svc, _ := newAdmin(t)
	addLocation(t, svc, "Main warehouse")

	_, err := admin.StockLevelsJSON(context.Background(), "invitem_MISSING")

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestSetStockWritesThePhysicalCount proves the write reaches the service and
// leaves the reservation alone.
func TestSetStockWritesThePhysicalCount(t *testing.T) {
	admin, svc, store := newAdmin(t)
	store.seedItem(itemID, "SKU-1")
	loc := addLocation(t, svc, "Main warehouse")
	store.seedLevel(itemID, loc, 10, 4)

	require.NoError(t, admin.SetStockLevel(context.Background(), itemID, loc, 20))

	level := store.level(itemID, loc)
	assert.Equal(t, int64(20), level.StockedQuantity)
	assert.Equal(t, int64(4), level.ReservedQuantity, "a count is not a reservation")
}

// TestSetStockKeepsThePromisedStock proves the surface does not bypass the
// service's rule.
//
// This is the whole reason ADR 0013 sends the panel through the SERVICE and not
// the repository: a form writing straight to the table could destroy stock that
// a customer has already paid for.
func TestSetStockKeepsThePromisedStock(t *testing.T) {
	admin, svc, store := newAdmin(t)
	store.seedItem(itemID, "SKU-1")
	loc := addLocation(t, svc, "Main warehouse")
	store.seedLevel(itemID, loc, 10, 4)

	err := admin.SetStockLevel(context.Background(), itemID, loc, 3)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err),
		"the operator must be told the goods are promised, not that they typed nonsense")
	assert.Equal(t, int64(10), store.level(itemID, loc).StockedQuantity,
		"the refused write must change nothing")
}

// TestAnUnwiredAdminSurfaceSaysSoRatherThanPanicking proves the zero value is
// safe.
//
// The panel resolves this surface OPTIONALLY (ADR 0013, decision 4), so a
// half-built one is reachable in a misconfigured installation. Returning
// Unavailable makes that a 503 the operator can read; a nil dereference would
// make it a panic in the request path.
func TestAnUnwiredAdminSurfaceSaysSoRatherThanPanicking(t *testing.T) {
	var admin *service.AdminSurface

	_, readErr := admin.StockLevelsJSON(context.Background(), itemID)
	writeErr := admin.SetStockLevel(context.Background(), itemID, "sloc_A", 1)

	require.Error(t, readErr)
	require.Error(t, writeErr)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(readErr))
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(writeErr))
}
