//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	inventorymodels "github.com/bdrtr/gobit/internal/modules/inventory/models"
	inventorysvc "github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// This file runs ADR 0399 on the production wiring: units a supplier owes a
// warehouse are an expected supplier receipt, received through the ledger as
// `supplier_receipt`, and a storefront variant with nothing to sell shows when
// units are expected on sale again, after the waiting backorders take theirs.

// storeVariant reads one variant off the storefront product it belongs to,
// through the shared channel and key, and returns its body.
func storeVariant(ctx context.Context, t *testing.T, variantID string) map[string]any {
	t.Helper()

	variant, err := productSvc.GetVariant(ctx, variantID)
	require.NoError(t, err)
	rec := storeRequest(t, catalogPath(testChannelID, "/products/"+variant.ProductID), publishableKey)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var envelope struct {
		Data struct {
			Variants []map[string]any `json:"variants"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), rec.Body.String())
	for _, v := range envelope.Data.Variants {
		if v["id"] == variantID {
			return v
		}
	}
	t.Fatalf("variant %s is not on its storefront product: %s", variantID, rec.Body.String())

	return nil
}

// gqlStoreVariant reads the same variant through the GraphQL storefront.
func gqlStoreVariant(ctx context.Context, t *testing.T, variantID string) map[string]any {
	t.Helper()

	variant, err := productSvc.GetVariant(ctx, variantID)
	require.NoError(t, err)
	rec := gqlRequest(t, publishableKey,
		`query($id: ID) { product(id: $id) { variants { id inStock restockExpectedAt inventoryItem } } }`,
		map[string]any{"id": variant.ProductID})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var envelope struct {
		Data struct {
			Product struct {
				Variants []map[string]any `json:"variants"`
			} `json:"product"`
		} `json:"data"`
		Errors []any `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), rec.Body.String())
	require.Empty(t, envelope.Errors, rec.Body.String())
	for _, v := range envelope.Data.Product.Variants {
		if v["id"] == variantID {
			return v
		}
	}
	t.Fatalf("variant %s is not on its GraphQL product: %s", variantID, rec.Body.String())

	return nil
}

// TestTheStorefrontPublishesNoWarehouseBreakdown is D258: the inventory record
// a storefront variant carries is the provider's default field set, and a
// per-warehouse field is not in it (ADR 0093). At 8339896c both bodies carried
// `available_by_location`, computed on every read.
func TestTheStorefrontPublishesNoWarehouseBreakdown(t *testing.T) {
	ctx := t.Context()
	variantID, itemID := newStockedVariant(ctx, t, "E2E Unbroken Down Kettle", map[string]int64{taxedCurrency: 20_000}, 4)

	for name, variant := range map[string]map[string]any{
		"REST":    storeVariant(ctx, t, variantID),
		"GraphQL": gqlStoreVariant(ctx, t, variantID),
	} {
		key := "inventory_item"
		if name == "GraphQL" {
			key = "inventoryItem"
		}
		record, ok := variant[key].(map[string]any)
		require.True(t, ok, "%s: the variant carries its inventory record: %v", name, variant)
		assert.Equal(t, itemID, record["id"], name)
		assert.EqualValues(t, 4, record["available_quantity"], name)
		assert.NotContains(t, record, "available_by_location",
			"%s: a shop's warehouse topology is not a shopper's business (ADR 0093)", name)
	}
}

// recordSupplierReceipt records units owed through the admin endpoint and
// returns the receipt's id.
func recordSupplierReceipt(t *testing.T, itemID string, quantity int64, at time.Time) string {
	t.Helper()

	rec, err := adminRequestWithBody(http.MethodPost, "/admin/v1/inventory-items/"+itemID+"/supplier-receipts",
		map[string]any{
			"location_id": stockLocationID, "quantity": quantity,
			"expected_at": at.Format(time.RFC3339), "reference": "PO-" + itemID,
		})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var body struct {
		Data struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "expected", body.Data.Status)

	return body.Data.ID
}

