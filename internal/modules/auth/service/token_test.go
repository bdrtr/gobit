package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// This file proves the validity conditions of the session token OTHER THAN its
// LIFETIME; its real subject is the password change dropping the open sessions.
//
// The token holds no state and there is no revocation list: the ONLY way to
// take back a leaked admin token before it expires is to change the password.
// This is why every one of the claims below is a security claim, not a
// behavior preference.

// The constants the tests share. The passwords are long enough to exceed the
// lower bound of the policy ([service.MinPasswordLen]).
const (
	sessionSecret        = "test-jwt-signing-secret"
	sessionEmail         = "admin@gobit.test"
	sessionPassword      = "old-Password-1234"
	sessionNewPassword   = "new-Password-1234"
	sessionWrongPassword = "wrong-Password-9999"
	sessionUserID        = "user_test"
	sessionIdentityID    = "authid_test"
)

// sessionClock is the time source the test advances.
//
// Working with the real clock IS NOT POSSIBLE in this file: the subject of the
// claims is the difference IN SECONDS between the token's "iat" value and the
// moment of the password change, and on a real clock that difference would
// depend on the speed of the test.
type sessionClock struct {
	moment time.Time
}

// now returns the current moment.
func (s *sessionClock) now() time.Time { return s.moment }

// advance moves the clock forward by the given duration.
func (s *sessionClock) advance(d time.Duration) { s.moment = s.moment.Add(d) }

// sessionRepo is the in-memory, SINGLE-USER implementation of the service's
// repository surface.
//
// [service.Repository] is kept embedded and only the methods these tests touch
// are written: the interface carries more than thirty methods and writing all
// of them by hand would fill the file with irrelevant bodies. If a method that
// was not written is touched, the call PANICS over the nil interface; it does
// not silently return a zero value, that is, the claim "this flow must never
// go there" cannot rot while the test is green.
//
// The write methods imitate the CONTRACT of the corresponding SQL query; which
// query carries updated_at is exactly the subject of these tests (see
// queries/identities.sql).
type sessionRepo struct {
	service.Repository

	user models.User
	// identities are the user's identity rows: AT MOST ONE per provider. The
	// slice imitates the (user_id, provider) uniqueness on the table and makes
	// the multiplicity the real schema allows — a second provider — set up-able
	// in a test.
	identities []models.AuthIdentity
	// identityDeleted, when true, makes the repository behave as though the user
	// has no live identity left; the scenario of a token belonging to a user
	// whose identity was deleted is set up this way.
	identityDeleted bool
}

// identity returns the row of the given provider; nil if there is none.
//
// It returns a pointer because most of its callers WRITE to the row; had a copy
// been returned the write would never reach the repository and the tests would
// silently stay green.
func (d *sessionRepo) identity(provider string) *models.AuthIdentity {
	if d.identityDeleted {
		return nil
	}
	for i := range d.identities {
		if d.identities[i].Provider == provider {
			return &d.identities[i]
		}
	}
	return nil
}

// identityByID returns the row with the given identifier; nil if there is none.
func (d *sessionRepo) identityByID(id string) *models.AuthIdentity {
	if d.identityDeleted {
		return nil
	}
	for i := range d.identities {
		if d.identities[i].ID == id {
			return &d.identities[i]
		}
	}
	return nil
}

// anchor returns the session anchor of the given provider; the zero time if
// there is no row.
//
// The claim of the tests looks at the updated_at value of the rows one by one:
// "the logout advanced all of them" can only be proved by asking row by row.
func (d *sessionRepo) anchor(provider string) time.Time {
	identity := d.identity(provider)
	if identity == nil {
		return time.Time{}
	}
	return identity.UpdatedAt
}

// GetUser returns the user; errors.NotFound if it holds no such identifier.
func (d *sessionRepo) GetUser(_ context.Context, id string) (models.User, error) {
	if id != d.user.ID {
		return models.User{}, errors.NotFound("test_user_missing", "no such user: %s", id)
	}
	return d.user, nil
}

// GetUserByEmail returns the user by their email address; errors.NotFound if
// there is none.
func (d *sessionRepo) GetUserByEmail(_ context.Context, email string) (models.User, error) {
	if email != d.user.Email {
		return models.User{}, errors.NotFound("test_user_missing", "no such user: %s", email)
	}
	return d.user, nil
}

