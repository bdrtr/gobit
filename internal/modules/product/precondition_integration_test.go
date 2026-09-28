//go:build integration

package product_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestAVersionIsComparedUnderTheLock is ADR 0222 on the real schema: a
// transaction holding the product's row lock moves it to version 2; a variant
// write asked on version 1 meanwhile waits for it and is refused, rather than
// passing a check made before the lock and writing over it.
func TestAVersionIsComparedUnderTheLock(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	product, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("asked"), Title: "Asked", Status: models.StatusDraft,
		Variants: []service.CreateVariantInput{{Title: "One size"}},
	})
	require.NoError(t, err)
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
		VALUES ($1, $2, 2, now(), '{title}', $3)`, "prodrev_asked_"+product.ID, product.ID, heldSnapshot)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, err := svc.UpdateVariant(service.ExpectVersion(ctx, 1), product.Variants[0].ID,
			service.UpdateVariantInput{Title: ptrString("Late")})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("the write finished while the lock was held: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	require.NoError(t, tx.Commit(ctx))

	late := <-done
	require.Error(t, late, "the write was asked on the version the held one moved past")
	assert.True(t, errors.IsPreconditionFailed(late), "%v", late)
	assert.Len(t, revisionRows(t, product.ID), 2, "the refused write appended nothing")
	variant, err := svc.GetVariant(ctx, product.Variants[0].ID)
	require.NoError(t, err)
	assert.Equal(t, "One size", variant.Title)
}
