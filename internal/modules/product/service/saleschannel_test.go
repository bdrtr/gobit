package service_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// This file tests the SALES CHANNEL filter of the storefront.
//
// The rule: a product with NO channel assignment is visible in all channels, a
// product that HAS one is visible only in the channels it is assigned to. The
// tests here verify the SERVICE-side counterpart of the rule (the option being
// carried through, the single endpoint being filtered too, the paging not
// breaking); that the rule is really applied in SQL is proven in the integration
// tests — a fake repository cannot verify the condition it wrote itself.

// channelFixture is the shared setup of the channel filtering tests.
type channelFixture struct {
	svc   *service.Service
	links *fakeLinker
	store *memStore
}

// newChannelFixture builds a service on which published products can be set up.
func newChannelFixture(t *testing.T) channelFixture {
	t.Helper()

	links := newFakeLinker()
	store := newMemStore()
	return channelFixture{svc: newService(t, store, links, nil), links: links, store: store}
}

// variantProvider builds a variant provider over the fixture's repository.
//
// The provider is the surface that asks the scope question on the WRITE path
// (the cart flow comes down here through Query); that it sits in the same
// fixture as the storefront tests is deliberate — the two surfaces giving THE
// SAME answer in the same setup is what says the rule is one.
func (f channelFixture) variantProvider() query.Provider {
	return service.NewVariantProvider(f.store)
}

// storeHandles gives the handles of the products returned from the storefront
// list.
func storeHandles(items []service.StoreProduct) []string {
	out := make([]string, 0, len(items))
	for i := range items {
		out = append(out, items[i].Handle)
	}
	return out
}

// TestSalesChannelLinkTableMatchesLinkName verifies that the link table name the
// repository writes by hand IS DERIVED FROM the link name the service declares.
//
// The two constants live in two separate packages and there is no compiler bond
// between them: the repository cannot import service (service already imports
// the repository), which is why the table name is written by hand. If they drift
// apart the filter asks for a table that does not exist and the storefront list
// falls over entirely — but that would only be seen once the database is
// reached, that is, in an integration test. This test moves the bond to the fast
// suite.
func TestSalesChannelLinkTableMatchesLinkName(t *testing.T) {
	t.Parallel()

	table, err := link.TableName(service.LinkProductSalesChannel)
	require.NoError(t, err, "the link name should pass the core's table name validation")
	assert.Equal(t, table, repository.SalesChannelLinkTable,
		"the table the repository queries should be the table derived from the declared link name")
}

// TestStoreListingShowsUnassignedProductInEveryChannel verifies the BACKWARDS
// COMPATIBLE half of the rule: a product with no channel assignment at all is
// visible in every channel.
//
// This is the evidence that the strict alternative (unassigned = hidden) was
// deliberately not chosen; had it been, the existing catalogs would empty out
// overnight.
func TestStoreListingShowsUnassignedProductInEveryChannel(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()
	seedProduct(t, fx.svc, "shirt", "Shirt")

	for _, channels := range [][]string{{"sc_a"}, {"sc_b"}, {"sc_a", "sc_b"}} {
		result, err := fx.svc.ListStoreProducts(ctx, service.StoreListOptions{SalesChannelIDs: channels})
		require.NoError(t, err)
		assert.Equal(t, []string{"shirt"}, storeHandles(result.Items),
			"an unassigned product should be visible in the %v channels too", channels)
		assert.Equal(t, 1, requireCount(t, result), "the count should count the unassigned product too")
	}
}

