package identitypasskey_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/descope/virtualwebauthn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identitypasskey "github.com/bdrtr/gobit/contrib/identity-passkey"
	identitysession "github.com/bdrtr/gobit/contrib/identity-session"
	"github.com/bdrtr/gobit/core/openapi"
)

// The rules of the signature counter (ADR 0382), driven through the sign-in
// endpoint with a software authenticator that signs whatever count it is given,
// as a copied private key does.

const signInFinish = "/store/v1/auth/passkey/sign-in/finish"

// assertion begins a sign-in and signs it with key at count, from auth; it
// answers the finish's body and the ceremony cookie, so a test can send them
// twice.
func (h *harness) assertion(
	t *testing.T, auth virtualwebauthn.Authenticator, key virtualwebauthn.Credential, count uint32,
) (string, *http.Cookie) {
	t.Helper()

	begun := h.post(t, "/store/v1/auth/passkey/sign-in/begin", "", nil)
	require.Equal(t, http.StatusOK, begun.Code, begun.Body.String())
	options, err := virtualwebauthn.ParseAssertionOptions(begun.Body.String())
	require.NoError(t, err)
	key.Counter = count

	return virtualwebauthn.CreateAssertionResponse(h.rp, auth, key, *options), ceremonyCookieOf(t, begun)
}

// signIn finishes one sign-in with key at count, from auth.
func (h *harness) signIn(
	t *testing.T, auth virtualwebauthn.Authenticator, key virtualwebauthn.Credential, count uint32,
) *httptest.ResponseRecorder {
	t.Helper()

	body, ceremony := h.assertion(t, auth, key, count)

	return h.post(t, signInFinish, body, ceremony)
}

// registeredKey registers a fresh key, counting from zero, on the test
// customer's account from auth.
func (h *harness) registeredKey(t *testing.T, auth virtualwebauthn.Authenticator) virtualwebauthn.Credential {
	t.Helper()

	key := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)
	registered := h.registerFrom(t, auth, h.signedInAs(t, testCustomer), key)
	require.Equal(t, http.StatusNoContent, registered.Code, registered.Body.String())

	return key
}

// storedCount is the count the store holds for key.
func (h *harness) storedCount(t *testing.T, key virtualwebauthn.Credential) uint32 {
	t.Helper()

	_, credential, err := h.store.ByCredentialID(t.Context(), key.ID)
	require.NoError(t, err)

	return credential.Authenticator.SignCount
}

// issuedNoSession asserts a response set no session cookie with a value.
func issuedNoSession(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	cookie := cookieNamed(rec, identitysession.DefaultCookieName)
	assert.True(t, cookie == nil || cookie.Value == "",
		"a refused sign-in issues no session: %v", cookie)
}

// settableClock is a store clock a test moves.
type settableClock struct{ at time.Time }

func (c *settableClock) now() time.Time { return c.at }

// syncable is an authenticator whose keys are backup-eligible, reporting the
// given backup state.
func syncable(backedUp bool) virtualwebauthn.Authenticator {
	return virtualwebauthn.NewAuthenticatorWithOptions(virtualwebauthn.AuthenticatorOptions{
		UserHandle: []byte(testCustomer), BackupEligible: true, BackupState: backedUp,
	})
}

// TestASignInRecordsWhatTheKeyReported: every sign-in writes the count its key
// presented, so the next one is compared with it.
func TestASignInRecordsWhatTheKeyReported(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	key := h.registeredKey(t, h.auth)

	for _, count := range []uint32{5, 6} {
		in := h.signIn(t, h.auth, key, count)
		require.Equal(t, http.StatusNoContent, in.Code, in.Body.String())
		sessionCookieOf(t, in)
	}
	assert.Equal(t, uint32(6), h.storedCount(t, key))
	assert.Equal(t, key.ID, h.store.used)
}

