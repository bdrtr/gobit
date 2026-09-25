//go:build integration

package product_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// serviceAt builds a service over the real schema whose clock reads the given
// moment, which is how these tests make a schedule come due.
func serviceAt(t *testing.T, now time.Time) *service.Service {
	t.Helper()

	svc, err := service.New(service.Options{
		Repo: repository.New(testPool.Pool()),
		Now:  func() time.Time { return now },
	})
	require.NoError(t, err)

	return svc
}

// scheduledDraft writes a draft and schedules it an hour from now.
func scheduledDraft(t *testing.T, svc *service.Service) models.Product {
	t.Helper()

	draft, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Handle: uniqueHandle("scheduled"), Title: "Scheduled", Status: models.StatusDraft,
	})
	require.NoError(t, err)
	at := time.Now().Add(time.Hour)
	scheduled, err := svc.SetSchedule(context.Background(), draft.ID, service.Schedule{PublishAt: &at})
	require.NoError(t, err)

	return scheduled
}

// TestAScheduleComesDueInTheRealSchema verifies the publishing statement and the
// column's rules on PostgreSQL (ADR 0177): the pass publishes only what is due,
// a hand publication clears the moment in the same statement, and a published
// product cannot be given one.
func TestAScheduleComesDueInTheRealSchema(t *testing.T) {
	ctx := context.Background()
	now := serviceAt(t, time.Now())
	later := serviceAt(t, time.Now().Add(2*time.Hour))

	t.Run("the pass publishes what is due and nothing else", func(t *testing.T) {
		due := scheduledDraft(t, now)

		early, _, err := now.ApplyDueSchedules(ctx, 500)
		require.NoError(t, err)
		assert.NotContains(t, early, due.ID, "an hour early, the draft is not due")

		published, _, err := later.ApplyDueSchedules(ctx, 500)
		require.NoError(t, err)
		assert.Contains(t, published, due.ID)

		read, err := now.GetProduct(ctx, due.ID)
		require.NoError(t, err)
		assert.Equal(t, models.StatusPublished, read.Status)
		assert.Nil(t, read.PublishAt, "the moment is spent")

		again, _, err := later.ApplyDueSchedules(ctx, 500)
		require.NoError(t, err)
		assert.NotContains(t, again, due.ID, "a product is published once")
	})

	t.Run("publishing by hand takes the schedule with it", func(t *testing.T) {
		draft := scheduledDraft(t, now)
		published := models.StatusPublished

		updated, err := now.UpdateProduct(ctx, draft.ID, service.UpdateProductInput{Status: &published})
		require.NoError(t, err,
			"the constraint would refuse a published product with a moment; the update clears it")
		assert.Nil(t, updated.PublishAt)
	})

	t.Run("a published product cannot carry a moment", func(t *testing.T) {
		live, err := now.CreateProduct(ctx, service.CreateProductInput{
			Handle: uniqueHandle("live"), Title: "Live", Status: models.StatusPublished,
		})
		require.NoError(t, err)

		_, err = testPool.Pool().Exec(ctx,
			`UPDATE product SET publish_at = now() + interval '1 hour' WHERE id = $1`, live.ID)
		require.Error(t, err, "product_publish_at_draft_only has to refuse it")
		assert.Contains(t, err.Error(), "product_publish_at_draft_only")
	})

	t.Run("a live product leaves at its moment, and archiving by hand spends it", func(t *testing.T) {
		live, err := now.CreateProduct(ctx, service.CreateProductInput{
			Handle: uniqueHandle("seasonal"), Title: "Seasonal", Status: models.StatusPublished,
		})
		require.NoError(t, err)
		leave := time.Now().Add(time.Hour)
		_, err = now.SetSchedule(ctx, live.ID, service.Schedule{ArchiveAt: &leave})
		require.NoError(t, err)

		_, archived, err := later.ApplyDueSchedules(ctx, 500)
		require.NoError(t, err)
		assert.Contains(t, archived, live.ID)
		read, err := now.GetProduct(ctx, live.ID)
		require.NoError(t, err)
		assert.Equal(t, models.StatusArchived, read.Status)
		assert.Nil(t, read.ArchiveAt, "the moment is spent")

		byHand, err := now.CreateProduct(ctx, service.CreateProductInput{
			Handle: uniqueHandle("by-hand"), Title: "By hand", Status: models.StatusPublished,
		})
		require.NoError(t, err)
		_, err = now.SetSchedule(ctx, byHand.ID, service.Schedule{ArchiveAt: &leave})
		require.NoError(t, err)
		archivedStatus := models.StatusArchived
		updated, err := now.UpdateProduct(ctx, byHand.ID, service.UpdateProductInput{Status: &archivedStatus})
		require.NoError(t, err, "the constraint would refuse an archived product with a moment; the update clears it")
		assert.Nil(t, updated.ArchiveAt)
	})

	t.Run("the constraints refuse a pair out of order and a moment on an archived product", func(t *testing.T) {
		draft := scheduledDraft(t, now)
		_, err := testPool.Pool().Exec(ctx,
			`UPDATE product SET archive_at = publish_at - interval '1 minute' WHERE id = $1`, draft.ID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "product_archive_after_publish")

		gone, err := now.CreateProduct(ctx, service.CreateProductInput{
			Handle: uniqueHandle("gone"), Title: "Gone", Status: models.StatusArchived,
		})
		require.NoError(t, err)
		_, err = testPool.Pool().Exec(ctx,
			`UPDATE product SET archive_at = now() + interval '1 hour' WHERE id = $1`, gone.ID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "product_archive_at_live_only")
	})

	t.Run("a deleted draft is not published", func(t *testing.T) {
		draft := scheduledDraft(t, now)
		require.NoError(t, now.DeleteProduct(ctx, draft.ID))

		published, _, err := later.ApplyDueSchedules(ctx, 500)
		require.NoError(t, err)
		assert.NotContains(t, published, draft.ID)
	})

	t.Run("a draft being edited is left for the next pass", func(t *testing.T) {
		draft := scheduledDraft(t, now)

		tx, err := testPool.Pool().Begin(ctx)
		require.NoError(t, err)
		// Rolled back however the subtest ends: a failure below would otherwise
		// leave the connection in a transaction, and closing the pool at the end
		// of the run waits for it for ever.
		t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
		_, err = tx.Exec(ctx, `SELECT id FROM product WHERE id = $1 FOR UPDATE`, draft.ID)
		require.NoError(t, err)

		// A pass that waited on the lock would wait for ever here — the lock is
		// held by this goroutine — so it is given seconds, and waiting is the
		// failure.
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		skipped, _, err := later.ApplyDueSchedules(bounded, 500)
		require.NoError(t, err, "the pass waited on a locked row instead of leaving it")
		assert.NotContains(t, skipped, draft.ID,
			"the pass does not publish a draft someone holds")
		require.NoError(t, tx.Rollback(ctx))

		published, _, err := later.ApplyDueSchedules(ctx, 500)
		require.NoError(t, err)
		assert.Contains(t, published, draft.ID, "the next pass takes it")
	})
}

