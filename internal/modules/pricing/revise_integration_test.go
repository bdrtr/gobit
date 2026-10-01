//go:build integration

package pricing_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// TestAUnitPriceWriteKeepsAConcurrentWriteToAnotherCurrency is D193 over the
// real schema, with the interleaving forced rather than hoped for: a write to
// one currency's base price waits on the set's lock while another currency's
// price changes and commits, and when it goes on the other change is still
// there. Read before the lock, the waiting write had already read the other
// currency's old price and wrote it back.
func TestAUnitPriceWriteKeepsAConcurrentWriteToAnotherCurrency(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{
		{CurrencyCode: "TRY", Amount: 10_000},
		{CurrencyCode: "USD", Amount: 1_000},
	})
	require.NoError(t, err)

	// The other writer holds the set's lock, as ReplacePrices takes it.
	holder, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = holder.Rollback(context.Background()) })
	var holderPID int32
	require.NoError(t, holder.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID))
	_, err = holder.Exec(ctx, `SELECT id FROM price_set WHERE id = $1 FOR UPDATE`, set.ID)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, writeErr := svc.SetUnitBasePrices(ctx, set.ID, map[string]int64{"TRY": 20_000})
		done <- writeErr
	}()

	// The write is waiting on the lock — whatever it read before it, it read
	// already.
	require.Eventually(t, func() bool {
		var waiting int
		if err := testPool.Pool().QueryRow(ctx,
			`SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND wait_event_type = 'Lock'
			   AND $1 = ANY(pg_blocking_pids(pid))`, holderPID).Scan(&waiting); err != nil {
			return false
		}
		return waiting > 0
	}, 10*time.Second, 20*time.Millisecond, "the unit price write never waited on the set's lock")

	// The other writer's change to USD commits while the first waits.
	_, err = holder.Exec(ctx, `UPDATE price SET amount = 2000 WHERE price_set_id = $1 AND currency_code = 'USD'`, set.ID)
	require.NoError(t, err)
	require.NoError(t, holder.Commit(ctx))

	select {
	case writeErr := <-done:
		require.NoError(t, writeErr)
	case <-time.After(10 * time.Second):
		t.Fatal("the unit price write did not finish once the lock was released")
	}

	prices, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	amounts := map[string]int64{}
	for i := range prices {
		amounts[prices[i].CurrencyCode] = prices[i].Amount
	}
	assert.Equal(t, int64(20_000), amounts["TRY"], "the waiting write's own change")
	assert.Equal(t, int64(2_000), amounts["USD"],
		"the change committed while the write waited is kept, not overwritten with what was read before the lock")
}

// TestTwoSavesDrawnAtOnePriceDoNotOverwriteEachOther is ADR 0280 over the real
// schema: two forms drawn at the same amount save one after the other, the
// first is written, and the second is refused rather than overwriting what it
// never saw.
func TestTwoSavesDrawnAtOnePriceDoNotOverwriteEachOther(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	surface := service.NewAdminSurface(svc)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{{CurrencyCode: "TRY", Amount: 10_000}})
	require.NoError(t, err)

	require.NoError(t, surface.SetBasePriceAmount(ctx, set.ID, "TRY", 10_000, 12_000))
	err = surface.SetBasePriceAmount(ctx, set.ID, "TRY", 10_000, 11_000)
	require.Error(t, err)
	assert.Equal(t, service.CodePriceMoved, errors.CodeOf(err))

	prices, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, prices, 1)
	assert.Equal(t, int64(12_000), prices[0].Amount, "the first save stands")
}
