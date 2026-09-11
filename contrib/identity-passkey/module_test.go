package identitypasskey_test

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identitypasskey "github.com/bdrtr/gobit/contrib/identity-passkey"
	identitysession "github.com/bdrtr/gobit/contrib/identity-session"
	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/openapi"
)

// registeredSession is a session module that has already registered.
func registeredSession(t *testing.T) (*identitysession.Module, *container.Container) {
	t.Helper()

	session := identitysession.New(identitysession.Options{
		Secret:      []byte(testSecret),
		Credentials: noCredentials{},
	})
	c := container.New(nil)
	require.NoError(t, session.Register(t.Context(), c))

	return session, c
}

// TestRegisterRefusesWhatWouldBeSilentlyWrong covers the three ways an
// installation can be misconfigured in a way nothing else would report.
//
// A missing relying party id is the one worth naming: it does not fail, it mints
// credentials the site that created them cannot use, and the person finds out
// the next time they try to sign in.
func TestRegisterRefusesWhatWouldBeSilentlyWrong(t *testing.T) {
	t.Parallel()

	session, c := registeredSession(t)

	for name, tc := range map[string]struct {
		options identitypasskey.Options
		says    string
	}{
		"no session module": {
			options: identitypasskey.Options{RPID: testRPID, RPOrigins: []string{testOrigin}},
			says:    "Options.Session",
		},
		"a session module that has not registered": {
			options: identitypasskey.Options{
				Session:   identitysession.New(identitysession.Options{Secret: []byte(testSecret)}),
				RPID:      testRPID,
				RPOrigins: []string{testOrigin},
			},
			says: "BEFORE this one",
		},
		"no relying party id": {
			options: identitypasskey.Options{Session: session, RPOrigins: []string{testOrigin}},
			says:    "RPID",
		},
		"no origins": {
			options: identitypasskey.Options{Session: session, RPID: testRPID},
			says:    "RPOrigins",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := identitypasskey.New(tc.options).Register(t.Context(), c)

			require.Error(t, err, "%s must stop the startup", name)
			assert.Contains(t, err.Error(), tc.says,
				"the failure has to name what an operator sets")
		})
	}
}

// TestRegisterNeedsAnIdentityInTheSlot is the dependency the module cannot fake.
//
// Registering a passkey adds it to an account, so this module has to know whose.
// An installation that bound no verifier at all cannot answer that, and saying
// so at startup beats a 401 on every registration.
func TestRegisterNeedsAnIdentityInTheSlot(t *testing.T) {
	t.Parallel()

	session := identitysession.New(identitysession.Options{
		Secret:      []byte(testSecret),
		Credentials: noCredentials{},
	})
	// A container the session module never filled: its Register is what provides
	// core.identity, so skipping it leaves the slot empty.
	empty := container.New(nil)
	require.NoError(t, session.Register(t.Context(), container.New(nil)))

	err := identitypasskey.New(identitypasskey.Options{
		Session:   session,
		RPID:      testRPID,
		RPOrigins: []string{testOrigin},
		// A store is given so the pool is not what is missing: this test is
		// about the IDENTITY slot, and a failure naming core.db would pass a
		// weaker assertion while proving nothing about it.
		Credentials: &memoryCredentials{},
	}).Register(t.Context(), empty)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "core.identity",
		"the failure has to name the empty slot")
}

// TestRoutesMountNothingWithoutRegister keeps a handler with no engine from
// answering.
func TestRoutesMountNothingWithoutRegister(t *testing.T) {
	t.Parallel()

	r := chi.NewRouter()
	identitypasskey.New(identitypasskey.Options{}).Routes(r)

	rctx := chi.NewRouteContext()
	assert.False(t, r.Match(rctx, http.MethodPost, "/store/v1/auth/passkey/sign-in/begin"),
		"an endpoint that exists and panics is worse than one that does not exist")
}

// TestEveryRouteIsDescribedAndEveryDescriptionMatchesARoute closes the loop the
// core knows how to close and reports only to whoever asks.
func TestEveryRouteIsDescribedAndEveryDescriptionMatchesARoute(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	doc := openapi.New("identity-passkey", "v1")
	h.module.Describe(doc)
	_, err := doc.Build(h.router)
	require.NoError(t, err)

	assert.Empty(t, doc.UnmatchedDescriptions(),
		"a description matching no route promises an endpoint that does not exist")
	assert.Empty(t, doc.UndescribedRoutes(),
		"a route nobody described is an endpoint an integrator can call and cannot find")
}
