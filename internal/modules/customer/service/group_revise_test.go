package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// ReviseGroup mirrors the query: name and rank are written only while they
// are the ones read, and a name another live group holds is a conflict.
func (m *memRepo) ReviseGroup(
	_ context.Context, id, readName string, readRank int32, name string, rank int32, now time.Time,
) (models.CustomerGroup, bool, error) {
	m.record("ReviseGroup")
	g, live := m.liveGroup(id)
	if !live || g.Name != readName || g.Rank != readRank {
		return models.CustomerGroup{}, false, nil
	}
	for other := range m.groups {
		if existing := m.groups[other]; other != id && existing.DeletedAt == nil && existing.Name == name {
			return models.CustomerGroup{}, false, errors.Conflict("customer_group_name_taken",
				"a customer group named %q already exists", name)
		}
	}
	g.Name, g.Rank, g.UpdatedAt = name, rank, now
	m.groups[id] = g

	return g, true, nil
}

// TestAGroupIsRevisedFromWhatWasRead is ADR 0329: the name, trimmed, and the
// rank are written from what was read; a group revised since is refused by
// what it is now; a taken name and an empty one are refused; an unknown group
// is not found.
func TestAGroupIsRevisedFromWhatWasRead(t *testing.T) {
	ctx := context.Background()
	svc := New(newMemRepo(), Options{})
	trade, err := svc.CreateGroup(ctx, GroupInput{Name: "Trade", Rank: 2})
	require.NoError(t, err)
	_, err = svc.CreateGroup(ctx, GroupInput{Name: "VIP"})
	require.NoError(t, err)

	revised, err := svc.ReviseGroup(ctx, trade.ID, "Trade", 2, " Wholesale ", -1)
	require.NoError(t, err)
	assert.Equal(t, "Wholesale", revised.Name)
	assert.Equal(t, int32(-1), revised.Rank)

	_, err = svc.ReviseGroup(ctx, trade.ID, "Trade", 2, "Retail", 0)
	require.Error(t, err)
	assert.Equal(t, CodeGroupMoved, errors.CodeOf(err), "read as Trade, it is Wholesale now: %v", err)
	assert.Contains(t, err.Error(), `is "Wholesale" at rank -1 now, not "Trade" at rank 2`)

	_, err = svc.ReviseGroup(ctx, trade.ID, "Wholesale", -1, "VIP", 0)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "a name another group holds: %v", err)
	_, err = svc.ReviseGroup(ctx, trade.ID, "Wholesale", -1, "  ", 0)
	assert.True(t, errors.IsInvalid(err), "an empty name: %v", err)
	_, err = svc.ReviseGroup(ctx, "custgrp_missing", "X", 0, "Y", 0)
	assert.True(t, errors.IsNotFound(err), "an unknown group: %v", err)
}

// TestThePanelRevisesACustomerGroup is ADR 0329 through the surface: the
// read pair and the written pair reach the service in their places, and a
// stale read is refused.
func TestThePanelRevisesACustomerGroup(t *testing.T) {
	ctx := context.Background()
	svc := New(newMemRepo(), Options{})
	surface := NewAdminSurface(svc)
	id, err := surface.CreateGroup(ctx, "Trade", 4)
	require.NoError(t, err)

	require.NoError(t, surface.ReviseGroup(ctx, id, "Trade", 4, "Wholesale", -2))
	group, err := svc.GetGroup(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "Wholesale", group.Name)
	assert.Equal(t, int32(-2), group.Rank)

	err = surface.ReviseGroup(ctx, id, "Trade", 4, "Retail", 0)
	require.Error(t, err)
	assert.Equal(t, CodeGroupMoved, errors.CodeOf(err))
}
