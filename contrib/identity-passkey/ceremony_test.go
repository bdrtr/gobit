package identitypasskey_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/descope/virtualwebauthn"
	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identitypasskey "github.com/bdrtr/gobit/contrib/identity-passkey"
	identitysession "github.com/bdrtr/gobit/contrib/identity-session"
	"github.com/bdrtr/gobit/core/container"
)

// The tests in this file PERFORM the ceremonies.
//
// A passkey module whose ceremonies are never actually run is a wiring test
// wearing a security feature's name: the interesting part is a signature over a
// challenge, and nothing about the handlers proves the library was given the
// right challenge, the right origin or the right user handle.
//
// virtualwebauthn is a software authenticator that signs the way a real one
// does. It costs one module in the graph of this module's TESTS — measured, and
// it shares every other dependency with go-webauthn itself.

const (
	testRPID     = "example.test"
	testOrigin   = "https://example.test"
	testCustomer = "cust_06G8PASSKEYCUSTOMER00000"
	testSecret   = "a passkey test signing secret of thirty-two"
)

// harness is a registered module and everything a ceremony needs.
type harness struct {
	module   *identitypasskey.Module
	router   chi.Router
	sessions *identitysession.Sessions
	store    *memoryCredentials
	rp       virtualwebauthn.RelyingParty
	auth     virtualwebauthn.Authenticator
	// logs is everything the module wrote while serving.
	//
	// It is a test SUBJECT rather than noise, because the framework masks the
	// message of every KindInternal error on the wire — deliberately, so a DSN or
	// a query cannot leak. Two unrelated faults therefore answer one identical
	// body, and the log line is the only place an operator learns which of them
	// happened.
	logs *lockedBuffer
}

// lockedBuffer is a [bytes.Buffer] that survives being written from two
// goroutines.
//
// slog holds no lock for its writer, and the concurrency tests in this package
// serve requests in parallel against one harness.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends under the lock.
func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

// logLine is what the module logged, required to be non-empty.
//
// A test that asserts a sentence is ABSENT from the log would pass against a log
// that was never written at all, so the presence of SOMETHING is checked here
// once rather than remembered at each call.
func (h *harness) logLine(t *testing.T) string {
	t.Helper()

	written := h.logs.String()
	require.NotEmpty(t, written, "the module logged nothing, so no assertion about its log means anything")

	return written
}

// String reads under the lock.
func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// newHarness builds a module signed into a real session verifier.
//
// # Why it does not bind a test identity
//
// The session module fills corehttp.IdentityName itself, and the passkey module
// resolves whatever is in that slot. Binding a second one is refused by the
// container, which is the container saying what the composition already says:
// there is ONE answer to "who is this request", and in this arrangement it is
// the session cookie.
//
// So a test that needs a signed-in caller signs one in — [harness.signedInAs] —
// and the proof travels the way a browser's would.
func newHarness(t *testing.T) *harness {
	t.Helper()

	return newHarnessWith(t, alwaysAnotherWayIn{})
}

// newHarnessWith is [newHarness] with the installation's answer about other ways
// in spelled out.
//
// The ceremonies do not consult it, so they take the ordinary answer; the tests
// that are ABOUT the answer name it.
func newHarnessWith(t *testing.T, other identitypasskey.OtherSignIn) *harness {
	t.Helper()

	store := &memoryCredentials{}
	h := newHarnessWithStore(t, other, store)
	h.store = store

	return h
}

