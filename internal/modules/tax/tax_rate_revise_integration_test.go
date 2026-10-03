//go:build integration

package tax_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

// TestARateIsRevisedOnlyFromWhatWasRead is ADR 0378 against the real schema:
// the statement writes the name and the rate while they are the ones read,
// leaves the code and the default alone, writes nothing on a deleted rate, and
// of two corrections drawn from one reading exactly one is written.
func TestARateIsRevisedOnlyFromWhatWasRead(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	root := newRootRegion(ctx, t, svc)
	rate, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: root.ID, Name: "VAT", Code: "VAT20", RateBps: 2000, IsDefault: true,
	})
	require.NoError(t, err)
	read := models.TaxRateTerms{Name: "VAT", RateBps: 2000}

	revised, err := svc.ReviseTaxRate(ctx, rate.ID, read, models.TaxRateTerms{Name: "VAT", RateBps: 1800})
	require.NoError(t, err)
	assert.Equal(t, int32(1800), revised.RateBps)
	stored, err := svc.GetTaxRate(ctx, rate.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(1800), stored.RateBps)
	require.NotNil(t, stored.Code)
	assert.Equal(t, "VAT20", *stored.Code, "the code is not touched")
	assert.True(t, stored.IsDefault, "the default is not touched")

	_, err = svc.ReviseTaxRate(ctx, rate.ID, read, read)
	assert.Equal(t, service.CodeTaxRateRevised, errors.CodeOf(err), "a stale reading writes nothing: %v", err)

	// A reading whose rate still matches but whose name does not is as stale.
	_, err = svc.ReviseTaxRate(ctx, rate.ID, models.TaxRateTerms{Name: "VAT", RateBps: 1800},
		models.TaxRateTerms{Name: "Standard", RateBps: 1800})
	require.NoError(t, err)
	_, err = svc.ReviseTaxRate(ctx, rate.ID, models.TaxRateTerms{Name: "VAT", RateBps: 1800},
		models.TaxRateTerms{Name: "VAT", RateBps: 1700})
	assert.Equal(t, service.CodeTaxRateRevised, errors.CodeOf(err), "the name was read stale: %v", err)

	// Two operators correcting from the same reading write once.
	now := models.TaxRateTerms{Name: "Standard", RateBps: 1800}
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i, next := range []models.TaxRateTerms{{Name: "Standard", RateBps: 1000}, {Name: "Standard", RateBps: 800}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = svc.ReviseTaxRate(ctx, rate.ID, now, next)
		}()
	}
	wg.Wait()
	written := 0
	for _, err := range results {
		if err == nil {
			written++
			continue
		}
		assert.Equal(t, service.CodeTaxRateRevised, errors.CodeOf(err))
	}
	assert.Equal(t, 1, written, "one correction of a reading is written")

	// A deleted rate is not there to correct, even from what it holds now.
	current, err := svc.GetTaxRate(ctx, rate.ID)
	require.NoError(t, err)
	held := models.TaxRateTerms{Name: current.Name, RateBps: current.RateBps}
	require.NoError(t, svc.DeleteTaxRate(ctx, rate.ID))
	_, err = svc.ReviseTaxRate(ctx, rate.ID, held, held)
	assert.True(t, errors.IsNotFound(err), "a deleted rate is not there to correct: %v", err)
}
