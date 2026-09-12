package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// This file is the DEMAND: a password alone does not open an account that has
// proven a second factor (ADR 0147).
//
// # Why the fixture enrolls instead of writing a credential
//
// The stored secret is a ciphertext and the service holds the only key. A test
// that wrote a row by hand would have to seal the secret itself, which means
// reaching into the module to do what the module does — and the sealed value is
// exactly the thing a wrong implementation would get wrong. So the fixture goes
// through the endpoints a person goes through: enroll, confirm, then sign in.

// mfaLoginRepo is the session fixture with the MFA table added.
//
// The writes live here rather than beside the fixture because they exist for this
// claim: the login tests need an account that really holds a factor, and the
// session fixture is the only one that carries a password identity to sign in
// with.
type mfaLoginRepo struct {
	*sessionRepo
}

// PutMFACredential writes an enrollment nobody has proven yet, and refuses to
// touch a confirmed one — the real query's WHERE clause.
func (d *mfaLoginRepo) PutMFACredential(
	_ context.Context, userID string, sealed []byte,
) (models.MFACredential, error) {
	if d.mfa != nil && d.mfa.Confirmed() {
		return models.MFACredential{}, repository.ErrNoMFACredential
	}

	d.mfa = &models.MFACredential{UserID: userID, Secret: sealed}

	return *d.mfa, nil
}

// PutPendingMFASecret parks a secret beside a confirmed one.
func (d *mfaLoginRepo) PutPendingMFASecret(
	_ context.Context, userID string, sealed []byte,
) (models.MFACredential, error) {
	if d.mfa == nil || d.mfa.UserID != userID || !d.mfa.Confirmed() {
		return models.MFACredential{}, repository.ErrNoMFACredential
	}

	d.mfa.PendingSecret = sealed

	return *d.mfa, nil
}

// PromotePendingMFASecret moves the waiting secret across in one step.
func (d *mfaLoginRepo) PromotePendingMFASecret(
	_ context.Context, userID string,
) (models.MFACredential, error) {
	if d.mfa == nil || d.mfa.UserID != userID || !d.mfa.Waiting() {
		return models.MFACredential{}, repository.ErrNoMFACredential
	}

	stamped := time.Now().UTC()
	d.mfa.Secret = d.mfa.PendingSecret
	d.mfa.PendingSecret = nil
	d.mfa.ConfirmedAt = &stamped

	return *d.mfa, nil
}

// ConfirmMFACredential stamps an unconfirmed credential and nothing else.
func (d *mfaLoginRepo) ConfirmMFACredential(
	_ context.Context, userID string,
) (models.MFACredential, error) {
	if d.mfa == nil || d.mfa.UserID != userID || d.mfa.Confirmed() {
		return models.MFACredential{}, repository.ErrNoMFACredential
	}

	stamped := time.Now().UTC()
	d.mfa.ConfirmedAt = &stamped

	return *d.mfa, nil
}

// DeleteMFACredential drops the credential and says whether there was one.
func (d *mfaLoginRepo) DeleteMFACredential(_ context.Context, _ string) (bool, error) {
	had := d.mfa != nil
	d.mfa = nil

	return had, nil
}

// setupMFALogin builds a service whose single user can enroll, and returns the
// service, the repository and the fixed clock.
func setupMFALogin(t *testing.T) (*service.Service, *mfaLoginRepo, *sessionClock) {
	t.Helper()

	start := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	clock := &sessionClock{moment: start}

	hash, err := bcrypt.GenerateFromPassword([]byte(sessionPassword), bcrypt.MinCost)
	require.NoError(t, err, "the test password could not be hashed")

	repo := &mfaLoginRepo{sessionRepo: &sessionRepo{
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
	}}

	svc := service.New(repo, service.Options{
		Now:          clock.now,
		JWTSecret:    sessionSecret,
		MFASecretKey: testMFAKey,
		BcryptCost:   bcrypt.MinCost,
	})

	return svc, repo, clock
}

// enrolAndConfirm gives the fixture's user a proven second factor and returns
// the secret an authenticator would hold.
func enrolAndConfirm(t *testing.T, svc *service.Service, at time.Time) string {
	t.Helper()

	enrollment, err := svc.EnrolMFA(t.Context(), sessionUserID, "Acme")
	require.NoError(t, err, "the enrollment has to succeed")
	require.NoError(t, svc.ConfirmMFA(t.Context(), sessionUserID, codeFor(t, enrollment.Secret, at)),
		"the confirmation has to succeed")

	return enrollment.Secret
}

