//go:build integration

package e2e

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authapi "github.com/bdrtr/gobit/internal/modules/auth/api"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
)

// This file sets up a single INVARIANT and audits it across the WHOLE admin
// surface:
//
//	An unauthorized identity can do no work on any /admin/v1 endpoint.
//
// # Why the routes are not written out one by one
//
// A hand-written endpoint list goes blind on the first endpoint someone
// forgets to add to the list — and the endpoint that gets forgotten is
// precisely the freshly written one. The test WALKS the router tree instead:
// every admin endpoint registered today falls under coverage automatically,
// and so does the one that will be added tomorrow.
//
// # Why 403 is expected
//
// The identity IS VALID (the user exists, its password is right, its token is
// signed); what is missing is authorization. Returning 401 would mean "tell me
// who you are" and the client would retry forever with the same token. The
// distinction is corehttp.RequireScope's contract.
//
// # An unauthorized user is not an accident, it is a CONTRACT
//
// The godoc of service.CreateUserInput.Scopes writes this: "an EMPTY but
// non-nil slice ... produces an unauthorized user — it can log in but cannot
// reach any protected endpoint." This test is the audit of that sentence.

// unauthorizedExemptPaths are the admin endpoints that DO NOT ASK for
// authorization.
//
// Both are deliberate, and the list staying this short is the test's real
// claim: the login endpoint is only about to establish the identity, while the
// identity endpoint reads back the established identity itself. An
// unauthorized caller not even being able to learn who it is would make
// debugging impossible without protecting anything.
var unauthorizedExemptPaths = map[string]struct{}{
	authapi.LoginPath:       {},
	"/admin/v1/auth/me":     {},
	"/admin/v1/auth/logout": {},
}

// pathParamRe captures the {param} and {param:regex} pieces of a chi route
// pattern.
var pathParamRe = regexp.MustCompile(`\{[^}]*\}`)

// adminRoute is one walked admin endpoint.
type adminRoute struct {
	method  string
	pattern string
	path    string
}

// adminRoutes returns every /admin/v1 endpoint in the router tree.
func adminRoutes(t *testing.T) []adminRoute {
	t.Helper()

	var routes []adminRoute

	err := chi.Walk(testRouter, func(
		method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler,
	) error {
		// chi appends "/" to the pattern in Mount-ed subtrees; that does not
		// happen for routes registered with a full path, but normalizing
		// prevents the test from silently trying the wrong path if a module
		// switches to Mount later on.
		pattern = strings.TrimSuffix(pattern, "/*")
		if pattern != "/" {
			pattern = strings.TrimSuffix(pattern, "/")
		}

		if !strings.HasPrefix(pattern, "/admin/v1") {
			return nil
		}
		if _, exempt := unauthorizedExemptPaths[pattern]; exempt {
			return nil
		}

		routes = append(routes, adminRoute{
			method:  method,
			pattern: pattern,
			// Path parameters are filled in with a fake value: because the
			// authorization check runs BEFORE the handler, the record need not
			// exist. Getting a 404 for an identity that does not exist does
			// not hide the failure the test wants to catch (2xx instead of
			// 403); since 403 is expected, a 404 counts as a failure too, and
			// that is correct — authorization must come BEFORE the existence
			// check.
			path: pathParamRe.ReplaceAllString(pattern, "authz_test_fake_id"),
		})

		return nil
	})
	require.NoError(t, err, "could not walk the router tree")

	return routes
}

// TestUnauthorizedIdentityCanDoNoWorkOnAnyAdminEndpoint audits Phase 8's RBAC
// leg across ALL modules.
//
// If a module forgets to add authorization enforcement, this test lights up
// red on every one of that module's endpoints; there is no path along which
// forgetting stays silent.
func TestUnauthorizedIdentityCanDoNoWorkOnAnyAdminEndpoint(t *testing.T) {
	token := yetkisizYoneticiJetonu(t)
	routes := adminRoutes(t)

	// The lower bound guards against the test itself breaking: if the router
	// came back empty (e.g. if the walking logic broke), the test would stay
	// green without trying anything.
	require.Greater(t, len(routes), 50,
		"the admin surface looked smaller than expected; the walking logic may be broken")

	for _, route := range routes {
		t.Run(route.method+" "+route.pattern, func(t *testing.T) {
			req := httptest.NewRequest(route.method, route.path, http.NoBody)
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")

			rec := httptest.NewRecorder()
			testRouter.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusForbidden, rec.Code,
				"an unauthorized identity must get 403 on this endpoint; body: %s",
				rec.Body.String())
		})
	}
}

// TestUnauthorizedUserCanStillLogIn verifies that being unauthorized removes
// the authorization, NOT the identity.
//
// If the two were not separated, an unauthorized user could never log in and
// therefore could not even ask to be granted authorization.
func TestUnauthorizedUserCanStillLogIn(t *testing.T) {
	token := yetkisizYoneticiJetonu(t)

	identity := readIdentity(t, "Bearer "+token)
	assert.Equal(t, authsvc.PrincipalKindUser, identity.Kind)
	assert.Empty(t, identity.Scopes, "an unauthorized user's scope list must be empty")
}

// yetkisizYoneticiJetonu creates a user with an EMPTY scope list and returns
// its token.
//
// The user is created once and the tests share it; creating a new one in every
// test would repeat the bcrypt cost across hundreds of subtests.
func yetkisizYoneticiJetonu(t *testing.T) string {
	t.Helper()

	unauthorizedOnce.Do(func() {
		_, err := authSvc.CreateUser(t.Context(), authsvc.CreateUserInput{
			Email:     unauthorizedEmail,
			FirstName: "Unauthorized",
			LastName:  "User",
			// NOT nil, an empty slice: the service deliberately preserves the
			// difference between "I forgot the scope field" and "let it be
			// unauthorized".
			Scopes: []string{},
		}, unauthorizedPassword)
		unauthorizedSetupErr = err
	})
	require.NoError(t, unauthorizedSetupErr, "could not set up the unauthorized user")

	return jetonAl(t, unauthorizedEmail, unauthorizedPassword)
}

// The constants of the unauthorized fixture user and the one-time setup state.
var (
	// unauthorizedEmail is the email of the fixture user whose scope list is
	// empty.
	unauthorizedEmail = "unauthorized@gobit.test"
	// unauthorizedPassword is that user's password.
	unauthorizedPassword = "unauthorized-password-42"
	// unauthorizedOnce makes sure the user is created only once.
	unauthorizedOnce sync.Once
	// unauthorizedSetupErr carries the setup error to the tests.
	unauthorizedSetupErr error
)
