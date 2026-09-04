package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// This file proves the CONTRACT of the logout endpoint.
//
// The fixtures are in token_test.go ([setupSession], [obtainSessionToken],
// [resolveSessionPrincipal], [requireSessionRejected]) and they are shared
// deliberately: the logout and the password change write to THE SAME anchor, so
// the tests of both have to stand on the same ground.

// TestLogoutDropsTheCallingToken proves that the logout drops the very token
// that called it.
//
// The finding was exactly this: there was no explicit logout path and the only
// way to drop all sessions was to change the password. This test pins down that
// the path has been opened and that it really is EFFECTIVE — an endpoint that
// returns 204 and writes nothing would pass a test that only looks at the
// status code.
func TestLogoutDropsTheCallingToken(t *testing.T) {
	svc, interop, _, clock := setupSession(t)
	ctx := context.Background()

	token := obtainSessionToken(t, svc, sessionPassword)
	_, err := resolveSessionPrincipal(interop, token)
	require.NoError(t, err, "the token has to be accepted before the logout")

	clock.advance(2 * time.Second)
	revokedAt, err := svc.Logout(ctx, sessionUserID, service.PrincipalKindUser)
	require.NoError(t, err, "the user has to be able to log out")
	assert.Equal(t, clock.now(), revokedAt,
		"the returned moment has to be the anchor written to the identity; the client eliminates its token by it")

	_, err = resolveSessionPrincipal(interop, token)
	requireSessionRejected(t, err, "the same token must not be accepted after the logout")
}

// TestTokenObtainedAfterLogoutWorks proves that the logout DOES NOT LOCK the
// door.
//
// Had the comparison been set up backwards while the anchor was advanced (or
// had the anchor been written into the future), the user could never get back
// in after logging out: the login would return 200 and the first request made
// with the token would get a 401.
func TestTokenObtainedAfterLogoutWorks(t *testing.T) {
	svc, interop, _, clock := setupSession(t)
	ctx := context.Background()

	_ = obtainSessionToken(t, svc, sessionPassword)

	clock.advance(2 * time.Second)
	_, err := svc.Logout(ctx, sessionUserID, service.PrincipalKindUser)
	require.NoError(t, err)

	// Because the ambiguity at the second boundary is resolved in favor of
	// USABILITY (see service/token.go, issuedBefore), a token obtained in the
	// same second would work too; the test still moves on to the next second,
	// because what it wants to prove is not the edge case but the normal flow.
	clock.advance(time.Second)
	newToken := obtainSessionToken(t, svc, sessionPassword)

	principal, err := resolveSessionPrincipal(interop, newToken)
	require.NoError(t, err, "a token obtained AFTER the logout has to work")
	assert.Equal(t, sessionUserID, principal.ID)
	assert.Equal(t, []string{models.ScopeAdmin}, principal.Scopes)
}

// TestLogoutDropsEveryDevice proves that the logout is WHOLESALE.
//
// This is the most easily misunderstood side of the contract: the other devices
// of a user who thinks "I logged out" drop as well. Dropping a single device
// would require a jti-based blacklist and that decision has been deliberately
// DEFERRED; this test pins down today's behavior in a way that cannot change in
// silence.
func TestLogoutDropsEveryDevice(t *testing.T) {
	svc, interop, _, clock := setupSession(t)
	ctx := context.Background()

	phone := obtainSessionToken(t, svc, sessionPassword)
	clock.advance(time.Second)
	laptop := obtainSessionToken(t, svc, sessionPassword)
	require.NotEqual(t, phone, laptop, "two logins have to produce different tokens")

	clock.advance(2 * time.Second)
	_, err := svc.Logout(ctx, sessionUserID, service.PrincipalKindUser)
	require.NoError(t, err)

	_, err = resolveSessionPrincipal(interop, phone)
	requireSessionRejected(t, err, "the token of the device that logged out has to drop")

	_, err = resolveSessionPrincipal(interop, laptop)
	requireSessionRejected(t, err, "the token of the OTHER device has to drop as well")
}

// TestLogoutDoesNotChangeThePassword proves that the logout does not touch the
// credential.
//
// The logout and the password change advance the same anchor; the logout
// endpoint writing the password as a side effect of that would mean the user
// never being able to log in again.
func TestLogoutDoesNotChangeThePassword(t *testing.T) {
	svc, _, _, clock := setupSession(t)
	ctx := context.Background()

	clock.advance(time.Second)
	_, err := svc.Logout(ctx, sessionUserID, service.PrincipalKindUser)
	require.NoError(t, err)

	clock.advance(time.Second)
	_, _, err = svc.Login(ctx, sessionEmail, sessionPassword)
	require.NoError(t, err, "the logout must not invalidate the old password")
}