// GetIdentity returns the login identity; errors.NotFound if there is none.
func (d *sessionRepo) GetIdentity(_ context.Context, userID, provider string) (models.AuthIdentity, error) {
	identity := d.identity(provider)
	if identity == nil || userID != identity.UserID {
		return models.AuthIdentity{}, errors.NotFound("test_identity_missing",
			"user %s has no %q identity", userID, provider)
	}
	return *identity, nil
}

// SetPasswordHash writes the hash, resets the lock counters and advances
// updated_at.
//
// It touches only the row of the GIVEN provider: the password is the
// information of the emailpass identity and it has no counterpart in the rows
// of other providers.
//
// updated_at advancing is the contract of the UpdatePasswordHash query and it
// is the anchor of session revocation.
func (d *sessionRepo) SetPasswordHash(
	_ context.Context,
	userID, provider, providerIdentity, hash string,
	now time.Time,
) (models.AuthIdentity, error) {
	identity := d.identity(provider)
	if identity == nil {
		// The real repository CREATES the identity if there is none; the fake
		// repository does the same, otherwise an "assign a password" call would
		// fail here with an error that does not really exist.
		d.identities = append(d.identities, models.AuthIdentity{
			ID:               "authid_" + provider,
			UserID:           userID,
			Provider:         provider,
			ProviderIdentity: providerIdentity,
			CreatedAt:        now,
		})
		identity = &d.identities[len(d.identities)-1]
	}
	identity.PasswordHash = hash
	identity.FailedAttempts = 0
	identity.LockedUntil = nil
	identity.UpdatedAt = now
	return *identity, nil
}

// SessionAnchor returns the user's NEWEST session anchor; errors.NotFound if
// there is no live identity.
//
// That is the contract of the query (see queries/identities.sql,
// GetSessionAnchor): no provider is selected, the furthest ahead of the rows is
// taken.
func (d *sessionRepo) SessionAnchor(_ context.Context, userID string) (time.Time, error) {
	if d.identityDeleted || userID != d.user.ID || len(d.identities) == 0 {
		return time.Time{}, errors.NotFound("test_identity_missing",
			"user %s has no identity at all", userID)
	}

	var newest time.Time
	for i := range d.identities {
		if d.identities[i].UpdatedAt.After(newest) {
			newest = d.identities[i].UpdatedAt
		}
	}
	return newest, nil
}

// RevokeSessions advances the anchor of ALL of the user's identities and DOES
// NOT TOUCH the credential.
//
// That is the contract of the query (see queries/identities.sql,
// RevokeSessions): no provider is selected, only updated_at is written; the
// password, counter and lock fields are preserved.
func (d *sessionRepo) RevokeSessions(
	_ context.Context,
	userID string,
	now time.Time,
) ([]models.AuthIdentity, error) {
	if d.identityDeleted || userID != d.user.ID || len(d.identities) == 0 {
		return nil, errors.NotFound("test_identity_missing",
			"user %s has no identity at all", userID)
	}

	advanced := make([]models.AuthIdentity, 0, len(d.identities))
	for i := range d.identities {
		d.identities[i].UpdatedAt = now
		advanced = append(advanced, d.identities[i])
	}
	return advanced, nil
}

// RegisterLoginSuccess clears the counters and writes the last login moment.
//
// It DOES NOT TOUCH updated_at; that is the contract of the query.
func (d *sessionRepo) RegisterLoginSuccess(_ context.Context, identityID string, now time.Time) error {
	identity := d.identityByID(identityID)
	if identity == nil {
		return errors.NotFound("test_identity_missing", "no such identity: %s", identityID)
	}
	identity.FailedAttempts = 0
	identity.LockedUntil = nil
	identity.LastLoginAt = &now
	return nil
}

// RegisterLoginFailure counts the failed attempt and locks at the threshold.
//
// It DOES NOT TOUCH updated_at; that is the contract of the query.
func (d *sessionRepo) RegisterLoginFailure(
	_ context.Context,
	identityID string,
	threshold int,
	lockUntil, _ time.Time,
) (models.AuthIdentity, error) {
	identity := d.identityByID(identityID)
	if identity == nil {
		return models.AuthIdentity{}, errors.NotFound("test_identity_missing",
			"no such identity: %s", identityID)
	}
	identity.FailedAttempts++
	if identity.FailedAttempts >= threshold {
		lock := lockUntil
		identity.LockedUntil = &lock
	}
	return *identity, nil
}