// TestStoreListingHidesProductFromForeignChannel verifies the FILTERING half of
// the rule: a product assigned to channel A is visible in A and not in B.
//
// The fault itself was exactly this — the channels of the publishable key were
// being resolved but no module was reading them, so every key got THE SAME
// catalog.
func TestStoreListingHidesProductFromForeignChannel(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()
	product := seedProduct(t, fx.svc, "shirt", "Shirt")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_a"))

	visible, err := fx.svc.ListStoreProducts(ctx, service.StoreListOptions{SalesChannelIDs: []string{"sc_a"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"shirt"}, storeHandles(visible.Items), "the product should be visible in the channel it is assigned to")
	assert.Equal(t, 1, requireCount(t, visible))

	hidden, err := fx.svc.ListStoreProducts(ctx, service.StoreListOptions{SalesChannelIDs: []string{"sc_b"}})
	require.NoError(t, err)
	assert.Empty(t, hidden.Items, "the product SHOULD NOT BE VISIBLE in a channel it is not assigned to")
	assert.Zero(t, requireCount(t, hidden), "the count should not count the hidden product either")
}

// TestStoreListingShowsProductInAllAssignedChannels verifies that the many to
// many link really is multiple.
//
// Had the cardinality been declared wrongly (OneToOne/OneToMany), the second
// assignment would fall over with a conflict and the product would not be
// visible in the second storefront at all.
func TestStoreListingShowsProductInAllAssignedChannels(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()
	product := seedProduct(t, fx.svc, "shirt", "Shirt")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_a"))
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_b"))

	for _, channel := range []string{"sc_a", "sc_b"} {
		result, err := fx.svc.ListStoreProducts(ctx, service.StoreListOptions{SalesChannelIDs: []string{channel}})
		require.NoError(t, err)
		assert.Equal(t, []string{"shirt"}, storeHandles(result.Items),
			"a product assigned to two channels should be visible in the %q channel too", channel)
	}

	ids, err := fx.svc.ProductSalesChannelIDs(ctx, product.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"sc_a", "sc_b"}, ids, "both links should be durable")
}

// TestRemoveSalesChannelMakesProductGloballyVisible verifies that removing the
// last link DOES NOT HIDE the product but, on the contrary, makes it visible in
// every channel.
//
// This is the most easily misunderstood consequence of the rule; the behavior is
// written down in the godoc and is pinned here.
func TestRemoveSalesChannelMakesProductGloballyVisible(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()
	product := seedProduct(t, fx.svc, "shirt", "Shirt")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_a"))

	hidden, err := fx.svc.ListStoreProducts(ctx, service.StoreListOptions{SalesChannelIDs: []string{"sc_b"}})
	require.NoError(t, err)
	require.Empty(t, hidden.Items)

	require.NoError(t, fx.svc.RemoveProductSalesChannel(ctx, product.ID, "sc_a"))

	result, err := fx.svc.ListStoreProducts(ctx, service.StoreListOptions{SalesChannelIDs: []string{"sc_b"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"shirt"}, storeHandles(result.Items),
		"a product left with no assignment is visible in all channels again")
}

// TestStoreListingFilterKeepsPagingConsistent verifies that the filter does not
// break THE PAGING.
//
// Had the filtering been done on the Go side, LIMIT/OFFSET would be applied over
// the unfiltered set, the pages would fill up short and the total count would
// promise pages the client could never reach. The test pins two assertions
// together: the count reflects the FILTERED set and the pages carry as many
// records as that count says.
func TestStoreListingFilterKeepsPagingConsistent(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()

	// Two of the six products are assigned to a foreign channel; the remaining
	// four are unassigned.
	var hidden []string
	for i := range 6 {
		handle := fmt.Sprintf("product-%d", i)
		product := seedProduct(t, fx.svc, handle, "Product "+handle)
		if i%3 == 0 {
			require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_b"))
			hidden = append(hidden, handle)
		}
	}
	require.Len(t, hidden, 2)

	const pageSize = 3
	seen := map[string]bool{}
	for offset := 0; offset < 6; offset += pageSize {
		page, err := fx.svc.ListStoreProducts(ctx, service.StoreListOptions{
			SalesChannelIDs: []string{"sc_a"},
			Limit:           pageSize,
			Offset:          offset,
		})
		require.NoError(t, err)
		assert.Equal(t, 4, requireCount(t, page),
			"the total count should reflect the FILTERED set (offset=%d)", offset)
		for _, handle := range storeHandles(page.Items) {
			assert.NotContains(t, hidden, handle, "the foreign channel's product should not appear on any page")
			seen[handle] = true
		}
	}
	assert.Len(t, seen, 4, "as many records as the count promised should be gathered from the pages")
}

// TestGetStoreProductIsFilteredToo verifies that the SINGLE endpoint is filtered
// as well.
//
// Hiding in the list and showing through the single endpoint would make the
// hiding pointless: storefront addresses carry the handle, that is, this is
// exactly the endpoint that is guessable. A product invisible in a foreign
// channel returns the SAME error (NotFound) as an unpublished one; a different
// kind would give away the product's existence.
func TestGetStoreProductIsFilteredToo(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()
	product := seedProduct(t, fx.svc, "shirt", "Shirt")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_a"))

	found, err := fx.svc.GetStoreProduct(ctx, "shirt", []string{"sc_a"})
	require.NoError(t, err)
	assert.Equal(t, product.ID, found.ID)

	_, err = fx.svc.GetStoreProduct(ctx, "shirt", []string{"sc_b"})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "the product should not be findable in a foreign channel: %v", err)

	// A call by id is subject to the same filter; closing the handle and leaving
	// the id open would pierce the hiding.
	_, err = fx.svc.GetStoreProduct(ctx, product.ID, []string{"sc_b"})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a call by id should be filtered too: %v", err)
}

