//go:build integration

package tax_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

// newStack opens a region holding a 6000 bps default with a 3000 bps rate
// standing on it.
func newStack(ctx context.Context, t *testing.T, svc *service.Service) (base, top models.TaxRate) {
	t.Helper()

	region := newRootRegion(ctx, t, svc)
	base, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: region.ID, Name: "state", RateBps: 6000, IsDefault: true,
	})
	require.NoError(t, err)
	top, err = svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: region.ID, Name: "county", RateBps: 3000, StacksOnID: base.ID,
	})
	require.NoError(t, err)

	return base, top
}

// TestAStackMemberIsNotRaisedPastItsLineInTheSchema is gap D237 against the
// real schema: the update and the correction refuse a value that makes the
// stack take more than its line, and the stored value stays.
func TestAStackMemberIsNotRaisedPastItsLineInTheSchema(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	base, top := newStack(ctx, t, svc)

	raised := int32(6000)
	_, err := svc.UpdateTaxRate(ctx, top.ID, service.UpdateTaxRateInput{RateBps: &raised})
	require.Error(t, err)
	assert.Equal(t, service.CodeStackExceedsBase, errors.CodeOf(err))
	_, err = svc.ReviseTaxRate(ctx, base.ID,
		models.TaxRateTerms{Name: "state", RateBps: 6000}, models.TaxRateTerms{Name: "state", RateBps: 7001})
	require.Error(t, err)
	assert.Equal(t, service.CodeStackExceedsBase, errors.CodeOf(err))

	for id, want := range map[string]int32{base.ID: 6000, top.ID: 3000} {
		stored, err := svc.GetTaxRate(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, want, stored.RateBps, "a refused value is not written")
	}

	lowered := int32(4000)
	_, err = svc.UpdateTaxRate(ctx, top.ID, service.UpdateTaxRateInput{RateBps: &lowered})
	require.NoError(t, err, "the two together take exactly the line")
}

// TestTwoRaisesOfOneStackAreCheckedOneAfterTheOther is the lock D237 takes:
// each of two raises fits the stack alone and the two together do not. An open
// transaction holds both rows. Under the region's write lock the first writer
// takes the region and waits at its row write, and the second waits at the
// region before its check; once the rows are released the first commits, and
// the second checks against it and is refused. Under a shared lock both would
// check against the other's old value and wait at their rows, and both would
// be written. The outcome is asserted, not the waiting.
func TestTwoRaisesOfOneStackAreCheckedOneAfterTheOther(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	base, top := newStack(ctx, t, svc)

	hold, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = hold.Rollback(ctx) }()
	_, err = hold.Exec(ctx, `SELECT id FROM tax_rate WHERE id = ANY($1) FOR UPDATE`, []string{base.ID, top.ID})
	require.NoError(t, err)

	raises := map[string]int32{base.ID: 6600, top.ID: 3500}
	results := make(chan error, len(raises))
	var wg sync.WaitGroup
	for id, value := range raises {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, raiseErr := svc.UpdateTaxRate(ctx, id, service.UpdateTaxRateInput{RateBps: &value})
			results <- raiseErr
		}()
	}

	var waiting int64
	require.Eventually(t, func() bool {
		scanErr := testPool.Pool().QueryRow(ctx,
			`SELECT count(*) FROM pg_stat_activity
             WHERE wait_event_type = 'Lock' AND query ILIKE '%FOR UPDATE%'
               AND query NOT ILIKE '%pg_stat_activity%'`).Scan(&waiting)
		return scanErr == nil && waiting >= int64(len(raises))
	}, 10*time.Second, 20*time.Millisecond)
	assert.Equal(t, int64(len(raises)), waiting, "both writers are held")

	require.NoError(t, hold.Rollback(ctx))
	wg.Wait()
	close(results)

	written := 0
	for raiseErr := range results {
		if raiseErr == nil {
			written++
			continue
		}
		assert.Equal(t, service.CodeStackExceedsBase, errors.CodeOf(raiseErr), "%v", raiseErr)
	}
	assert.Equal(t, 1, written, "one raise is written and the other is checked against it")

	storedBase, err := svc.GetTaxRate(ctx, base.ID)
	require.NoError(t, err)
	storedTop, err := svc.GetTaxRate(ctx, top.ID)
	require.NoError(t, err)
	assert.LessOrEqual(t, storedBase.RateBps+storedTop.RateBps, int32(10_000), "the stack never takes more than its line")
}
