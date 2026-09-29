package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// giftBoxCatalog answers a gift box of a towel and two soaps, and a plain
// variant with no composition; the soap's count is a float, as it is once the
// answer crossed JSON.
func giftBoxCatalog() *scriptedCatalog {
	return &scriptedCatalog{records: []query.Record{
		{query.IDField: "variant_BOX", service.CatalogFieldBundleComponents: []query.Record{
			{service.CatalogFieldBundleComponentVariantID: "variant_TOWEL", service.CatalogFieldBundleComponentQuantity: int64(1)},
			{service.CatalogFieldBundleComponentVariantID: "variant_SOAP", service.CatalogFieldBundleComponentQuantity: float64(2)},
		}},
		{query.IDField: "variant_larger"},
	}}
}

// TestAReplacementOfABundleVariantKeepsWhatTheCatalogMakesIt is ADR 0244: an
// exchange that sends a gift box the order never sold records the box's parts
// as the catalog makes it now, and a plain variant beside it records none.
func TestAReplacementOfABundleVariantKeepsWhatTheCatalogMakesIt(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	catalog := giftBoxCatalog()
	svc := e.withCatalog(t, catalog)
	exchange, _ := exchangeToSend(t, e, 0)
	in := replacementOfVariant(exchange.ID, "variant_BOX", 1)
	in.Lines = append(in.Lines, service.ReplacementLineInput{VariantID: "variant_larger", Quantity: 1})

	record, err := svc.CreateReplacement(ctx, in)
	require.NoError(t, err)

	require.Len(t, record.Items, 2)
	assert.Equal(t, []models.ReplacementItemPart{
		{VariantID: "variant_TOWEL", Quantity: 1}, {VariantID: "variant_SOAP", Quantity: 2},
	}, record.Items[0].Parts)
	assert.Nil(t, record.Items[1].Parts, "a variant that is no bundle is sent as itself")
	assert.Equal(t, service.CatalogEntityVariant, catalog.asked.Entity)
	assert.ElementsMatch(t, []string{"variant_BOX", "variant_larger"},
		catalog.asked.Filters[service.CatalogFilterIDs], "one read for every variant the request names")
}

// TestABundleVariantsPartsAreHeldOneByOne carries ADR 0238's promise to the
// variant-shaped item: each part takes its own promise, and the record is sent
// only when every part holds one.
func TestABundleVariantsPartsAreHeldOneByOne(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	svc := e.withCatalog(t, giftBoxCatalog())
	exchange, _ := exchangeToSend(t, e, 0)
	record, err := svc.CreateReplacement(ctx, replacementOfVariant(exchange.ID, "variant_BOX", 1))
	require.NoError(t, err)
	box := record.Items[0].ID

	require.NoError(t, svc.RecordReplacementReservation(ctx, record.ID, box, "variant_TOWEL", "invres_towel"))
	_, err = svc.MarkReplacementDispatched(ctx, record.ID, "ful_box")
	require.Error(t, err, "the soaps hold nothing yet")
	require.NoError(t, svc.RecordReplacementReservation(ctx, record.ID, box, "variant_SOAP", "invres_soap"))
	_, err = svc.MarkReplacementDispatched(ctx, record.ID, "ful_box")
	require.NoError(t, err)
}

// TestACatalogThatCannotBeReadRecordsNothing keeps a replacement from being
// recorded without the parts it would send: a failed read and a composition
// that cannot be counted are refused before anything is written.
func TestACatalogThatCannotBeReadRecordsNothing(t *testing.T) {
	ctx := context.Background()

	for name, catalog := range map[string]*scriptedCatalog{
		"the read failed": {err: errors.Unavailable("query_down", "the query layer is down")},
		"a part in halves": {records: []query.Record{{query.IDField: "variant_BOX", service.CatalogFieldBundleComponents: []any{
			map[string]any{service.CatalogFieldBundleComponentVariantID: "variant_SOAP", service.CatalogFieldBundleComponentQuantity: 1.5},
		}}}},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			exchange, _ := exchangeToSend(t, e, 0)

			_, err := e.withCatalog(t, catalog).CreateReplacement(ctx, replacementOfVariant(exchange.ID, "variant_BOX", 1))

			require.Error(t, err)
			assert.Equal(t, service.CodeCatalogReadFailed, errors.CodeOf(err))
			replacements, err := e.svc.ListReplacementsOfExchange(ctx, exchange.ID)
			require.NoError(t, err)
			assert.Empty(t, replacements, "nothing was recorded")
		})
	}
}
