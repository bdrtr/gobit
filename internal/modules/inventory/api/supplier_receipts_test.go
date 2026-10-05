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

// This file holds the HTTP half of ADR 0399: units a supplier owes a warehouse
// are recorded, listed, received and canceled under an item.

// TestARecordedReceiptAnswers201WithTheReceipt pins the create's envelope and
// what the handler hands the service.
func TestARecordedReceiptAnswers201WithTheReceipt(t *testing.T) {
	router, svc := newRouter(t)
	expected := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	svc.receipt = models.SupplierReceipt{
		ID: "invsup_1", InventoryItemID: "invitem_1", LocationID: "sloc_1", Quantity: 5,
		ExpectedAt: expected, Reference: "PO-7", Status: models.SupplierReceiptExpected,
	}

	rec := sendRequestWithBody(t, router, http.MethodPost, "/admin/v1/inventory-items/invitem_1/supplier-receipts",
		`{"location_id":"sloc_1","quantity":5,"expected_at":"2026-11-01T12:00:00+03:00","reference":"PO-7"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "invitem_1", svc.lastReceiptInput.InventoryItemID)
	assert.Equal(t, "sloc_1", svc.lastReceiptInput.LocationID)
	assert.Equal(t, int64(5), svc.lastReceiptInput.Quantity)
	assert.True(t, expected.Equal(svc.lastReceiptInput.ExpectedAt), "the offset is read, not dropped")
	assert.Equal(t, "PO-7", svc.lastReceiptInput.Reference)

	data, ok := jsonBody(t, rec)["data"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.Equal(t, "invsup_1", data["id"])
	assert.Equal(t, "expected", data["status"])
	assert.Equal(t, "PO-7", data["reference"])
	assert.NotContains(t, data, "received_at", "an expected receipt carries no receiving")
	assert.NotContains(t, data, "canceled_at")
}

// TestAReceiptWithoutItsCountOrDateIsRefused: quantity and expected_at are
// required, and a missing one never reaches the service.
func TestAReceiptWithoutItsCountOrDateIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"no quantity":    `{"location_id":"sloc_1","expected_at":"2026-11-01T09:00:00Z"}`,
		"no date":        `{"location_id":"sloc_1","quantity":5}`,
		"no offset":      `{"location_id":"sloc_1","quantity":5,"expected_at":"2026-11-01T09:00:00"}`,
		"an empty body":  ``,
		"a stray number": `{"location_id":"sloc_1","quantity":5,"expected_at":"2026-11-01T09:00:00Z","cost":3}`,
	} {
		t.Run(name, func(t *testing.T) {
			router, svc := newRouter(t)

			rec := sendRequestWithBody(t, router, http.MethodPost,
				"/admin/v1/inventory-items/invitem_1/supplier-receipts", body)

			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "inventory_invalid_request")
			assert.Zero(t, svc.lastReceiptInput, "a refused body must never reach the service")
		})
	}
}

// TestAReceiveAnswersTheReceiptAndTheLevel pins the receive's shape: the count
// is required, and the answer names the receipt and the level after it.
func TestAReceiveAnswersTheReceiptAndTheLevel(t *testing.T) {
	router, svc := newRouter(t)
	at := time.Now().UTC()
	svc.receipt = models.SupplierReceipt{
		ID: "invsup_1", InventoryItemID: "invitem_1", LocationID: "sloc_1", Quantity: 5,
		Status: models.SupplierReceiptReceived, ReceivedQuantity: 3, ReceivedAt: &at,
	}
	svc.receivedLevel = &models.InventoryLevel{
		ID: "invlevel_1", InventoryItemID: "invitem_1", LocationID: "sloc_1", StockedQuantity: 3,
	}

	rec := sendRequestWithBody(t, router, http.MethodPost,
		"/admin/v1/inventory-items/invitem_1/supplier-receipts/invsup_1/receive", `{"quantity":3}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "invitem_1", svc.lastID)
	assert.Equal(t, "invsup_1", svc.lastReceiptID)
	assert.Equal(t, int64(3), svc.lastReceived)
	data, ok := jsonBody(t, rec)["data"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	receipt, ok := data["supplier_receipt"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.Equal(t, "received", receipt["status"])
	assert.InDelta(t, 3, receipt["received_quantity"], 0)
	level, ok := data["level"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.InDelta(t, 3, level["available_quantity"], 0)

	missing := sendRequestWithBody(t, router, http.MethodPost,
		"/admin/v1/inventory-items/invitem_1/supplier-receipts/invsup_2/receive", `{}`)
	assert.Equal(t, http.StatusUnprocessableEntity, missing.Code, "a receipt is always a count")
	assert.Equal(t, "invsup_1", svc.lastReceiptID, "a receive with no count never reaches the service")
}

// TestAConflictingRepeatIsA409: the service's refusal of a receipt no longer
// expected reaches the caller with its code.
func TestAConflictingRepeatIsA409(t *testing.T) {
	router, svc := newRouter(t)
	svc.err = errors.Conflict("inventory_supplier_receipt_not_expected", "received with 3 units, not 4")

	rec := sendRequestWithBody(t, router, http.MethodPost,
		"/admin/v1/inventory-items/invitem_1/supplier-receipts/invsup_1/receive", `{"quantity":4}`)
	assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "inventory_supplier_receipt_not_expected")

	canceled := sendRequest(t, router, http.MethodPost,
		"/admin/v1/inventory-items/invitem_1/supplier-receipts/invsup_1/cancel")
	assert.Equal(t, http.StatusConflict, canceled.Code, canceled.Body.String())
}

// TestTheReceiptsAreAPagedList pins the listing's envelope and status filter,
// and the 422 for a status the service does not know.
func TestTheReceiptsAreAPagedList(t *testing.T) {
	router, svc := newRouter(t)
	svc.count = 4
	svc.receipts = []models.SupplierReceipt{{ID: "invsup_1", Status: models.SupplierReceiptExpected}}

	rec := sendRequest(t, router, http.MethodGet,
		"/admin/v1/inventory-items/invitem_1/supplier-receipts?status=expected&limit=2&offset=2")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "invitem_1", svc.lastReceiptList.InventoryItemID)
	assert.Equal(t, "expected", svc.lastReceiptList.Status)
	assert.Equal(t, int64(2), svc.lastReceiptList.Limit)
	assert.Equal(t, int64(2), svc.lastReceiptList.Offset)
	body := jsonBody(t, rec)
	assert.InDelta(t, 4, body["count"], 0)

	svc.err = errors.Invalid("inventory_invalid_input", "unknown supplier receipt status \"late\"")
	refused := sendRequest(t, router, http.MethodGet,
		"/admin/v1/inventory-items/invitem_1/supplier-receipts?status=late")
	assert.Equal(t, http.StatusUnprocessableEntity, refused.Code, refused.Body.String())
}

// TestAMovementPublishesWhatItWasFor: the ledger's rows carry their reference,
// so a supplier receipt's row names its receipt.
func TestAMovementPublishesWhatItWasFor(t *testing.T) {
	router, svc := newRouter(t)
	svc.movements = []models.Movement{
		{
			ID: "invmov_2", InventoryItemID: "invitem_1", LocationID: "sloc_1",
			Reason: models.MovementSupplierReceipt, Reference: "invsup_1", Delta: 3, StockedAfter: 3,
			CreatedAt: time.Now().UTC(),
		},
		{
			ID: "invmov_1", InventoryItemID: "invitem_1", LocationID: "sloc_1",
			Reason: models.MovementAdjustment, Delta: 1, StockedAfter: 1, CreatedAt: time.Now().UTC(),
		},
	}

	rec := sendRequest(t, router, http.MethodGet, "/admin/v1/inventory-items/invitem_1/movements")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rows, ok := jsonBody(t, rec)["data"].([]any)
	require.True(t, ok)
	require.Len(t, rows, 2)
	received, ok := rows[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "supplier_receipt", received["reason"])
	assert.Equal(t, "invsup_1", received["reference"])
	assert.Equal(t, true, received["from_admin_request"], "an operator received it")
	adjusted, ok := rows[1].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, adjusted, "reference", "an adjustment names nothing")
}
