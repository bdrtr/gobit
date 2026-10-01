package service

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// GetGroupsByIDs mirrors the query: the live groups of the ids, by id.
func (m *memRepo) GetGroupsByIDs(_ context.Context, ids []string) ([]models.CustomerGroup, error) {
	m.record("GetGroupsByIDs")
	out := []models.CustomerGroup{}
	for _, id := range ids {
		if g, live := m.liveGroup(id); live {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	return out, nil
}

// TestTheGroupsAreReadThroughTheirOwnEntity is ADR 0321: the provider lists
// the live groups with their name and rank, fetches an id set in one round
// leaving a deleted group out, and refuses a field or a filter it does not
// offer, an unsupported filter beside the id one included.
func TestTheGroupsAreReadThroughTheirOwnEntity(t *testing.T) {
	ctx := context.Background()
	repo := newMemRepo()
	svc := New(repo, Options{})
	vip, err := svc.CreateGroup(ctx, GroupInput{Name: "VIP", Rank: 1})
	require.NoError(t, err)
	wholesale, err := svc.CreateGroup(ctx, GroupInput{Name: "Wholesale"})
	require.NoError(t, err)
	gone, err := svc.CreateGroup(ctx, GroupInput{Name: "Gone"})
	require.NoError(t, err)
	require.NoError(t, svc.DeleteGroup(ctx, gone.ID))
	provider := NewGroupProvider(svc)
	assert.Equal(t, EntityGroup, provider.Entity())

	listed, err := provider.List(ctx, query.ListOptions{Fields: []string{fieldGroupName}})
	require.NoError(t, err)
	names := map[string]any{}
	for _, record := range listed {
		id, ok := record[fieldID].(string)
		require.True(t, ok, "a record carries its id")
		names[id] = record[fieldGroupName]
		assert.Len(t, record, 2, "the name asked for and the id Query joins over")
	}
	assert.Equal(t, map[string]any{vip.ID: "VIP", wholesale.ID: "Wholesale"}, names, "the live groups")

	fetched, err := provider.FetchByIDs(ctx, []string{wholesale.ID, gone.ID, vip.ID}, nil)
	require.NoError(t, err)
	require.Len(t, fetched, 2, "a deleted group is not fetched")
	calls := repo.calls["GetGroupsByIDs"]
	assert.Equal(t, 1, calls, "one round for the set")
	byID := map[string]query.Record{}
	for _, record := range fetched {
		id, ok := record[fieldID].(string)
		require.True(t, ok, "a record carries its id")
		byID[id] = record
	}
	assert.Equal(t, int32(1), byID[vip.ID][fieldGroupRank])
	assert.Equal(t, "Wholesale", byID[wholesale.ID][fieldGroupName])

	filtered, err := provider.List(ctx, query.ListOptions{Filters: map[string]any{filterID: []string{vip.ID}}})
	require.NoError(t, err)
	require.Len(t, filtered, 1, "the id filter names an exact set")

	_, err = provider.List(ctx, query.ListOptions{Fields: []string{"email"}})
	assert.True(t, errors.IsInvalid(err), "a customer's field is not a group's: %v", err)
	for range 8 {
		_, err = provider.List(ctx, query.ListOptions{Filters: map[string]any{filterID: vip.ID, filterEmail: "x@example.com"}})
		require.Error(t, err, "an unsupported filter beside the id one is refused, whichever the map yields first")
		assert.True(t, errors.IsInvalid(err))
	}
	_, err = provider.FetchByIDs(ctx, []string{vip.ID}, []string{"members"})
	assert.True(t, errors.IsInvalid(err), "an unknown field: %v", err)
}
