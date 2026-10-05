//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	inventorymodels "github.com/bdrtr/gobit/internal/modules/inventory/models"
	inventorysvc "github.com/bdrtr/gobit/internal/modules/inventory/service"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
)

// This file runs ADR 0392 on the production wiring: a line the checkout lets
// through without stock is a claim the inventory module fills from the next
// units that arrive where the order may ship from, and a write-off withdraws
// what will not leave and puts back only what the line's stock lost (D242).

// backorderRow is one claim as the admin queue answers it.
type backorderRow struct {
	ID                string   `json:"id"`
	OrderID           string   `json:"order_id"`
	OrderLineItemID   string   `json:"order_line_item_id"`
	Quantity          int64    `json:"quantity"`
	WithdrawnQuantity int64    `json:"withdrawn_quantity"`
	LocationIDs       []string `json:"location_ids"`
	Status            string   `json:"status"`
	ReservationID     string   `json:"reservation_id"`
	FilledLocationID  string   `json:"filled_location_id"`
}

// allowBackorder ticks the variant's "may be sold beyond the shelf" box.
func allowBackorder(ctx context.Context, t *testing.T, variantID string) {
	t.Helper()

	allow := true
	_, err := productSvc.UpdateVariant(ctx, variantID, productsvc.UpdateVariantInput{AllowBackorder: &allow})
	require.NoError(t, err)
}

// backorderQueue reads the item's claims through the admin endpoint.
func backorderQueue(t *testing.T, itemID, status string) []backorderRow {
	t.Helper()

	path := "/admin/v1/inventory-items/" + itemID + "/backorders"
	if status != "" {
		path += "?status=" + status
	}
	rec, err := adminRequestWithBody(http.MethodGet, path, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var page struct {
		Data []backorderRow `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))

	return page.Data
}

// storefrontOrder places an order of the given engravings of one variant through
// the storefront and returns the order's id.
func storefrontOrder(t *testing.T, variantID string, lines map[string]int) string {
	t.Helper()

	cartID := openCartWithKey(t, publishableKey)
	for _, words := range []string{"Ada", "Bo"} {
		quantity, ok := lines[words]
		if !ok {
			continue
		}
		rec := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/line-items", fmt.Sprintf(
			`{"variant_id":%q,"quantity":%d,"properties":{"Engraving":%q}}`, variantID, quantity, words))
		require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	}
	read := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+cartID, "")
	total, ok := storefrontData(t, read)["total"].(float64)
	require.True(t, ok, read.Body.String())
	done := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		storefrontCompletionBody(t, int64(total)))
	require.Equal(t, http.StatusOK, done.Code, "body: %s", done.Body.String())
	data := storefrontData(t, done)
	assert.Empty(t, data["warnings"], "a backordered line is claimed without a warning")
	orderID, _ := data["order_id"].(string)
	require.NotEmpty(t, orderID)

	return orderID
}

// lineOf returns the id of the order's line that bought quantity units.
func lineOf(ctx context.Context, t *testing.T, orderID string, quantity int64) string {
	t.Helper()

	order, err := orderSvc.GetOrder(ctx, orderID)
	require.NoError(t, err)
	for i := range order.Items {
		if order.Items[i].Quantity == quantity {
			return order.Items[i].ID
		}
	}
	t.Fatalf("order %s has no line of %d", orderID, quantity)

	return ""
}

// stockedAt is the item's physical count at one location; zero when unleveled.
func stockedAt(ctx context.Context, itemID, locationID string) (int64, error) {
	levels, err := inventorySvc.ListInventoryLevels(ctx, itemID)
	if err != nil {
		return 0, err
	}
	for i := range levels {
		if levels[i].LocationID == locationID {
			return levels[i].StockedQuantity, nil
		}
	}

	return 0, nil
}

// TestABackorderedLineIsFilledByTheNextArrival is E1: two units ordered with
// none on the shelf wait on the order's line; a count of five answers three,
// the ledger shows the count and the sale naming the claim's reservation, and a
// second order of four, more than is left, waits in turn.
func TestABackorderedLineIsFilledByTheNextArrival(t *testing.T) {
	ctx := t.Context()
	variantID, itemID := newStockedVariant(ctx, t, "E2E Backordered Lamp", map[string]int64{taxedCurrency: 20_000}, 0)
	allowBackorder(ctx, t, variantID)

	orderID := storefrontOrder(t, variantID, map[string]int{"Ada": 2})
	queue := backorderQueue(t, itemID, "")
	require.Len(t, queue, 1)
	assert.Equal(t, "waiting", queue[0].Status)
	assert.Equal(t, orderID, queue[0].OrderID)
	assert.Equal(t, lineOf(ctx, t, orderID, 2), queue[0].OrderLineItemID)
	assert.Equal(t, int64(2), queue[0].Quantity)
	assert.Contains(t, queue[0].LocationIDs, stockLocationID)

	counted, err := adminRequestWithBody(http.MethodPost, "/admin/v1/inventory-items/"+itemID+"/levels",
		map[string]any{"location_id": stockLocationID, "stocked_quantity": 5})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, counted.Code, counted.Body.String())
	var level struct {
		Data struct {
			StockedQuantity int64 `json:"stocked_quantity"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(counted.Body.Bytes(), &level))
	assert.Equal(t, int64(3), level.Data.StockedQuantity, "two of the five went to the waiting order")

	filled := backorderQueue(t, itemID, "filled")
	require.Len(t, filled, 1)
	assert.Equal(t, stockLocationID, filled[0].FilledLocationID)

	ledger, err := inventorySvc.ListMovements(ctx, inventorysvc.ListMovementsInput{InventoryItemID: itemID})
	require.NoError(t, err)
	require.Len(t, ledger, 2)
	assert.Equal(t, inventorymodels.MovementSale, ledger[0].Reason)
	assert.Equal(t, int64(-2), ledger[0].Delta)
	assert.Equal(t, filled[0].ReservationID, ledger[0].ReservationID)
	assert.Equal(t, orderID, ledger[0].Reference)
	assert.Equal(t, inventorymodels.MovementStockCount, ledger[1].Reason)
	assert.Equal(t, int64(5), ledger[1].Delta)

	second := storefrontOrder(t, variantID, map[string]int{"Ada": 4})
	waiting := backorderQueue(t, itemID, "waiting")
	require.Len(t, waiting, 1)
	assert.Equal(t, second, waiting[0].OrderID)
	assert.Equal(t, int64(4), waiting[0].Quantity)
	assert.Equal(t, int64(3), stockLevel(ctx, t, itemID).StockedQuantity, "three are not four; they stay on sale")
}

// TestAWrittenOffBackorderPutsNothingBack is E2 and D242: one variant, two
// engravings; the one unit on the shelf goes to the first line and the second
// line's five wait. Writing the five off withdraws the claim and credits no
// shelf — at HEAD the five were credited to the shelf the first line sold from
// — and a later count fills nothing.
func TestAWrittenOffBackorderPutsNothingBack(t *testing.T) {
	ctx := t.Context()
	variantID, itemID := newStockedVariant(ctx, t, "E2E Backordered Ring", map[string]int64{taxedCurrency: 20_000}, 1)
	allowBackorder(ctx, t, variantID)

	orderID := storefrontOrder(t, variantID, map[string]int{"Ada": 1, "Bo": 5})
	require.Equal(t, int64(0), stockLevel(ctx, t, itemID).StockedQuantity, "the one unit went to the first line")
	backordered := lineOf(ctx, t, orderID, 5)

	canceled, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+orderID+"/line-cancellations",
		map[string]any{"order_line_item_id": backordered, "quantity": 5, "reason": "never made"})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, canceled.Code, canceled.Body.String())

	// The poll does not assert, for requireStockEventually's reason.
	require.Eventually(t, func() bool {
		rec, err := adminRequestWithBody(http.MethodGet,
			"/admin/v1/inventory-items/"+itemID+"/backorders?status=withdrawn", nil)
		return err == nil && rec.Code == http.StatusOK && countRows(rec.Body.Bytes()) == 1
	}, 10*time.Second, 50*time.Millisecond, "the write-off withdraws the claim")
	require.Never(t, func() bool {
		stocked, err := stockedAt(ctx, itemID, stockLocationID)
		return err == nil && stocked != 0
	}, 2*time.Second, 100*time.Millisecond, "no unit of the written-off line ever left a shelf")

	counted, err := adminRequestWithBody(http.MethodPost, "/admin/v1/inventory-items/"+itemID+"/levels",
		map[string]any{"location_id": stockLocationID, "stocked_quantity": 5})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, counted.Code, counted.Body.String())
	assert.Equal(t, int64(5), stockLevel(ctx, t, itemID).Available(), "a withdrawn claim takes nothing")
}

