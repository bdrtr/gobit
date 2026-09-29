//go:build integration

package product_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// seedBundleParts creates a bundle product and two component products, each
// with one variant.
func seedBundleParts(t *testing.T, svc *service.Service) (box, soap, towel models.Product) {
	t.Helper()
	seed := func(prefix string) models.Product {
		p, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
			Handle: uniqueHandle(prefix), Title: prefix, Status: models.StatusPublished,
			Variants: []service.CreateVariantInput{{Title: "One size"}},
		})
		require.NoError(t, err)
		require.Len(t, p.Variants, 1)
		return p
	}
	return seed("box"), seed("soap"), seed("towel")
}

// storedComponents reads a bundle's rows straight from the table, in rank
// order.
func storedComponents(t *testing.T, bundleID string) []models.BundleComponent {
	t.Helper()
	rows, err := testPool.Pool().Query(context.Background(),
		`SELECT component_variant_id, quantity FROM product_bundle_component
		 WHERE bundle_variant_id = $1 ORDER BY rank`, bundleID)
	require.NoError(t, err)
	defer rows.Close()
	out := []models.BundleComponent{}
	for rows.Next() {
		var c models.BundleComponent
		require.NoError(t, rows.Scan(&c.VariantID, &c.Quantity))
		out = append(out, c)
	}
	require.NoError(t, rows.Err())
	return out
}

// variantIsLive reports whether the variant's row carries no deletion.
func variantIsLive(t *testing.T, id string) bool {
	t.Helper()
	var live bool
	require.NoError(t, testPool.Pool().QueryRow(context.Background(),
		`SELECT deleted_at IS NULL FROM product_variant WHERE id = $1`, id).Scan(&live))
	return live
}

// TestABundleRoundTripsOnTheRealSchema is ADR 0234 on the real schema: the
// composition is written in the operator's order, read back on the product,
// carried by the revision, and taken with the bundle's deletion.
func TestABundleRoundTripsOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	box, soap, towel := seedBundleParts(t, svc)
	bundle := box.Variants[0].ID
	want := []models.BundleComponent{
		{VariantID: towel.Variants[0].ID, Quantity: 1},
		{VariantID: soap.Variants[0].ID, Quantity: 2},
	}

	written, err := svc.SetVariantBundle(ctx, bundle, want)
	require.NoError(t, err)
	assert.Equal(t, want, written)
	assert.Equal(t, want, storedComponents(t, bundle))
	read, err := svc.GetProduct(ctx, box.ID)
	require.NoError(t, err)
	assert.Equal(t, want, read.Variants[0].BundleComponents)
	revisions := revisionRows(t, box.ID)
	assert.Contains(t, string(revisions[len(revisions)-1].Snapshot), `"bundle_components"`)

	_, err = svc.SetVariantBundle(ctx, bundle, want[1:])
	require.NoError(t, err)
	assert.Equal(t, want[1:], storedComponents(t, bundle), "a write replaces the rows")

	require.NoError(t, svc.DeleteVariant(ctx, bundle))
	assert.Empty(t, storedComponents(t, bundle), "the bundle's deletion takes its rows")

	again, err := svc.CreateVariant(ctx, box.ID, service.CreateVariantInput{Title: "Second"})
	require.NoError(t, err)
	_, err = svc.SetVariantBundle(ctx, again.ID, want)
	require.NoError(t, err)
	require.NoError(t, svc.DeleteProduct(ctx, box.ID))
	assert.Empty(t, storedComponents(t, again.ID), "the bundle's product takes its rows")
}

