//go:build integration

package pricing_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// listHistoryCount counts the list's history snapshots.
func listHistoryCount(ctx context.Context, t *testing.T, id string) int {
	t.Helper()

	var count int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM price_list_history WHERE price_list_id = $1`, id).Scan(&count))

	return count
}

// TestThePanelRevisesAPriceListFromWhatItRead is ADR 0330 against a real
// PostgreSQL: the title, the description and the window are written from the
// terms read, a start to the microsecond included, the type, the status and
// the metadata kept and the revision recorded in the list's history; a stale
// title or a stale start writes and records nothing; a deleted list is not
// found.
func TestThePanelRevisesAPriceListFromWhatItRead(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	surface := service.NewAdminSurface(svc)
	starts := time.Date(2026, 3, 1, 9, 30, 15, 123456000, time.UTC)
	created, err := svc.CreatePriceList(ctx, service.PriceListInput{
		Title: "Spring", Description: "the spring sale", Type: models.PriceListOverride,
		Status: models.PriceListActive, StartsAt: &starts, Metadata: map[string]any{"source": "erp"},
	})
	require.NoError(t, err)
	before := listHistoryCount(ctx, t, created.ID)

	ends := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, surface.RevisePriceList(ctx, created.ID, "Spring", "the spring sale", &starts, nil,
		"Spring 2026", "", nil, &ends))
	list, err := svc.GetPriceList(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "Spring 2026|", list.Title+"|"+list.Description)
	assert.Nil(t, list.StartsAt, "the start opened")
	require.NotNil(t, list.EndsAt)
	assert.True(t, ends.Equal(*list.EndsAt), "the end as written")
	assert.Equal(t, "override|active", string(list.Type)+"|"+string(list.Status), "the type and the status are kept")
	assert.Equal(t, "erp", list.Metadata["source"], "and so is the metadata")
	after := listHistoryCount(ctx, t, created.ID)
	assert.Equal(t, before+1, after, "the revision is recorded in the list's history")

	for label, read := range map[string]models.PriceListTerms{
		"a title read before the revision": {Title: "Spring", EndsAt: &ends},
		"an end read a microsecond off":    {Title: "Spring 2026", EndsAt: new(ends.Add(time.Microsecond))},
		"an end read as open":              {Title: "Spring 2026"},
	} {
		err = surface.RevisePriceList(ctx, created.ID, read.Title, read.Description, read.StartsAt, read.EndsAt,
			"Summer", "", nil, nil)
		require.Error(t, err, label)
		assert.Equal(t, service.CodePriceListMoved, errors.CodeOf(err), "%s: %v", label, err)
		assert.Contains(t, err.Error(), `it is "Spring 2026" from always until 2026-06-01T00:00:00Z now`, label)
	}
	list, err = svc.GetPriceList(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "Spring 2026", list.Title, "a stale read writes nothing")
	assert.Equal(t, after, listHistoryCount(ctx, t, created.ID), "and records nothing")

	require.NoError(t, svc.DeletePriceList(ctx, created.ID))
	err = surface.RevisePriceList(ctx, created.ID, "Spring 2026", "", nil, &ends, "Gone", "", nil, nil)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a deleted list: %v", err)
}

// TestARevisionReadsTheListUnderItsLock is ADR 0330's lock: while another
// transaction holds the list and renames it, a revision from the old title
// waits, and once it is let through it reads the list as the other left it
// and is refused. Read before the lock, it would have found the old title and
// written over the other's rename.
func TestARevisionReadsTheListUnderItsLock(t *testing.T) {
	ctx := context.Background()
	surface := service.NewAdminSurface(newService(t))
	id, err := surface.CreatePriceList(ctx, "Locked", "", "sale", "draft", nil, nil)
	require.NoError(t, err)

	other, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = other.Rollback(context.Background()) })
	_, err = other.Exec(ctx, `SELECT 1 FROM price_list WHERE id = $1 FOR UPDATE`, id)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- surface.RevisePriceList(ctx, id, "Locked", "", nil, nil, "Mine", "", nil, nil) }()
	require.Eventually(t, func() bool {
		var waiting int
		err := testPool.Pool().QueryRow(ctx,
			`SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND datname = current_database()`).
			Scan(&waiting)
		return err == nil && waiting > 0
	}, 5*time.Second, 10*time.Millisecond, "the revision waits for the list")

	_, err = other.Exec(ctx, `UPDATE price_list SET title = 'Theirs' WHERE id = $1`, id)
	require.NoError(t, err)
	require.NoError(t, other.Commit(ctx))

	err = <-done
	require.Error(t, err, "the revision read the list after the other renamed it")
	assert.Equal(t, service.CodePriceListMoved, errors.CodeOf(err))
}
