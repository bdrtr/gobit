package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// TestAStackMemberIsNotRaisedPastItsLine is gap D237: a value written to a
// rate in a stack is checked as creation checks a new member, through the
// update and the panel's correction, whichever member it is, and a refused
// value is not written.
func TestAStackMemberIsNotRaisedPastItsLine(t *testing.T) {
	ctx := context.Background()
	raise := map[string]func(svc *Service, id string, from, to int32) error{
		"update": func(svc *Service, id string, _, to int32) error {
			_, err := svc.UpdateTaxRate(ctx, id, UpdateTaxRateInput{RateBps: &to})
			return err
		},
		"revise": func(svc *Service, id string, from, to int32) error {
			_, err := svc.ReviseTaxRate(ctx, id,
				models.TaxRateTerms{Name: "test", RateBps: from}, models.TaxRateTerms{Name: "test", RateBps: to})
			return err
		},
	}

	for name, write := range raise {
		t.Run(name, func(t *testing.T) {
			svc, repo := newTestService(t)
			repo.seedRootRegion(trRegionID, "TR")
			repo.seedRate(models.TaxRate{ID: rateA, TaxRegionID: trRegionID, Name: "test", RateBps: 6000, IsDefault: true})
			base := rateA
			repo.seedRate(models.TaxRate{ID: rateB, TaxRegionID: trRegionID, Name: "test", RateBps: 3000, StacksOnID: &base})
			repo.seedRate(models.TaxRate{ID: rateC, TaxRegionID: trRegionID, Name: "test", RateBps: 1000})

			for id, value := range map[string]int32{rateB: 6000, rateA: 7001} {
				err := write(svc, id, repo.rates[id].RateBps, value)
				require.Error(t, err, "%s raised to %d", id, value)
				assert.Equal(t, CodeStackExceedsBase, errors.CodeOf(err), "%s: %v", id, err)
			}
			assert.Equal(t, int32(6000), repo.rates[rateA].RateBps, "a refused value is not written")
			assert.Equal(t, int32(3000), repo.rates[rateB].RateBps, "a refused value is not written")

			require.NoError(t, write(svc, rateB, 3000, 4000), "the two together take exactly the line")
			require.NoError(t, write(svc, rateC, 1000, 10000), "a rate in no stack is bounded by itself alone")
			assert.Positive(t, repo.calls["LockTaxRegionForWrite"], "the value is checked under the region's write lock")
		})
	}
}
