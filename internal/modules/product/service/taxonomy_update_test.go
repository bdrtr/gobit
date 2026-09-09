package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// The tests in this file pin what the SERVICE decides about a category update.
//
// The rule that a category may not be moved under its own descendant is NOT
// here: it lives in the statement, and it is proved against a real database in
// the module's integration tests. What is decided here is which requests are
// refused before any statement runs, and which fields a PATCH leaves alone.

// TestUpdatingACategoryLeavesTheFieldsItWasNotGiven pins the PATCH contract.
//
// A field that is not supplied is preserved. It is the same contract
// UpdateProduct documents, and it is worth pinning separately because the
// category's update carries a field the product's does not — the parent — and
// an implementation that wrote every column from a partially filled struct
// would blank a name nobody asked to change.
func TestUpdatingACategoryLeavesTheFieldsItWasNotGiven(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	description := "the winter range"
	created, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Winter", Handle: "winter", Description: &description, Rank: 7,
	})
	require.NoError(t, err)

	renamed := "Winter 2027"
	updated, err := svc.UpdateCategory(ctx, created.ID, service.UpdateCategoryInput{Name: &renamed})
	require.NoError(t, err)

	assert.Equal(t, renamed, updated.Name, "the name was the field that was given")
	assert.Equal(t, "winter", updated.Handle, "the handle was not given and must not move")
	require.NotNil(t, updated.Description)
	assert.Equal(t, description, *updated.Description, "the description was not given")
	assert.Equal(t, int32(7), updated.Rank, "the rank was not given")
	assert.True(t, updated.IsActive, "the flag was not given")
}

// TestACategoryIsMadeARootByClearParentAndNotByAnEmptyParent pins why the
// request carries a separate word for it.
//
// nil already means "leave the parent alone", so it cannot also mean "remove
// it". Without clear_parent a child could be renamed but never promoted, which
// is half of the same defect this write was added for.
func TestACategoryIsMadeARootByClearParentAndNotByAnEmptyParent(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	parent, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Clothing", Handle: "clothing",
	})
	require.NoError(t, err)
	child, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Coats", Handle: "coats", ParentID: &parent.ID,
	})
	require.NoError(t, err)
	require.NotNil(t, child.ParentID)

	// A PATCH that says nothing about the parent keeps it.
	renamed := "Winter coats"
	kept, err := svc.UpdateCategory(ctx, child.ID, service.UpdateCategoryInput{Name: &renamed})
	require.NoError(t, err)
	require.NotNil(t, kept.ParentID, "an unmentioned parent must not be removed")

	promoted, err := svc.UpdateCategory(ctx, child.ID, service.UpdateCategoryInput{ClearParent: true})
	require.NoError(t, err)
	assert.Nil(t, promoted.ParentID, "clear_parent has to make the category a root")
}

// TestACategoryUpdateRefusesTwoAnswersAboutTheParent keeps the request from
// being half-applied.
//
// parent_id names a new parent and clear_parent removes it; a body carrying
// both has asked for two different trees. The statement resolves the conflict
// in ONE direction, which is exactly why the service refuses it here — a caller
// who sent both would otherwise be told the write succeeded and find the other
// half of their request silently dropped.
func TestACategoryUpdateRefusesTwoAnswersAboutTheParent(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	parent, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Clothing", Handle: "clothing",
	})
	require.NoError(t, err)
	child, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Coats", Handle: "coats", ParentID: &parent.ID,
	})
	require.NoError(t, err)

	_, err = svc.UpdateCategory(ctx, child.ID, service.UpdateCategoryInput{
		ParentID: &parent.ID, ClearParent: true,
	})

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "error: %v", err)
}

// TestUpdatingAnUnknownCategoryIsNotFoundAndNotARefusal keeps the two answers
// apart.
//
// The statement matches no row for a missing id AND for a refused move, so on
// its own it cannot tell a caller which happened. The service resolves the id
// first for that reason, and this is what says so.
func TestUpdatingAnUnknownCategoryIsNotFoundAndNotARefusal(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	renamed := "anything"
	_, err := svc.UpdateCategory(ctx, "pcat_does_not_exist",
		service.UpdateCategoryInput{Name: &renamed})

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "error: %v", err)
	assert.NotEqual(t, "product_category_cycle", errors.CodeOf(err),
		"a missing id must not be reported as a refused move")
}

// TestUpdatingACategoryUnderAnUnknownParentNamesTheParent keeps the diagnosis
// on the field the caller got wrong.
func TestUpdatingACategoryUnderAnUnknownParentNamesTheParent(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	created, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Coats", Handle: "coats",
	})
	require.NoError(t, err)

	missing := "pcat_missing_parent"
	_, err = svc.UpdateCategory(ctx, created.ID, service.UpdateCategoryInput{ParentID: &missing})

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "error: %v", err)
	assert.Contains(t, err.Error(), missing, "the error has to name the parent that was not found")
}