// TestACountThatGoesBackSuspendsTheKey: a copy that signs behind the owner is
// refused and the key is suspended, so the owner's next sign-in is refused too,
// and the listing says so.
func TestACountThatGoesBackSuspendsTheKey(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	key := h.registeredKey(t, h.auth)
	require.Equal(t, http.StatusNoContent, h.signIn(t, h.auth, key, 10).Code)

	copied := h.signIn(t, h.auth, key, 4)
	require.Equal(t, http.StatusForbidden, copied.Code, copied.Body.String())
	assert.Contains(t, copied.Body.String(), identitypasskey.CodeKeySuspended)
	issuedNoSession(t, copied)
	assert.Contains(t, h.logLine(t),
		`level=WARN msg="identity-passkey: a key's signature counter did not advance, so the key is suspended"`+
			" customer_id="+testCustomer+" credential_id="+keyIDOf(key)+" presented=4 stored_when_read=10")

	owners := h.signIn(t, h.auth, key, 11)
	assert.Equal(t, http.StatusForbidden, owners.Code, owners.Body.String())
	assert.Contains(t, owners.Body.String(), identitypasskey.CodeKeySuspended)
	issuedNoSession(t, owners)
	assert.Contains(t, h.logLine(t), `msg="identity-passkey: a suspended key tried to sign in"`)

	var body listed
	listing := h.get(t, "/store/v1/auth/passkey/keys", h.signedInAs(t, testCustomer))
	require.NoError(t, json.Unmarshal(listing.Body.Bytes(), &body))
	require.Len(t, body.Data, 1)
	assert.NotNil(t, body.Data[0].SuspendedAt, "the listing names the suspension")

	doc := openapi.New("identity-passkey", "v1")
	h.module.Describe(doc)
	built, err := doc.Build(h.router)
	require.NoError(t, err)
	assert.Equal(t, "403",
		documentedStatusForCode(t, built, signInFinish, http.MethodPost, identitypasskey.CodeKeySuspended))
}

// TestAFinishSentTwiceIsRefusedOnce: the same body and cookie again carry the
// count the first finish recorded, which is a lost answer and not a copy.
func TestAFinishSentTwiceIsRefusedOnce(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	key := h.registeredKey(t, h.auth)
	body, ceremony := h.assertion(t, h.auth, key, 3)

	require.Equal(t, http.StatusNoContent, h.post(t, signInFinish, body, ceremony).Code)
	again := h.post(t, signInFinish, body, ceremony)
	assert.Equal(t, http.StatusUnauthorized, again.Code, again.Body.String())
	assert.Contains(t, again.Body.String(), identitypasskey.CodeCeremonyRefused)
	issuedNoSession(t, again)

	assert.Equal(t, http.StatusNoContent, h.signIn(t, h.auth, key, 4).Code,
		"the repeat suspended nothing")
}

// TestAnEqualCountLaterIsACopy: the same count is a repeat only while the
// record it repeats is fresh.
func TestAnEqualCountLaterIsACopy(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	clock := &settableClock{at: time.Now()}
	h.store.now = clock.now
	key := h.registeredKey(t, h.auth)
	require.Equal(t, http.StatusNoContent, h.signIn(t, h.auth, key, 5).Code)

	clock.at = clock.at.Add(3 * time.Minute)
	assert.Equal(t, http.StatusUnauthorized, h.signIn(t, h.auth, key, 5).Code,
		"inside twice the ceremony's lifetime it is a repeat")

	clock.at = clock.at.Add(2 * time.Minute)
	later := h.signIn(t, h.auth, key, 5)
	assert.Equal(t, http.StatusForbidden, later.Code, later.Body.String())
}

// TestAKeyThatDoesNotCountSignsInEveryTime: a key that always reports zero is
// not compared.
func TestAKeyThatDoesNotCountSignsInEveryTime(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	key := h.registeredKey(t, h.auth)
	for range 3 {
		in := h.signIn(t, h.auth, key, 0)
		assert.Equal(t, http.StatusNoContent, in.Code, in.Body.String())
	}
}

