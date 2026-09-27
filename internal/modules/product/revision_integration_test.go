//go:build integration

package product_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// revisionRows reads a product's revisions from the table, oldest first.
func revisionRows(t *testing.T, productID string) []models.Revision {
	t.Helper()

	rows, err := testPool.Pool().Query(context.Background(),
		`SELECT version, changed, request_id, snapshot FROM product_revision WHERE product_id = $1 ORDER BY version`,
		productID)
	require.NoError(t, err)
	defer rows.Close()
	var out []models.Revision
	for rows.Next() {
		var rev models.Revision
		require.NoError(t, rows.Scan(&rev.Version, &rev.Changed, &rev.RequestID, &rev.Snapshot))
		out = append(out, rev)
	}
	require.NoError(t, rows.Err())
	return out
}

// storedVersion reads the product row's version.
func storedVersion(t *testing.T, productID string) int64 {
	t.Helper()
	var version int64
	require.NoError(t, testPool.Pool().QueryRow(context.Background(),
		`SELECT version FROM product WHERE id = $1`, productID).Scan(&version))
	return version
}

// TestARevisionReadBackFromJSONBEqualsTheViewItWasTakenOf is ADR 0221 on the
// real schema: JSONB orders keys its own way and keeps a number's spelling, and
// a write that changes nothing has to be told apart from one that does through
// it.
func TestARevisionReadBackFromJSONBEqualsTheViewItWasTakenOf(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	product, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("revised"), Title: "Revised", Status: models.StatusDraft,
		Metadata: map[string]any{"zeta": 1.5, "alpha": map[string]any{"nested": []any{3, "b", true}}, "price": 1.50},
		Variants: []service.CreateVariantInput{{Title: "One size", Metadata: map[string]any{"b": 1, "a": 2}}},
		Options:  []service.CreateOptionInput{{Title: "Size", Values: []string{"S", "M"}}},
	})
	require.NoError(t, err)

	_, err = svc.UpdateProduct(ctx, product.ID, service.UpdateProductInput{Title: ptrString("Revised")})
	require.NoError(t, err)
	assert.Equal(t, int64(1), storedVersion(t, product.ID), "a write that changes nothing records nothing")

	_, err = svc.UpdateProduct(ctx, product.ID, service.UpdateProductInput{Metadata: map[string]any{"zeta": 2}})
	require.NoError(t, err)

	revisions := revisionRows(t, product.ID)
	require.Len(t, revisions, 2)
	assert.Equal(t, []string{}, revisions[0].Changed)
	assert.Equal(t, []string{"metadata"}, revisions[1].Changed)
	assert.Nil(t, revisions[1].RequestID, "a call outside a request names none")
	var snapshot map[string]any
	require.NoError(t, json.Unmarshal(revisions[0].Snapshot, &snapshot))
	assert.Equal(t, "Revised", snapshot["title"])
	assert.NotContains(t, snapshot, "created_at")
	assert.Len(t, snapshot["variants"], 1)
	assert.Equal(t, int64(2), storedVersion(t, product.ID))

	listed, err := svc.ListProducts(ctx, service.ListProductsOptions{Handle: &product.Handle})
	require.NoError(t, err)
	require.Len(t, listed.Items, 1)
	assert.Equal(t, int64(2), listed.Items[0].Version, "the listing's own column list reads the version in its place")
}

// TestAProductWrittenBeforeRevisionsBeginsWithWhatItWasOnTheRealSchema: a
// product the migration found, version 0 and no revision, gets what it was as
// revision 1 in the transaction of its first write.
func TestAProductWrittenBeforeRevisionsBeginsWithWhatItWasOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	product, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("legacy"), Title: "Legacy", Status: models.StatusPublished,
	})
	require.NoError(t, err)
	_, err = testPool.Pool().Exec(ctx, `DELETE FROM product_revision WHERE product_id = $1`, product.ID)
	require.NoError(t, err)
	_, err = testPool.Pool().Exec(ctx, `UPDATE product SET version = 0 WHERE id = $1`, product.ID)
	require.NoError(t, err)

	requestCtx := corehttp.WithRequestID(ctx, "req-legacy")
	_, err = svc.UpdateProduct(requestCtx, product.ID, service.UpdateProductInput{Title: ptrString("Legacy, renamed")})
	require.NoError(t, err)

	revisions := revisionRows(t, product.ID)
	require.Len(t, revisions, 2)
	var first, second map[string]any
	require.NoError(t, json.Unmarshal(revisions[0].Snapshot, &first))
	require.NoError(t, json.Unmarshal(revisions[1].Snapshot, &second))
	assert.Equal(t, "Legacy", first["title"])
	assert.Equal(t, "Legacy, renamed", second["title"])
	require.NotNil(t, revisions[1].RequestID)
	assert.Equal(t, "req-legacy", *revisions[1].RequestID)
}