// TestStoreListingWithoutChannelIdentityIsNotFiltered verifies that a nil channel
// list means "no filtering".
//
// This is the setup where store authentication is not wired up at all (product
// is deployable on its own). Had the filter been applied, the storefront would
// silently empty out in such a setup.
func TestStoreListingWithoutChannelIdentityIsNotFiltered(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()
	product := seedProduct(t, fx.svc, "shirt", "Shirt")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_a"))

	result, err := fx.svc.ListStoreProducts(ctx, service.StoreListOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"shirt"}, storeHandles(result.Items),
		"the filter should not be applied on a request that carries no channel identity")

	single, err := fx.svc.GetStoreProduct(ctx, "shirt", nil)
	require.NoError(t, err)
	assert.Equal(t, product.ID, single.ID)
}

// TestStoreListingWithEmptyChannelSetShowsOnlyUnassigned pins the defensive
// behavior of an identity WITH NO CHANNELS.
//
// In practice this case does not occur: auth already rejects a publishable key
// that has no active channel left. Should it occur one day all the same, an
// empty set counts NOT as "no filtering" but as "there is no channel to match" —
// the opposite would open the catalog of all channels to an identity with no
// channels. Unassigned products keep showing up; the rule itself does not
// change, there is simply no channel to match.
func TestStoreListingWithEmptyChannelSetShowsOnlyUnassigned(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()
	assigned := seedProduct(t, fx.svc, "assigned", "Assigned")
	seedProduct(t, fx.svc, "unassigned", "Unassigned")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, assigned.ID, "sc_a"))

	result, err := fx.svc.ListStoreProducts(ctx, service.StoreListOptions{SalesChannelIDs: []string{}})
	require.NoError(t, err)
	assert.Equal(t, []string{"unassigned"}, storeHandles(result.Items),
		"an identity with no channels should see only the unassigned products")
	assert.Equal(t, 1, requireCount(t, result))
}

// TestAdminListingIgnoresSalesChannels verifies that the admin listing is not
// filtered.
//
// The admin identity has no sales channel and has to see the catalog as a whole;
// were it filtered, assigning a product to a channel would drop it from the
// admin listing too.
func TestAdminListingIgnoresSalesChannels(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()
	product := seedProduct(t, fx.svc, "shirt", "Shirt")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_a"))

	result, err := fx.svc.ListProducts(ctx, service.ListProductsOptions{})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "shirt", result.Items[0].Handle)
}

// TestAddSalesChannelRejectsUnknownProduct verifies that no link can be created
// to a product that does not exist.
//
// The link service sees the ids as free-form strings; without the check an id
// carrying a typo would be linked silently and that link would show up in no
// query.
func TestAddSalesChannelRejectsUnknownProduct(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)

	err := fx.svc.AddProductSalesChannel(context.Background(), "prod_missing", "sc_a")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "the expected kind is not_found: %v", err)
	assert.Empty(t, fx.links.linked(service.LinkProductSalesChannel, "prod_missing"),
		"a rejected request should leave no link behind")
}

// TestAddSalesChannelIsIdempotent verifies that creating the same link a second
// time does not give an error.
//
// A retried admin request (or a saga step) links the same pair again; returning
// a conflict would make it look like a fault.
func TestAddSalesChannelIsIdempotent(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()
	product := seedProduct(t, fx.svc, "shirt", "Shirt")

	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_a"))
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_a"))

	assert.Equal(t, []string{"sc_a"}, fx.links.linked(service.LinkProductSalesChannel, product.ID),
		"the second call should not add a second row")
}

// TestDeleteProductCleansSalesChannelLinks verifies that the channel links of a
// deleted product are cleaned up.
//
// Had the link remained, a read in the reverse direction on the auth side
// ("which products are in this channel") would land on a deleted product.
func TestDeleteProductCleansSalesChannelLinks(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()
	product := seedProduct(t, fx.svc, "shirt", "Shirt")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, product.ID, "sc_a"))

	require.NoError(t, fx.svc.DeleteProduct(ctx, product.ID))

	assert.Empty(t, fx.links.linked(service.LinkProductSalesChannel, product.ID),
		"no channel link of the deleted product should remain")
}