// TestTheScheduleColumnIsReadInItsPlace pins publish_at's position in the
// hand-written product statement.
//
// That statement resolves columns by position, and
// [TestProductColumnMappingHasNotDrifted] reads a PUBLISHED product, whose moment
// is always NULL — so it could not see publish_at trade places with another
// nullable moment such as deleted_at. The admin listing runs the same statement
// over drafts, and a scheduled draft carries a moment unlike any other it has.
func TestTheScheduleColumnIsReadInItsPlace(t *testing.T) {
	ctx := context.Background()
	svc := serviceAt(t, time.Now())
	draft := scheduledDraft(t, svc)
	handle := draft.Handle
	// Both moments, and different ones, so neither can stand in for the other.
	leave := draft.PublishAt.Add(24 * time.Hour)
	_, err := svc.SetSchedule(ctx, draft.ID, service.Schedule{PublishAt: draft.PublishAt, ArchiveAt: &leave})
	require.NoError(t, err)

	page, err := svc.ListProducts(ctx, service.ListProductsOptions{Handle: &handle, Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	read := page.Items[0]

	require.NotNil(t, read.PublishAt, "the moment has to be read")
	assert.True(t, read.PublishAt.Equal(*draft.PublishAt),
		"publish_at came back as %s and %s was written: the column has moved", read.PublishAt, draft.PublishAt)
	require.NotNil(t, read.ArchiveAt)
	assert.True(t, read.ArchiveAt.Equal(leave), "archive_at came back as %s and %s was written", read.ArchiveAt, leave)
	assert.Nil(t, read.DeletedAt, "deleted_at and a schedule moment may have traded places")
	assert.False(t, read.CreatedAt.Equal(*draft.PublishAt), "created_at and publish_at may have traded places")
}
