package service_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// testMFAKey is the encryption key these tests give the installation, and
// testUser the administrator enrolling.
const (
	testMFAKey = "an-mfa-key-that-is-long-enough-for-a-test"
	testUser   = "usr_01MFAENROLLINGPERSON"
)

// codeFor is the code an authenticator holding the secret would show.
func codeFor(t *testing.T, secret string, at time.Time) string {
	t.Helper()

	code, err := service.TOTPCodeAt(secret, at)
	require.NoError(t, err)

	return code
}

// newMFAService wires a service that CAN hold a secret, with a fixed clock.
//
// The clock is fixed because TOTP is a function of time: a test that could not
// say what "now" is could only compare the implementation with itself.
func newMFAService(t *testing.T, now time.Time) (*service.Service, *fakeRepo) {
	t.Helper()

	repo := &fakeRepo{userEmail: "ada@example.com"}
	svc := service.New(repo, service.Options{
		JWTSecret:    "a-signing-secret-that-is-long-enough",
		MFASecretKey: testMFAKey,
		Now:          func() time.Time { return now },
	})

	return svc, repo
}

// TestAnEnrollmentHandsBackASecretThatIsNotStoredInTheClear is the shape of the
// whole feature.
func TestAnEnrollmentHandsBackASecretThatIsNotStoredInTheClear(t *testing.T) {
	t.Parallel()

	svc, repo := newMFAService(t, time.Unix(1111111111, 0).UTC())
	user := testUser

	enrollment, err := svc.EnrolMFA(t.Context(), user, "Acme Shop")
	require.NoError(t, err)

	require.NotEmpty(t, enrollment.Secret)
	assert.Contains(t, enrollment.URI, "otpauth://totp/")
	assert.Contains(t, enrollment.URI, "secret="+enrollment.Secret,
		"the URI carries the same secret the person may type by hand")

	stored := repo.mfa[user]
	require.NotEmpty(t, stored.Secret)
	assert.NotContains(t, string(stored.Secret), enrollment.Secret,
		"the stored bytes must not contain the secret; a database read would hand over "+
			"the second factor")
	assert.Nil(t, stored.ConfirmedAt,
		"nobody has shown yet that the app holds it")
}

// TestAnInstallationWithNoKeyREFUSESRatherThanStoringPlaintext is the fail-closed
// half.
func TestAnInstallationWithNoKeyREFUSESRatherThanStoringPlaintext(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{userEmail: "ada@example.com"}
	svc := service.New(repo, service.Options{JWTSecret: "a-signing-secret-that-is-long"})
	user := testUser

	_, err := svc.EnrolMFA(t.Context(), user, "Acme")

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInternal, coreerrors.KindOf(err))
	assert.Empty(t, repo.mfa, "nothing may be written when there is nowhere safe to put it")
	assert.Contains(t, err.Error(), "MFA_SECRET_KEY",
		"the message has to NAME the variable. The cipher refuses on its own, so the "+
			"guard above it changes no outcome — what it changes is what an operator "+
			"reads, and \"nothing can be sealed\" tells them nothing they can act on. "+
			"A mutation that removed the guard survived until this line existed.")
}

// TestACredentialCountsOnlyAfterTheFirstCorrectCode is the confirmation rule.
func TestACredentialCountsOnlyAfterTheFirstCorrectCode(t *testing.T) {
	t.Parallel()

	now := time.Unix(1111111111, 0).UTC()
	svc, _ := newMFAService(t, now)
	user := testUser

	enrollment, err := svc.EnrolMFA(t.Context(), user, "Acme")
	require.NoError(t, err)

	confirmed, err := svc.HasConfirmedMFA(t.Context(), user)
	require.NoError(t, err)
	assert.False(t, confirmed,
		"an enrollment nobody proved would lock out everybody whose scan failed halfway")

	require.NoError(t, svc.ConfirmMFA(t.Context(), user, codeFor(t, enrollment.Secret, now)))

	confirmed, err = svc.HasConfirmedMFA(t.Context(), user)
	require.NoError(t, err)
	assert.True(t, confirmed)
}

// TestAWrongCodeConfirmsNothing pins the refusal and its kind.
func TestAWrongCodeConfirmsNothing(t *testing.T) {
	t.Parallel()

	now := time.Unix(1111111111, 0).UTC()
	svc, _ := newMFAService(t, now)
	user := testUser

	_, err := svc.EnrolMFA(t.Context(), user, "Acme")
	require.NoError(t, err)

	err = svc.ConfirmMFA(t.Context(), user, "000000")

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err))

	confirmed, err := svc.HasConfirmedMFA(t.Context(), user)
	require.NoError(t, err)
	assert.False(t, confirmed)
}