// TestARestoreWritesNullsBackOnTheRealSchema: the PATCH statement cannot clear
// a field, and the restore's statement does.
func TestARestoreWritesNullsBackOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	tag, err := svc.CreateTag(ctx, uniqueHandle("gone-tag"))
	require.NoError(t, err)
	product, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("restored"), Title: "Restored", Status: models.StatusPublished, TagIDs: []string{tag.ID},
	})
	require.NoError(t, err)
	_, err = svc.UpdateProduct(ctx, product.ID, service.UpdateProductInput{
		Title: ptrString("Restored, edited"), Subtitle: ptrString("Added"), Material: ptrString("steel"), Weight: ptrInt32(120),
	})
	require.NoError(t, err)
	require.NoError(t, svc.DeleteTag(ctx, tag.ID))

	result, err := svc.RestoreRevision(ctx, product.ID, 1)
	require.NoError(t, err)

	var subtitle, material *string
	var weight *int32
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT subtitle, material, weight FROM product WHERE id = $1`, product.ID).Scan(&subtitle, &material, &weight))
	assert.Nil(t, subtitle)
	assert.Nil(t, material)
	assert.Nil(t, weight)
	assert.Equal(t, "Restored", result.Product.Title)
	assert.Equal(t, []string{"tag:" + tag.ID}, result.Dropped)
	assert.Equal(t, int64(3), storedVersion(t, product.ID))
}

// TestTheScheduleRecordsTheRevisionOfWhatItPublished: the pass's statement and
// the revisions of what it changed are one transaction.
func TestTheScheduleRecordsTheRevisionOfWhatItPublished(t *testing.T) {
	ctx := context.Background()
	now := serviceAt(t, time.Now())
	due := scheduledDraft(t, now)

	published, _, err := serviceAt(t, time.Now().Add(2*time.Hour)).ApplyDueSchedules(ctx, 500)
	require.NoError(t, err)
	require.Contains(t, published, due.ID)

	revisions := revisionRows(t, due.ID)
	require.Len(t, revisions, 2)
	assert.Equal(t, []string{"status"}, revisions[1].Changed)
	assert.Nil(t, revisions[1].RequestID, "a job's revision names no request")
}

// TestAWriterWaitsForTheRevisionBeforeItsOwn: a transaction holding the
// product's row lock appends revision 2; a variant write asked for meanwhile,
// which touches no row the lock covers, waits for it and appends 3 instead of
// colliding with it.
func TestAWriterWaitsForTheRevisionBeforeItsOwn(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	product, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("contended"), Title: "Contended", Status: models.StatusDraft,
		Variants: []service.CreateVariantInput{{Title: "One size"}},
	})
	require.NoError(t, err)
	require.Len(t, product.Variants, 1)
	first := revisionRows(t, product.ID)[0]

	tx, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, err = tx.Exec(ctx, `SELECT id FROM product WHERE id = $1 FOR UPDATE`, product.ID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE product SET title = 'Held', version = 2 WHERE id = $1`, product.ID)
	require.NoError(t, err)
	var held map[string]any
	require.NoError(t, json.Unmarshal(first.Snapshot, &held))
	held["title"] = "Held"
	heldSnapshot, err := json.Marshal(held)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO product_revision (id, product_id, version, recorded_at, changed, snapshot)
		VALUES ($1, $2, 2, now(), '{title}', $3)`, "prodrev_held_"+product.ID, product.ID, heldSnapshot)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, err := svc.UpdateVariant(ctx, product.Variants[0].ID, service.UpdateVariantInput{Title: ptrString("Late")})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("the write finished while the lock was held: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	require.NoError(t, tx.Commit(ctx))

	require.NoError(t, <-done, "the waiting write appends after the held one")
	revisions := revisionRows(t, product.ID)
	require.Len(t, revisions, 3)
	assert.Equal(t, []string{"variants"}, revisions[2].Changed, "compared with the held revision, not the first")
	var last map[string]any
	require.NoError(t, json.Unmarshal(revisions[2].Snapshot, &last))
	assert.Equal(t, "Held", last["title"])
	assert.Equal(t, int64(3), storedVersion(t, product.ID))
}

// TestTheRevisionSchemaRefuses holds the table's constraints.
func TestTheRevisionSchemaRefuses(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	product, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("constrained"), Title: "Constrained", Status: models.StatusDraft,
	})
	require.NoError(t, err)

	for name, tc := range map[string]struct {
		statement  string
		args       []any
		constraint string
	}{
		"a version below one": {
			`INSERT INTO product_revision (id, product_id, version, recorded_at, changed, snapshot)
			 VALUES ('prodrev_c1', $1, 0, now(), '{}', '{}')`, []any{product.ID}, "product_revision_version_positive",
		},
		"a version taken": {
			`INSERT INTO product_revision (id, product_id, version, recorded_at, changed, snapshot)
			 VALUES ('prodrev_c2', $1, 1, now(), '{}', '{}')`, []any{product.ID}, "product_revision_version_key",
		},
		"a snapshot that is not an object": {
			`INSERT INTO product_revision (id, product_id, version, recorded_at, changed, snapshot)
			 VALUES ('prodrev_c3', $1, 7, now(), '{}', '[]')`, []any{product.ID}, "product_revision_snapshot_is_object",
		},
		"a blank request": {
			`INSERT INTO product_revision (id, product_id, version, recorded_at, changed, request_id, snapshot)
			 VALUES ('prodrev_c4', $1, 8, now(), '{}', ' ', '{}')`, []any{product.ID}, "product_revision_request_not_blank",
		},
		"an unknown product": {
			`INSERT INTO product_revision (id, product_id, version, recorded_at, changed, snapshot)
			 VALUES ('prodrev_c5', 'prod_missing', 1, now(), '{}', '{}')`, nil, "product_revision_product_id_fkey",
		},
		"a negative version on the product": {
			`UPDATE product SET version = -1 WHERE id = $1`, []any{product.ID}, "product_version_not_negative",
		},
	} {
		_, err := testPool.Pool().Exec(ctx, tc.statement, tc.args...)
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr, name)
		assert.Equal(t, tc.constraint, pgErr.ConstraintName, name)
	}
}