// newHarnessWithStore is [newHarnessWith] with the bound store spelled out.
//
// The store is a constructor argument rather than something set afterwards,
// because that is how the module takes it: [identitypasskey.Module.Register]
// reads Options once and a store swapped in later would be a shape no
// installation can produce.
//
// The returned harness's store field is left nil unless the caller is using the
// in-memory one — a test that binds something else has no business reaching into
// a map it does not own.
func newHarnessWithStore(
	t *testing.T, other identitypasskey.OtherSignIn, credentials identitypasskey.Credentials,
) *harness {
	t.Helper()

	session := identitysession.New(identitysession.Options{
		Secret:      []byte(testSecret),
		Insecure:    true,
		Credentials: noCredentials{},
	})
	c := container.New(nil)
	require.NoError(t, session.Register(t.Context(), c))

	logs := &lockedBuffer{}
	m := identitypasskey.New(identitypasskey.Options{
		Logger:      slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Session:     session,
		RPID:        testRPID,
		RPOrigins:   []string{testOrigin},
		DisplayName: "Example Shop",
		Credentials: credentials,
		OtherSignIn: other,
	})
	require.NoError(t, m.Register(t.Context(), c))

	r := chi.NewRouter()
	m.Routes(r)

	return &harness{
		module:   m,
		router:   r,
		sessions: session.Sessions(),
		logs:     logs,
		rp: virtualwebauthn.RelyingParty{
			ID: testRPID, Name: "Example Shop", Origin: testOrigin,
		},
		// The authenticator carries the USER HANDLE, which is what makes it a
		// discoverable (resident) credential rather than one the server has to
		// name. Without it the library refuses the assertion with "Client-side
		// Discoverable Assertion was attempted with a blank User Handle" — which
		// is the library refusing exactly the sign-in this module offers, and is
		// how this test found that the handle has to be set on the DEVICE and
		// not only in the options.
		auth: virtualwebauthn.NewAuthenticatorWithOptions(virtualwebauthn.AuthenticatorOptions{
			UserHandle: []byte(testCustomer),
		}),
	}
}

// TestRegisteringAndSigningInWithAPasskey walks both ceremonies for real.
//
// It is the one test here that would notice a wrong relying party id, a wrong
// origin, a challenge taken from the wrong place or a user handle that does not
// resolve — every part the handlers cannot check about themselves.
func TestRegisteringAndSigningInWithAPasskey(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	signedIn := h.signedInAs(t, testCustomer)

	// --- registration ---
	begun := h.post(t, "/store/v1/auth/passkey/register/begin", "", signedIn)
	require.Equal(t, http.StatusOK, begun.Code, "body: %s", begun.Body.String())
	ceremony := ceremonyCookieOf(t, begun)

	options, err := virtualwebauthn.ParseAttestationOptions(begun.Body.String())
	require.NoError(t, err, "the begin answer has to be what a browser can use")
	assert.Equal(t, testRPID, options.RelyingPartyID)
	assert.Equal(t, testCustomer, options.UserID,
		"the user handle IS the customer id, which is what makes a discoverable "+
			"sign-in resolve without a second table")

	credential := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)
	attestation := virtualwebauthn.CreateAttestationResponse(h.rp, h.auth, credential, *options)

	finished := h.post(t, "/store/v1/auth/passkey/register/finish", attestation, signedIn, ceremony)
	require.Equal(t, http.StatusNoContent, finished.Code, "body: %s", finished.Body.String())
	require.Len(t, h.store.byCustomer[testCustomer], 1, "the key is stored against the caller")

	h.auth.AddCredential(credential)

	// --- sign-in, as a caller proving NOBODY ---
	anonymous := newHarness(t)
	anonymous.store.byCustomer = h.store.byCustomer

	signingIn := anonymous.post(t, "/store/v1/auth/passkey/sign-in/begin", "", nil)
	require.Equal(t, http.StatusOK, signingIn.Code, "body: %s", signingIn.Body.String())
	signInCeremony := ceremonyCookieOf(t, signingIn)

	assertionOptions, err := virtualwebauthn.ParseAssertionOptions(signingIn.Body.String())
	require.NoError(t, err)
	assert.Empty(t, assertionOptions.AllowCredentials,
		"a DISCOVERABLE sign-in names nobody; a list here would say which keys exist")

	assertion := virtualwebauthn.CreateAssertionResponse(
		anonymous.rp, h.auth, credential, *assertionOptions)

	in := anonymous.post(t, "/store/v1/auth/passkey/sign-in/finish", assertion, signInCeremony)
	require.Equal(t, http.StatusNoContent, in.Code, "body: %s", in.Body.String())

	// The cookie it set is the session module's, and it proves the customer.
	proved := sessionCookieOf(t, in)
	request := httptest.NewRequestWithContext(t.Context(),
		http.MethodGet, "/store/v1/customers/"+testCustomer, http.NoBody)
	request.Header.Set("Cookie", proved.Name+"="+proved.Value)

	id, err := anonymous.sessions.CustomerID(request)
	require.NoError(t, err)
	assert.Equal(t, testCustomer, id,
		"a passkey sign-in issues the SAME session a password does")
	assert.Equal(t, credential.ID, anonymous.store.used,
		"the key's use is stamped, and it is the key that actually signed")
}

