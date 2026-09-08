// This file is English because ADR 0012 makes language a property of the FILE
// and every new file is English; api_test.go beside it stays Turkish.
package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
)

// movementListing is the listing name a cursor for this endpoint carries. It is
// written out here rather than imported: the api package's constant is
// unexported, and a test that reached for it would stop checking the WIRE value
// a client sends back.
const movementListing = "inventory_movements"

// TestTheLedgerListingCarriesACursorAndNoTotals pins the envelope.
//
// It is the module's only keyset page, so the shape is the thing most likely to
// drift back towards the list envelope every other endpoint here uses. A "count"
// on this response would be a total counted over an append-only table on every
// request, and an "offset" would name a position this listing does not have.
func TestTheLedgerListingCarriesACursorAndNoTotals(t *testing.T) {
	router, svc := newRouter(t)
	svc.movements = []models.Movement{{
		ID: "invmov_2", InventoryItemID: "invitem_1", LocationID: "sloc_1",
		Reason: models.MovementSale, ReservationID: "invres_9",
		Delta: -3, StockedAfter: 7, CreatedAt: time.Now().UTC(),
	}}

	rec := sendRequest(t, router, http.MethodGet,
		"/admin/v1/inventory-items/invitem_1/movements")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	body := jsonBody(t, rec)
	assert.NotContains(t, body, "count")
	assert.NotContains(t, body, "offset")
	assert.NotContains(t, body, "limit")

	rows, ok := body["data"].([]any)
	require.True(t, ok, "the page's rows come back under data: %s", rec.Body.String())
	require.Len(t, rows, 1)

	row, ok := rows[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "sale", row["reason"])
	assert.Equal(t, "invres_9", row["reservation_id"])
	assert.InDelta(t, -3, row["delta"], 0)
	assert.InDelta(t, 7, row["stocked_after"], 0)
	assert.Equal(t, false, row["from_admin_request"],
		"a sale has no person behind it, and the field is what says so without the "+
			"reader having to know the mapping")
}

// TestAnOperatorsMovementSaysWhereItsActorIs is the wire half of ADR 0068's
// third answer: the ledger holds no actor, and the reader publishes which rows
// have one to go and look for in the audit log.
func TestAnOperatorsMovementSaysWhereItsActorIs(t *testing.T) {
	router, svc := newRouter(t)
	svc.movements = []models.Movement{{
		ID: "invmov_1", InventoryItemID: "invitem_1", LocationID: "sloc_1",
		Reason: models.MovementStockCount, Delta: 5, StockedAfter: 5,
		CreatedAt: time.Now().UTC(),
	}}

	rec := sendRequest(t, router, http.MethodGet,
		"/admin/v1/inventory-items/invitem_1/movements")
	require.Equal(t, http.StatusOK, rec.Code)

	rows, ok := jsonBody(t, rec)["data"].([]any)
	require.True(t, ok)
	row, ok := rows[0].(map[string]any)
	require.True(t, ok)

	assert.Equal(t, "stock_count", row["reason"])
	assert.Equal(t, true, row["from_admin_request"])
	assert.NotContains(t, row, "reservation_id",
		"only a sale names a reservation, and an empty string on the wire would read "+
			"as a reservation the client could look up")
}

// TestAFullPageOffersThePositionOfTheNext walks the contract a client follows.
//
// The fake returns exactly as many rows as the limit asked for, which is the
// only signal this listing has that more may exist — a page shorter than the
// limit is the last one and must carry no cursor.
func TestAFullPageOffersThePositionOfTheNext(t *testing.T) {
	router, svc := newRouter(t)
	at := time.Now().UTC()
	svc.movements = []models.Movement{
		{ID: "invmov_2", Reason: models.MovementAdjustment, Delta: 1, CreatedAt: at},
		{ID: "invmov_1", Reason: models.MovementAdjustment, Delta: 1, CreatedAt: at.Add(-time.Hour)},
	}

	rec := sendRequest(t, router, http.MethodGet,
		"/admin/v1/inventory-items/invitem_1/movements?limit=2")
	require.Equal(t, http.StatusOK, rec.Code)

	cursor, ok := jsonBody(t, rec)["next_cursor"].(string)
	require.True(t, ok, "a full page has to offer the next position: %s", rec.Body.String())

	decoded, err := page.Decode(movementListing, cursor)
	require.NoError(t, err)
	assert.Equal(t, "invmov_1", decoded.ID,
		"the position is the LAST row of the page, not the first")
}

