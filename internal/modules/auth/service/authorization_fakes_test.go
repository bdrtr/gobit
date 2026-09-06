package service_test

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// fakeRepo is the implementation of [service.Repository] written for the scope
// tests.
//
// The repository DOES NOT STORE the data, it only remembers what was written to
// it. The tests of this file exercise the service's scope gate; while the gate
// is shut nothing must reach the repository, and while it is open the resolved
// scope list has to pass through as it is. A real in-memory repository would
// add nothing to these two claims and would make the test unreadable.
type fakeRepo struct {
	// writeCount is the number of write calls that came down to the repository.
	writeCount int
	// lastKey is the API key written to the repository last.
	lastKey models.APIKey
	// lastUser is the user written to the repository last.
	lastUser models.User
	// lastPatch is the partial update applied to a user last.
	lastPatch models.UserPatch
}

var _ service.Repository = (*fakeRepo)(nil)

func (d *fakeRepo) CreateUser(
	_ context.Context,
	u models.User,
	_ *models.AuthIdentity,
) (models.User, error) {
	d.writeCount++
	d.lastUser = u
	return u, nil
}

func (d *fakeRepo) GetUser(_ context.Context, id string) (models.User, error) {
	return models.User{ID: id}, nil
}

func (d *fakeRepo) GetUserByEmail(_ context.Context, _ string) (models.User, error) {
	return models.User{}, errors.NotFound("user_not_found", "the user was not found")
}

func (d *fakeRepo) ListUsers(
	_ context.Context,
	_ models.UserFilter,
	_, _ int64,
) ([]models.User, int64, error) {
	return nil, 0, nil
}

func (d *fakeRepo) GetUsersByIDs(_ context.Context, _ []string) ([]models.User, error) {
	return nil, nil
}

func (d *fakeRepo) UpdateUser(
	_ context.Context,
	id string,
	patch models.UserPatch,
	_ time.Time,
) (models.User, error) {
	d.writeCount++
	d.lastPatch = patch
	return models.User{ID: id, Scopes: patch.Scopes}, nil
}

func (d *fakeRepo) DeleteUser(_ context.Context, _ string, _ time.Time) error {
	d.writeCount++
	return nil
}

func (d *fakeRepo) GetIdentity(_ context.Context, _, _ string) (models.AuthIdentity, error) {
	return models.AuthIdentity{}, errors.NotFound("identity_not_found", "the identity was not found")
}

func (d *fakeRepo) SetPasswordHash(
	_ context.Context,
	_, _, _ string,
	_ time.Time,
) (models.AuthIdentity, error) {
	d.writeCount++
	return models.AuthIdentity{}, nil
}

func (d *fakeRepo) SessionAnchor(_ context.Context, _ string) (time.Time, error) {
	return time.Time{}, errors.NotFound("identity_not_found", "the identity was not found")
}

func (d *fakeRepo) RevokeSessions(
	_ context.Context,
	_ string,
	now time.Time,
) ([]models.AuthIdentity, error) {
	d.writeCount++
	return []models.AuthIdentity{{UpdatedAt: now}}, nil
}

func (d *fakeRepo) RegisterLoginFailure(
	_ context.Context,
	_ string,
	_ int,
	_, _ time.Time,
) (models.AuthIdentity, error) {
	return models.AuthIdentity{}, nil
}

func (d *fakeRepo) RegisterLoginSuccess(_ context.Context, _ string, _ time.Time) error {
	return nil
}

func (d *fakeRepo) CreateAPIKey(_ context.Context, k models.APIKey) (models.APIKey, error) {
	d.writeCount++
	d.lastKey = k
	return k, nil
}

func (d *fakeRepo) GetAPIKey(_ context.Context, id string) (models.APIKey, error) {
	return models.APIKey{ID: id, Type: models.APIKeyPublishable}, nil
}

func (d *fakeRepo) GetAPIKeyByHash(_ context.Context, _ string) (models.APIKey, error) {
	return models.APIKey{}, errors.NotFound("api_key_not_found", "the api key was not found")
}

func (d *fakeRepo) ListAPIKeys(
	_ context.Context,
	_ models.APIKeyFilter,
	_, _ int64,
) ([]models.APIKey, int64, error) {
	return nil, 0, nil
}

func (d *fakeRepo) RevokeAPIKey(
	_ context.Context,
	id, _ string,
	_ time.Time,
) (models.APIKey, error) {
	d.writeCount++
	return models.APIKey{ID: id}, nil
}

func (d *fakeRepo) DeleteAPIKey(_ context.Context, _ string, _ time.Time) error {
	d.writeCount++
	return nil
}

func (d *fakeRepo) MarkAPIKeyUsed(_ context.Context, _ string, _, _ time.Time) error {
	return nil
}

func (d *fakeRepo) LinkSalesChannel(_ context.Context, _, _ string, _ time.Time) error {
	d.writeCount++
	return nil
}

func (d *fakeRepo) UnlinkSalesChannel(_ context.Context, _, _ string) error {
	d.writeCount++
	return nil
}

func (d *fakeRepo) ChannelIDsOfKey(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (d *fakeRepo) ChannelsOfKey(_ context.Context, _ string) ([]models.SalesChannel, error) {
	return nil, nil
}

func (d *fakeRepo) CreateSalesChannel(
	_ context.Context,
	c models.SalesChannel,
) (models.SalesChannel, error) {
	d.writeCount++
	return c, nil
}

func (d *fakeRepo) GetSalesChannel(_ context.Context, id string) (models.SalesChannel, error) {
	return models.SalesChannel{ID: id}, nil
}

func (d *fakeRepo) ListSalesChannels(
	_ context.Context,
	_ models.SalesChannelFilter,
	_, _ int64,
) ([]models.SalesChannel, int64, error) {
	return nil, 0, nil
}

func (d *fakeRepo) GetSalesChannelsByIDs(
	_ context.Context,
	_ []string,
) ([]models.SalesChannel, error) {
	return nil, nil
}

func (d *fakeRepo) UpdateSalesChannel(
	_ context.Context,
	id string,
	_ models.SalesChannelPatch,
	_ time.Time,
) (models.SalesChannel, error) {
	d.writeCount++
	return models.SalesChannel{ID: id}, nil
}

func (d *fakeRepo) DeleteSalesChannel(_ context.Context, _ string, _ time.Time) error {
	d.writeCount++
	return nil
}
