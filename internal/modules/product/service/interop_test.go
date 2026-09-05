package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// This file tests the "product.interop" surface.
//
// The most important assertion is THE CHANNEL FILTERING: this surface is the
// door search opens onto the catalog, and if the filtering is not applied here
// search becomes a BYPASS of the storefront's visibility rule — a client could
// read the record of a product that is not sold in its own channel through
// search.

// interopFixture is the shared setup of the surface tests.
type interopFixture struct {
	// store counts the repository calls; it is the evidence for the claim "no
	// query is made per record".
	store   *memStore
	svc     *service.Service
	interop *service.Interop
}

// newInteropFixture builds a surface with published products that can be set up.
func newInteropFixture(t *testing.T) interopFixture {
	t.Helper()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)

	return interopFixture{svc: svc, interop: service.NewInterop(svc), store: store}
}

// products calls the surface and decodes the storefront records it returns.
func (f interopFixture) products(t *testing.T, request string) []map[string]any {
	t.Helper()

	body, err := f.interop.StoreProductsByIDsJSON(context.Background(), json.RawMessage(request))
	require.NoError(t, err)

	var out struct {
		Products []map[string]any `json:"products"`
	}
	require.NoError(t, json.Unmarshal(body, &out), "response: %s", string(body))
	return out.Products
}

// ids gives the ids of the returned records IN ORDER.
func ids(records []map[string]any) []string {
	out := make([]string, 0, len(records))
	for _, rec := range records {
		id, _ := rec["id"].(string)
		out = append(out, id)
	}
	return out
}

// TestInteropAppliesTheChannelFilter verifies that the surface really applies
// the sales channel filter: the product of ANOTHER channel is NOT RETURNED even
// when its id is asked for explicitly.
//
// The rule is the same as in the storefront: a product with no assignment is
// visible in every channel, a product that has one only in the channels it is
// assigned to.
func TestInteropAppliesTheChannelFilter(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newInteropFixture(t)

	ours := seedProduct(t, fx.svc, "ours", "Our product")
	theirs := seedProduct(t, fx.svc, "theirs", "Another channel's product")
	unassigned := seedProduct(t, fx.svc, "unassigned", "Unassigned product")

	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, ours.ID, "sc_ours"))
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, theirs.ID, "sc_theirs"))

	request := fmt.Sprintf(`{"ids": [%q, %q, %q], "sales_channel_ids": ["sc_ours"]}`,
		ours.ID, theirs.ID, unassigned.ID)

	assert.Equal(t, []string{ours.ID, unassigned.ID}, ids(fx.products(t, request)),
		"a product assigned to another channel should not come back even when its id is asked for explicitly")
}

// TestInteropDoesNotFilterARequestWithoutChannels verifies that when the channel
// field is not given at all the filter is not applied.
//
// The meaning is the SAME as in the storefront listing (see
// service.StoreListOptions): a missing field means "the request carries no
// channel id" and it is the counterpart of a setup where store authentication is
// not wired up. An empty ARRAY says something different: there is an identity
// but it has no channels — in that case only the unassigned products remain.
func TestInteropDoesNotFilterARequestWithoutChannels(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newInteropFixture(t)

	assigned := seedProduct(t, fx.svc, "assigned", "Assigned")
	unassigned := seedProduct(t, fx.svc, "unassigned", "Unassigned")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, assigned.ID, "sc_one"))

	missing := fmt.Sprintf(`{"ids": [%q, %q]}`, assigned.ID, unassigned.ID)
	assert.Equal(t, []string{assigned.ID, unassigned.ID}, ids(fx.products(t, missing)),
		"while the field is missing the filter should not be applied")

	empty := fmt.Sprintf(`{"ids": [%q, %q], "sales_channel_ids": []}`, assigned.ID, unassigned.ID)
	assert.Equal(t, []string{unassigned.ID}, ids(fx.products(t, empty)),
		"an empty array means 'an identity with no channels'; only the unassigned product should remain")
}

// TestInteropSkipsUnknownIDsSilently verifies that ids which are not found,
// deleted or unpublished produce not an error but a missing record.
//
// Returning an error would mean search falling over entirely because a single
// product was deleted. Besides, "in another channel" and "does not exist at all"
// stay INDISTINGUISHABLE TO THE CALLER; the same rationale as the single
// storefront endpoint returning NotFound for both.
func TestInteropSkipsUnknownIDsSilently(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newInteropFixture(t)

	published := seedProduct(t, fx.svc, "published", "Published")
	deleted := seedProduct(t, fx.svc, "deleted", "To be deleted")
	require.NoError(t, fx.svc.DeleteProduct(ctx, deleted.ID))

	draft, err := fx.svc.CreateProduct(ctx, service.CreateProductInput{
		Title:  "Draft",
		Status: models.StatusDraft,
	})
	require.NoError(t, err)

	request := fmt.Sprintf(`{"ids": ["prod_missing", %q, %q, %q]}`, deleted.ID, draft.ID, published.ID)
	assert.Equal(t, []string{published.ID}, ids(fx.products(t, request)))

	// If none of them is found the response is an empty list, not an error.
	assert.Empty(t, fx.products(t, `{"ids": ["prod_missing"]}`))
	assert.Empty(t, fx.products(t, `{"ids": []}`))
}