// TestABegunCeremonyCannotBeReplayed is why the finish CLEARS the cookie.
//
// A challenge is single-use and the library cannot notice a second use: the
// signature over that challenge is genuinely valid. What stops it is that the
// ceremony is gone.
func TestABegunCeremonyCannotBeReplayed(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	signedIn := h.signedInAs(t, testCustomer)

	begun := h.post(t, "/store/v1/auth/passkey/register/begin", "", signedIn)
	require.Equal(t, http.StatusOK, begun.Code)
	ceremony := ceremonyCookieOf(t, begun)

	options, err := virtualwebauthn.ParseAttestationOptions(begun.Body.String())
	require.NoError(t, err)
	credential := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)
	attestation := virtualwebauthn.CreateAttestationResponse(h.rp, h.auth, credential, *options)

	first := h.post(t, "/store/v1/auth/passkey/register/finish", attestation, signedIn, ceremony)
	require.Equal(t, http.StatusNoContent, first.Code)
	assert.Empty(t, cookieNamed(first, "gobit_passkey_ceremony").Value,
		"the finish clears the ceremony")

	// The SAME body and the SAME cookie, sent again.
	second := h.post(t, "/store/v1/auth/passkey/register/finish", attestation, signedIn, ceremony)
	assert.Equal(t, http.StatusNoContent, second.Code,
		"re-registering the same key is idempotent by the store's conflict target")
	assert.Len(t, h.store.byCustomer[testCustomer], 1,
		"and it is still ONE key, not two rows signing the same challenge")
}

// TestACeremonyBegunByAnotherAccountIsRefused closes the window between the two
// calls.
//
// Without it a person could begin as themselves, sign in as somebody else, and
// finish — writing their own key onto the second account, which is a takeover
// with every signature valid.
func TestACeremonyBegunByAnotherAccountIsRefused(t *testing.T) {
	t.Parallel()

	first := newHarness(t)
	firstSession := first.signedInAs(t, testCustomer)
	begun := first.post(t, "/store/v1/auth/passkey/register/begin", "", firstSession)
	require.Equal(t, http.StatusOK, begun.Code)
	ceremony := ceremonyCookieOf(t, begun)

	options, err := virtualwebauthn.ParseAttestationOptions(begun.Body.String())
	require.NoError(t, err)
	credential := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)
	attestation := virtualwebauthn.CreateAttestationResponse(first.rp, first.auth, credential, *options)

	// The same ceremony, finished by somebody else.
	second := newHarness(t)
	secondSession := second.signedInAs(t, "cust_SOMEBODY_ELSE")
	refused := second.post(t, "/store/v1/auth/passkey/register/finish", attestation, secondSession, ceremony)

	assert.Equal(t, http.StatusForbidden, refused.Code, "body: %s", refused.Body.String())
	assert.Empty(t, second.store.byCustomer, "nothing is written")
}

// TestRegisteringNeedsAnAccount refuses the shape that would be a takeover.
func TestRegisteringNeedsAnAccount(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	// No session cookie: a caller this installation cannot identify.
	refused := h.post(t, "/store/v1/auth/passkey/register/begin", "")

	assert.Equal(t, http.StatusUnauthorized, refused.Code, "body: %s", refused.Body.String())
	assert.Contains(t, refused.Body.String(), identitypasskey.CodeNotSignedIn)
}

