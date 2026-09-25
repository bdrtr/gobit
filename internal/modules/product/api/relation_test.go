package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/product/api"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// relationCatalog records what the relation handlers hand the service.
type relationCatalog struct {
	api.Catalog

	id       string
	kind     models.RelationType
	ids      []string
	channels []string
}

// SetProductRelations records the call and answers with the list written.
func (c *relationCatalog) SetProductRelations(
	_ context.Context, id string, kind models.RelationType, ids []string,
) (map[models.RelationType][]string, error) {
	c.id, c.kind, c.ids = id, kind, ids
	return map[models.RelationType][]string{kind: ids}, nil
}

// StoreRelatedProducts records the call and answers with nothing.
func (c *relationCatalog) StoreRelatedProducts(
	_ context.Context, idOrHandle string, kind models.RelationType, channels []string,
) ([]service.StoreProduct, error) {
	c.id, c.kind, c.channels = idOrHandle, kind, channels
	return nil, nil
}

// TestTheRelationWriteCarriesItsKindAndList verifies the admin write: the kind
// from the path, the list from the body in its order, and a body with every kind
// present whatever the service left out.
func TestTheRelationWriteCarriesItsKindAndList(t *testing.T) {
	t.Parallel()

	catalog := &relationCatalog{}
	rec := do(t, newRouter(catalog), http.MethodPut, "/admin/v1/products/prod_1/relations/up_sell",
		`{"product_ids":["prod_3","prod_2"]}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "prod_1", catalog.id)
	assert.Equal(t, models.RelationUpSell, catalog.kind)
	assert.Equal(t, []string{"prod_3", "prod_2"}, catalog.ids)

	data, ok := decodeBody(t, rec)["data"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.Equal(t, []any{"prod_3", "prod_2"}, data["up_sell"])
	assert.Contains(t, data, "cross_sell", "every kind is a key of the body")
	assert.Contains(t, data, "substitute")
}

// TestTheRelatedReadAnswersAnEmptyList verifies the storefront read: the kind
// from the query, the channel from the path, and an empty list as [] rather than
// null.
func TestTheRelatedReadAnswersAnEmptyList(t *testing.T) {
	t.Parallel()

	catalog := &relationCatalog{}
	rec := storeRequest(t, newRouter(catalog), storeProductPath+"shirt/related?type=substitute", nil)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "shirt", catalog.id)
	assert.Equal(t, models.RelationSubstitute, catalog.kind)
	assert.Equal(t, []string{"sc_a"}, catalog.channels)
	assert.JSONEq(t, `{"data":[]}`, rec.Body.String())
}
