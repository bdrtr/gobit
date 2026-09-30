package service_test

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// The fake store's after-sales reads for the read layer (ADR 0270). They sort
// the way the real queries do — newest first, the identifier breaking a tie —
// because the provider hands that order to its reader.

// newestFirst orders two records by their creation, newest first.
func newestFirst(aAt, bAt time.Time, aID, bID string) int {
	if !aAt.Equal(bAt) {
		return bAt.Compare(aAt)
	}

	return strings.Compare(bID, aID)
}

// ReturnsByIDs returns the given returns, newest first.
func (f *fakeStore) ReturnsByIDs(ctx context.Context, ids []string) ([]models.Return, error) {
	snapshot := f.view(ctx)
	out := make([]models.Return, 0, len(ids))
	for _, id := range ids {
		if record, ok := snapshot.returns[id]; ok {
			out = append(out, record)
		}
	}
	slices.SortFunc(out, func(a, b models.Return) int { return newestFirst(a.CreatedAt, b.CreatedAt, a.ID, b.ID) })

	return out, nil
}

// ClaimsByIDs returns the given claims, newest first.
func (f *fakeStore) ClaimsByIDs(ctx context.Context, ids []string) ([]models.Claim, error) {
	snapshot := f.view(ctx)
	out := make([]models.Claim, 0, len(ids))
	for _, id := range ids {
		if record, ok := snapshot.claims[id]; ok {
			out = append(out, record)
		}
	}
	slices.SortFunc(out, func(a, b models.Claim) int { return newestFirst(a.CreatedAt, b.CreatedAt, a.ID, b.ID) })

	return out, nil
}

// ExchangesByIDs returns the given exchanges, newest first.
func (f *fakeStore) ExchangesByIDs(ctx context.Context, ids []string) ([]models.Exchange, error) {
	snapshot := f.view(ctx)
	out := make([]models.Exchange, 0, len(ids))
	for _, id := range ids {
		if record, ok := snapshot.exchanges[id]; ok {
			out = append(out, record)
		}
	}
	slices.SortFunc(out, func(a, b models.Exchange) int { return newestFirst(a.CreatedAt, b.CreatedAt, a.ID, b.ID) })

	return out, nil
}

// ReplacementsByIDs returns the given replacements, newest first.
func (f *fakeStore) ReplacementsByIDs(ctx context.Context, ids []string) ([]models.Replacement, error) {
	snapshot := f.view(ctx)
	out := make([]models.Replacement, 0, len(ids))
	for _, id := range ids {
		if record, ok := snapshot.replaces[id]; ok {
			out = append(out, record)
		}
	}
	slices.SortFunc(out, func(a, b models.Replacement) int {
		return newestFirst(a.CreatedAt, b.CreatedAt, a.ID, b.ID)
	})

	return out, nil
}

// PageReplacementsOfOrder pages the order's replacements, newest first,
// reaching the order through the claim or the exchange as the query does.
func (f *fakeStore) PageReplacementsOfOrder(
	ctx context.Context, filter models.ChildFilter,
) ([]models.Replacement, error) {
	snapshot := f.view(ctx)
	out := make([]models.Replacement, 0)
	for id := range snapshot.replaces {
		replacement := snapshot.replaces[id]
		claim, byClaim := snapshot.claims[replacement.ClaimID]
		exchange, byExchange := snapshot.exchanges[replacement.ExchangeID]
		if (byClaim && claim.OrderID == filter.OrderID) || (byExchange && exchange.OrderID == filter.OrderID) {
			out = append(out, replacement)
		}
	}
	slices.SortFunc(out, func(a, b models.Replacement) int {
		return newestFirst(a.CreatedAt, b.CreatedAt, a.ID, b.ID)
	})
	total := int64(len(out))
	if filter.Offset >= total {
		return []models.Replacement{}, nil
	}

	return out[filter.Offset:min(filter.Offset+filter.Limit, total)], nil
}

// ReturnItemsOf returns the given returns' lines by return.
func (f *fakeStore) ReturnItemsOf(
	ctx context.Context, returnIDs []string,
) (map[string][]models.ReturnItem, error) {
	snapshot := f.view(ctx)
	out := map[string][]models.ReturnItem{}
	for _, id := range slices.Sorted(maps.Keys(snapshot.retItems)) {
		item := snapshot.retItems[id]
		if slices.Contains(returnIDs, item.ReturnID) {
			out[item.ReturnID] = append(out[item.ReturnID], item)
		}
	}

	return out, nil
}

// ReplacementItemsOf returns the given replacements' lines by replacement.
func (f *fakeStore) ReplacementItemsOf(
	_ context.Context, replacementIDs []string,
) (map[string][]models.ReplacementItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := map[string][]models.ReplacementItem{}
	for _, id := range slices.Sorted(maps.Keys(f.replItems)) {
		item := f.replItems[id]
		if slices.Contains(replacementIDs, item.ReplacementID) {
			out[item.ReplacementID] = append(out[item.ReplacementID], item)
		}
	}

	return out, nil
}
