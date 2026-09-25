package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestTheVariantFilterKeepsTheProductsOwningThem shows the filter on both of
// the storefront's paths: the plain listing, and the scan the in-stock filter
// takes, which builds its own listing options (ADR 0191).
func TestTheVariantFilterKeepsTheProductsOwningThem(t *testing.T) {
	t.Parallel()

	fx := newStoreFixture(t)
	shirt := fx.products[0]

	for name, opts := range map[string]service.StoreListOptions{
		"listing": {VariantIDs: []string{shirt.Variants[0].ID, "variant_never_existed"}},
		// Neither fixture product counts as in stock, so the scan is asked for
		// the ones that are not: two match it, and the variant keeps one.
		"scan": {VariantIDs: []string{shirt.Variants[0].ID}, InStock: ptr(false)},
	} {
		result, err := fx.svc.ListStoreProducts(context.Background(), opts)
		require.NoError(t, err, name)
		require.Len(t, result.Items, 1, name)
		assert.Equal(t, shirt.ID, result.Items[0].ID, name)
	}
}

// TestAVariantFilterIsRefusedPastAPageOrMalformed shows the bound and the form
// judged before the store is asked.
func TestAVariantFilterIsRefusedPastAPageOrMalformed(t *testing.T) {
	t.Parallel()

	fx := newStoreFixture(t)
	tooMany := strings.Split(strings.Repeat("variant_x,", service.MaxLimit+1), ",")[:service.MaxLimit+1]

	for name, ids := range map[string][]string{
		"too many": tooMany,
		"blank":    {""},
		"padded":   {" variant_x"},
	} {
		_, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{VariantIDs: ids})
		require.Error(t, err, name)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err), name)
	}

	_, err := fx.svc.ListStoreProducts(context.Background(),
		service.StoreListOptions{VariantIDs: tooMany[:service.MaxLimit]})
	require.NoError(t, err, "a page's worth is taken")
}