// setupSession produces a service on a fixed clock, its authenticator and the
// fake repository.
//
// The bcrypt cost is at its lowest value: no claim in this file has anything to
// do with the cost, and the default cost would slow every test down by a
// quarter of a second.
func setupSession(t *testing.T) (*service.Service, *service.Interop, *sessionRepo, *sessionClock) {
	t.Helper()

	start := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	clock := &sessionClock{moment: start}

	hash, err := bcrypt.GenerateFromPassword([]byte(sessionPassword), bcrypt.MinCost)
	require.NoError(t, err, "the test password could not be hashed")

	repo := &sessionRepo{
		user: models.User{
			ID:        sessionUserID,
			Email:     sessionEmail,
			Scopes:    []string{models.ScopeAdmin},
			CreatedAt: start,
			UpdatedAt: start,
		},
		identities: []models.AuthIdentity{{
			ID:               sessionIdentityID,
			UserID:           sessionUserID,
			Provider:         models.ProviderEmailPass,
			ProviderIdentity: sessionEmail,
			PasswordHash:     string(hash),
			CreatedAt:        start,
			UpdatedAt:        start,
		}},
	}

	svc := service.New(repo, service.Options{
		Now:        clock.now,
		JWTSecret:  sessionSecret,
		BcryptCost: bcrypt.MinCost,
	})
	return svc, service.NewInterop(svc), repo, clock
}

// obtainSessionToken logs in and returns the session token.
func obtainSessionToken(t *testing.T, svc *service.Service, password string) string {
	t.Helper()

	token, _, err := svc.Login(context.Background(), sessionEmail, password)
	require.NoError(t, err, "the login has to succeed")
	require.NotEmpty(t, token, "the login has to return a token")
	return token
}

// resolveSessionPrincipal verifies the token on the admin surface.
func resolveSessionPrincipal(interop *service.Interop, token string) (corehttp.Principal, error) {
	return interop.AuthenticateAdmin(context.Background(), "Bearer", token)
}

// requireSessionRejected verifies that the token fell with errors.Unauthorized.
func requireSessionRejected(t *testing.T, err error, reason string) {
	t.Helper()

	require.Error(t, err, reason)
	assert.True(t, errors.IsUnauthorized(err),
		"%s — the expected kind is Unauthorized, got: %v", reason, errors.KindOf(err))
	assert.Equal(t, service.CodeTokenInvalid, errors.CodeOf(err),
		"%s — a token rejection has to be reported with a single code", reason)
}

// TestPasswordChangeRejectsTheEarlierToken proves that a password change drops
// the open sessions.
//
// Without the check, a leaked admin token would keep producing a fully
// privileged identity for [service.DefaultJWTTTL] (12 hours by default) even if
// the password had been changed.
func TestPasswordChangeRejectsTheEarlierToken(t *testing.T) {
	svc, interop, repo, clock := setupSession(t)
	ctx := context.Background()

	oldToken := obtainSessionToken(t, svc, sessionPassword)
	_, err := resolveSessionPrincipal(interop, oldToken)
	require.NoError(t, err, "the token has to be accepted before the password changes")

	clock.advance(2 * time.Second)
	require.NoError(t, svc.SetPassword(ctx, repo.user.ID, sessionNewPassword),
		"the password change has to succeed")

	_, err = resolveSessionPrincipal(interop, oldToken)
	requireSessionRejected(t, err, "the old token must not be accepted after the password changed")

	clock.advance(time.Second)
	newToken := obtainSessionToken(t, svc, sessionNewPassword)
	principal, err := resolveSessionPrincipal(interop, newToken)
	require.NoError(t, err, "a token obtained AFTER the change has to work")
	assert.Equal(t, sessionUserID, principal.ID)
	assert.Equal(t, []string{models.ScopeAdmin}, principal.Scopes)
}