// TestAPasswordAloneDoesNotOpenAnAccountWithAFactor is the decision.
func TestAPasswordAloneDoesNotOpenAnAccountWithAFactor(t *testing.T) {
	t.Parallel()

	svc, _, clock := setupMFALogin(t)
	secret := enrolAndConfirm(t, svc, clock.moment)

	token, _, err := svc.Login(t.Context(), sessionEmail, sessionPassword, "")

	require.Error(t, err, "the password alone must not produce a token")
	assert.Empty(t, token)
	assert.Equal(t, service.CodeMFARequired, coreerrors.CodeOf(err),
		"the refusal has to say the factor is what is missing, or no client can ask for it")

	// And the same request WITH the code does open it: without this half the test
	// above would pass on an implementation that refused every login.
	withCode, _, err := svc.Login(t.Context(), sessionEmail, sessionPassword,
		codeFor(t, secret, clock.moment))
	require.NoError(t, err, "the code the authenticator shows has to open the account")
	assert.NotEmpty(t, withCode)
}

// TestAnAccountWithNoFactorIsUnchanged keeps the ordinary login ordinary.
//
// Every installation on earth is this case until somebody enrolls, so a demand
// that reached them would lock out every administrator at once.
func TestAnAccountWithNoFactorIsUnchanged(t *testing.T) {
	t.Parallel()

	svc, _, _ := setupMFALogin(t)

	token, _, err := svc.Login(t.Context(), sessionEmail, sessionPassword, "")

	require.NoError(t, err)
	assert.NotEmpty(t, token)
}

// TestAnEnrolmentNobodyProvedDemandsNothing is the half that protects the person
// whose scan failed.
//
// Between drawing a secret and the first correct code nobody has shown that the
// app holds it. Demanding it there would lock somebody out with a secret that is
// on no phone — and the way out would be a login they cannot make.
func TestAnEnrolmentNobodyProvedDemandsNothing(t *testing.T) {
	t.Parallel()

	svc, _, _ := setupMFALogin(t)
	_, err := svc.EnrolMFA(t.Context(), sessionUserID, "Acme")
	require.NoError(t, err)

	token, _, err := svc.Login(t.Context(), sessionEmail, sessionPassword, "")

	require.NoError(t, err, "an unconfirmed enrollment is not a factor yet")
	assert.NotEmpty(t, token)
}

// TestAWrongCodeCountsAsAnAttemptAndAnAbsentOneDoesNot is the bound on guessing.
//
// # Why the counter is the whole defense
//
// Six digits is a million possibilities and a TOTP step is thirty seconds wide,
// so somebody holding the password needs attempts, not time. The failed-attempt
// counter is the only thing in this module that bounds them — and it is cleared
// by a SUCCESSFUL login. Registering success before the code was checked would
// therefore reset the count on every guess and the bound would not exist.
//
// The absent code is the other half and it is not symmetric: it is the first step
// of an ordinary two-step sign-in, so counting it would spend an attempt every
// time anybody signs in and lock out a person who typed nothing wrong.
func TestAWrongCodeCountsAsAnAttemptAndAnAbsentOneDoesNot(t *testing.T) {
	t.Parallel()

	svc, repo, clock := setupMFALogin(t)
	enrolAndConfirm(t, svc, clock.moment)

	_, _, err := svc.Login(t.Context(), sessionEmail, sessionPassword, "")
	require.Error(t, err)
	assert.Zero(t, repo.identities[0].FailedAttempts,
		"a login that only lacks the code is not a failed attempt")

	_, _, err = svc.Login(t.Context(), sessionEmail, sessionPassword, "000000")
	require.Error(t, err)
	assert.Equal(t, service.CodeMFACodeWrong, coreerrors.CodeOf(err))
	assert.Equal(t, 1, repo.identities[0].FailedAttempts,
		"a wrong code is an attempt; it is the only bound on guessing six digits")
}

