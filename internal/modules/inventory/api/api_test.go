package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/api"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// fakeInventory is the test counterpart of api.Inventory. It exists so that
// the handlers' HTTP behavior (status code, envelope, error mapping) can be
// exercised without a database.
type fakeInventory struct {
	// Return values.
	location  models.StockLocation
	item      models.InventoryItem
	level     models.InventoryLevel
	items     []models.InventoryItem
	levels    []models.InventoryLevel
	movements []models.Movement
	count     int64
	err       error

	// The recorded call details.
	lastLocationInput service.ListStockLocationsInput
	lastItemInput     service.CreateInventoryItemInput
	lastListInput     service.ListInventoryItemsInput
	lastID            string
	lastLocationID    string
	lastStocked       int64
	lastDelta         int64
	lastMovementInput service.ListMovementsInput
}

// That the fake satisfies the surface the handler expects is verified at
// compile time.
var _ api.Inventory = (*fakeInventory)(nil)

// CreateStockLocation answers the location creation call; it records no input.
func (f *fakeInventory) CreateStockLocation(_ context.Context, _ service.CreateStockLocationInput) (models.StockLocation, error) {
	return f.location, f.err
}

// GetStockLocation records the requested location id.
func (f *fakeInventory) GetStockLocation(_ context.Context, id string) (models.StockLocation, error) {
	f.lastID = id
	return f.location, f.err
}

// ListStockLocations records the listing input.
func (f *fakeInventory) ListStockLocations(_ context.Context, in service.ListStockLocationsInput) ([]models.StockLocation, int64, error) {
	f.lastLocationInput = in
	return []models.StockLocation{f.location}, f.count, f.err
}

// CloseStockLocation records the id that was closed.
func (f *fakeInventory) CloseStockLocation(_ context.Context, id string) (models.StockLocation, error) {
	f.lastID = id
	return f.location, f.err
}

// CreateInventoryItem records the item creation input.
func (f *fakeInventory) CreateInventoryItem(_ context.Context, in service.CreateInventoryItemInput) (models.InventoryItem, error) {
	f.lastItemInput = in
	return f.item, f.err
}

// GetInventoryItem records the requested id.
func (f *fakeInventory) GetInventoryItem(_ context.Context, id string) (models.InventoryItem, error) {
	f.lastID = id
	return f.item, f.err
}

// ListInventoryItems records the listing input.
func (f *fakeInventory) ListInventoryItems(_ context.Context, in service.ListInventoryItemsInput) ([]models.InventoryItem, int64, error) {
	f.lastListInput = in
	return f.items, f.count, f.err
}

// DeleteInventoryItem records the deleted id.
func (f *fakeInventory) DeleteInventoryItem(_ context.Context, id string) error {
	f.lastID = id
	return f.err
}

// ListInventoryLevels returns the item's levels.
func (f *fakeInventory) ListInventoryLevels(_ context.Context, itemID string) ([]models.InventoryLevel, error) {
	f.lastID = itemID
	return f.levels, f.err
}

// SetInventoryLevel records the quantity written.
func (f *fakeInventory) SetInventoryLevel(_ context.Context, itemID, locationID string, stockedQty int64) (models.InventoryLevel, error) {
	f.lastID, f.lastLocationID, f.lastStocked = itemID, locationID, stockedQty
	return f.level, f.err
}

// AdjustInventory records the adjustment amount.
func (f *fakeInventory) AdjustInventory(_ context.Context, itemID, locationID string, delta int64) (models.InventoryLevel, error) {
	f.lastID, f.lastLocationID, f.lastDelta = itemID, locationID, delta
	return f.level, f.err
}

// ListMovements records the listing input the handler assembled.
func (f *fakeInventory) ListMovements(_ context.Context, in service.ListMovementsInput) ([]models.Movement, error) {
	f.lastMovementInput = in
	return f.movements, f.err
}

