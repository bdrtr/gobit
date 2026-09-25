//go:build integration

package product_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// relationsPath is the admin address of a product's relations.
//
// Written out rather than imported, for the reason storeCatalogPath is.
func relationsPath(productID, suffix string) string {
	return "/admin/v1/products/" + productID + "/relations" + suffix
}

// setRelations replaces one kind of a product's relations over the admin API.
func (f channelFixture) setRelations(t *testing.T, productID, kind string, ids ...string) {
	t.Helper()

	body, err := json.Marshal(map[string][]string{"product_ids": ids})
	require.NoError(t, err)
	rec := f.sys.request(t, http.MethodPut, relationsPath(productID, "/"+kind), string(body))
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
}

// relations reads a product's relations over the admin API.
func (f channelFixture) relations(t *testing.T, productID string) map[string]any {
	t.Helper()

	rec := f.sys.request(t, http.MethodGet, relationsPath(productID, ""), "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	return itemData(t, rec)
}

// related reads one kind of a product's relations on the storefront, in one
// channel with a key bound to it, and returns the status and the handles.
func (f channelFixture) related(t *testing.T, channel, idOrHandle, kind string) (code int, handles []string) {
	t.Helper()

	rec := f.sys.storeChannelRequest(t,
		storeCatalogPath(channel, "/"+idOrHandle+"/related?type="+kind), []string{channel})
	if rec.Code != http.StatusOK {
		return rec.Code, nil
	}

	var body struct {
		Data []struct {
			Handle string `json:"handle"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "body: %s", rec.Body.String())
	require.NotNil(t, body.Data, "an empty list is [], not null: %s", rec.Body.String())
	handles = make([]string, 0, len(body.Data))
	for _, p := range body.Data {
		handles = append(handles, p.Handle)
	}
	return rec.Code, handles
}

// TestRelationsKeepTheirRankInTheRealSchema verifies the rank the operator's
// order is stored as (ADR 0180): a list comes back in the order it was written,
// a second write replaces it, and the kinds do not touch each other.
func TestRelationsKeepTheirRankInTheRealSchema(t *testing.T) {
	fx := newChannelFixture(t)
	shirt := fx.seedPublished(t, uniqueHandle("rel-shirt"))
	belt := fx.seedPublished(t, uniqueHandle("rel-belt"))
	socks := fx.seedPublished(t, uniqueHandle("rel-socks"))
	tie := fx.seedPublished(t, uniqueHandle("rel-tie"))

	// Neither the creation order nor the ids' order: the operator's.
	fx.setRelations(t, shirt, "cross_sell", socks, tie, belt)
	fx.setRelations(t, shirt, "up_sell", tie)
	read := fx.relations(t, shirt)
	assert.Equal(t, []any{socks, tie, belt}, read["cross_sell"])
	assert.Equal(t, []any{tie}, read["up_sell"])
	assert.Equal(t, []any{}, read["substitute"], "a kind with nothing in it is an empty list")

	fx.setRelations(t, shirt, "cross_sell", belt, socks)
	read = fx.relations(t, shirt)
	assert.Equal(t, []any{belt, socks}, read["cross_sell"], "the write replaced the list")
	assert.Equal(t, []any{tie}, read["up_sell"], "and left the other kind alone")

	rec := fx.sys.request(t, http.MethodPut, relationsPath(shirt, "/cross_sell"),
		`{"product_ids":["`+belt+`","prod_nope"]}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), "prod_nope", "the refusal names what it refused")
	assert.Equal(t, []any{belt, socks}, fx.relations(t, shirt)["cross_sell"], "and nothing was written")
}

// TestTheStorefrontReadsOnlyWhatItMayShow verifies the storefront read on the
// real visibility queries: a draft and another channel's product lined up by the
// operator are left out, in place, and a source the storefront may not show is a
// 404 rather than a list.
func TestTheStorefrontReadsOnlyWhatItMayShow(t *testing.T) {
	fx := newChannelFixture(t)
	shirtHandle := uniqueHandle("rel-shirt")
	shirt := fx.seedPublished(t, shirtHandle)
	socksHandle, beltHandle, elsewhereHandle := uniqueHandle("rel-socks"), uniqueHandle("rel-belt"),
		uniqueHandle("rel-elsewhere")
	socks := fx.seedPublished(t, socksHandle)
	belt := fx.seedPublished(t, beltHandle)
	elsewhere := fx.seedPublished(t, elsewhereHandle)
	fx.assign(t, elsewhere, fx.channelB)

	rec := fx.sys.request(t, http.MethodPost, "/admin/v1/products", `{
		"handle": "`+uniqueHandle("rel-draft")+`", "title": "Upcoming", "status": "draft"
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	draft, ok := itemData(t, rec)["id"].(string)
	require.True(t, ok)

	fx.setRelations(t, shirt, "cross_sell", socks, draft, elsewhere, belt)

	code, handles := fx.related(t, fx.channelA, shirtHandle, "cross_sell")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, []string{socksHandle, beltHandle}, handles,
		"the draft and the other channel's product are left out; the order stays")

	code, handles = fx.related(t, fx.channelB, shirt, "cross_sell")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, []string{socksHandle, elsewhereHandle, beltHandle}, handles,
		"in its own channel the other channel's product is where the operator put it")

	code, handles = fx.related(t, fx.channelA, shirt, "substitute")
	require.Equal(t, http.StatusOK, code)
	assert.Empty(t, handles)

	fx.setRelations(t, elsewhere, "cross_sell", socks)
	code, _ = fx.related(t, fx.channelA, elsewhere, "cross_sell")
	assert.Equal(t, http.StatusNotFound, code, "a list is not read off another channel's product")
	fx.setRelations(t, draft, "cross_sell", socks)
	code, _ = fx.related(t, fx.channelA, draft, "cross_sell")
	assert.Equal(t, http.StatusNotFound, code, "nor off a draft")
	code, _ = fx.related(t, fx.channelA, shirt, "accessory")
	assert.Equal(t, http.StatusUnprocessableEntity, code, "an unknown kind is refused")
	code, _ = fx.related(t, fx.channelA, shirt, "")
	assert.Equal(t, http.StatusUnprocessableEntity, code, "and so is none")
}

// TestADeletedProductLeavesEveryListInTheRealSchema verifies both directions of
// a deletion on the real statements: the soft delete through the admin API, and
// the foreign keys' cascade under a row removed outright.
func TestADeletedProductLeavesEveryListInTheRealSchema(t *testing.T) {
	ctx := context.Background()
	fx := newChannelFixture(t)
	shirt := fx.seedPublished(t, uniqueHandle("rel-shirt"))
	belt := fx.seedPublished(t, uniqueHandle("rel-belt"))
	socks := fx.seedPublished(t, uniqueHandle("rel-socks"))
	purged := fx.seedPublished(t, uniqueHandle("rel-purged"))

	fx.setRelations(t, shirt, "cross_sell", belt, socks, purged)
	fx.setRelations(t, belt, "substitute", shirt)

	rec := fx.sys.request(t, http.MethodDelete, "/admin/v1/products/"+belt, "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, []any{socks, purged}, fx.relations(t, shirt)["cross_sell"], "it left the list naming it")
	assert.Zero(t, relationRows(ctx, t, belt), "its own list went with it")

	_, err := testPool.Pool().Exec(ctx, `DELETE FROM product WHERE id = $1`, purged)
	require.NoError(t, err)
	assert.Equal(t, []any{socks}, fx.relations(t, shirt)["cross_sell"],
		"a row removed outright takes its relations with it")
}

// relationRows counts the relation rows touching a product in either direction.
func relationRows(ctx context.Context, t *testing.T, productID string) int {
	t.Helper()

	var n int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM product_relation WHERE product_id = $1 OR related_product_id = $1`,
		productID).Scan(&n))
	return n
}

