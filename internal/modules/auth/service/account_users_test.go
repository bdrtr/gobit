package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// listingRepo answers the user listing as scripted, recording what it was
// asked for, over the shared fake.
type listingRepo struct {
	*fakeRepo
	users         []models.User
	count         int64
	filter        models.UserFilter
	limit, offset int64
}

func (r *listingRepo) ListUsers(
	_ context.Context, filter models.UserFilter, limit, offset int64,
) ([]models.User, int64, error) {
	r.filter, r.limit, r.offset = filter, limit, offset
	return r.users, r.count, nil
}

// TestThePanelListsTheUsers is ADR 0345: the surface lists a page of the
// users with how many there are, each with their privileges and whether
// they have proven an authenticator; it asks for the one with the e-mail
// and for those with or without a proven authenticator, and refuses a tab
// there is not.
func TestThePanelListsTheUsers(t *testing.T) {
	t.Parallel()

	repo := &listingRepo{fakeRepo: &fakeRepo{mfa: map[string]models.MFACredential{}}}
	confirmed := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	repo.mfa["usr_ada"] = models.MFACredential{UserID: "usr_ada", ConfirmedAt: &confirmed}
	repo.users = []models.User{
		{ID: "usr_ada", Email: "ada@example.test", FirstName: "Ada", LastName: "Lovelace",
			Scopes: []string{"order:read", "order:write"}, CreatedAt: time.Date(2026, 5, 2, 8, 0, 0, 0, time.UTC)},
		{ID: "usr_bob", Email: "bob@example.test", CreatedAt: time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)},
	}
	repo.count = 12
	svc := service.New(repo, service.Options{JWTSecret: "test-signing-secret-long-enough"})
	surface := service.NewAccountSurface(svc, "gobit")

	raw, total, err := surface.UsersJSON(context.Background(), "", "", 25, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(12), total)
	assert.JSONEq(t, `[{"id":"usr_ada","email":"ada@example.test","first_name":"Ada","last_name":"Lovelace",
		"scopes":["order:read","order:write"],"second_factor":true,"created_at":"2026-05-02T08:00:00Z"},
		{"id":"usr_bob","email":"bob@example.test","first_name":"","last_name":"","scopes":[],
		"second_factor":false,"created_at":"2026-05-01T08:00:00Z"}]`, string(raw))
	assert.Equal(t, []int64{25, 50}, []int64{repo.limit, repo.offset})
	assert.Nil(t, repo.filter.Email, "every user")
	assert.Nil(t, repo.filter.SecondFactor)
	assert.Nil(t, repo.filter.Scope)

	_, _, err = surface.UsersJSON(context.Background(), " Ada@Example.test ", service.SecondFactorMissing, 25, 0)
	require.NoError(t, err)
	require.NotNil(t, repo.filter.Email)
	assert.Equal(t, "ada@example.test", *repo.filter.Email, "the e-mail as the API finds it")
	require.NotNil(t, repo.filter.SecondFactor)
	assert.False(t, *repo.filter.SecondFactor, "those who have not proven one")
	_, _, err = surface.UsersJSON(context.Background(), "", service.SecondFactorProven, 25, 0)
	require.NoError(t, err)
	require.NotNil(t, repo.filter.SecondFactor)
	assert.True(t, *repo.filter.SecondFactor, "those who have")
	assert.Nil(t, repo.filter.Email)

	_, _, err = surface.UsersJSON(context.Background(), "", "maybe", 25, 0)
	assert.True(t, errors.IsInvalid(err), "a tab there is not: %v", err)
	repo.users = nil
	raw, _, err = surface.UsersJSON(context.Background(), "", "", 25, 0)
	require.NoError(t, err)
	assert.JSONEq(t, `[]`, string(raw))
}