// TestTokenIssuedInTheSameSecondAsThePasswordChangeStaysValid pins down the
// choice made in the edge case.
//
// "iat" has second resolution; whether a token produced in the same second as
// the change was born before or after it cannot be read from the token. The
// ambiguity is resolved in favor of USABILITY (the reasoning is in
// service/token.go, issuedBefore). The opposite choice would drop the fresh
// token of a user who changes their password and logs in right away — which is
// exactly what setup scripts do.
func TestTokenIssuedInTheSameSecondAsThePasswordChangeStaysValid(t *testing.T) {
	svc, interop, repo, clock := setupSession(t)
	ctx := context.Background()

	clock.advance(500 * time.Millisecond)
	require.NoError(t, svc.SetPassword(ctx, repo.user.ID, sessionNewPassword))

	// We stay inside the same second: 10:00:00.500 -> 10:00:00.900.
	clock.advance(400 * time.Millisecond)
	token := obtainSessionToken(t, svc, sessionNewPassword)

	_, err := resolveSessionPrincipal(interop, token)
	require.NoError(t, err,
		"a token produced in the same second as the change must not be rejected")
}

// TestFailedLoginAttemptDoesNotDropTheSession proves that an attacker cannot
// throw the victim out.
//
// The anchor of session revocation is the identity's updated_at value. Had the
// failed attempt counter advanced that column, anyone who knows the victim's
// email address could close all of their sessions with a single wrong password
// attempt: a targeted denial-of-service tool.
func TestFailedLoginAttemptDoesNotDropTheSession(t *testing.T) {
	svc, interop, _, clock := setupSession(t)
	ctx := context.Background()

	token := obtainSessionToken(t, svc, sessionPassword)

	clock.advance(5 * time.Second)
	_, _, err := svc.Login(ctx, sessionEmail, sessionWrongPassword)
	require.Error(t, err, "a wrong password has to be rejected")

	_, err = resolveSessionPrincipal(interop, token)
	require.NoError(t, err, "a failed attempt must not close the victim's session")
}

// TestSecondLoginDoesNotDropTheFirstSession proves that the same user can stay
// open on two devices.
//
// Had a successful login advanced updated_at too, logging in from a second
// device would silently close the session of the first one.
func TestSecondLoginDoesNotDropTheFirstSession(t *testing.T) {
	svc, interop, _, clock := setupSession(t)

	firstToken := obtainSessionToken(t, svc, sessionPassword)

	clock.advance(5 * time.Second)
	secondToken := obtainSessionToken(t, svc, sessionPassword)
	require.NotEqual(t, firstToken, secondToken, "two logins have to produce different tokens")

	_, err := resolveSessionPrincipal(interop, firstToken)
	require.NoError(t, err, "the first device's session has to stay valid after the second login")

	_, err = resolveSessionPrincipal(interop, secondToken)
	require.NoError(t, err, "the second device's session has to be valid")
}

// TestTokenWithoutIssueTimeIsRejected proves that the "iat" claim is mandatory.
//
// Session revocation rests on this claim; when a token does not carry it, when
// it was produced cannot be known and no comparison can be made. The signing
// secret here is deliberately the RIGHT one: the reason for the rejection is
// not the signature but the missing claim.
func TestTokenWithoutIssueTimeIsRejected(t *testing.T) {
	_, interop, _, clock := setupSession(t)

	claims := jwt.MapClaims{
		"sub":    sessionUserID,
		"iss":    service.DefaultIssuer,
		"exp":    clock.now().Add(time.Hour).Unix(),
		"scopes": []string{models.ScopeAdmin},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(sessionSecret))
	require.NoError(t, err, "the test token could not be signed")

	_, err = resolveSessionPrincipal(interop, token)
	requireSessionRejected(t, err, "a token with no \"iat\" claim must not be accepted")
}

// TestTokenOfDeletedLoginIdentityIsRejected proves that the token of a user
// whose identity was deleted drops.
//
// If there is no identity row there is also no value that could say when the
// token became invalid; accepting it would leave a door open to bypassing the
// check by deleting the identity.
func TestTokenOfDeletedLoginIdentityIsRejected(t *testing.T) {
	svc, interop, repo, _ := setupSession(t)

	token := obtainSessionToken(t, svc, sessionPassword)
	repo.identityDeleted = true

	_, err := resolveSessionPrincipal(interop, token)
	requireSessionRejected(t, err, "the token of a user whose login identity was deleted must not be accepted")
}
