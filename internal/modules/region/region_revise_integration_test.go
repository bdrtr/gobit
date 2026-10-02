//go:build integration

package region_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
	"github.com/bdrtr/gobit/internal/modules/region/service"
)

// TestARegionIsRevisedOnlyAsItWasRead is ADR 0362 against a real PostgreSQL:
// the name, automatic taxes and tax rate are written while they are the ones
// read and the currency is kept; a term read wrong, and a deleted region,
// write nothing.
func TestARegionIsRevisedOnlyAsItWasRead(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	region := newRegion(ctx, t, svc, "TRY")
	read := region.Terms()

	next := models.RegionTerms{Name: t.Name() + " revised", AutomaticTaxes: false, TaxRate: 1800}
	_, err := svc.ReviseRegion(ctx, region.ID, read, next)
	require.NoError(t, err)
	stored, err := svc.GetRegion(ctx, region.ID)
	require.NoError(t, err)
	assert.Equal(t, next, stored.Terms(), "every term written")
	assert.Equal(t, "TRY", stored.CurrencyCode, "the currency kept")
	assert.True(t, stored.UpdatedAt.After(region.UpdatedAt), "the moment it was written moves")

	for label, stale := range map[string]models.RegionTerms{
		"a name read before":          {Name: read.Name, AutomaticTaxes: next.AutomaticTaxes, TaxRate: next.TaxRate},
		"automatic taxes read before": {Name: next.Name, AutomaticTaxes: read.AutomaticTaxes, TaxRate: next.TaxRate},
		"a tax rate read before":      {Name: next.Name, AutomaticTaxes: next.AutomaticTaxes, TaxRate: read.TaxRate},
	} {
		_, err = svc.ReviseRegion(ctx, region.ID, stale, read)
		require.Error(t, err, label)
		assert.Equal(t, service.CodeRegionRevised, errors.CodeOf(err), "%s: %v", label, err)
	}
	stored, err = svc.GetRegion(ctx, region.ID)
	require.NoError(t, err)
	assert.Equal(t, next, stored.Terms(), "nothing was written")

	require.NoError(t, svc.DeleteRegion(ctx, region.ID))
	_, err = svc.ReviseRegion(ctx, region.ID, next, read)
	assert.True(t, errors.IsNotFound(err), "a deleted region: %v", err)
}
