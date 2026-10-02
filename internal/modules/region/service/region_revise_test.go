package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// TestARegionIsRevisedFromWhatWasRead is ADR 0362: the name, automatic taxes
// and tax rate are written from the ones read, the name trimmed and the
// currency kept; a name or rate the update refuses reaches nothing; nothing
// written is a refusal naming that it was revised since, or that the region
// is not there.
func TestARegionIsRevisedFromWhatWasRead(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService(t)
	region := newRegion(t, svc, "TRY")
	read := region.Terms()

	revised, err := svc.ReviseRegion(ctx, region.ID, read,
		models.RegionTerms{Name: "  Turkey  ", AutomaticTaxes: false, TaxRate: 1800})
	require.NoError(t, err)
	assert.Equal(t, models.RegionTerms{Name: "Turkey", AutomaticTaxes: false, TaxRate: 1800}, revised.Terms())
	assert.Equal(t, "TRY", revised.CurrencyCode, "the currency is kept")

	_, err = svc.ReviseRegion(ctx, region.ID, read, read)
	require.Error(t, err)
	assert.Equal(t, CodeRegionRevised, errors.CodeOf(err), "read before the revision: %v", err)

	calls := repo.calls["ReviseRegion"]
	for label, next := range map[string]models.RegionTerms{
		"an empty name": {Name: " ", TaxRate: 0},
		"a long name":   {Name: strings.Repeat("n", 300), TaxRate: 0},
		"a rate above":  {Name: "Turkey", TaxRate: 10001},
		"a rate below":  {Name: "Turkey", TaxRate: -1},
	} {
		_, err = svc.ReviseRegion(ctx, region.ID, revised.Terms(), next)
		assert.True(t, errors.IsInvalid(err), "%s: %v", label, err)
	}
	_, err = svc.ReviseRegion(ctx, "cus_1", revised.Terms(), revised.Terms())
	assert.True(t, errors.IsInvalid(err), "an id that is not a region's: %v", err)
	assert.Equal(t, calls, repo.calls["ReviseRegion"], "nothing refused reached the store")

	_, err = svc.ReviseRegion(ctx, "reg_MISSING", revised.Terms(), revised.Terms())
	assert.True(t, errors.IsNotFound(err), "a region that is not there: %v", err)
}

// TestThePanelRevisesARegion is ADR 0362 through the surface: the read and
// the written terms cross as JSON under the provider's field names.
func TestThePanelRevisesARegion(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	surface := NewAdminSurface(svc)
	region := newRegion(t, svc, "TRY")

	read := json.RawMessage(`{"name":"` + region.Name + `","automatic_taxes":true,"tax_rate":2000}`)
	require.NoError(t, surface.ReviseRegion(ctx, region.ID, read,
		json.RawMessage(`{"name":"Turkey","automatic_taxes":false,"tax_rate":1000}`)))
	stored, err := svc.GetRegion(ctx, region.ID)
	require.NoError(t, err)
	assert.Equal(t, models.RegionTerms{Name: "Turkey", AutomaticTaxes: false, TaxRate: 1000}, stored.Terms())

	err = surface.ReviseRegion(ctx, region.ID, read, read)
	assert.Equal(t, CodeRegionRevised, errors.CodeOf(err))
	err = surface.ReviseRegion(ctx, region.ID, json.RawMessage(`[`), read)
	assert.True(t, errors.IsInvalid(err))
}
