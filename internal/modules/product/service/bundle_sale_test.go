package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// partSpec is how one component of the test box is counted and stocked.
type partSpec struct {
	manage, backorder bool
	// inventory is the component's stock record; nil is a component linked to
	// no item.
	inventory query.Record
	// byLocation is the breakdown a narrowed read asks for.
	byLocation map[string]int64
}

// boxFixture is a gift box of one towel and two soaps, with the two parts
// counted and stocked as given, read through a fake graph.
type boxFixture struct {
	svc          *service.Service
	graph        *fakeGraph
	linker       *fakeLinker
	box          string
	towel, soaps string
}

func newBoxFixture(t *testing.T, towel, soap partSpec) boxFixture {
	t.Helper()
	ctx := context.Background()
	graph := &fakeGraph{}
	linker := newFakeLinker()
	svc := newService(t, newMemStore(), linker, graph)

	seed := func(handle string, spec partSpec) string {
		product := seedProductInput(t, svc, service.CreateProductInput{
			Handle: handle, Title: handle, Status: models.StatusPublished,
			Variants: []service.CreateVariantInput{{
				Title: handle, ManageInventory: ptr(spec.manage), AllowBackorder: ptr(spec.backorder),
			}},
		})
		return product.Variants[0].ID
	}
	fx := boxFixture{
		svc: svc, graph: graph, linker: linker,
		box:   seed("gift-box", partSpec{manage: true}),
		towel: seed("towel", towel),
		soaps: seed("soap", soap),
	}
	_, err := svc.SetVariantBundle(ctx, fx.box, []models.BundleComponent{
		{VariantID: fx.towel, Quantity: 1},
		{VariantID: fx.soaps, Quantity: 2},
	})
	require.NoError(t, err)

	record := func(id string, spec partSpec) query.Record {
		out := query.Record{"id": id}
		if spec.inventory != nil {
			out["inventory_item"] = spec.inventory
		}
		if spec.byLocation != nil {
			out["inventory_stock"] = query.Record{"available_by_location": spec.byLocation}
		}
		return out
	}
	graph.records = []query.Record{
		// The box itself is linked to nothing: a bundle takes no item.
		{"id": fx.box},
		record(fx.towel, towel),
		record(fx.soaps, soap),
	}
	return fx
}

// boxBadge reads the storefront listing and returns the box's variant and
// product badges.
func (fx boxFixture) boxBadge(t *testing.T, channels ...string) (variant, product bool) {
	t.Helper()
	result, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
		SalesChannelIDs: channels,
	})
	require.NoError(t, err)
	for i := range result.Items {
		if p := &result.Items[i]; p.Handle == "gift-box" {
			require.Len(t, p.Variants, 1)
			return p.Variants[0].InStock, p.InStock
		}
	}
	require.FailNow(t, "the box is not on the page")
	return false, false
}

// stocked is a counted part with the given units available everywhere.
func stocked(units int64) partSpec {
	return partSpec{manage: true, inventory: query.Record{"id": "invitem", "available_quantity": units}}
}

// TestABundlesBadgeIsReadFromItsParts is ADR 0235's badge: the box is in stock
// when every part can supply its units for one box, and a part answers by ADR
// 0040's clauses counted in units.
//
// The rows that answer false are the ones that pin the rule: each short part
// alone makes the box unsellable, so a badge that stopped reading one part, or
// read a part as a yes rather than as units, fails one of them.
func TestABundlesBadgeIsReadFromItsParts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		towel, soap partSpec
		want        bool
	}{
		{name: "every part covers one box exactly", towel: stocked(1), soap: stocked(2), want: true},
		{name: "one soap is not two", towel: stocked(5), soap: stocked(1), want: false},
		{name: "no towel", towel: stocked(0), soap: stocked(5), want: false},
		{
			name:  "a counted part linked to no item supplies nothing",
			towel: stocked(5), soap: partSpec{manage: true}, want: false,
		},
		{
			name:  "a part nobody counts never limits the box",
			towel: stocked(1), soap: partSpec{manage: false}, want: true,
		},
		{
			name:  "a part sold past zero never limits the box",
			towel: stocked(1), soap: partSpec{manage: true, backorder: true, inventory: query.Record{"available_quantity": int64(0)}},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := newBoxFixture(t, tc.towel, tc.soap)

			variant, product := fx.boxBadge(t)
			assert.Equal(t, tc.want, variant, "the box's badge is its parts'")
			assert.Equal(t, tc.want, product, "a one-variant product carries its variant's answer")
		})
	}
}

// TestABundlesPartsRideOnThePagesGraphCall holds the round trips: the parts'
// stock arrives in the call that reads the page's variants, not in one of its
// own. The read names the box alone -- the stock alert's read (ADR 0215) -- so
// the parts are in the call only because the box brought them; a listing would
// carry them anyway, their products being on the same page.
func TestABundlesPartsRideOnThePagesGraphCall(t *testing.T) {
	t.Parallel()
	fx := newBoxFixture(t, stocked(1), stocked(2))

	inStock, err := fx.svc.VariantsInStock(context.Background(), []string{fx.box}, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{fx.box: true}, inStock, "a stock alert reads the box's badge from its parts")
	require.Equal(t, 1, fx.graph.callCount(), "one graph call for the box and its parts")
	ids, ok := fx.graph.lastSpec(t).Filters["ids"].([]string)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{fx.box, fx.towel, fx.soaps}, ids)
}

