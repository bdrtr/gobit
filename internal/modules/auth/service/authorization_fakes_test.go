package service_test

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository"
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
	// userEmail is what GetUser answers with, because an otpauth label carries
	// the account name a person sees in their authenticator.
	userEmail string
	// mfa is the second-factor credential per user, and mfaErr the fault to
	// inject.
	mfa    map[string]models.MFACredential
	mfaErr error
	// writeCount is the number of write calls that came down to the repository.
	writeCount int
	// lastKey is the API key written to the repository last.
	lastKey models.APIKey
	// lastUser is the user written to the repository last.
	lastUser models.User
	// lastPatch is the partial update applied to a user last.
	lastPatch models.UserPatch
	// invites is the invitation table.
	invites fakeInvitations
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
	return models.User{ID: id, Email: d.userEmail}, nil
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

// fakeInvitations is the fake's invitation table, keyed by token hash.
//
// It imitates the two OBSERVABLE answers the real store gives: writing replaces the
// row that user already had, and taking one removes it in the same breath. That the
// SQL really does those is the integration lane's question.
type fakeInvitations struct {
	byHash map[string]models.UserInvitation
	// expired makes every row written already past its deadline.
	expired bool
}

// PutInvitation writes an invitation, replacing the user's own.
func (d *fakeRepo) PutInvitation(
	_ context.Context,
	tokenHash, userID, invitedBy string,
	expiresAt time.Time,
) (models.UserInvitation, error) {
	d.writeCount++
	if d.invites.byHash == nil {
		d.invites.byHash = map[string]models.UserInvitation{}
	}
	for hash, existing := range d.invites.byHash {
		if existing.UserID == userID {
			delete(d.invites.byHash, hash)
		}
	}
	if d.invites.expired {
		expiresAt = time.Now().UTC().Add(-time.Minute)
	}

	row := models.UserInvitation{
		TokenHash: tokenHash, UserID: userID, InvitedBy: invitedBy,
		ExpiresAt: expiresAt, CreatedAt: time.Now().UTC(),
	}
	d.invites.byHash[tokenHash] = row

	return row, nil
}

// TakeInvitation removes the row and answers it, refusing an expired one.
func (d *fakeRepo) TakeInvitation(
	_ context.Context, tokenHash string,
) (models.UserInvitation, error) {
	row, found := d.invites.byHash[tokenHash]
	if !found {
		return models.UserInvitation{}, repository.ErrNoInvitation
	}
	delete(d.invites.byHash, tokenHash)
	if !row.ExpiresAt.After(time.Now().UTC()) {
		return models.UserInvitation{}, repository.ErrNoInvitation
	}

	return row, nil
}

// --- second factor ------------------------------------------------------------

// PutMFACredential writes the enrolment, replacing any the user had.
//
// It clears the confirmation with it, which is what the ON CONFLICT in the real
// query does: a fake that kept the old stamp would let a test prove that a NEW
// secret counts as already proven.
func (d *fakeRepo) PutMFACredential(
	_ context.Context, userID string, sealed []byte,
) (models.MFACredential, error) {
	if d.mfaErr != nil {
		return models.MFACredential{}, d.mfaErr
	}
	if d.mfa == nil {
		d.mfa = map[string]models.MFACredential{}
	}

	d.mfa[userID] = models.MFACredential{UserID: userID, Secret: sealed}

	return d.mfa[userID], nil
}

// GetMFACredential reads one, or says there is none.
func (d *fakeRepo) GetMFACredential(
	_ context.Context, userID string,
) (models.MFACredential, error) {
	if d.mfaErr != nil {
		return models.MFACredential{}, d.mfaErr
	}

	credential, ok := d.mfa[userID]
	if !ok {
		return models.MFACredential{}, repository.ErrNoMFACredential
	}

	return credential, nil
}

// ConfirmMFACredential stamps an UNCONFIRMED credential and answers the sentinel
// for anything else, which is what the WHERE clause in the real query does.
func (d *fakeRepo) ConfirmMFACredential(
	_ context.Context, userID string,
) (models.MFACredential, error) {
	if d.mfaErr != nil {
		return models.MFACredential{}, d.mfaErr
	}

	credential, ok := d.mfa[userID]
	if !ok || credential.Confirmed() {
		return models.MFACredential{}, repository.ErrNoMFACredential
	}

	stamped := time.Now().UTC()
	credential.ConfirmedAt = &stamped
	d.mfa[userID] = credential

	return credential, nil
}