// TestInteropPreservesTheIDOrder verifies that the response preserves the ID
// ORDER of the request.
//
// The order is part of the contract: search supplies the relevance order from
// outside and if the response breaks it the caller has to rebuild the ordering
// on its own side — that is, every consumer writes the same work again. The
// repository's own order (created_at DESC) is deliberately IGNORED here.
func TestInteropPreservesTheIDOrder(t *testing.T) {
	t.Parallel()

	fx := newInteropFixture(t)

	first := seedProduct(t, fx.svc, "first", "First")
	second := seedProduct(t, fx.svc, "second", "Second")
	third := seedProduct(t, fx.svc, "third", "Third")

	request := fmt.Sprintf(`{"ids": [%q, %q, %q]}`, third.ID, first.ID, second.ID)
	assert.Equal(t, []string{third.ID, first.ID, second.ID}, ids(fx.products(t, request)),
		"the response should preserve the order of the request")

	// A repeated id comes back ONCE and keeps the position of its first
	// occurrence.
	repeated := fmt.Sprintf(`{"ids": [%q, %q, %q]}`, second.ID, first.ID, second.ID)
	assert.Equal(t, []string{second.ID, first.ID}, ids(fx.products(t, repeated)))
}

// TestInteropReturnsTheSameShapeAsTheStorefront verifies that what the surface
// returns has the SAME shape as the storefront representation.
//
// The same type is serialized; the test checks this not field by field but by
// COMPARING the return of the storefront list with the return of the surface.
// Counting the fields by hand would be a copy that does not catch the two
// representations drifting apart.
func TestInteropReturnsTheSameShapeAsTheStorefront(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newInteropFixture(t)
	product := seedProduct(t, fx.svc, "shirt", "Shirt")

	storefront, err := fx.svc.GetStoreProduct(ctx, product.ID, nil)
	require.NoError(t, err)
	expectedRaw, err := json.Marshal(storefront)
	require.NoError(t, err)
	var expected map[string]any
	require.NoError(t, json.Unmarshal(expectedRaw, &expected))

	records := fx.products(t, fmt.Sprintf(`{"ids": [%q]}`, product.ID))
	require.Len(t, records, 1)
	assert.Equal(t, expected, records[0],
		"the surface should return THE SAME record the storefront endpoint writes")
	assert.Contains(t, records[0], "variants", "the variants are part of the storefront record")
}

// TestInteropRejectsAnInvalidRequest verifies that bodies which cannot be
// decoded return a typed error.
//
// Rejecting an unrecognized field is ESPECIALLY important on this surface: a
// consumer writing "channel_ids" would read the whole published catalog while
// believing it had applied the filter.
func TestInteropRejectsAnInvalidRequest(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newInteropFixture(t)

	for name, body := range map[string]string{
		"empty body":    ``,
		"null":          `null`,
		"array":         `[]`,
		"unknown field": `{"ids": [], "channel_ids": ["sc_1"]}`,
		"empty id":      `{"ids": [""]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := fx.interop.StoreProductsByIDsJSON(ctx, json.RawMessage(body))
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "the error kind should be Invalid: %v", err)
		})
	}
}

// TestInteropRejectsARequestAboveTheLimit verifies that the number of ids is
// bounded.
//
// A silent truncation would silently shorten the search result and the caller
// could never see it; an explicit error forces it to paginate.
func TestInteropRejectsARequestAboveTheLimit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newInteropFixture(t)

	wanted := make([]string, 0, service.MaxLimit+1)
	for i := 0; i <= service.MaxLimit; i++ {
		wanted = append(wanted, fmt.Sprintf("prod_%03d", i))
	}
	body, err := json.Marshal(map[string]any{"ids": wanted})
	require.NoError(t, err)

	_, err = fx.interop.StoreProductsByIDsJSON(ctx, body)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the error kind should be Invalid: %v", err)
}

// TestInteropAsksVisibilityInASingleQuery verifies that the search path does not
// fall into N+1.
//
// Asking visibility per id means as many round trips as there are search
// results. This repository lives in an architecture that structurally keeps N+1
// out (see core/query, "fetch the roots -> resolve the links -> fetch in batch")
// and bringing the pattern back at the hottest endpoint — in search — would
// break the architecture's own rule exactly where it is needed most.
//
// The assertion is made with a number, not by eye: as the number of ids grows
// the number of queries SHOULD NOT GROW.
func TestInteropAsksVisibilityInASingleQuery(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newInteropFixture(t)

	productIDs := make([]string, 0, 8)

	for i := range 8 {
		product := seedProduct(t, fx.svc, fmt.Sprintf("product-%d", i), fmt.Sprintf("Product %d", i))
		require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_ours"))
		productIDs = append(productIDs, product.ID)
	}

	encoded, err := json.Marshal(productIDs)
	require.NoError(t, err)

	request := fmt.Sprintf(`{"ids": %s, "sales_channel_ids": ["sc_ours"]}`, encoded)
	require.Len(t, fx.products(t, request), len(productIDs), "all of them should be visible")

	assert.Equal(t, 1, fx.store.callCount("VisibleProductIDs"),
		"whatever the number of ids, visibility should be asked in a SINGLE query")
	assert.Zero(t, fx.store.callCount("ProductVisibleInSalesChannels"),
		"the singular query should not be used on the batch path; if it is, N+1 comes back")
}
