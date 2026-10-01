package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// RevisePriceList mirrors the repository through the stub's own read and
// write: the terms are written only while they are the ones the caller read.
func (r *stubRepo) RevisePriceList(
	ctx context.Context, id string, read, next models.PriceListTerms, clock func() time.Time,
) (models.PriceList, bool, error) {
	r.calls["RevisePriceList"]++
	list, err := r.GetPriceList(ctx, id)
	if err != nil || !list.Terms().Same(read) {
		return list, false, err
	}
	list.Title, list.Description, list.StartsAt, list.EndsAt = next.Title, next.Description, next.StartsAt, next.EndsAt
	list, err = r.UpdatePriceList(ctx, list, clock)

	return list, err == nil, err
}

// TestAPriceListIsRevisedFromWhatWasRead is ADR 0330: the title and the
// description trimmed and the window in UTC are written from the terms read,
// the type and the status kept; a list revised since is refused by what it is
// now; an empty title and a window that ends before it starts are refused
// before the store is asked; an unknown list is not found.
func TestAPriceListIsRevisedFromWhatWasRead(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	march := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	lists := map[string]models.PriceList{"plist_1": {
		ID: "plist_1", Title: "Spring", Description: "seasonal", Type: models.PriceListSale,
		Status: models.PriceListActive, StartsAt: &march,
	}}
	repo := newStubRepo()
	repo.getPriceListFn = func(_ context.Context, id string) (models.PriceList, error) {
		list, ok := lists[id]
		if !ok {
			return models.PriceList{}, errors.NotFound("price_list_not_found", "price list not found: %s", id)
		}
		return list, nil
	}
	repo.updatePriceListFn = func(_ context.Context, list models.PriceList, _ time.Time) (models.PriceList, error) {
		lists[list.ID] = list
		return list, nil
	}
	svc := New(repo, Options{})
	read := lists["plist_1"].Terms()

	istanbul := time.FixedZone("TRT", 3*60*60)
	starts := time.Date(2026, 4, 1, 3, 0, 0, 0, istanbul)
	ends := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	revised, err := svc.RevisePriceList(ctx, "plist_1", read, models.PriceListTerms{
		Title: " Spring 2026 ", Description: " the season's prices ", StartsAt: &starts, EndsAt: &ends,
	})
	require.NoError(t, err)
	assert.Equal(t, "Spring 2026|the season's prices", revised.Title+"|"+revised.Description)
	require.NotNil(t, revised.StartsAt)
	assert.Equal(t, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), *revised.StartsAt, "the window in UTC")
	assert.Equal(t, time.UTC, revised.StartsAt.Location())
	assert.Equal(t, ends, *revised.EndsAt)
	assert.Equal(t, models.PriceListSale, lists["plist_1"].Type, "the type is kept")
	assert.Equal(t, models.PriceListActive, lists["plist_1"].Status, "the status is kept")

	_, err = svc.RevisePriceList(ctx, "plist_1", read, models.PriceListTerms{Title: "Summer"})
	require.Error(t, err)
	assert.Equal(t, CodePriceListMoved, errors.CodeOf(err), "read as Spring, it is Spring 2026 now: %v", err)
	assert.Contains(t, err.Error(), `it is "Spring 2026" from 2026-04-01T00:00:00Z until 2026-06-01T00:00:00Z now`)
	assert.Equal(t, "Spring 2026", lists["plist_1"].Title, "a stale read writes nothing")

	calls := repo.calls["RevisePriceList"]
	_, err = svc.RevisePriceList(ctx, "plist_1", revised.Terms(), models.PriceListTerms{Title: "  "})
	assert.True(t, errors.IsInvalid(err), "an empty title: %v", err)
	_, err = svc.RevisePriceList(ctx, "plist_1", revised.Terms(), models.PriceListTerms{Title: "Back", StartsAt: &ends, EndsAt: &ends})
	assert.True(t, errors.IsInvalid(err), "a window that does not start before it ends: %v", err)
	assert.Equal(t, calls, repo.calls["RevisePriceList"], "a refused input never reaches the store")

	_, err = svc.RevisePriceList(ctx, "plist_missing", read, models.PriceListTerms{Title: "X"})
	assert.True(t, errors.IsNotFound(err), "an unknown list: %v", err)
	_, err = svc.RevisePriceList(ctx, "nope", read, models.PriceListTerms{Title: "X"})
	assert.True(t, errors.IsInvalid(err), "an id that is not a list's: %v", err)

	open, err := svc.RevisePriceList(ctx, "plist_1", revised.Terms(), models.PriceListTerms{Title: "Spring 2026"})
	require.NoError(t, err, "the window opened at both ends")
	assert.Nil(t, open.StartsAt)
	assert.Nil(t, open.EndsAt)
}

// TestPriceListTermsCompareInstants: the same instant in two zones is the
// same end, an open end is only the same as an open end, and the text is
// compared as it is.
func TestPriceListTermsCompareInstants(t *testing.T) {
	t.Parallel()

	utc := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	local := utc.In(time.FixedZone("TRT", 3*60*60))
	later := utc.Add(time.Microsecond)
	base := models.PriceListTerms{Title: "A", Description: "d", StartsAt: &utc}

	assert.True(t, base.Same(models.PriceListTerms{Title: "A", Description: "d", StartsAt: &local}))
	for name, other := range map[string]models.PriceListTerms{
		"a later start":       {Title: "A", Description: "d", StartsAt: &later},
		"an open start":       {Title: "A", Description: "d"},
		"a closed end":        {Title: "A", Description: "d", StartsAt: &utc, EndsAt: &utc},
		"another title":       {Title: "B", Description: "d", StartsAt: &utc},
		"another description": {Title: "A", Description: "e", StartsAt: &utc},
	} {
		assert.False(t, base.Same(other), name)
	}
}