// TestFinishingWithoutACeremonyIsRefused covers the client that drops cookies.
func TestFinishingWithoutACeremonyIsRefused(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	signedIn := h.signedInAs(t, testCustomer)

	for _, path := range []string{
		"/store/v1/auth/passkey/register/finish",
		"/store/v1/auth/passkey/sign-in/finish",
	} {
		t.Run(path, func(t *testing.T) {
			refused := h.post(t, path, `{}`, signedIn)

			assert.Equal(t, http.StatusUnprocessableEntity, refused.Code, "body: %s", refused.Body.String())
			assert.Contains(t, refused.Body.String(), identitypasskey.CodeCeremonyMissing)
		})
	}
}

// TestAnEditedCeremonyCookieIsRefused is the seal doing its work on this path.
func TestAnEditedCeremonyCookieIsRefused(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	signedIn := h.signedInAs(t, testCustomer)
	begun := h.post(t, "/store/v1/auth/passkey/register/begin", "", signedIn)
	require.Equal(t, http.StatusOK, begun.Code)

	edited := ceremonyCookieOf(t, begun)
	edited.Value = strings.TrimSuffix(edited.Value, "a") + "b"

	refused := h.post(t, "/store/v1/auth/passkey/register/finish", `{}`, signedIn, edited)

	assert.Equal(t, http.StatusUnprocessableEntity, refused.Code, "body: %s", refused.Body.String())
	assert.Contains(t, refused.Body.String(), identitypasskey.CodeCeremonyMissing)
}

// signedInAs issues a real session cookie for a customer.
//
// It goes through the session module rather than faking a header, because that
// is the only proof this arrangement accepts and a test using anything else
// would be testing an arrangement nobody runs.
func (h *harness) signedInAs(t *testing.T, customerID string) *http.Cookie {
	t.Helper()

	rec := httptest.NewRecorder()
	h.sessions.Issue(rec, customerID)

	return sessionCookieOf(t, rec)
}

// post sends a POST carrying whichever cookies the test hands it.
func (h *harness) post(
	t *testing.T, path, body string, cookies ...*http.Cookie,
) *httptest.ResponseRecorder {
	t.Helper()

	return h.do(t, http.MethodPost, path, body, cookies...)
}

// do sends one request.
func (h *harness) do(
	t *testing.T, method, path, body string, cookies ...*http.Cookie,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	var sent []string
	for _, cookie := range cookies {
		if cookie != nil {
			sent = append(sent, cookie.Name+"="+cookie.Value)
		}
	}
	if len(sent) > 0 {
		req.Header.Set("Cookie", strings.Join(sent, "; "))
	}

	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)

	return rec
}

// get sends a GET carrying whichever cookies the test hands it.
func (h *harness) get(
	t *testing.T, path string, cookies ...*http.Cookie,
) *httptest.ResponseRecorder {
	t.Helper()

	return h.do(t, http.MethodGet, path, "", cookies...)
}

// remove sends the DELETE for one credential id.
func (h *harness) remove(
	t *testing.T, credentialID string, cookies ...*http.Cookie,
) *httptest.ResponseRecorder {
	t.Helper()

	return h.do(t, http.MethodDelete,
		"/store/v1/auth/passkey/keys/"+url.PathEscape(credentialID), "", cookies...)
}

// ceremonyCookieOf reads the ceremony cookie a begin call set.
func ceremonyCookieOf(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()

	cookie := cookieNamed(rec, "gobit_passkey_ceremony")
	require.NotNil(t, cookie, "a begin call has to set the ceremony cookie")
	require.NotEmpty(t, cookie.Value)

	return cookie
}

// sessionCookieOf reads the session cookie a sign-in set.
func sessionCookieOf(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()

	cookie := cookieNamed(rec, identitysession.DefaultCookieName)
	require.NotNil(t, cookie, "a passkey sign-in has to set the session cookie")

	return cookie
}