// TestAKeyThatStopsCountingIsSuspended: zero after a count is a count that went
// back, not a key that does not count.
func TestAKeyThatStopsCountingIsSuspended(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	clock := &settableClock{at: time.Now()}
	h.store.now = clock.now
	key := h.registeredKey(t, h.auth)
	require.Equal(t, http.StatusNoContent, h.signIn(t, h.auth, key, 5).Code)

	clock.at = clock.at.Add(time.Hour)
	assert.Equal(t, http.StatusForbidden, h.signIn(t, h.auth, key, 0).Code)
}

// TestASyncableKeyIsNeverRefusedOverItsCount: a backup-eligible key may live on
// several devices, so its count is not compared, only kept at its highest, and
// its backup state is the one it last reported.
func TestASyncableKeyIsNeverRefusedOverItsCount(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	key := h.registeredKey(t, syncable(false))

	require.Equal(t, http.StatusNoContent, h.signIn(t, syncable(false), key, 10).Code)
	behind := h.signIn(t, syncable(true), key, 4)
	assert.Equal(t, http.StatusNoContent, behind.Code, behind.Body.String())

	_, stored, err := h.store.ByCredentialID(t.Context(), key.ID)
	require.NoError(t, err)
	assert.Equal(t, uint32(10), stored.Authenticator.SignCount)
	assert.True(t, stored.Flags.BackupState)
}

// TestASignInRecordsTheFlagsThisAssertionCarried: the user verification and the
// backup state the store records are the ones this sign-in's authenticator data
// reported, not a constant and not the registration's.
func TestASignInRecordsTheFlagsThisAssertionCarried(t *testing.T) {
	t.Parallel()

	stored := func(t *testing.T, h *harness, key virtualwebauthn.Credential) (verified, backedUp bool) {
		t.Helper()

		_, credential, err := h.store.ByCredentialID(t.Context(), key.ID)
		require.NoError(t, err)

		return credential.Flags.UserVerified, credential.Flags.BackupState
	}

	t.Run("user verification", func(t *testing.T) {
		t.Parallel()

		unverified := virtualwebauthn.NewAuthenticatorWithOptions(virtualwebauthn.AuthenticatorOptions{
			UserHandle: []byte(testCustomer), UserNotVerified: true,
		})
		h := newHarness(t)
		key := h.registeredKey(t, unverified)

		require.Equal(t, http.StatusNoContent, h.signIn(t, unverified, key, 1).Code)
		verified, _ := stored(t, h, key)
		assert.False(t, verified, "a sign-in that verified nobody records no verification")

		require.Equal(t, http.StatusNoContent, h.signIn(t, h.auth, key, 2).Code)
		verified, _ = stored(t, h, key)
		assert.True(t, verified, "a sign-in that verified the person records it")
	})

	t.Run("backup state", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		key := h.registeredKey(t, syncable(true))

		require.Equal(t, http.StatusNoContent, h.signIn(t, syncable(false), key, 1).Code)
		_, backedUp := stored(t, h, key)
		assert.False(t, backedUp, "a key that reported itself not synced is recorded so")
	})
}

// believer is a store whose SignedIn writes whatever count it is given and
// answers nil: the store an installation binds without reading the contract.
type believer struct{ *memoryCredentials }

func (b believer) SignedIn(_ context.Context, a identitypasskey.Assertion) error {
	if credential := b.held(a.CredentialID); credential != nil {
		credential.Authenticator.SignCount = a.SignCount
	}

	return nil
}

// TestTheHandlerDoesNotBelieveAStoreThatRecordsAnything: a count the library
// saw not advance is refused whatever the store answered, except for a key
// that may live on several devices.
func TestTheHandlerDoesNotBelieveAStoreThatRecordsAnything(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		auth virtualwebauthn.Authenticator
		want int
	}{
		"device-bound": {virtualwebauthn.NewAuthenticatorWithOptions(virtualwebauthn.AuthenticatorOptions{
			UserHandle: []byte(testCustomer),
		}), http.StatusUnauthorized},
		"syncable": {syncable(false), http.StatusNoContent},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := &memoryCredentials{}
			h := newHarnessWithStore(t, alwaysAnotherWayIn{}, believer{store})
			h.store = store
			key := h.registeredKey(t, tc.auth)

			require.Equal(t, http.StatusNoContent, h.signIn(t, tc.auth, key, 5).Code)
			behind := h.signIn(t, tc.auth, key, 3)
			assert.Equal(t, tc.want, behind.Code, behind.Body.String())
			if tc.want != http.StatusNoContent {
				issuedNoSession(t, behind)
				assert.Contains(t, h.logLine(t), `level=ERROR msg="identity-passkey: the store recorded a count`+
					` the library saw not advance; the sign-in was refused"`)
			}
		})
	}
}