// TestAPIKeyCannotLogOut proves that a key is rejected at the logout endpoint.
//
// A key has no session: it arrives not with a token but with a permanent
// secret, and that secret would keep working after this call too. Returning
// success in silence would leave the caller with the illusion that the key had
// been closed — the real path is the POST /admin/v1/api-keys/{id}/revoke
// endpoint.
func TestAPIKeyCannotLogOut(t *testing.T) {
	svc, _, repo, _ := setupSession(t)
	ctx := context.Background()

	previousAnchor := repo.anchor(models.ProviderEmailPass)

	_, err := svc.Logout(ctx, "apk_01JABC", service.PrincipalKindAPIKey)

	require.Error(t, err, "an api key must not be able to log out")
	assert.True(t, errors.IsInvalid(err),
		"the expected kind is Invalid (422), got: %v", errors.KindOf(err))
	assert.Equal(t, service.CodeNoSession, errors.CodeOf(err),
		"the rejection has to be reported with a separate code; the client has to be able to tell it apart from a credentials error")
	assert.Equal(t, previousAnchor, repo.anchor(models.ProviderEmailPass),
		"a rejected logout must not advance the anchor of any identity")
}

// sessionSecondProvider is the second identity provider set up BY HAND in the
// test.
//
// The raw string is deliberate: the models package holds only the
// [models.ProviderEmailPass] constant, and adding a constant there for a
// provider that has not been implemented would make it look as though a login
// path the code does not support existed. The value itself does not enter any
// claim; its meaning is only "a row that is NOT emailpass".
const sessionSecondProvider = "google"

// addSecondProvider sets up the identity row of a second provider in the
// repository.
//
// The row is set up by hand because the service has no endpoint that OPENS a
// second provider: the only login path today is emailpass. The schema, however,
// accepts such a row EVEN TODAY — the uniqueness is on (user_id, provider),
// that is, it allows one row per provider. This is why the test sets up not an
// imaginary situation but one the schema already expresses.
func addSecondProvider(repo *sessionRepo, createdAt time.Time) {
	repo.identities = append(repo.identities, models.AuthIdentity{
		ID:               sessionIdentityID + "_second",
		UserID:           sessionUserID,
		Provider:         sessionSecondProvider,
		ProviderIdentity: "google-oauth-sub-123",
		// There IS NO password: an OAuth identity has no password and a login
		// with an empty hash is rejected anyway (see password.go, Login).
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	})
}

// TestLogoutAdvancesTheAnchorOfEveryProvider proves that the logout advances
// not A SINGLE provider but all of the user's identities.
//
// # What the test proves and what it does not
//
// Because there is only one live provider today the observable behavior HAS NOT
// CHANGED; two things are proved:
//
//   - the anchor of the emailpass identity advances as before (the existing
//     behavior is preserved),
//   - the anchor of a second provider row set up BY HAND advances as well.
//
// The second one is not a user story but a lock placed for the future: the day
// OAuth is added, had the logout picked a single provider, the tokens obtained
// from that provider would not drop and this would stay SILENT — the endpoint
// would still return 204 and the user saying "I logged out" would still be
// logged in.
func TestLogoutAdvancesTheAnchorOfEveryProvider(t *testing.T) {
	svc, interop, repo, clock := setupSession(t)
	ctx := context.Background()
	addSecondProvider(repo, clock.now())

	token := obtainSessionToken(t, svc, sessionPassword)
	_, err := resolveSessionPrincipal(interop, token)
	require.NoError(t, err, "the token has to be accepted before the logout")

	clock.advance(2 * time.Second)
	revokedAt, err := svc.Logout(ctx, sessionUserID, service.PrincipalKindUser)
	require.NoError(t, err, "the user has to be able to log out")

	assert.Equal(t, clock.now(), repo.anchor(models.ProviderEmailPass),
		"the anchor of the emailpass identity has to advance; today's behavior has to be preserved")
	assert.Equal(t, clock.now(), repo.anchor(sessionSecondProvider),
		"the anchor of the second provider has to advance too — had it not, the tokens "+
			"obtained from that provider would still be accepted after the logout")
	assert.Equal(t, clock.now(), revokedAt,
		"the returned moment has to be the anchor written to the identities")

	_, err = resolveSessionPrincipal(interop, token)
	requireSessionRejected(t, err, "the token must not be accepted after the logout")
}

// TestAnchorOnTheSecondProviderDropsTheToken proves that the verification side
// DOES NOT LOOK at a single provider either.
//
// Without this end of the chain the change on the logout side would be good for
// nothing: even if the logout advanced all the rows, if the verification only
// looks at the emailpass row, the anchor written to the other row would never
// be read.
//
// The test tries this by setting up an asymmetry the logout could not produce:
// the anchor of the second provider is advanced and the emailpass row is LEFT
// IN PLACE. A verification looking at a fixed provider would keep accepting
// this token.
//
// The accepted price is plain and deliberate: a revocation on one provider
// drops the tokens of the other as well (the reasoning is in interop.go,
// principalFromToken).
func TestAnchorOnTheSecondProviderDropsTheToken(t *testing.T) {
	svc, interop, repo, clock := setupSession(t)
	addSecondProvider(repo, clock.now())

	token := obtainSessionToken(t, svc, sessionPassword)
	_, err := resolveSessionPrincipal(interop, token)
	require.NoError(t, err, "the token has to be accepted while no anchor has advanced")

	clock.advance(2 * time.Second)
	second := repo.identity(sessionSecondProvider)
	require.NotNil(t, second, "test ground: the second provider row has to have been set up")
	second.UpdatedAt = clock.now()

	_, err = resolveSessionPrincipal(interop, token)
	requireSessionRejected(t, err,
		"an anchor advancing on the second provider has to drop the token as well")
}
