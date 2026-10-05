package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
)

// TestTheBackorderQueueIsAPagedList pins the envelope of ADR 0392's queue: the
// list envelope every paged listing here uses, the status filter and the page
// handed to the service, and a filled claim's reservation and shelf on the row.
func TestTheBackorderQueueIsAPagedList(t *testing.T) {
	router, svc := newRouter(t)
	svc.count = 7
	svc.backorders = []models.Backorder{{
		ID: "invbo_1", InventoryItemID: "invitem_1", OrderID: "order_1", OrderLineItemID: "oli_1",
		Quantity: 5, WithdrawnQuantity: 2, LocationIDs: []string{"sloc_2", "sloc_1"},
		Status: models.BackorderFilled, ReservationID: "invres_9", FilledLocationID: "sloc_2",
		Seq: 3, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}}

	rec := sendRequest(t, router, http.MethodGet,
		"/admin/v1/inventory-items/invitem_1/backorders?status=filled&limit=5&offset=5")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "invitem_1", svc.lastBackorderInput.InventoryItemID)
	assert.Equal(t, "filled", svc.lastBackorderInput.Status)
	assert.Equal(t, int64(5), svc.lastBackorderInput.Limit)
	assert.Equal(t, int64(5), svc.lastBackorderInput.Offset)

	body := jsonBody(t, rec)
	assert.InDelta(t, 7, body["count"], 0)
	assert.InDelta(t, 5, body["offset"], 0)
	assert.InDelta(t, 5, body["limit"], 0)
	rows, ok := body["data"].([]any)
	require.True(t, ok, rec.Body.String())
	require.Len(t, rows, 1)
	row, ok := rows[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "oli_1", row["order_line_item_id"])
	assert.Equal(t, "filled", row["status"])
	assert.Equal(t, "invres_9", row["reservation_id"])
	assert.Equal(t, "sloc_2", row["filled_location_id"])
	assert.Equal(t, []any{"sloc_2", "sloc_1"}, row["location_ids"], "the warehouses in rank order")
	assert.InDelta(t, 2, row["withdrawn_quantity"], 0)
	assert.InDelta(t, 3, row["seq"], 0)
}

// TestAnUnknownBackorderStatusIsRefused: the service's refusal of a status it
// does not know reaches the caller as a 422 with its code.
func TestAnUnknownBackorderStatusIsRefused(t *testing.T) {
	router, svc := newRouter(t)
	svc.err = errors.Invalid("inventory_invalid_input", "unknown backorder status \"late\"")

	rec := sendRequest(t, router, http.MethodGet, "/admin/v1/inventory-items/invitem_1/backorders?status=late")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Equal(t, "late", svc.lastBackorderInput.Status)
	assert.Contains(t, rec.Body.String(), "inventory_invalid_input")
}
