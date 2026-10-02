package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// TestInteropSurfaceUsesPrimitiveTypes proves that the cross-module surface
// structurally satisfies the narrow interfaces a consumer can define in its OWN
// package (ADR 0001).
//
// The interfaces here deliberately name none of region's types; the cart module
// will write exactly such an interface in Phase 5 and will resolve the concrete
// service from the container under the name "region.service". If this
// assignment does not compile, it will not compile on the consumer side either
// — the error is caught HERE, not at the moment of resolution from the
// container at run time.
func TestInteropSurfaceUsesPrimitiveTypes(t *testing.T) {
	// Exact copies of the interfaces the consuming module will write.
	type regionResolver interface {
		RegionIDForCountry(ctx context.Context, countryCode string) (string, error)
	}
	type regionCurrencyReader interface {
		RegionCurrency(ctx context.Context, regionID string) (string, int32, error)
	}
	type regionTaxReader interface {
		RegionTax(ctx context.Context, regionID string) (int32, bool, error)
	}
	type currencyReader interface {
		CurrencyDecimalDigits(ctx context.Context, currencyCode string) (int32, error)
	}

	svc, _ := newTestService(t)

	var (
		_ regionResolver       = svc
		_ regionCurrencyReader = svc
		_ regionTaxReader      = svc
		_ currencyReader       = svc
	)
}

// TestRegionIDForCountry proves the narrow surface that leads from a country to
// a region id.
func TestRegionIDForCountry(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	region := newRegion(t, svc, "TRY")
	_, err := svc.AddCountryToRegion(ctx, region.ID, "TR")
	require.NoError(t, err)

	id, err := svc.RegionIDForCountry(ctx, "tr")
	require.NoError(t, err)
	assert.Equal(t, region.ID, id)

	_, err = svc.RegionIDForCountry(ctx, "DE")
	require.Error(t, err)
	assert.Equal(t, CodeCountryUnassigned, errors.CodeOf(err))
}

// TestRegionCurrencyReturnsDecimalDigits proves that the region's currency comes
// back together with its number of DECIMAL DIGITS.
//
// Without the digit count the cart cannot know by which factor to present the
// minor unit integer; a presentation layer assuming a fixed 100 would show yen
// amounts a hundred times too small.
func TestRegionCurrencyReturnsDecimalDigits(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	tryRegion := newRegion(t, svc, "TRY")
	jpyRegion := newRegion(t, svc, "JPY")
	kwdRegion := newRegion(t, svc, "KWD")

	code, digits, err := svc.RegionCurrency(ctx, tryRegion.ID)
	require.NoError(t, err)
	assert.Equal(t, "TRY", code)
	assert.Equal(t, int32(2), digits)

	code, digits, err = svc.RegionCurrency(ctx, jpyRegion.ID)
	require.NoError(t, err)
	assert.Equal(t, "JPY", code)
	assert.Equal(t, int32(0), digits, "JPY has no decimal digits")

	code, digits, err = svc.RegionCurrency(ctx, kwdRegion.ID)
	require.NoError(t, err)
	assert.Equal(t, "KWD", code)
	assert.Equal(t, int32(3), digits, "KWD has three decimal digits")
}

// TestRegionCurrencyRejectsUnknownRegion proves that not found is returned for a
// region that does not exist.
func TestRegionCurrencyRejectsUnknownRegion(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	_, _, err := svc.RegionCurrency(ctx, "reg_MISSING")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	_, _, err = svc.RegionCurrency(ctx, "cart_01")
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "a wrong prefix is a validation error")
}

// TestRegionTaxReturnsBasisPoints proves that the tax rate comes back as a BASIS
// POINT integer and that it carries the automatic tax flag.
func TestRegionTaxReturnsBasisPoints(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	region := newRegion(t, svc, "TRY")

	rate, automatic, err := svc.RegionTax(ctx, region.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(2000), rate, "20% = 2000 basis points")
	assert.True(t, automatic)

	// Tax is computed with integer arithmetic: for 19.99 TRY (1999 minor
	// units), 1999 * 2000 / 10000 = 399 minor units. The same computation with
	// a float rate would silently drift at the minor-unit level.
	const subtotal int64 = 1999
	tax := subtotal * int64(rate) / int64(models.MaxTaxRate)
	assert.Equal(t, int64(399), tax)

	off := false
	zero := int32(0)
	_, err = svc.UpdateRegion(ctx, region.ID, UpdateRegionInput{AutomaticTaxes: &off, TaxRate: &zero})
	require.NoError(t, err)

	rate, automatic, err = svc.RegionTax(ctx, region.ID)
	require.NoError(t, err)
	assert.Zero(t, rate)
	assert.False(t, automatic)
}

// TestCurrencyDecimalDigits proves reading the decimal digits by currency code.
func TestCurrencyDecimalDigits(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	digits, err := svc.CurrencyDecimalDigits(ctx, "jpy")
	require.NoError(t, err)
	assert.Zero(t, digits)

	digits, err = svc.CurrencyDecimalDigits(ctx, "KWD")
	require.NoError(t, err)
	assert.Equal(t, int32(3), digits)

	_, err = svc.CurrencyDecimalDigits(ctx, "XYZ")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}
