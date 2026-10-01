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

// SwitchPriceListStatus mirrors the repository through the stub's own read
// and write: the status moves only from the one the caller read.
func (r *stubRepo) SwitchPriceListStatus(
	ctx context.Context, id string, from, to models.PriceListStatus, clock func() time.Time,
) (models.PriceList, bool, error) {
	r.calls["SwitchPriceListStatus"]++
	list, err := r.GetPriceList(ctx, id)
	if err != nil || list.Status != from {
		return list, false, err
	}
	list.Status = to
	list, err = r.UpdatePriceList(ctx, list, clock)

	return list, err == nil, err
}

// TestAPriceListMovesOnlyFromTheStatusItWasReadIn is ADR 0328: a draft is
// published, a list another operator moved first is refused by the status it
// is in now, an undefined status and a switch to the status it is in are
// refused before the store is asked.
func TestAPriceListMovesOnlyFromTheStatusItWasReadIn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	lists := map[string]models.PriceList{"plist_1": {ID: "plist_1", Title: "Spring", Status: models.PriceListDraft}}
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

	published, err := svc.SwitchPriceListStatus(ctx, "plist_1", models.PriceListDraft, models.PriceListActive)
	require.NoError(t, err)
	assert.Equal(t, models.PriceListActive, published.Status)
	assert.Equal(t, "Spring", lists["plist_1"].Title, "the list's other fields are kept")

	_, err = svc.SwitchPriceListStatus(ctx, "plist_1", models.PriceListDraft, models.PriceListActive)
	require.Error(t, err)
	assert.Equal(t, CodePriceListMoved, errors.CodeOf(err), "read as a draft, it is active now: %v", err)
	assert.Contains(t, err.Error(), "is active now, not draft")

	calls := repo.calls["SwitchPriceListStatus"]
	_, err = svc.SwitchPriceListStatus(ctx, "plist_1", models.PriceListActive, "paused")
	assert.True(t, errors.IsInvalid(err), "an undefined status: %v", err)
	_, err = svc.SwitchPriceListStatus(ctx, "plist_1", models.PriceListActive, models.PriceListActive)
	assert.True(t, errors.IsInvalid(err), "the status it is in: %v", err)
	assert.Equal(t, calls, repo.calls["SwitchPriceListStatus"], "refused before the store is asked")

	_, err = svc.SwitchPriceListStatus(ctx, "plist_missing", models.PriceListDraft, models.PriceListActive)
	assert.True(t, errors.IsNotFound(err), "a list that is not there: %v", err)
}
