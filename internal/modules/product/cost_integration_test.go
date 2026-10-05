//go:build integration

package product_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// seedCostedVariant creates a published product with one variant and returns
// the product.
func seedCostedVariant(t *testing.T, svc *service.Service, prefix string) models.Product {
	t.Helper()
	p, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Handle: uniqueHandle(prefix), Title: prefix, Status: models.StatusPublished,
		Variants: []service.CreateVariantInput{{Title: "One size"}},
	})
	require.NoError(t, err)
	require.Len(t, p.Variants, 1)
	return p
}

// storedCosts reads a variant's cost rows straight from the table, in currency
// order.
func storedCosts(t *testing.T, variantID string) []models.VariantCost {
	t.Helper()
	rows, err := testPool.Pool().Query(context.Background(),
		`SELECT currency_code, amount FROM product_variant_cost WHERE variant_id = $1 ORDER BY currency_code`,
		variantID)
	require.NoError(t, err)
	defer rows.Close()
	out := []models.VariantCost{}
	for rows.Next() {
		var c models.VariantCost
		require.NoError(t, rows.Scan(&c.CurrencyCode, &c.Amount))
		out = append(out, c)
	}
	require.NoError(t, rows.Err())
	return out
}

// TestTheVariantCostSchemaRefuses holds the table's constraints (ADR 0401) with
// raw statements, each refusal naming its constraint, and the bounds
// themselves accepted.
func TestTheVariantCostSchemaRefuses(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	variant := seedCostedVariant(t, svc, "kettle").Variants[0].ID
	insert := `INSERT INTO product_variant_cost (variant_id, currency_code, amount) VALUES ($1, $2, $3)`

	_, err := testPool.Pool().Exec(ctx, insert, variant, "TRY", int64(0))
	require.NoError(t, err, "a cost of zero is a cost")
	_, err = testPool.Pool().Exec(ctx, insert, variant, "EUR", int64(1_000_000_000_000))
	require.NoError(t, err, "the bound is a cost")

	for name, tc := range map[string]struct {
		args       []any
		constraint string
	}{
		"a negative amount":        {[]any{variant, "USD", int64(-1)}, "product_variant_cost_amount_check"},
		"an amount past the bound": {[]any{variant, "USD", int64(1_000_000_000_001)}, "product_variant_cost_amount_check"},
		"a lower-case currency":    {[]any{variant, "try", int64(1)}, "product_variant_cost_currency_check"},
		"a currency twice":         {[]any{variant, "TRY", int64(1)}, "product_variant_cost_pkey"},
		"an unknown variant":       {[]any{"variant_missing", "USD", int64(1)}, "product_variant_cost_variant_id_fkey"},
	} {
		_, err := testPool.Pool().Exec(ctx, insert, tc.args...)
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr, name)
		assert.Equal(t, tc.constraint, pgErr.ConstraintName, name)
	}
}

// TestACostWriteReplacesTheWholeSet is the replace on the real repository: a
// second write leaves only what it names, at its amounts, and an empty one
// clears the rows.
func TestACostWriteReplacesTheWholeSet(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	variant := seedCostedVariant(t, svc, "kettle").Variants[0].ID

	written, err := svc.SetVariantCosts(ctx, variant, []models.VariantCost{
		{CurrencyCode: "TRY", Amount: 100}, {CurrencyCode: "eur", Amount: 5},
	})
	require.NoError(t, err)
	want := []models.VariantCost{{CurrencyCode: "EUR", Amount: 5}, {CurrencyCode: "TRY", Amount: 100}}
	assert.Equal(t, want, written)
	assert.Equal(t, want, storedCosts(t, variant))

	_, err = svc.SetVariantCosts(ctx, variant, []models.VariantCost{{CurrencyCode: "TRY", Amount: 120}})
	require.NoError(t, err)
	assert.Equal(t, []models.VariantCost{{CurrencyCode: "TRY", Amount: 120}}, storedCosts(t, variant),
		"a write replaces the rows: EUR is gone and TRY carries the new amount")

	cleared, err := svc.SetVariantCosts(ctx, variant, nil)
	require.NoError(t, err)
	assert.Empty(t, cleared)
	assert.Empty(t, storedCosts(t, variant))
}

// TestTwoCostWritesLeaveOneWholeSet: a transaction writing a variant's costs
// holds the variant's row; a second write waits for it and replaces what it
// wrote, so the variant ends with one writer's list rather than both merged.
// The two lists name different currencies, so nothing but the row lock can
// make the second wait: without it the two would commit side by side. What is
// asserted is the final set, not the wait.
func TestTwoCostWritesLeaveOneWholeSet(t *testing.T) {
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
	_, err = first.Exec(ctx, `DELETE FROM product_variant_cost WHERE variant_id = $1`, variant)
	require.NoError(t, err)
	_, err = first.Exec(ctx,
		`INSERT INTO product_variant_cost (variant_id, currency_code, amount) VALUES ($1, 'TRY', 400)`, variant)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, err := svc.SetVariantCosts(ctx, variant, []models.VariantCost{{CurrencyCode: "EUR", Amount: 5}})
		done <- err
	}()

	// The second write either finishes on its own or is seen waiting on the
	// first; either way the first commits next. The loop asserts nothing: what
	// decides the test is the set left behind.
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

	require.NoError(t, secondErr)
	assert.Equal(t, []models.VariantCost{{CurrencyCode: "EUR", Amount: 5}}, storedCosts(t, variant),
		"the later write replaces the earlier one whole; TRY beside EUR is two writers' lists merged")
}

// TestAStorefrontProductCarriesNoCost: a variant's cost is the shop's business.
// The storefront's product, read by id and in the listing, carries no key
// naming a cost at any depth and not the planted amount anywhere.
func TestAStorefrontProductCarriesNoCost(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	product := seedCostedVariant(t, svc, "kettle")
	const planted = 987_654_321
	_, err := svc.SetVariantCosts(ctx, product.Variants[0].ID, []models.VariantCost{{CurrencyCode: "TRY", Amount: planted}})
	require.NoError(t, err)

	single, err := svc.GetStoreProduct(ctx, product.ID, nil)
	require.NoError(t, err)
	page, err := svc.ListStoreProducts(ctx, service.StoreListOptions{VariantIDs: []string{product.Variants[0].ID}})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)

	for name, body := range map[string]any{"by id": single, "listing": page.Items[0]} {
		raw, err := json.Marshal(body)
		require.NoError(t, err, name)
		require.Contains(t, string(raw), product.Variants[0].ID, "%s: the variant is in the body", name)
		assert.NotContains(t, string(raw), "987654321", "%s: the cost's amount reached the storefront", name)

		var decoded any
		require.NoError(t, json.Unmarshal(raw, &decoded), name)
		assert.Empty(t, keysNaming(decoded, "cost"), "%s: a key naming a cost reached the storefront", name)
	}
}

// keysNaming returns every object key, at any depth, that contains the word.
func keysNaming(value any, word string) []string {
	var out []string
	switch v := value.(type) {
	case map[string]any:
		for key, inner := range v {
			if strings.Contains(strings.ToLower(key), word) {
				out = append(out, key)
			}
			out = append(out, keysNaming(inner, word)...)
		}
	case []any:
		for _, inner := range v {
			out = append(out, keysNaming(inner, word)...)
		}
	}
	return out
}