// TestASignInThatCannotBeRecordedIsRefused: a count that could not be checked
// was not checked, so the sign-in fails and issues nothing.
func TestASignInThatCannotBeRecordedIsRefused(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	key := h.registeredKey(t, h.auth)
	h.store.signedInErr = errors.New("the database is down")

	failed := h.signIn(t, h.auth, key, 1)
	assert.Equal(t, http.StatusInternalServerError, failed.Code, failed.Body.String())
	assert.Contains(t, failed.Body.String(), identitypasskey.CodeUnavailable)
	issuedNoSession(t, failed)
	assert.Contains(t, h.logLine(t), `msg="identity-passkey: the sign-in could not be recorded"`)

	doc := openapi.New("identity-passkey", "v1")
	h.module.Describe(doc)
	built, err := doc.Build(h.router)
	require.NoError(t, err)
	assert.Equal(t, "500",
		documentedStatusForCode(t, built, signInFinish, http.MethodPost, identitypasskey.CodeUnavailable))
}

// TestAKeyRemovedDuringTheCeremonySignsNobodyIn: the store no longer holding
// the key, or holding another under its id, is a refused ceremony.
func TestAKeyRemovedDuringTheCeremonySignsNobodyIn(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	key := h.registeredKey(t, h.auth)
	h.store.signedInErr = identitypasskey.ErrNoCredential

	refused := h.signIn(t, h.auth, key, 1)
	assert.Equal(t, http.StatusUnauthorized, refused.Code, refused.Body.String())
	assert.Contains(t, refused.Body.String(), identitypasskey.CodeCeremonyRefused)
	issuedNoSession(t, refused)
}

// TestARepeatOnlyTheStoreSawSignsNobodyIn: two finishes of one body that race
// both read the count before either records it, so the library sees the count
// advance and only the store, under its lock, sees the repeat.
func TestARepeatOnlyTheStoreSawSignsNobodyIn(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	key := h.registeredKey(t, h.auth)
	h.store.signedInErr = identitypasskey.ErrCountRepeated

	refused := h.signIn(t, h.auth, key, 1)
	assert.Equal(t, http.StatusUnauthorized, refused.Code, refused.Body.String())
	assert.Contains(t, refused.Body.String(), identitypasskey.CodeCeremonyRefused)
	issuedNoSession(t, refused)
}

// copiedBehindTheLibrary is a store that suspends whatever it is given, as a
// store does when another sign-in recorded a higher count after the library
// read the key.
type copiedBehindTheLibrary struct{ *memoryCredentials }

func (copiedBehindTheLibrary) SignedIn(context.Context, identitypasskey.Assertion) error {
	return identitypasskey.ErrKeyCopied
}

// TestASuspensionIsLoggedWithTheCountTheLibraryRead: the WARN line names the
// count the library compared with, which on an advance is not the count the
// credential it returned carries.
func TestASuspensionIsLoggedWithTheCountTheLibraryRead(t *testing.T) {
	t.Parallel()

	store := &memoryCredentials{}
	h := newHarnessWithStore(t, alwaysAnotherWayIn{}, copiedBehindTheLibrary{store})
	h.store = store
	key := h.registeredKey(t, h.auth)

	copied := h.signIn(t, h.auth, key, 7)
	assert.Equal(t, http.StatusForbidden, copied.Code, copied.Body.String())
	assert.Contains(t, h.logLine(t), " presented=7 stored_when_read=0")
}
