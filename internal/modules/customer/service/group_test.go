package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// newTestGroup opens a customer group for a test.
func newTestGroup(ctx context.Context, t *testing.T, svc *Service, name string) models.CustomerGroup {
	t.Helper()

	g, err := svc.CreateGroup(ctx, GroupInput{Name: name})
	require.NoError(t, err)
	return g
}

// TestAGroupNameIsRequiredAndUnique proves that an empty group name and a
// repeated one are rejected.
func TestAGroupNameIsRequiredAndUnique(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	_, err := svc.CreateGroup(ctx, GroupInput{Name: "   "})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = svc.CreateGroup(ctx, GroupInput{Name: "VIP"})
	require.NoError(t, err)

	_, err = svc.CreateGroup(ctx, GroupInput{Name: "VIP"})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}

// TestAGroupCanBeUpdated proves that a group's name and metadata can be
// corrected.
//
// A name is unique among live groups; without a way to correct it, a mistyped
// name would occupy that name forever, and pricing's segment context would
// stay pinned to an id that could not be corrected.
func TestAGroupCanBeUpdated(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	group := newTestGroup(ctx, t, svc, "VIPP")

	corrected := "  VIP  "
	updated, err := svc.UpdateGroup(ctx, group.ID, UpdateGroupInput{
		Name:     &corrected,
		Metadata: map[string]any{"discount": "10"},
	})
	require.NoError(t, err)
	assert.Equal(t, "VIP", updated.Name, "the name has to be written trimmed")
	assert.Equal(t, "10", updated.Metadata["discount"])

	// The fields that are not given stay AS THEY ARE.
	updated, err = svc.UpdateGroup(ctx, group.ID, UpdateGroupInput{})
	require.NoError(t, err)
	assert.Equal(t, "VIP", updated.Name, "a name that is not given has to be kept")
	assert.Equal(t, "10", updated.Metadata["discount"], "metadata that is not given has to be kept")

	// A name, IF GIVEN, cannot be empty; a partial update cannot lift a
	// requirement.
	blank := "   "
	_, err = svc.UpdateGroup(ctx, group.ID, UpdateGroupInput{Name: &blank})
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "an empty name has to be rejected")

	// Another live group's name cannot be taken.
	other := newTestGroup(ctx, t, svc, "B2B")
	taken := "VIP"
	_, err = svc.UpdateGroup(ctx, other.ID, UpdateGroupInput{Name: &taken})
	assert.Equal(t, errors.KindConflict, errors.KindOf(err), "a name in use has to give a conflict")

	// A group that does not exist is NotFound.
	_, err = svc.UpdateGroup(ctx, models.NewCustomerGroupID(fixedClock), UpdateGroupInput{})
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestADeletedGroupAppearsInNoRead proves that a soft-deleted group drops out
// of every read path.
//
// The membership rows are not deleted; the one thing that makes the group
// invisible is the deleted_at IS NULL filter of every query that reads a
// group. Were the filter dropped, a deleted group would stay among the
// customer's segments and be carried into the price computation.
func TestADeletedGroupAppearsInNoRead(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	member := newTestCustomer(ctx, t, svc, "segment@example.com")
	group := newTestGroup(ctx, t, svc, "VIP")
	require.NoError(t, svc.AddToGroup(ctx, member.ID, group.ID))

	require.NoError(t, svc.DeleteGroup(ctx, group.ID))

	_, err := svc.GetGroup(ctx, group.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "a deleted group must not be readable")

	groupPage, err := svc.ListGroups(ctx, 0, 0)
	require.NoError(t, err)
	assert.Zero(t, groupPage.Count, "a deleted group must not appear in the list")

	groups, err := svc.ListGroupsOf(ctx, member.ID)
	require.NoError(t, err)
	assert.Empty(t, groups, "a deleted group must not appear among the customer's groups")

	ids, err := svc.CustomerGroupIDs(ctx, member.ID)
	require.NoError(t, err)
	assert.Empty(t, ids, "a deleted group must not be carried into the price context")

	page, err := svc.ListCustomers(ctx, ListCustomersInput{GroupID: &group.ID})
	require.NoError(t, err)
	assert.Zero(t, page.Count, "a deleted group's members must not be listed by the filter")

	assert.Equal(t, errors.KindNotFound, errors.KindOf(svc.AddToGroup(ctx, member.ID, group.ID)),
		"no member may be added to a deleted group")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(svc.DeleteGroup(ctx, group.ID)),
		"a deleted group must not be deletable a second time")

	// The name is free again because it has left the partial unique index's
	// scope.
	_, err = svc.CreateGroup(ctx, GroupInput{Name: "VIP"})
	require.NoError(t, err, "a deleted group's name has to be reusable")
}