// TestTheSchemaRefusesWhatTheServiceRefuses is the raw SQL witness for the
// relation table's rules: the service checks each before writing, and a check
// the service makes cannot show the constraint behind it holds — including the
// NULL a CHECK lets through.
func TestTheSchemaRefusesWhatTheServiceRefuses(t *testing.T) {
	ctx := context.Background()
	fx := newChannelFixture(t)
	shirt := fx.seedPublished(t, uniqueHandle("rel-shirt"))
	belt := fx.seedPublished(t, uniqueHandle("rel-belt"))
	tie := fx.seedPublished(t, uniqueHandle("rel-tie"))

	insert := `INSERT INTO product_relation (product_id, type, related_product_id, rank) VALUES ($1, $2, $3, $4)`
	for name, tc := range map[string]struct {
		args []any
		want string
	}{
		"an unknown kind":       {[]any{shirt, "accessory", belt, 0}, "product_relation_type_check"},
		"no kind":               {[]any{shirt, nil, belt, 0}, "null value"},
		"the product itself":    {[]any{shirt, "cross_sell", shirt, 0}, "product_relation_not_itself"},
		"a negative rank":       {[]any{shirt, "cross_sell", belt, -1}, "product_relation_rank_nonneg"},
		"a product not there":   {[]any{shirt, "cross_sell", "prod_nope", 0}, "product_relation_related_product_id_fkey"},
		"a source not there":    {[]any{"prod_nope", "cross_sell", belt, 0}, "product_relation_product_id_fkey"},
		"a product in it twice": {[]any{shirt, "up_sell", belt, 1}, "product_relation_pkey"},
		"two at one place":      {[]any{shirt, "up_sell", tie, 0}, "product_relation_rank_unique"},
	} {
		t.Run(name, func(t *testing.T) {
			if name == "a product in it twice" || name == "two at one place" {
				fx.setRelations(t, shirt, "up_sell", belt)
			}
			_, err := testPool.Pool().Exec(ctx, insert, tc.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestAHandleResolvesToTheLiveProductOnly verifies the panel's handle lookup on
// the real schema (ADR 0181): a handle is unique among LIVE products only, so a
// deleted product and its successor can share one, and the lookup must answer
// with the successor.
func TestAHandleResolvesToTheLiveProductOnly(t *testing.T) {
	ctx := context.Background()
	fx := newChannelFixture(t)
	shirt := fx.seedPublished(t, uniqueHandle("rel-shirt"))
	handle := uniqueHandle("rel-reused")
	first := fx.seedPublished(t, handle)
	rec := fx.sys.request(t, http.MethodDelete, "/admin/v1/products/"+first, "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	second := fx.seedPublished(t, handle)
	require.NotEqual(t, first, second)

	svc := newService(t, nil, nil)
	admin := service.NewAdminSurface(svc)
	require.NoError(t, admin.SetProductRelations(ctx, shirt, map[string][]string{"cross_sell": {handle}}))
	assert.Equal(t, []any{second}, fx.relations(t, shirt)["cross_sell"],
		"the handle named the live product, not the deleted one")

	err := admin.SetProductRelations(ctx, shirt, map[string][]string{"up_sell": {"rel-nobody-has-this"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such product: rel-nobody-has-this")

	// A handle only a DELETED product carries is refused as the operator typed
	// it. Were the deleted row found, the refusal would come one step later and
	// name an id the operator never saw.
	orphan := uniqueHandle("rel-orphan")
	gone := fx.seedPublished(t, orphan)
	rec = fx.sys.request(t, http.MethodDelete, "/admin/v1/products/"+gone, "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	err = admin.SetProductRelations(ctx, shirt, map[string][]string{"up_sell": {orphan}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such product: "+orphan)
}