// TestReEnrollingKeepsTheProvenFactorUntilTheNewOneIsScanned is somebody moving
// to a new phone while still holding the old one.
//
// # The assertion that matters is the one in the middle
//
// A second enrollment used to clear the confirmation, and that was harmless while
// nothing demanded the factor. Once a login does (ADR 0147), it becomes a way out
// of the second factor that needs no secret at all: start an enrollment, walk away,
// and the next password alone is enough. So the account stays CONFIRMED across the
// enrollment, and what changes is only which secret the confirm endpoint compares
// against.
//
// A person who has LOST the phone cannot come through here any more — they cannot
// sign in to ask. That case is `gobit mfa-reset`, at the machine.
func TestReEnrollingKeepsTheProvenFactorUntilTheNewOneIsScanned(t *testing.T) {
	t.Parallel()

	now := time.Unix(1111111111, 0).UTC()
	svc, _ := newMFAService(t, now)
	user := testUser

	first, err := svc.EnrolMFA(t.Context(), user, "Acme")
	require.NoError(t, err)
	require.NoError(t, svc.ConfirmMFA(t.Context(), user, codeFor(t, first.Secret, now)))

	second, err := svc.EnrolMFA(t.Context(), user, "Acme")
	require.NoError(t, err)
	require.NotEqual(t, first.Secret, second.Secret, "a second enrollment draws a NEW secret")

	confirmed, err := svc.HasConfirmedMFA(t.Context(), user)
	require.NoError(t, err)
	assert.True(t, confirmed,
		"the proven factor stands until the replacement is scanned; an abandoned "+
			"enrollment must not be a way out of it")

	err = svc.ConfirmMFA(t.Context(), user, codeFor(t, first.Secret, now))
	require.Error(t, err,
		"the OLD phone's code must not confirm the new enrollment; it would leave "+
			"somebody believing the app they just scanned works")

	require.NoError(t, svc.ConfirmMFA(t.Context(), user, codeFor(t, second.Secret, now)))

	// And now the old one is the one that stopped working: the promotion moved the
	// secret, so what the first phone shows confirms nothing and signs nobody in.
	stillConfirmed, err := svc.HasConfirmedMFA(t.Context(), user)
	require.NoError(t, err)
	assert.True(t, stillConfirmed)
}

// TestRemovingTheFactorLeavesTheAccountWithNone is the operator's reset and the
// owner's switch-off, which are the same write.
//
// The second call is the one worth having: an account that has no factor is the
// state the caller asked for, so removing nothing is not an error — it is the
// answer "there is none", which is what an operator running the reset by hand
// needs to be able to tell apart from "done".
func TestRemovingTheFactorLeavesTheAccountWithNone(t *testing.T) {
	t.Parallel()

	now := time.Unix(1111111111, 0).UTC()
	svc, _ := newMFAService(t, now)
	user := testUser

	enrollment, err := svc.EnrolMFA(t.Context(), user, "Acme")
	require.NoError(t, err)
	require.NoError(t, svc.ConfirmMFA(t.Context(), user, codeFor(t, enrollment.Secret, now)))

	removed, err := svc.RemoveMFA(t.Context(), user)
	require.NoError(t, err)
	assert.True(t, removed, "there was one to remove")

	confirmed, err := svc.HasConfirmedMFA(t.Context(), user)
	require.NoError(t, err)
	assert.False(t, confirmed, "the account holds no factor after the reset")

	again, err := svc.RemoveMFA(t.Context(), user)
	require.NoError(t, err)
	assert.False(t, again, "removing nothing is not a failure, it is an answer")
}

// TestConfirmingTwiceIsRefusedRatherThanSilent keeps the first moment.
func TestConfirmingTwiceIsRefusedRatherThanSilent(t *testing.T) {
	t.Parallel()

	now := time.Unix(1111111111, 0).UTC()
	svc, _ := newMFAService(t, now)
	user := testUser

	enrollment, err := svc.EnrolMFA(t.Context(), user, "Acme")
	require.NoError(t, err)
	code := codeFor(t, enrollment.Secret, now)
	require.NoError(t, svc.ConfirmMFA(t.Context(), user, code))

	err = svc.ConfirmMFA(t.Context(), user, code)

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err),
		"there is nothing left to confirm, and saying so is how somebody learns to "+
			"enroll again instead of retrying")
}

// TestConfirmingWithoutEnrollingIsAConflict covers the person who never started.
func TestConfirmingWithoutEnrollingIsAConflict(t *testing.T) {
	t.Parallel()

	svc, _ := newMFAService(t, time.Unix(1111111111, 0).UTC())
	user := testUser

	err := svc.ConfirmMFA(t.Context(), user, "123456")

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))
}

// TestAStoredSecretSealedWithAnotherKeyIsAnERROR is the rotated-key case.
//
// Reading it as "no second factor" would turn a changed key into an account that
// silently lost its protection, which is the quietest possible way to remove one.
func TestAStoredSecretSealedWithAnotherKeyIsAnERROR(t *testing.T) {
	t.Parallel()

	now := time.Unix(1111111111, 0).UTC()
	svc, repo := newMFAService(t, now)
	user := testUser

	enrollment, err := svc.EnrolMFA(t.Context(), user, "Acme")
	require.NoError(t, err)

	// The same repository, a service whose key is a different one.
	rotated := service.New(repo, service.Options{
		JWTSecret:    "a-signing-secret-that-is-long-enough",
		MFASecretKey: testMFAKey + "-rotated",
		Now:          func() time.Time { return now },
	})

	err = rotated.ConfirmMFA(t.Context(), user, codeFor(t, enrollment.Secret, now))

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInternal, coreerrors.KindOf(err),
		"a key that no longer opens the secret is a fault of the installation")
}

// TestHasConfirmedMFAIsFalseForSomebodyWhoNeverEnrolled is the ordinary answer.
func TestHasConfirmedMFAIsFalseForSomebodyWhoNeverEnrolled(t *testing.T) {
	t.Parallel()

	svc, _ := newMFAService(t, time.Unix(1111111111, 0).UTC())
	user := testUser

	confirmed, err := svc.HasConfirmedMFA(t.Context(), user)

	require.NoError(t, err, "not having enrolled is not a fault")
	assert.False(t, confirmed)
}