// TestAShortPageIsTheLastPage is the other side of the same contract. Computing
// a "has more" flag would mean reading a row in order to throw it away, on every
// page, to save a caller one empty request at the end.
func TestAShortPageIsTheLastPage(t *testing.T) {
	router, svc := newRouter(t)
	svc.movements = []models.Movement{
		{ID: "invmov_1", Reason: models.MovementAdjustment, Delta: 1, CreatedAt: time.Now().UTC()},
	}

	rec := sendRequest(t, router, http.MethodGet,
		"/admin/v1/inventory-items/invitem_1/movements?limit=2")
	require.Equal(t, http.StatusOK, rec.Code)

	assert.NotContains(t, jsonBody(t, rec), "next_cursor")
}

// TestTheHandlerPassesEveryParameterItDescribes checks the three query
// parameters reach the service, which is the direction the OpenAPI audit cannot
// see: it compares the described set against the READ set, and a parameter can
// be read into a variable that goes nowhere.
func TestTheHandlerPassesEveryParameterItDescribes(t *testing.T) {
	router, svc := newRouter(t)

	at := time.Now().UTC().Truncate(time.Millisecond)
	cursor := page.Encode(movementListing, page.Cursor{Time: at, ID: "invmov_7"})

	rec := sendRequest(t, router, http.MethodGet,
		"/admin/v1/inventory-items/invitem_1/movements"+
			"?limit=5&location_id=sloc_2&after="+cursor)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	in := svc.gorulenMovementInput
	assert.Equal(t, "invitem_1", in.InventoryItemID)
	assert.Equal(t, "sloc_2", in.LocationID)
	assert.Equal(t, int64(5), in.Limit)
	assert.Equal(t, "invmov_7", in.After.ID)
	assert.True(t, at.Equal(in.After.Time), "want %s, got %s", at, in.After.Time)
}

// TestACursorFromAnotherListingIsRefused is why the listing name travels inside
// the encoded value. Applied here, a customer listing's position would still
// decode into a valid time and id and would quietly select the wrong rows — a
// fault that reads as missing data rather than as an error.
func TestACursorFromAnotherListingIsRefused(t *testing.T) {
	router, _ := newRouter(t)

	foreign := page.Encode("customers", page.Cursor{Time: time.Now().UTC(), ID: "cus_1"})

	rec := sendRequest(t, router, http.MethodGet,
		"/admin/v1/inventory-items/invitem_1/movements?after="+foreign)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestTheServiceErrorReachesTheClientUnchanged keeps the handler out of the
// business of choosing status codes: a missing item is a 404 because the
// service said NotFound, not because this endpoint decided so.
func TestTheServiceErrorReachesTheClientUnchanged(t *testing.T) {
	router, svc := newRouter(t)
	svc.err = errors.NotFound("inventory_item_not_found", "no such item")

	rec := sendRequest(t, router, http.MethodGet,
		"/admin/v1/inventory-items/invitem_9/movements")

	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
}

// TestTheEmptyLedgerIsAnEmptyArray pins the shape of "nothing happened yet".
//
// A nil slice encodes as JSON null, and a client that iterates the response
// would have to special-case it. The ledger of a brand new item is the ordinary
// case here, not an edge one — it is what EVERY item looks like the day this
// table is created.
func TestTheEmptyLedgerIsAnEmptyArray(t *testing.T) {
	router, _ := newRouter(t)

	rec := sendRequest(t, router, http.MethodGet,
		"/admin/v1/inventory-items/invitem_1/movements")
	require.Equal(t, http.StatusOK, rec.Code)

	var raw struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	assert.JSONEq(t, "[]", string(raw.Data))
}