// The helpers these tests use live beside the files that introduced them:
// newRouter, sendRequest and jsonBody in close_test.go, sendRequestWithBody in
// saleschannel_test.go. Every request they send carries a FULLY PRIVILEGED
// identity, so the tests here exercise the stock behavior rather than the
// scope layer; the scope ITSELF is tested in a separate file (yetki_test.go).

// TestCreateStockLocation verifies that a successful create returns 201 and the
// single-record envelope.
func TestCreateStockLocation(t *testing.T) {
	router, svc := newRouter(t)
	svc.location = models.StockLocation{
		ID: "sloc_1", Name: "Merkez", CountryCode: "TR",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}

	rec := sendRequestWithBody(t, router, http.MethodPost, "/admin/v1/stock-locations",
		`{"name":"Merkez","country_code":"TR"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	data, ok := jsonBody(t, rec)["data"].(map[string]any)
	require.True(t, ok, "the response has to carry the data envelope")
	assert.Equal(t, "sloc_1", data["id"])
	assert.Equal(t, "Merkez", data["name"])
}

// TestGetStockLocation verifies the envelope and the error mapping of a single
// location read.
func TestGetStockLocation(t *testing.T) {
	router, svc := newRouter(t)
	svc.location = models.StockLocation{ID: "sloc_1", Name: "Merkez"}

	rec := sendRequestWithBody(t, router, http.MethodGet, "/admin/v1/stock-locations/sloc_1", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "sloc_1", svc.lastID)
	data, ok := jsonBody(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Merkez", data["name"])

	svc.err = errors.NotFound("inventory_location_not_found", "no such location")
	rec = sendRequestWithBody(t, router, http.MethodGet, "/admin/v1/stock-locations/sloc_missing", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestListStockLocationsEnvelope verifies all four fields of the list envelope.
func TestListStockLocationsEnvelope(t *testing.T) {
	router, svc := newRouter(t)
	svc.location = models.StockLocation{ID: "sloc_1", Name: "Merkez"}
	svc.count = 42

	rec := sendRequestWithBody(t, router, http.MethodGet, "/admin/v1/stock-locations?limit=10&offset=20", "")

	require.Equal(t, http.StatusOK, rec.Code)
	body := jsonBody(t, rec)
	assert.Len(t, body["data"], 1)
	assert.InDelta(t, 42, body["count"], 0)
	assert.InDelta(t, 20, body["offset"], 0)
	assert.InDelta(t, 10, body["limit"], 0)
	assert.Equal(t, service.Page{Limit: 10, Offset: 20}, svc.lastLocationInput.Page)
	assert.False(t, svc.lastLocationInput.IncludeClosed,
		"a closed location must not enter the listing UNLESS it is asked for")
}

// TestListDefaultLimit verifies that the default is applied when no limit is
// given and that it SHOWS in the response; the client has to know the bound
// that was applied.
func TestListDefaultLimit(t *testing.T) {
	router, svc := newRouter(t)

	rec := sendRequestWithBody(t, router, http.MethodGet, "/admin/v1/stock-locations", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.InDelta(t, float64(service.DefaultLimit), jsonBody(t, rec)["limit"], 0)
	assert.Equal(t, service.DefaultLimit, svc.lastLocationInput.Page.Limit)
}

// TestListInvalidLimit verifies that a limit parameter that is not a number
// produces a 422.
func TestListInvalidLimit(t *testing.T) {
	router, _ := newRouter(t)

	rec := sendRequestWithBody(t, router, http.MethodGet, "/admin/v1/stock-locations?limit=abc", "")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestCreateItemDefaultShipping verifies that nil is passed to the service when
// the field is absent from the body (that is, that the default is decided in
// the service).
func TestCreateItemDefaultShipping(t *testing.T) {
	router, svc := newRouter(t)
	svc.item = models.InventoryItem{ID: "invitem_1", SKU: "SKU-1", RequiresShipping: true}

	rec := sendRequestWithBody(t, router, http.MethodPost, "/admin/v1/inventory-items", `{"sku":"SKU-1"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Nil(t, svc.lastItemInput.RequiresShipping)

	rec = sendRequestWithBody(t, router, http.MethodPost, "/admin/v1/inventory-items",
		`{"sku":"SKU-2","requires_shipping":false}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.NotNil(t, svc.lastItemInput.RequiresShipping,
		"a field that was sent has to be carried to the service")
	assert.False(t, *svc.lastItemInput.RequiresShipping)
}

// TestCreateItemUnknownField verifies that an extra field in the body is not
// silently swallowed but produces a 422.
func TestCreateItemUnknownField(t *testing.T) {
	router, _ := newRouter(t)

	rec := sendRequestWithBody(t, router, http.MethodPost, "/admin/v1/inventory-items",
		`{"sku":"SKU-1","fiyat":100}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestCreateItemEmptyBody verifies that an empty body produces a 422.
func TestCreateItemEmptyBody(t *testing.T) {
	router, _ := newRouter(t)

	rec := sendRequestWithBody(t, router, http.MethodPost, "/admin/v1/inventory-items", "")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestListItemsFilters verifies that the query parameters are carried to the
// service.
func TestListItemsFilters(t *testing.T) {
	router, svc := newRouter(t)

	rec := sendRequestWithBody(t, router, http.MethodGet,
		"/admin/v1/inventory-items?sku=SKU-1&requires_shipping=false", "")

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, svc.lastListInput.SKU)
	assert.Equal(t, "SKU-1", *svc.lastListInput.SKU)
	require.NotNil(t, svc.lastListInput.RequiresShipping)
	assert.False(t, *svc.lastListInput.RequiresShipping)
}

// TestListItemsInvalidFilter verifies that a requires_shipping value that is
// not a boolean produces a 422.
func TestListItemsInvalidFilter(t *testing.T) {
	router, _ := newRouter(t)

	rec := sendRequestWithBody(t, router, http.MethodGet, "/admin/v1/inventory-items?requires_shipping=belki", "")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestGetItemNotFound verifies that the service's NotFound error is turned into
// a 404 and that the error code is preserved in the body. The handler does NOT
// CHOOSE the status code; the mapping is done in the core.
func TestGetItemNotFound(t *testing.T) {
	router, svc := newRouter(t)
	svc.err = errors.NotFound("inventory_item_not_found", "no such item")

	rec := sendRequestWithBody(t, router, http.MethodGet, "/admin/v1/inventory-items/invitem_missing", "")

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "invitem_missing", svc.lastID)
	apiErr, ok := jsonBody(t, rec)["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "inventory_item_not_found", apiErr["code"])
}

// TestDeleteItem verifies that a successful delete returns 204 with no body.
func TestDeleteItem(t *testing.T) {
	router, svc := newRouter(t)

	rec := sendRequestWithBody(t, router, http.MethodDelete, "/admin/v1/inventory-items/invitem_1", "")

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
	assert.Equal(t, "invitem_1", svc.lastID)
}

// TestDeleteItemConflict verifies that the active reservation Conflict is
// turned into a 409.
func TestDeleteItemConflict(t *testing.T) {
	router, svc := newRouter(t)
	svc.err = errors.Conflict(service.CodeItemHasReservations, "the item has an active reservation")

	rec := sendRequestWithBody(t, router, http.MethodDelete, "/admin/v1/inventory-items/invitem_1", "")

	assert.Equal(t, http.StatusConflict, rec.Code)
}

// TestListLevelsIncludesAvailableQuantity verifies that the level response
// carries the derived available quantity.
func TestListLevelsIncludesAvailableQuantity(t *testing.T) {
	router, svc := newRouter(t)
	svc.levels = []models.InventoryLevel{
		{ID: "invlevel_1", InventoryItemID: "invitem_1", LocationID: "sloc_1",
			StockedQuantity: 10, ReservedQuantity: 4},
	}

	rec := sendRequestWithBody(t, router, http.MethodGet, "/admin/v1/inventory-items/invitem_1/levels", "")

	require.Equal(t, http.StatusOK, rec.Code)
	body := jsonBody(t, rec)
	assert.InDelta(t, 1, body["count"], 0)
	data, ok := body["data"].([]any)
	require.True(t, ok)
	require.Len(t, data, 1)
	level, ok := data[0].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 10, level["stocked_quantity"], 0)
	assert.InDelta(t, 4, level["reserved_quantity"], 0)
	assert.InDelta(t, 6, level["available_quantity"], 0)
}

// TestSetLevel verifies that the quantity in the body is carried to the service.
func TestSetLevel(t *testing.T) {
	router, svc := newRouter(t)
	svc.level = models.InventoryLevel{ID: "invlevel_1", StockedQuantity: 25}

	rec := sendRequestWithBody(t, router, http.MethodPost, "/admin/v1/inventory-items/invitem_1/levels",
		`{"location_id":"sloc_1","stocked_quantity":25}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "invitem_1", svc.lastID)
	assert.Equal(t, "sloc_1", svc.lastLocationID)
	assert.Equal(t, int64(25), svc.lastStocked)
}

// TestSetLevelZeroQuantity verifies that a zero quantity is not confused with
// "the field was not sent": without a pointer a client sending 0 would get a
// 422.
func TestSetLevelZeroQuantity(t *testing.T) {
	router, svc := newRouter(t)

	rec := sendRequestWithBody(t, router, http.MethodPost, "/admin/v1/inventory-items/invitem_1/levels",
		`{"location_id":"sloc_1","stocked_quantity":0}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, int64(0), svc.lastStocked)
}

// TestSetLevelQuantityRequired verifies that a 422 is returned when the
// quantity field is not sent.
func TestSetLevelQuantityRequired(t *testing.T) {
	router, _ := newRouter(t)

	rec := sendRequestWithBody(t, router, http.MethodPost, "/admin/v1/inventory-items/invitem_1/levels",
		`{"location_id":"sloc_1"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestSetLevelInsufficientStock verifies that the service's Conflict error is
// turned into a 409.
func TestSetLevelInsufficientStock(t *testing.T) {
	router, svc := newRouter(t)
	svc.err = errors.Conflict(service.CodeInsufficientStock, "cannot go below the reserved quantity")

	rec := sendRequestWithBody(t, router, http.MethodPost, "/admin/v1/inventory-items/invitem_1/levels",
		`{"location_id":"sloc_1","stocked_quantity":1}`)

	require.Equal(t, http.StatusConflict, rec.Code)
	apiErr, ok := jsonBody(t, rec)["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, service.CodeInsufficientStock, apiErr["code"])
}

// TestAdjustLevel verifies that the path parameters and a negative delta are
// carried correctly.
func TestAdjustLevel(t *testing.T) {
	router, svc := newRouter(t)
	svc.level = models.InventoryLevel{ID: "invlevel_1", StockedQuantity: 3}

	rec := sendRequestWithBody(t, router, http.MethodPost,
		"/admin/v1/inventory-items/invitem_1/levels/sloc_1/adjust", `{"delta":-2}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "invitem_1", svc.lastID)
	assert.Equal(t, "sloc_1", svc.lastLocationID)
	assert.Equal(t, int64(-2), svc.lastDelta)
}

// TestAdjustLevelDeltaRequired verifies that a 422 is returned when the delta
// field is absent.
func TestAdjustLevelDeltaRequired(t *testing.T) {
	router, _ := newRouter(t)

	rec := sendRequestWithBody(t, router, http.MethodPost,
		"/admin/v1/inventory-items/invitem_1/levels/sloc_1/adjust", `{}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestStockIsNotOpenToTheStore verifies that no store route is defined: the
// customer side sees stock only through the Query layer.
func TestStockIsNotOpenToTheStore(t *testing.T) {
	router, _ := newRouter(t)

	for _, path := range []string{
		"/store/v1/inventory-items",
		"/store/v1/stock-locations",
	} {
		rec := sendRequestWithBody(t, router, http.MethodGet, path, "")
		assert.Equal(t, http.StatusNotFound, rec.Code, "%s must not be open", path)
	}
}