// TestTheCounterIsClearedOnlyWhenTheWholeLoginSucceeded is the ordering claim.
//
// A correct password followed by a wrong code must leave the count where the
// wrong code put it. If the success were registered when the password matched,
// every guess would reset the counter and the lockout would be unreachable.
func TestTheCounterIsClearedOnlyWhenTheWholeLoginSucceeded(t *testing.T) {
	t.Parallel()

	svc, repo, clock := setupMFALogin(t)
	secret := enrolAndConfirm(t, svc, clock.moment)

	for range 3 {
		_, _, err := svc.Login(t.Context(), sessionEmail, sessionPassword, "000000")
		require.Error(t, err)
	}
	require.Equal(t, 3, repo.identities[0].FailedAttempts,
		"three guesses have to leave three attempts, not one")

	_, _, err := svc.Login(t.Context(), sessionEmail, sessionPassword,
		codeFor(t, secret, clock.moment))
	require.NoError(t, err)
	assert.Zero(t, repo.identities[0].FailedAttempts,
		"a whole successful login clears the counter")
}

// TestAnInstallationThatLostItsKeyRefusesRatherThanLettingThePasswordThrough is
// the failure mode that has to fail CLOSED.
//
// The key is an environment variable, so it can go missing between one boot and
// the next. Serving the login without the factor would turn a missing variable
// into an account with no second factor — silently, for exactly the people who
// took the trouble to enroll.
func TestAnInstallationThatLostItsKeyRefusesRatherThanLettingThePasswordThrough(t *testing.T) {
	t.Parallel()

	svc, repo, clock := setupMFALogin(t)
	enrolAndConfirm(t, svc, clock.moment)

	keyless := service.New(repo, service.Options{
		Now:        clock.now,
		JWTSecret:  sessionSecret,
		BcryptCost: bcrypt.MinCost,
	})

	token, _, err := keyless.Login(t.Context(), sessionEmail, sessionPassword, "123456")

	require.Error(t, err, "a login must not succeed while the stored factor cannot be read")
	assert.Empty(t, token)
	assert.Equal(t, service.CodeMFAUnavailable, coreerrors.CodeOf(err))
}

// TestRemovingTheFactorOpensTheAccountToThePasswordAgain is the operator's reset,
// seen from the login.
//
// It is what `gobit mfa-reset` buys: the person whose phone is gone cannot sign
// in, and this is the only act in the system that gives them their account back.
func TestRemovingTheFactorOpensTheAccountToThePasswordAgain(t *testing.T) {
	t.Parallel()

	svc, _, clock := setupMFALogin(t)
	enrolAndConfirm(t, svc, clock.moment)

	_, _, err := svc.Login(t.Context(), sessionEmail, sessionPassword, "")
	require.Error(t, err, "the factor stands before the reset")

	removed, err := svc.RemoveMFA(t.Context(), sessionUserID)
	require.NoError(t, err)
	require.True(t, removed)

	token, _, err := svc.Login(t.Context(), sessionEmail, sessionPassword, "")
	require.NoError(t, err, "after the reset the password alone signs in again")
	assert.NotEmpty(t, token)
}

// TestTheOldPhoneSignsInUntilTheNewOneIsProven is the enrollment rule seen from
// the login, which is the only place it matters.
func TestTheOldPhoneSignsInUntilTheNewOneIsProven(t *testing.T) {
	t.Parallel()

	svc, _, clock := setupMFALogin(t)
	oldSecret := enrolAndConfirm(t, svc, clock.moment)

	replacement, err := svc.EnrolMFA(t.Context(), sessionUserID, "Acme")
	require.NoError(t, err)

	// The new app is scanned and then the person is interrupted. Until they prove
	// it, the account is neither unprotected nor locked: the old phone still works.
	_, _, err = svc.Login(t.Context(), sessionEmail, sessionPassword,
		codeFor(t, replacement.Secret, clock.moment))
	require.Error(t, err, "an unproven secret must not sign anybody in")

	token, _, err := svc.Login(t.Context(), sessionEmail, sessionPassword,
		codeFor(t, oldSecret, clock.moment))
	require.NoError(t, err, "the proven phone keeps working until the new one is confirmed")
	assert.NotEmpty(t, token)

	require.NoError(t, svc.ConfirmMFA(t.Context(), sessionUserID,
		codeFor(t, replacement.Secret, clock.moment)))

	// And now they have swapped: the new one signs in and the old one does not.
	_, _, err = svc.Login(t.Context(), sessionEmail, sessionPassword,
		codeFor(t, replacement.Secret, clock.moment))
	require.NoError(t, err)

	_, _, err = svc.Login(t.Context(), sessionEmail, sessionPassword,
		codeFor(t, oldSecret, clock.moment))
	require.Error(t, err, "the replaced secret must stop working the moment the new one is proven")
}