// receiveSupplierReceipt receives a receipt's count through the admin endpoint.
func receiveSupplierReceipt(t *testing.T, itemID, receiptID string, quantity int64) {
	t.Helper()

	rec, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/inventory-items/"+itemID+"/supplier-receipts/"+receiptID+"/receive",
		map[string]any{"quantity": quantity})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// restockOf reads the variant's restock date and badge over REST and GraphQL,
// requires the two to agree, and returns them; a zero time is no date.
func restockOf(ctx context.Context, t *testing.T, variantID string) (time.Time, bool) {
	t.Helper()

	rest := storeVariant(ctx, t, variantID)
	gql := gqlStoreVariant(ctx, t, variantID)
	read := func(v any) time.Time {
		text, ok := v.(string)
		if !ok {
			return time.Time{}
		}
		at, err := time.Parse(time.RFC3339Nano, text)
		require.NoError(t, err)
		return at
	}
	restAt, gqlAt := read(rest["restock_expected_at"]), read(gql["restockExpectedAt"])
	require.True(t, restAt.Equal(gqlAt), "REST says %v, GraphQL %v", restAt, gqlAt)
	inStock, ok := rest["in_stock"].(bool)
	require.True(t, ok)
	require.Equal(t, inStock, gql["inStock"], "the two surfaces share one badge")

	return restAt, inStock
}

// TestStockOnItsWayHasADate runs ADR 0399 end to end: a variant with nothing to
// sell shows the date the next receipt leaves units for sale, a backordered
// order that takes the whole of an earlier receipt moves the date to the one
// after, and receiving the receipts through the admin endpoints fills the order,
// puts the rest on sale and leaves a supplier_receipt naming each in the ledger.
func TestStockOnItsWayHasADate(t *testing.T) {
	ctx := t.Context()
	variantID, itemID := newStockedVariant(ctx, t, "E2E Kettle On Its Way", map[string]int64{taxedCurrency: 20_000}, 0)
	soon := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	later := soon.Add(48 * time.Hour)

	at, inStock := restockOf(ctx, t, variantID)
	assert.False(t, inStock)
	assert.True(t, at.IsZero(), "nothing is expected, so no date")

	second := recordSupplierReceipt(t, itemID, 3, later)
	at, inStock = restockOf(ctx, t, variantID)
	assert.False(t, inStock)
	assert.True(t, later.Equal(at), "the date the units are expected: %v", at)

	allowBackorder(ctx, t, variantID)
	orderID := storefrontOrder(t, variantID, map[string]int{"Ada": 2})
	require.Len(t, backorderQueue(t, itemID, "waiting"), 1)
	first := recordSupplierReceipt(t, itemID, 2, soon)
	at, _ = restockOf(ctx, t, variantID)
	assert.True(t, later.Equal(at), "the earlier two go to the waiting order, so the date is the next: %v", at)

	receiveSupplierReceipt(t, itemID, first, 2)
	filled := backorderQueue(t, itemID, "filled")
	require.Len(t, filled, 1, "the units received went to the waiting order")
	assert.Equal(t, orderID, filled[0].OrderID)
	assert.Equal(t, int64(0), stockLevel(ctx, t, itemID).StockedQuantity)
	at, _ = restockOf(ctx, t, variantID)
	assert.True(t, later.Equal(at), "%v", at)

	receiveSupplierReceipt(t, itemID, second, 3)
	at, inStock = restockOf(ctx, t, variantID)
	assert.True(t, inStock)
	assert.True(t, at.IsZero(), "a variant with something to sell has no restock date")
	assert.Equal(t, int64(3), stockLevel(ctx, t, itemID).Available())

	ledger, err := inventorySvc.ListMovements(ctx, inventorysvc.ListMovementsInput{InventoryItemID: itemID})
	require.NoError(t, err)
	var references []string
	for i := range ledger {
		if ledger[i].Reason == inventorymodels.MovementSupplierReceipt {
			references = append(references, ledger[i].Reference)
		}
	}
	assert.ElementsMatch(t, []string{first, second}, references, "each receipt is one ledger row naming it")

	rec, err := adminRequestWithBody(http.MethodGet, "/admin/v1/inventory-items/"+itemID+"/movements", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"reason":"supplier_receipt"`)
	assert.Contains(t, rec.Body.String(), `"reference":"`+second+`"`)
}
