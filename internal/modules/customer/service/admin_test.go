package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// TestThePanelPutsACustomerIntoAGroupAndTakesThemOut is ADR 0322: the
// surface puts a customer into a group, leaves one already in it there,
// takes them out, and refuses taking out one who is not in it; a surface
// that was never built is unavailable rather than a panic.
func TestThePanelPutsACustomerIntoAGroupAndTakesThemOut(t *testing.T) {
	ctx := context.Background()
	svc := New(newMemRepo(), Options{})
	surface := NewAdminSurface(svc)
	customer, err := svc.CreateCustomer(ctx, CustomerInput{Email: "ada@example.test"})
	require.NoError(t, err)
	group, err := svc.CreateGroup(ctx, GroupInput{Name: "Trade"})
	require.NoError(t, err)

	require.NoError(t, surface.AddCustomerToGroup(ctx, customer.ID, group.ID))
	require.NoError(t, surface.AddCustomerToGroup(ctx, customer.ID, group.ID), "a second press is harmless")
	groups, err := svc.ListGroupsOf(ctx, customer.ID)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, group.ID, groups[0].ID)

	require.NoError(t, surface.RemoveCustomerFromGroup(ctx, customer.ID, group.ID))
	groups, err = svc.ListGroupsOf(ctx, customer.ID)
	require.NoError(t, err)
	assert.Empty(t, groups)

	err = surface.RemoveCustomerFromGroup(ctx, customer.ID, group.ID)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "taking out one who is not in the group: %v", err)

	var unset *AdminSurface
	err = unset.AddCustomerToGroup(ctx, customer.ID, group.ID)
	require.Error(t, err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err))
}