// cookieNamed finds a cookie in a response.
func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}

	return nil
}

// noCredentials is a password store nothing here reaches.
type noCredentials struct{}

// Credential refuses everything.
func (noCredentials) Credential(context.Context, string) (customerID, hash string, err error) {
	return "", "", identitysession.ErrPasswordMismatch
}

// Put writes nothing.
func (noCredentials) Put(context.Context, string, string, string) error { return nil }

// memoryCredentials is a passkey store in a map.
type memoryCredentials struct {
	byCustomer map[string][]webauthn.Credential
	used       []byte
	// listErr and removeErr are the store FAILING, which is a different answer
	// from the rule refusing and the handler has to tell them apart.
	listErr   error
	removeErr error
	// phantomKeys makes ListForCustomer report MORE rows than Remove holds.
	//
	// It is the concurrency window written as a fixture: the handler counts
	// without a lock, another request removes a row, and the locked count is then
	// lower than the number the decision was made on. A store that cannot lie in
	// that direction cannot exercise the branch that survives it.
	phantomKeys int
}

// ForCustomer answers what the customer registered.
func (s *memoryCredentials) ForCustomer(
	_ context.Context, customerID string,
) ([]webauthn.Credential, error) {
	return s.byCustomer[customerID], nil
}

// ByCredentialID finds a credential by its id.
func (s *memoryCredentials) ByCredentialID(
	_ context.Context, credentialID []byte,
) (string, webauthn.Credential, error) {
	for customerID, credentials := range s.byCustomer {
		for i := range credentials {
			if bytes.Equal(credentials[i].ID, credentialID) {
				return customerID, credentials[i], nil
			}
		}
	}

	return "", webauthn.Credential{}, identitypasskey.ErrNoCredential
}

// Put stores a credential, replacing one with the same id.
func (s *memoryCredentials) Put(
	_ context.Context, customerID string, credential webauthn.Credential,
) error {
	if s.byCustomer == nil {
		s.byCustomer = map[string][]webauthn.Credential{}
	}
	existing := s.byCustomer[customerID]
	for i := range existing {
		if bytes.Equal(existing[i].ID, credential.ID) {
			existing[i] = credential

			return nil
		}
	}
	s.byCustomer[customerID] = append(s.byCustomer[customerID], credential)

	return nil
}

// Used records the stamp.
func (s *memoryCredentials) Used(_ context.Context, credentialID []byte) error {
	s.used = credentialID

	return nil
}

// ListForCustomer answers the narrow rows for a customer.
func (s *memoryCredentials) ListForCustomer(
	_ context.Context, customerID string,
) ([]identitypasskey.Key, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}

	credentials := s.byCustomer[customerID]
	out := make([]identitypasskey.Key, 0, len(credentials)+s.phantomKeys)
	for i := range credentials {
		out = append(out, identitypasskey.Key{
			ID:         base64.RawURLEncoding.EncodeToString(credentials[i].ID),
			CreatedAt:  time.Unix(int64(1_700_000_000+i), 0).UTC(),
			Transports: []string{"internal"},
		})
	}
	for i := range s.phantomKeys {
		out = append(out, identitypasskey.Key{
			ID:        base64.RawURLEncoding.EncodeToString([]byte("gone" + string(rune('a'+i)))),
			CreatedAt: time.Unix(int64(1_700_000_500+i), 0).UTC(),
		})
	}

	return out, nil
}