// TestSalesChannelLinksRequireLinkService verifies that a service built without
// a link service returns a typed "not ready" error.
func TestSalesChannelLinksRequireLinkService(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), nil, nil)
	product := seedProduct(t, svc, "shirt", "Shirt")
	ctx := context.Background()

	err := svc.AddProductSalesChannel(ctx, product.ID, "sc_a")
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "the expected kind is unavailable: %v", err)

	_, err = svc.ProductSalesChannelIDs(ctx, product.ID)
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "the expected kind is unavailable: %v", err)
}

// --- The scope question of the WRITE path -----------------------------------
//
// The tests below check that the rule is asked somewhere other than the
// storefront as well: the flow that adds a line to the cart reads the variant
// from the Query layer and scopes that read with the request's channels. The
// rule itself is NOT rewritten here — the provider goes down to the repository
// and the repository uses the same template as the storefront listing — so the
// assertions here prove not the CORRECTNESS of the rule but that the write path
// is BOUND to the same rule. Whether the SQL is really right is in the
// integration tests.

// variantIDsOf extracts the variant ids from the provider records.
func variantIDsOf(records []query.Record) []string {
	out := make([]string, 0, len(records))
	for i := range records {
		id, _ := records[i][query.IDField].(string)
		out = append(out, id)
	}
	return out
}

// TestVariantProviderScopesBySalesChannel verifies that the variant read obeys
// the channel filter.
//
// All three cases are tested because all three are met on the write path: an
// assigned variant being visible in its own channel, not being visible in a
// foreign channel, and an unassigned variant being visible in every channel.
func TestVariantProviderScopesBySalesChannel(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()

	assigned := seedProduct(t, fx.svc, "shirt", "Shirt")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, assigned.ID, "sc_a"))
	unassigned := seedProduct(t, fx.svc, "socks", "Socks")

	assignedVariant := assigned.Variants[0].ID
	unassignedVariant := unassigned.Variants[0].ID
	provider := fx.variantProvider()

	cases := map[string]struct {
		channels []string
		expected []string
	}{
		"its own channel": {
			channels: []string{"sc_a"},
			expected: []string{assignedVariant, unassignedVariant},
		},
		"a foreign channel": {
			channels: []string{"sc_b"},
			expected: []string{unassignedVariant},
		},
		"an identity with no channels": {
			// An empty but NON-nil slice: there is an identity, it has no
			// channel. Only the unassigned variant remains — the same meaning as
			// on the read surface.
			channels: []string{},
			expected: []string{unassignedVariant},
		},
	}

	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			records, err := provider.List(ctx, query.ListOptions{
				Filters: map[string]any{
					"ids":                         []string{assignedVariant, unassignedVariant},
					service.FilterSalesChannelIDs: tt.channels,
				},
			})
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.expected, variantIDsOf(records))
		})
	}
}

// TestVariantProviderWithoutChannelFilterSeesEverything verifies that the filter
// has no SILENT default.
//
// If the key is not given at all the scope is not applied: not every caller that
// reads from this surface has a customer request behind it, and filtering by an
// identity that does not exist would make the cart entirely unusable in setups
// with no identity.
func TestVariantProviderWithoutChannelFilterSeesEverything(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)
	ctx := context.Background()

	assigned := seedProduct(t, fx.svc, "shirt", "Shirt")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, assigned.ID, "sc_a"))

	records, err := fx.variantProvider().List(ctx, query.ListOptions{
		Filters: map[string]any{"ids": []string{assigned.Variants[0].ID}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{assigned.Variants[0].ID}, variantIDsOf(records),
		"when no filter is asked for, the assigned variant should be visible too")
}

// TestVariantProviderRejectsChannelFilterWithoutIDs verifies that using the
// channel filter without ids is REJECTED.
//
// The paths without ids do their paging in the database; had the filter been
// applied there in memory, the page would SILENTLY fill up short. Rejecting the
// combination is better than opening a surface that pages wrongly in silence;
// the full rationale is in the List documentation of
// [service.NewVariantProvider].
func TestVariantProviderRejectsChannelFilterWithoutIDs(t *testing.T) {
	t.Parallel()

	fx := newChannelFixture(t)

	_, err := fx.variantProvider().List(context.Background(), query.ListOptions{
		Filters: map[string]any{service.FilterSalesChannelIDs: []string{"sc_a"}},
	})

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid),
		"an unsupported combination should be errors.Invalid (ADR 0004): %v", err)
}
