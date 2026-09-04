package api_test

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/auth/api"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// fakeAuth is the tests' implementation of the [api.Auth] surface.
//
// The fake DOES NOT and must not imitate the BUSINESS LOGIC: the tests in this
// file test the authorization layer, that is, the cases in which the handler
// MUST NOT BE REACHED AT ALL. Every call is counted and the tests catch the "a
// 403 was returned but the service ran anyway" state with that counter — a test
// looking only at the status code could not notice a handler that returns the
// error after having performed the write.
type fakeAuth struct {
	// callCount is the number of calls that reached the service.
	callCount int
	// lastLogoutPrincipalID is the identity the logout endpoint passed to the
	// service.
	lastLogoutPrincipalID string
	// lastLogoutPrincipalKind is the identity KIND the logout endpoint passed
	// to the service.
	//
	// The field is kept separate because the "an api key cannot log out"
	// decision is the service's, and to be able to make that decision it has to
	// SEE the kind; if the handler does not pass the kind the service would
	// take every caller for a user.
	lastLogoutPrincipalKind string
	// logoutErr, when set, is the error Logout returns.
	logoutErr error
}

var _ api.Auth = (*fakeAuth)(nil)

// logoutMoment is the fixed revocation moment the fake returns from the logout
// endpoint.
//
// Being fixed is deliberate: that the response body carries this value AS IT IS
// can only be verified against a known moment.
var logoutMoment = time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

// hit counts one service call.
func (f *fakeAuth) hit() { f.callCount++ }

func (f *fakeAuth) Login(_ context.Context, _, _ string) (string, time.Time, error) {
	f.hit()
	return "token", time.Unix(0, 0).UTC(), nil
}

func (f *fakeAuth) Logout(_ context.Context, principalID, principalKind string) (time.Time, error) {
	f.hit()
	f.lastLogoutPrincipalID = principalID
	f.lastLogoutPrincipalKind = principalKind
	if f.logoutErr != nil {
		return time.Time{}, f.logoutErr
	}
	return logoutMoment, nil
}

func (f *fakeAuth) CreateUser(_ context.Context, _ service.CreateUserInput, _ string) (models.User, error) {
	f.hit()
	return models.User{ID: "usr_1"}, nil
}

func (f *fakeAuth) GetUser(_ context.Context, id string) (models.User, error) {
	f.hit()
	return models.User{ID: id}, nil
}

func (f *fakeAuth) ListUsers(_ context.Context, _ service.ListUsersInput) (service.Page[models.User], error) {
	f.hit()
	return service.Page[models.User]{}, nil
}

func (f *fakeAuth) UpdateUser(_ context.Context, id string, _ service.UpdateUserInput) (models.User, error) {
	f.hit()
	return models.User{ID: id}, nil
}

func (f *fakeAuth) DeleteUser(_ context.Context, _ string) error {
	f.hit()
	return nil
}

func (f *fakeAuth) SetPassword(_ context.Context, _, _ string) error {
	f.hit()
	return nil
}

func (f *fakeAuth) CreateAPIKey(_ context.Context, _ service.CreateAPIKeyInput) (models.APIKey, string, error) {
	f.hit()
	return models.APIKey{ID: "apk_1", Type: models.APIKeySecret}, "sk_plain", nil
}

func (f *fakeAuth) GetAPIKey(_ context.Context, id string) (models.APIKey, error) {
	f.hit()
	return models.APIKey{ID: id, Type: models.APIKeySecret}, nil
}

func (f *fakeAuth) ListAPIKeys(_ context.Context, _ service.ListAPIKeysInput) (service.Page[models.APIKey], error) {
	f.hit()
	return service.Page[models.APIKey]{}, nil
}

func (f *fakeAuth) RevokeAPIKey(_ context.Context, id, _ string) (models.APIKey, error) {
	f.hit()
	return models.APIKey{ID: id, Type: models.APIKeySecret}, nil
}

func (f *fakeAuth) DeleteAPIKey(_ context.Context, _ string) error {
	f.hit()
	return nil
}

func (f *fakeAuth) LinkSalesChannel(_ context.Context, _, _ string) error {
	f.hit()
	return nil
}

func (f *fakeAuth) UnlinkSalesChannel(_ context.Context, _, _ string) error {
	f.hit()
	return nil
}

func (f *fakeAuth) SalesChannelsOfAPIKey(_ context.Context, _ string) ([]models.SalesChannel, error) {
	f.hit()
	return nil, nil
}

func (f *fakeAuth) CreateSalesChannel(_ context.Context, _ service.SalesChannelInput) (models.SalesChannel, error) {
	f.hit()
	return models.SalesChannel{ID: "sc_1"}, nil
}

func (f *fakeAuth) GetSalesChannel(_ context.Context, id string) (models.SalesChannel, error) {
	f.hit()
	return models.SalesChannel{ID: id}, nil
}

func (f *fakeAuth) ListSalesChannels(
	_ context.Context,
	_ service.ListSalesChannelsInput,
) (service.Page[models.SalesChannel], error) {
	f.hit()
	return service.Page[models.SalesChannel]{}, nil
}

func (f *fakeAuth) UpdateSalesChannel(
	_ context.Context,
	id string,
	_ service.UpdateSalesChannelInput,
) (models.SalesChannel, error) {
	f.hit()
	return models.SalesChannel{ID: id}, nil
}

func (f *fakeAuth) DeleteSalesChannel(_ context.Context, _ string) error {
	f.hit()
	return nil
}