// Remove imitates the real store's OBSERVABLE answers.
//
// The three it has to get right are the three the handler branches on, and a
// fake that answered a plain error for any of them would make the handler's
// branching untestable: a credential that is not this customer's is
// [identitypasskey.ErrNoCredential] and not a refusal, the last row is
// [identitypasskey.ErrLastWayIn] and not a not-found, and the surviving count
// comes back either way. What it cannot imitate is the LOCK, which is why the
// race has an integration test of its own.
func (s *memoryCredentials) Remove(
	_ context.Context, customerID string, credentialID []byte, allowLast bool,
) (int, error) {
	if s.removeErr != nil {
		return 0, s.removeErr
	}

	credentials := s.byCustomer[customerID]
	held := len(credentials)
	at := -1
	for i := range credentials {
		if bytes.Equal(credentials[i].ID, credentialID) {
			at = i
		}
	}
	if at < 0 {
		return held, identitypasskey.ErrNoCredential
	}
	if held < 2 && !allowLast {
		return held, identitypasskey.ErrLastWayIn
	}

	s.byCustomer[customerID] = append(credentials[:at], credentials[at+1:]...)

	return held - 1, nil
}

// TestRegistrationAsksForADiscoverableCredential is gap D65, at the ceremony.
//
// The only sign-in this module offers is a discoverable one: the authenticator
// names the account and the server knows nothing until it answers. A
// registration that did not ask for a resident key accepted a credential the
// authenticator stores no user handle for, answered 204, and the person could
// never use it — the library refuses the assertion with "blank User Handle".
func TestRegistrationAsksForADiscoverableCredential(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	begun := h.post(t, "/store/v1/auth/passkey/register/begin", "", h.signedInAs(t, testCustomer))
	require.Equal(t, http.StatusOK, begun.Code, "body: %s", begun.Body.String())

	var options struct {
		PublicKey struct {
			AuthenticatorSelection struct {
				ResidentKey        string `json:"residentKey"`
				RequireResidentKey *bool  `json:"requireResidentKey"`
			} `json:"authenticatorSelection"`
		} `json:"publicKey"`
	}
	require.NoError(t, json.Unmarshal(begun.Body.Bytes(), &options),
		"the begin answer has to be the options object a browser is handed")

	assert.Equal(t, "required", options.PublicKey.AuthenticatorSelection.ResidentKey,
		"a discoverable sign-in needs a DISCOVERABLE credential, and asking for it is "+
			"the only moment the authenticator can be told")
	require.NotNil(t, options.PublicKey.AuthenticatorSelection.RequireResidentKey,
		"the older spelling has to travel too; a browser may read either")
	assert.True(t, *options.PublicKey.AuthenticatorSelection.RequireResidentKey)
}

// TestRegistrationExcludesTheKeysThePersonAlreadyHas keeps "add another passkey
// first" meaning another DEVICE.
//
// Without the exclusion list an authenticator that already holds a key for this
// site mints a SECOND credential rather than saying so, and every sentence in
// this module that offers "register another one" can be satisfied twice on one
// device — which is exactly the lockout those sentences exist to prevent.
func TestRegistrationExcludesTheKeysThePersonAlreadyHas(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	signedIn := h.signedInAs(t, testCustomer)

	first := h.post(t, "/store/v1/auth/passkey/register/begin", "", signedIn)
	require.Equal(t, http.StatusOK, first.Code)
	options, err := virtualwebauthn.ParseAttestationOptions(first.Body.String())
	require.NoError(t, err)
	assert.Empty(t, options.ExcludeCredentials,
		"a person with no keys excludes nothing")

	credential := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)
	attestation := virtualwebauthn.CreateAttestationResponse(h.rp, h.auth, credential, *options)
	stored := h.post(t, "/store/v1/auth/passkey/register/finish", attestation,
		signedIn, ceremonyCookieOf(t, first))
	require.Equal(t, http.StatusNoContent, stored.Code, "body: %s", stored.Body.String())

	second := h.post(t, "/store/v1/auth/passkey/register/begin", "", signedIn)
	require.Equal(t, http.StatusOK, second.Code)
	again, err := virtualwebauthn.ParseAttestationOptions(second.Body.String())
	require.NoError(t, err)

	require.Len(t, again.ExcludeCredentials, 1,
		"the key they already have has to be on the list the authenticator checks "+
			"itself against")
	assert.True(t, credential.IsExcludedForAttestation(*again),
		"and it has to be THAT key; an exclusion list of the wrong ids excludes nobody")
}