// TestABundleWriteWaitsForAComponentsDeletion: a transaction deleting a
// component holds its row; a bundle write naming it waits, reads it again once
// the deletion commits, and refuses it rather than writing a live bundle of a
// deleted variant.
func TestABundleWriteWaitsForAComponentsDeletion(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	box, soap, _ := seedBundleParts(t, svc)
	component := soap.Variants[0].ID

	tx, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, err = tx.Exec(ctx, `UPDATE product_variant SET deleted_at = now() WHERE id = $1`, component)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, err := svc.SetVariantBundle(ctx, box.Variants[0].ID,
			[]models.BundleComponent{{VariantID: component, Quantity: 1}})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("the bundle write finished while the deletion held the component: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	require.NoError(t, tx.Commit(ctx))

	err = <-done
	require.Error(t, err, "the waiting write sees the component gone")
	assert.True(t, errors.IsInvalid(err), "%v", err)
	assert.Empty(t, storedComponents(t, box.Variants[0].ID), "nothing is written")
}

// TestAComponentsDeletionWaitsForABundleWrite: a transaction writing a bundle
// holds the rows it names; a deletion of its component waits, reads the
// bundles again once the write commits, and is refused rather than leaving a
// live bundle of a deleted variant. The deletion's own UPDATE would wait on the
// same row, so what is asserted is how the wait ends.
func TestAComponentsDeletionWaitsForABundleWrite(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	box, soap, _ := seedBundleParts(t, svc)
	bundle, component := box.Variants[0].ID, soap.Variants[0].ID

	tx, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, err = tx.Exec(ctx, `SELECT id FROM product_variant WHERE id = ANY($1) ORDER BY id FOR NO KEY UPDATE`,
		[]string{bundle, component})
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO product_bundle_component (bundle_variant_id, component_variant_id, quantity, rank)
		VALUES ($1, $2, 1, 0)`, bundle, component)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- svc.DeleteVariant(ctx, component) }()
	select {
	case err := <-done:
		t.Fatalf("the deletion finished while the bundle write held the component: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	require.NoError(t, tx.Commit(ctx))

	err = <-done
	require.Error(t, err, "the waiting deletion sees the bundle")
	assert.True(t, errors.IsConflict(err), "%v", err)
	assert.True(t, variantIsLive(t, component), "the component is not deleted")
}

// TestAProductsDeletionCoversTheVariantItWaitedFor: a transaction holding the
// product's row adds a variant that a bundle holds; the product's deletion
// waits for it and refuses, because it reads the variants again under the
// product's lock rather than before it.
func TestAProductsDeletionCoversTheVariantItWaitedFor(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	box, soap, _ := seedBundleParts(t, svc)
	late := "variant_late_" + soap.ID

	tx, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, err = tx.Exec(ctx, `SELECT id FROM product WHERE id = $1 FOR UPDATE`, soap.ID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO product_variant (id, product_id, title) VALUES ($1, $2, 'Late')`,
		late, soap.ID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO product_bundle_component (bundle_variant_id, component_variant_id, quantity, rank)
		VALUES ($1, $2, 1, 0)`, box.Variants[0].ID, late)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- svc.DeleteProduct(ctx, soap.ID) }()
	select {
	case err := <-done:
		t.Fatalf("the deletion finished while the product was held: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	require.NoError(t, tx.Commit(ctx))

	err = <-done
	require.Error(t, err, "the waiting deletion sees the late variant's bundle")
	assert.True(t, errors.IsConflict(err), "%v", err)
	assert.True(t, variantIsLive(t, late), "the late variant is not deleted")
}

// TestTheBundleSchemaRefuses holds the table's constraints.
func TestTheBundleSchemaRefuses(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	box, soap, towel := seedBundleParts(t, svc)
	bundle, component, other := box.Variants[0].ID, soap.Variants[0].ID, towel.Variants[0].ID
	_, err := testPool.Pool().Exec(ctx, `INSERT INTO product_bundle_component
		(bundle_variant_id, component_variant_id, quantity, rank) VALUES ($1, $2, 1, 0)`, bundle, component)
	require.NoError(t, err)

	insert := `INSERT INTO product_bundle_component (bundle_variant_id, component_variant_id, quantity, rank)
		VALUES ($1, $2, $3, $4)`
	for name, tc := range map[string]struct {
		args       []any
		constraint string
	}{
		"itself":               {[]any{bundle, bundle, 1, 1}, "product_bundle_component_not_itself"},
		"no units":             {[]any{bundle, other, 0, 1}, "product_bundle_component_quantity"},
		"past the unit bound":  {[]any{bundle, other, 101, 1}, "product_bundle_component_quantity"},
		"a negative rank":      {[]any{bundle, other, 1, -1}, "product_bundle_component_rank_nonneg"},
		"a rank taken":         {[]any{bundle, other, 1, 0}, "product_bundle_component_rank_unique"},
		"a component twice":    {[]any{bundle, component, 1, 5}, "product_bundle_component_pkey"},
		"an unknown component": {[]any{bundle, "variant_missing", 1, 6}, "product_bundle_component_component_variant_id_fkey"},
		"an unknown bundle":    {[]any{"variant_missing", other, 1, 0}, "product_bundle_component_bundle_variant_id_fkey"},
	} {
		_, err := testPool.Pool().Exec(ctx, insert, tc.args...)
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr, name)
		assert.Equal(t, tc.constraint, pgErr.ConstraintName, name)
	}
}
