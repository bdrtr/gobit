//go:build integration

package product_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestOneCurrencysCostIsWrittenOnTheRealSchema is the panel's cost write
// (ADR 0412) on the real schema: a currency is set from none, changed from what
// was read and cleared, the other currency's row stays, and a write drawn from
// a cost that moved is refused and writes nothing.
func TestOneCurrencysCostIsWrittenOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	variant := seedCostedVariant(t, svc, "kettle").Variants[0].ID
	_, err := svc.SetVariantCosts(ctx, variant, []models.VariantCost{{CurrencyCode: "USD", Amount: 7}})
	require.NoError(t, err)
	usd := models.VariantCost{CurrencyCode: "USD", Amount: 7}
	cost := func(v int64) *int64 { return &v }

	require.NoError(t, svc.SetVariantCost(ctx, variant, "try", nil, cost(400)))
	assert.Equal(t, []models.VariantCost{{CurrencyCode: "TRY", Amount: 400}, usd}, storedCosts(t, variant))
	require.NoError(t, svc.SetVariantCost(ctx, variant, "TRY", cost(400), cost(450)))
	assert.Equal(t, []models.VariantCost{{CurrencyCode: "TRY", Amount: 450}, usd}, storedCosts(t, variant))

	err = svc.SetVariantCost(ctx, variant, "TRY", cost(400), cost(500))
	require.Error(t, err)
	assert.Equal(t, service.CodeVariantCostMoved, errors.CodeOf(err))
	assert.Equal(t, []models.VariantCost{{CurrencyCode: "TRY", Amount: 450}, usd}, storedCosts(t, variant),
		"a refused write moves no cost")

	require.NoError(t, svc.SetVariantCost(ctx, variant, "TRY", cost(450), nil))
	assert.Equal(t, []models.VariantCost{usd}, storedCosts(t, variant), "clearing drops the named currency alone")
}

// TestACostWrittenMeanwhileRefusesTheWriteDrawnBeforeIt is ADR 0412's compare
// under the variant's lock: a writer holds the row and writes a cost in TRY;
// a panel write drawn when TRY had none waits for it, then reads the cost the
// first one committed and is refused. A compare made before the lock reads
// none, waits, and overwrites the first writer's cost. What is asserted is the
// outcome and the cost left behind, not the wait.
func TestACostWrittenMeanwhileRefusesTheWriteDrawnBeforeIt(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	variant := seedCostedVariant(t, svc, "kettle").Variants[0].ID

	first, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = first.Rollback(context.Background()) }()
	var firstPID int32
	require.NoError(t, first.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&firstPID))
	_, err = first.Exec(ctx, `SELECT id FROM product_variant WHERE id = $1 FOR NO KEY UPDATE`, variant)
	require.NoError(t, err)
	_, err = first.Exec(ctx,
		`INSERT INTO product_variant_cost (variant_id, currency_code, amount) VALUES ($1, 'TRY', 400)`, variant)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		amount := int64(500)
		done <- svc.SetVariantCost(ctx, variant, "TRY", nil, &amount)
	}()

	// The panel's write either finishes on its own or is seen waiting on the
	// first; either way the first commits next. The loop asserts nothing.
	var secondErr error
	finished := false
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		select {
		case secondErr = <-done:
			finished = true
		default:
		}
		if finished {
			break
		}
		var waiting bool
		if err := testPool.Pool().QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid)))`,
			firstPID).Scan(&waiting); err == nil && waiting {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NoError(t, first.Commit(ctx))
	if !finished {
		secondErr = <-done
	}

	require.Error(t, secondErr, "the write drawn when TRY had none is refused once TRY holds a cost")
	assert.Equal(t, service.CodeVariantCostMoved, errors.CodeOf(secondErr))
	assert.Equal(t, []models.VariantCost{{CurrencyCode: "TRY", Amount: 400}}, storedCosts(t, variant),
		"the first writer's cost stands")
}