// TestAddingToAGroupIsIdempotent proves that adding the same membership twice
// gives no error.
//
// A membership is a SET; a retry or a double click has to give the same
// result.
func TestAddingToAGroupIsIdempotent(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	customer := newTestCustomer(ctx, t, svc, "member@example.com")
	group := newTestGroup(ctx, t, svc, "VIP")

	require.NoError(t, svc.AddToGroup(ctx, customer.ID, group.ID))
	require.NoError(t, svc.AddToGroup(ctx, customer.ID, group.ID), "the second addition must not give an error")

	groups, err := svc.ListGroupsOf(ctx, customer.ID)
	require.NoError(t, err)
	require.Len(t, groups, 1, "the membership must not be duplicated")
	assert.Equal(t, group.ID, groups[0].ID)
}

// TestRemovingFromAGroupIsNotIdempotent proves that removing a membership that
// does not exist returns NotFound.
//
// Adding is idempotent and removing is not: removing a membership that does not
// exist is the most common sign that the client called with the wrong id, and
// quietly returning success would hide that mistake.
func TestRemovingFromAGroupIsNotIdempotent(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	customer := newTestCustomer(ctx, t, svc, "remove@example.com")
	group := newTestGroup(ctx, t, svc, "B2B")

	err := svc.RemoveFromGroup(ctx, customer.ID, group.ID)
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	require.NoError(t, svc.AddToGroup(ctx, customer.ID, group.ID))
	require.NoError(t, svc.RemoveFromGroup(ctx, customer.ID, group.ID))

	groups, err := svc.ListGroupsOf(ctx, customer.ID)
	require.NoError(t, err)
	assert.Empty(t, groups)
}

// TestAMissingSideIsNotFound proves that NotFound is returned when the
// customer or the group does not exist.
func TestAMissingSideIsNotFound(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	customer := newTestCustomer(ctx, t, svc, "missing@example.com")
	group := newTestGroup(ctx, t, svc, "Wholesale")

	err := svc.AddToGroup(ctx, models.NewCustomerID(fixedClock), group.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "a missing customer")

	err = svc.AddToGroup(ctx, customer.ID, models.NewCustomerGroupID(fixedClock))
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "a missing group")

	_, err = svc.ListGroupsOf(ctx, models.NewCustomerID(fixedClock))
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"a missing customer has to get NotFound, not an empty list")
}

// TestListingByGroup proves filtering by group membership.
func TestListingByGroup(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	member := newTestCustomer(ctx, t, svc, "member2@example.com")
	newTestCustomer(ctx, t, svc, "nonmember@example.com")
	group := newTestGroup(ctx, t, svc, "VIP")
	require.NoError(t, svc.AddToGroup(ctx, member.ID, group.ID))

	page, err := svc.ListCustomers(ctx, ListCustomersInput{GroupID: &group.ID})
	require.NoError(t, err)
	assert.Equal(t, int64(1), page.Count)
	require.Len(t, page.Items, 1)
	assert.Equal(t, member.ID, page.Items[0].ID)
}

// TestTheCrossModuleSurface proves the cross-module methods with primitive
// signatures.
//
// The signatures use ONLY primitive types; a consumer module cannot import
// customer, so a signature like this is the only kind it can repeat in its own
// package (ADR 0001).
func TestTheCrossModuleSurface(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	id, err := svc.RegisterGuestCustomer(ctx, "Interop@Example.com", "Ali", "Veli", "555")
	require.NoError(t, err)
	assert.True(t, len(id) > len(models.CustomerIDPrefix))

	guest, err := svc.GetCustomer(ctx, id)
	require.NoError(t, err)
	assert.False(t, guest.HasAccount, "a cross-module registration has to open a GUEST too")

	email, err := svc.CustomerEmail(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "interop@example.com", email)

	ids, err := svc.CustomerGroupIDs(ctx, id)
	require.NoError(t, err)
	assert.NotNil(t, ids, "a customer with no group has to get an empty slice")
	assert.Empty(t, ids)

	group := newTestGroup(ctx, t, svc, "VIP")
	require.NoError(t, svc.AddToGroup(ctx, id, group.ID))

	ids, err = svc.CustomerGroupIDs(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, []string{group.ID}, ids)

	_, err = svc.CustomerEmail(ctx, models.NewCustomerID(fixedClock))
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}
