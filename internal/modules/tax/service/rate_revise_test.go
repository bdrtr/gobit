package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// TestARateIsRevisedFromWhatWasRead is ADR 0378: the name and the rate are
// written from the ones read, the name trimmed and the code and default kept;
// a name or rate the update refuses reaches nothing; nothing written is a
// refusal naming that it was revised since, or that the rate is not there.
func TestARateIsRevisedFromWhatWasRead(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	code := "KDV20"
	repo.seedRate(models.TaxRate{ID: rateA, TaxRegionID: trRegionID, Name: "VAT", Code: &code, RateBps: 2000, IsDefault: true})
	read := models.TaxRateTerms{Name: "VAT", RateBps: 2000}

	revised, err := svc.ReviseTaxRate(ctx, rateA, read, models.TaxRateTerms{Name: "  Standard VAT  ", RateBps: 1800})
	require.NoError(t, err)
	assert.Equal(t, "Standard VAT", revised.Name)
	assert.Equal(t, int32(1800), revised.RateBps)
	require.NotNil(t, revised.Code)
	assert.Equal(t, "KDV20", *revised.Code, "the code is kept")
	assert.True(t, revised.IsDefault, "the default is kept")

	_, err = svc.ReviseTaxRate(ctx, rateA, read, read)
	require.Error(t, err)
	assert.Equal(t, CodeTaxRateRevised, errors.CodeOf(err), "read before the revision: %v", err)

	now := models.TaxRateTerms{Name: revised.Name, RateBps: revised.RateBps}
	calls := repo.calls["ReviseTaxRate"]
	for label, next := range map[string]models.TaxRateTerms{
		"an empty name": {Name: " ", RateBps: 0},
		"a long name":   {Name: strings.Repeat("n", 300), RateBps: 0},
		"a rate above":  {Name: "VAT", RateBps: 10001},
		"a rate below":  {Name: "VAT", RateBps: -1},
	} {
		_, err = svc.ReviseTaxRate(ctx, rateA, now, next)
		assert.True(t, errors.IsInvalid(err), "%s: %v", label, err)
	}
	_, err = svc.ReviseTaxRate(ctx, "reg_1", now, now)
	assert.True(t, errors.IsInvalid(err), "an id that is not a rate's: %v", err)
	assert.Equal(t, calls, repo.calls["ReviseTaxRate"], "nothing refused reached the store")

	_, err = svc.ReviseTaxRate(ctx, rateB, now, now)
	assert.True(t, errors.IsNotFound(err), "a rate that is not there: %v", err)
}

// TestThePanelRevisesARate is ADR 0378 through the surface: the read and the
// written terms cross as JSON under the provider's field names.
func TestThePanelRevisesARate(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService(t)
	surface := NewAdminSurface(svc)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedRate(models.TaxRate{ID: rateA, TaxRegionID: trRegionID, Name: "VAT", RateBps: 2000, IsDefault: true})

	read := json.RawMessage(`{"name":"VAT","rate_bps":2000}`)
	require.NoError(t, surface.ReviseTaxRate(ctx, rateA, read, json.RawMessage(`{"name":"Reduced","rate_bps":1000}`)))
	stored, err := svc.GetTaxRate(ctx, rateA)
	require.NoError(t, err)
	assert.Equal(t, "Reduced", stored.Name)
	assert.Equal(t, int32(1000), stored.RateBps)

	err = surface.ReviseTaxRate(ctx, rateA, read, read)
	assert.Equal(t, CodeTaxRateRevised, errors.CodeOf(err))
	err = surface.ReviseTaxRate(ctx, rateA, json.RawMessage(`[`), read)
	assert.True(t, errors.IsInvalid(err))
}