// countRows counts a list envelope's rows.
func countRows(body []byte) int {
	var page struct {
		Data []json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &page) != nil {
		return -1
	}

	return len(page.Data)
}

// TestAFilledBackorderGoesBackWhereItWasFilled is E3: the first line sold the
// item at one warehouse and the second line's claim was filled at another;
// writing off two of the second line credits the second warehouse, and the
// first is untouched.
func TestAFilledBackorderGoesBackWhereItWasFilled(t *testing.T) {
	ctx := t.Context()
	sold := newWarehouse(ctx, t, "E2E Backorder Sold")
	arrived := newWarehouse(ctx, t, "E2E Backorder Arrived")
	variantID, itemID := variantAcrossWarehouses(ctx, t, "E2E Backordered Vase",
		map[string]int64{taxedCurrency: 20_000}, map[string]int64{sold: 1, arrived: 0})
	allowBackorder(ctx, t, variantID)

	orderID := storefrontOrder(t, variantID, map[string]int{"Ada": 1, "Bo": 5})
	backordered := lineOf(ctx, t, orderID, 5)

	counted, err := adminRequestWithBody(http.MethodPost, "/admin/v1/inventory-items/"+itemID+"/levels",
		map[string]any{"location_id": arrived, "stocked_quantity": 5})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, counted.Code, counted.Body.String())
	filled := backorderQueue(t, itemID, "filled")
	require.Len(t, filled, 1)
	require.Equal(t, arrived, filled[0].FilledLocationID)

	canceled, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+orderID+"/line-cancellations",
		map[string]any{"order_line_item_id": backordered, "quantity": 2, "reason": "two too many"})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, canceled.Code, canceled.Body.String())

	require.Eventually(t, func() bool {
		stocked, err := stockedAt(ctx, itemID, arrived)
		return err == nil && stocked == 2
	}, 10*time.Second, 50*time.Millisecond, "the two written-off units go back where they were filled")
	stocked, err := stockedAt(ctx, itemID, sold)
	require.NoError(t, err)
	assert.Equal(t, int64(0), stocked, "not to the shelf the first line sold from")
}
