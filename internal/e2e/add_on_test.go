//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestAProductNamesTheAddOnsItsLinesTake is ADR 0228 on the production wiring:
// the operator names an engraving and a draft wrap as a ring's add-ons, the
// storefront reads the engraving with its product and not the draft, over REST
// and GraphQL (ADR 0231), the ring's
// own variant is refused, and deleting the engraving, then the wrap's product,
// takes each off the list.
func TestAProductNamesTheAddOnsItsLinesTake(t *testing.T) {
	ctx := t.Context()
	suffix := fmt.Sprint(time.Now().UnixNano())
	unmanaged := false
	product := func(name string, status productmodels.Status) productmodels.Product {
		p, err := productSvc.CreateProduct(ctx, productsvc.CreateProductInput{
			Handle: "e2e-add-on-" + name + "-" + suffix, Title: name, Status: status,
			Variants: []productsvc.CreateVariantInput{{Title: name, ManageInventory: &unmanaged}},
		})
		require.NoError(t, err)
		return p
	}
	ring := product("ring", productmodels.StatusPublished)
	engraving := product("engraving", productmodels.StatusPublished).Variants[0].ID
	wrapProduct := product("wrap", productmodels.StatusDraft)
	wrap := wrapProduct.Variants[0].ID

	put := func(ids ...string) (int, string) {
		rec, err := adminRequestWithBody(http.MethodPut, "/admin/v1/products/"+ring.ID+"/add-ons",
			map[string]any{"variant_ids": ids})
		require.NoError(t, err)
		return rec.Code, rec.Body.String()
	}
	listed := func() []string {
		rec, err := adminRequestWithBody(http.MethodGet, "/admin/v1/products/"+ring.ID+"/add-ons", nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var body struct {
			Data struct {
				VariantIDs []string `json:"variant_ids"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		return body.Data.VariantIDs
	}

	code, body := put(ring.Variants[0].ID)
	assert.Equal(t, http.StatusUnprocessableEntity, code, "a product's own variant is not its add-on: %s", body)
	code, body = put(engraving, wrap)
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, []string{engraving, wrap}, listed())

	shown := magazaIstegi(t, catalogPath(testChannelID, "/products/"+ring.Handle+"/add-ons"), publishableKey)
	require.Equal(t, http.StatusOK, shown.Code, shown.Body.String())
	var store struct {
		Data []struct {
			VariantID string `json:"variant_id"`
			Product   struct {
				Title string `json:"title"`
			} `json:"product"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(shown.Body.Bytes(), &store))
	require.Len(t, store.Data, 1, "the draft wrap is not shown: %s", shown.Body.String())
	assert.Equal(t, engraving, store.Data[0].VariantID)
	assert.Equal(t, "engraving", store.Data[0].Product.Title)

	// The GraphQL storefront answers the same list (ADR 0231).
	graph := gqlRequest(t, publishableKey, `query($handle: String) {
		product(handle: $handle) { addOns { variantId product { title } } }
	}`, map[string]any{"handle": ring.Handle})
	require.Equal(t, http.StatusOK, graph.Code, graph.Body.String())
	assert.JSONEq(t, `{"data":{"product":{"addOns":[{"variantId":"`+engraving+`","product":{"title":"engraving"}}]}}}`,
		graph.Body.String())

	rec, err := adminRequestWithBody(http.MethodDelete, "/admin/v1/variants/"+engraving, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{wrap}, listed(), "a deleted variant leaves the list")

	require.NoError(t, productSvc.DeleteProduct(ctx, wrapProduct.ID))
	assert.Empty(t, listed(), "a deleted product's variants leave every list naming them")
}