// TestABundlesBadgeCountsOnlyTheChannelsWarehouses is ADR 0092's narrowing
// applied to a part: two soaps in a warehouse the storefront cannot ship from
// are no soaps for its box.
func TestABundlesBadgeCountsOnlyTheChannelsWarehouses(t *testing.T) {
	t.Parallel()

	towel := stocked(3)
	towel.byLocation = map[string]int64{servedWarehouse: 3}
	soap := stocked(2)
	soap.byLocation = map[string]int64{otherWarehouse: 2}
	fx := newBoxFixture(t, towel, soap)
	bindWarehouseToChannel(t, fx.linker, servedWarehouse, badgeChannel)

	narrowed, _ := fx.boxBadge(t, badgeChannel)
	assert.False(t, narrowed, "the soaps sit where this storefront does not ship from")

	whole, _ := fx.boxBadge(t)
	assert.True(t, whole, "an unnarrowed read counts every warehouse")
}

// TestTheVariantRecordNamesWhatABundleIsMadeOf is the field the checkout reads
// (ADR 0235): a bundle's record carries its components in the operator's order
// with their units, a plain variant's an empty list, through both provider
// reads.
func TestTheVariantRecordNamesWhatABundleIsMadeOf(t *testing.T) {
	t.Parallel()
	fx := newBundleFixture(t)
	ctx := context.Background()
	box := fx.box.Variants[0].ID
	_, err := fx.svc.SetVariantBundle(ctx, box, fx.components())
	require.NoError(t, err)

	want := []query.Record{
		{service.FieldBundleComponentVariantID: fx.towel.Variants[0].ID, service.FieldBundleComponentQuantity: int64(1)},
		{service.FieldBundleComponentVariantID: fx.soap.Variants[0].ID, service.FieldBundleComponentQuantity: int64(2)},
	}
	provider := fx.variantProvider()
	fields := []string{query.IDField, service.FieldBundleComponents}
	ids := []string{box, fx.soap.Variants[0].ID}

	fetched, err := provider.FetchByIDs(ctx, ids, fields)
	require.NoError(t, err)
	listed, err := provider.List(ctx, query.ListOptions{Fields: fields, Filters: map[string]any{"ids": ids}})
	require.NoError(t, err)
	for name, records := range map[string][]query.Record{"FetchByIDs": fetched, "List": listed} {
		require.Len(t, records, 2, name)
		for _, record := range records {
			if record[query.IDField] == box {
				assert.Equal(t, want, record[service.FieldBundleComponents], name)
			} else {
				assert.Equal(t, []query.Record{}, record[service.FieldBundleComponents], name)
			}
		}
	}

	plain, err := provider.FetchByIDs(ctx, []string{box}, []string{query.IDField})
	require.NoError(t, err)
	assert.NotContains(t, plain[0], service.FieldBundleComponents, "a field not asked for is not read")
}

// TestThePanelNamesABundlesPartsBySKU is ADR 0236's admin surface: parts typed
// as a SKU or an id with their units resolve in the order typed, on the
// version the form was read at; a quantity past the bound or a SKU nobody
// carries is refused naming what was typed, and a save on a stale version is
// refused as the product's own form is.
func TestThePanelNamesABundlesPartsBySKU(t *testing.T) {
	fx := newBundleFixture(t)
	ctx := context.Background()
	box := fx.box.Variants[0].ID
	towel, soap := fx.towel.Variants[0].ID, fx.soap.Variants[0].ID
	soapSKU := "SOAP-1"
	_, err := fx.svc.UpdateVariant(ctx, soap, service.UpdateVariantInput{SKU: &soapSKU})
	require.NoError(t, err)
	surface := service.NewAdminSurface(fx.svc)
	version := func() int64 {
		product, err := fx.svc.GetProduct(ctx, fx.box.ID)
		require.NoError(t, err)
		return product.Version
	}

	require.NoError(t, surface.SetVariantBundle(ctx, box, []string{towel, soapSKU}, []int64{1, 2}, version()))
	read, err := fx.svc.VariantBundle(ctx, box)
	require.NoError(t, err)
	assert.Equal(t, []models.BundleComponent{{VariantID: towel, Quantity: 1}, {VariantID: soap, Quantity: 2}}, read,
		"the SKU resolves in the order typed, with its units")

	for name, call := range map[string]func() error{
		"a quantity past the bound": func() error {
			return surface.SetVariantBundle(ctx, box, []string{soapSKU}, []int64{service.MaxBundleComponentQuantity + 1}, version())
		},
		"no units": func() error {
			return surface.SetVariantBundle(ctx, box, []string{soapSKU}, []int64{0}, version())
		},
		"units that wrap the column to one": func() error {
			return surface.SetVariantBundle(ctx, box, []string{soapSKU}, []int64{1<<32 + 1}, version())
		},
		"a SKU nobody carries": func() error {
			return surface.SetVariantBundle(ctx, box, []string{"SOAP-9"}, []int64{1}, version())
		},
		"parts and units that do not pair": func() error {
			return surface.SetVariantBundle(ctx, box, []string{soapSKU, towel}, []int64{1}, version())
		},
	} {
		err := call()
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalid(err), "%s: %v", name, err)
	}
	err = surface.SetVariantBundle(ctx, box, []string{soapSKU}, []int64{service.MaxBundleComponentQuantity + 1}, version())
	assert.Contains(t, err.Error(), soapSKU, "the refusal names the part as typed")

	err = surface.SetVariantBundle(ctx, box, []string{soapSKU}, []int64{1}, version()-1)
	require.Error(t, err)
	assert.True(t, errors.IsPreconditionFailed(err), "a save on a stale version is refused: %v", err)

	read, err = fx.svc.VariantBundle(ctx, box)
	require.NoError(t, err)
	assert.Equal(t, []models.BundleComponent{{VariantID: towel, Quantity: 1}, {VariantID: soap, Quantity: 2}}, read,
		"a refused save writes nothing")
}
