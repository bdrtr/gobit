//go:build integration

package e2e

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// This file audits the whole authorization matrix rather than one row of it.
//
// # What was here before, and what was missing
//
// [TestUnauthorizedIdentityCanDoNoWorkOnAnyAdminEndpoint] walks every admin
// endpoint and asserts 403 for a valid identity with no scope. That row is the
// sharpest one and it was automated first, for the right reason.
//
// What it cannot say is anything about the OTHER credentials. A storefront key
// reaching the admin surface, an admin token reaching the storefront without
// the publishable key, an endpoint that answers an anonymous caller — each is a
// different failure and none of them was checked anywhere.
//
// # The population is walked and every route must be CLASSIFIED
//
// A route that falls into no surface fails the test by name. That is the
// property the matrix is for: an endpoint added tomorrow under a prefix nobody
// thought about does not slip past by being unrecognized — the test says so and
// names the pattern, which is the moment somebody decides what may reach it.

// openRoutes are the endpoints that ask for NO identity, each for a written
// reason.
//
// The list is short and staying short is the claim. An endpoint added here is an
// endpoint anybody on the network may call, so the entry is where that decision
// is recorded rather than a prefix rule that would admit the next one silently.
//
// # It is NOT [corehttp.GuardOptions.OpenPrefixes], and the two must not be
// reconciled
//
// That field names the prefixes the RATE LIMIT still covers although they carry
// no identity — it answers "does this pay a quota", and it holds the panel,
// which is guarded by the panel's own ring and is not open at all. This list
// answers "may an anonymous caller have this", and the two sets differ on
// purpose. A reader who made one equal the other would either put the panel
// behind nothing or take the quota off a file endpoint that does a database
// read per request.
//
// # What this matrix does NOT cover
//
// The panel tree. `/admin/ui` is not mounted in this harness, so its own ring —
// identity, origin and the session cookie ADR 0076 widened — is audited in
// internal/app against the real guard stack instead. A matrix that walked a
// router the panel was missing from and said nothing about it would be claiming
// coverage it does not have.
var openRoutes = map[string]string{
	"/health": "the liveness probe; a checker that had to authenticate could not " +
		"report that authentication is what is broken",
	"/ready": "the readiness probe, for the same reason",
	"/openapi.json": "the generated schema. It describes the surface and carries no " +
		"data from it, and a client cannot integrate against a document it must " +
		"already be integrated to read",
	"/files/{key}": "a stored file, fetched by an <img> tag in a storefront that " +
		"cannot send a header (ADR 0011's file decision). The key is the capability",
}

// matrixRoute is one walked endpoint with the surface it belongs to.
type matrixRoute struct {
	method  string
	pattern string
	path    string
	surface string
}

// The surfaces a route can belong to.
const (
	surfaceAdmin = "admin"
	surfaceStore = "store"
	// surfaceCustomer is a storefront route that names a PERSON, and the
	// publishable key is not enough to reach it.
	//
	// The split was found by this test rather than assumed: ten routes refused
	// a valid publishable key, and every one of them was an address book or a
	// b2b membership — a customer's own data, which ADR 0043 says gobit
	// requires an identity for and ADR 0008 says gobit does not issue. So the
	// storefront is two surfaces, and the difference between them is the whole
	// of what keeps one shopper out of another's addresses.
	surfaceCustomer = "customer"
	surfaceOpen     = "open"
)

// customerRoutes are the storefront endpoints that name a person.
//
// The list is written out rather than derived from a prefix, and the reason is
// the direction of the failure. A prefix rule would admit a NEW route under
// `/store/v1/customers/` silently — and the entry that matters is the one
// somebody adds without noticing that a publishable key, which is visible in
// every shopper's browser, would then reach it.
//
// The assertion for these is the opposite of the storefront's: the publishable
// key must be REFUSED. A regression here is one shopper reading another's
// address book with a credential printed in the page source.
var customerRoutes = map[string]struct{}{
	"/store/v1/customers/{id}":                                         {},
	"/store/v1/customers/{id}/addresses":                               {},
	"/store/v1/customers/{id}/addresses/{address_id}":                  {},
	"/store/v1/customers/{id}/addresses/{address_id}/default-billing":  {},
	"/store/v1/customers/{id}/addresses/{address_id}/default-shipping": {},
	"/store/v1/b2b/customers/{customer_id}/company":                    {},
	"/store/v1/b2b/customers/{customer_id}/employee":                   {},
}

// matrixRoutes walks the router and classifies every endpoint.
//
// An unclassified route is a FAILURE and not a skip. A test that skipped what it
// did not recognize would go quiet exactly when somebody mounted a new prefix,
// which is the moment it is most needed.
func matrixRoutes(t *testing.T) []matrixRoute {
	t.Helper()

	var routes []matrixRoute

	err := chi.Walk(testRouter, func(
		method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler,
	) error {
		pattern = strings.TrimSuffix(pattern, "/*")
		if pattern != "/" {
			pattern = strings.TrimSuffix(pattern, "/")
		}

		route := matrixRoute{
			method:  method,
			pattern: pattern,
			path:    pathParamRe.ReplaceAllString(pattern, "authz_matrix_fake_id"),
		}

		switch {
		case strings.HasPrefix(pattern, corehttp.DefaultAdminPrefix):
			route.surface = surfaceAdmin
		case strings.HasPrefix(pattern, "/store/v1"):
			route.surface = surfaceStore
			if _, named := customerRoutes[pattern]; named {
				route.surface = surfaceCustomer
			}
		default:
			if _, open := openRoutes[pattern]; !open {
				t.Errorf("%s %s belongs to no surface this matrix knows.\n"+
					"Every endpoint is reachable by somebody, and this test is where "+
					"who is decided. Put it under the admin or the store prefix, or "+
					"add it to openRoutes with the reason anybody on the network may "+
					"call it.", method, pattern)

				return nil
			}

			route.surface = surfaceOpen
		}

		routes = append(routes, route)

		return nil
	})
	require.NoError(t, err, "could not walk the router tree")

	return routes
}

// request sends one matrix request.
func request(t *testing.T, route matrixRoute, decorate func(*http.Request)) int {
	t.Helper()

	req := httptest.NewRequest(route.method, route.path, http.NoBody)
	req.Header.Set("Content-Type", "application/json")
	if decorate != nil {
		decorate(req)
	}

	rec := httptest.NewRecorder()
	testRouter.ServeHTTP(rec, req)

	return rec.Code
}

// TestTheAuthorizationMatrixHoldsForEveryEndpoint is the audit.
//
// Each row is one credential against every endpoint, and the assertion is about
// the credential being ACCEPTED AS IDENTITY rather than about what the handler
// then does. A 404 for a fake path parameter and a 400 for an empty body are
// both fine; what is not fine is 401 where the credential should have been
// enough, or anything but 401 where it should not.
func TestTheAuthorizationMatrixHoldsForEveryEndpoint(t *testing.T) {
	routes := matrixRoutes(t)

	// The declared lists hold PATTERNS and the walk returns (method, pattern)
	// pairs — seven customer patterns are ten routes. Counting the pairs
	// against a list of patterns would compare two different things, and the
	// number it produced would be wrong in a way that looked like a finding.
	var admin, store int

	customer, open := map[string]bool{}, map[string]bool{}

	for _, route := range routes {
		switch route.surface {
		case surfaceAdmin:
			admin++
		case surfaceStore:
			store++
		case surfaceCustomer:
			customer[route.pattern] = true
		case surfaceOpen:
			open[route.pattern] = true
		}
	}

	// Floors, for the reason every floor in this repository exists: a walk that
	// returned nothing would satisfy every assertion below by making none.
	require.Greater(t, admin, 50, "the admin surface came back too small; the walk is broken")
	require.Greater(t, store, 10, "the store surface came back too small; the walk is broken")
	require.Equal(t, len(customerRoutes), len(customer),
		"the customer-named storefront routes walked do not match the ones declared. A "+
			"route left the list by being renamed, or one was added to the list that no "+
			"longer exists — and the list is what says a publishable key must not reach "+
			"a person's own data")
	require.Equal(t, len(openRoutes), len(open),
		"the open routes walked do not match the ones declared; one was added or removed "+
			"without the decision being written down")

	for _, route := range routes {
		t.Run(route.surface+" "+route.method+" "+route.pattern, func(t *testing.T) {
			switch route.surface {
			case surfaceAdmin:
				assertAdminRow(t, route)
			case surfaceStore:
				assertStoreRow(t, route)
			case surfaceCustomer:
				assertCustomerRow(t, route)
			case surfaceOpen:
				assertOpenRow(t, route)
			}
		})
	}
}

// assertAdminRow: who may reach an admin endpoint.
func assertAdminRow(t *testing.T, route matrixRoute) {
	t.Helper()

	if _, exempt := unauthorizedExemptPaths[route.pattern]; exempt {
		// Signing in and reading back one's own identity do not ask for a
		// scope; the identity row below still applies to the second, and the
		// first is exempt from identity too. Both are argued where the list is.
		return
	}

	assert.Equal(t, http.StatusUnauthorized, request(t, route, nil),
		"an anonymous caller was not refused as UNKNOWN on an admin endpoint")

	assert.Equal(t, http.StatusUnauthorized, request(t, route, func(r *http.Request) {
		r.Header.Set(corehttp.PublishableKeyHeader, publishableKey)
	}), "a STOREFRONT key was accepted as identity on the admin surface. The publishable "+
		"key is not a secret — it is visible in every shopper's browser — so an admin "+
		"endpoint that took it would be open to the internet")

	// The scoped credential must NOT be refused, and the row is limited to
	// reads: a write would run the handler, and a matrix that mutated the
	// database would change what every test after it sees. What it can still
	// catch is the failure worth catching — an endpoint that refuses a
	// legitimate operator because its scope name is wrong.
	if route.method == http.MethodGet {
		code := request(t, route, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+secretKey)
		})

		assert.NotEqual(t, http.StatusUnauthorized, code,
			"a valid admin key was told to identify itself")
		assert.NotEqual(t, http.StatusForbidden, code,
			"a fully scoped admin key was refused; the endpoint's scope is one nothing "+
				"grants, so no operator can ever reach it")
	}
}

// assertStoreRow: who may reach a storefront endpoint.
func assertStoreRow(t *testing.T, route matrixRoute) {
	t.Helper()

	assert.Equal(t, http.StatusUnauthorized, request(t, route, nil),
		"an anonymous caller reached a storefront endpoint; the publishable key is what "+
			"binds a request to a sales channel, and without it the endpoint does not "+
			"know which shop it is answering for")

	assert.Equal(t, http.StatusUnauthorized, request(t, route, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+secretKey)
	}), "an ADMIN key opened a storefront endpoint without a publishable key. The two "+
		"credentials answer different questions — who are you, and which channel — and "+
		"one standing in for the other means a request served for no channel at all")

	// The storefront's own credential must be accepted as identity. It may
	// still be refused for the CHANNEL: a channel-scoped path carrying a fake
	// id answers 403 because the key does not hold that channel, and that is
	// the rule working rather than failing.
	assert.NotEqual(t, http.StatusUnauthorized, request(t, route, func(r *http.Request) {
		r.Header.Set(corehttp.PublishableKeyHeader, publishableKey)
	}), "a valid publishable key was refused as identity on the storefront")
}

// assertOpenRow: an endpoint that asks for nothing must ask for nothing.
func assertOpenRow(t *testing.T, route matrixRoute) {
	t.Helper()

	code := request(t, route, nil)

	assert.NotEqual(t, http.StatusUnauthorized, code,
		"%s asks for identity, and it is on the list of endpoints that must not: %s",
		route.pattern, openRoutes[route.pattern])
	assert.NotEqual(t, http.StatusForbidden, code,
		"%s refuses an anonymous caller, and it is on the list of endpoints that must "+
			"not: %s", route.pattern, openRoutes[route.pattern])
}

// assertCustomerRow: a storefront endpoint that names a PERSON.
//
// Every credential is refused. The publishable key is refused because it is not
// a secret — it is printed into the storefront page — so a route it opened
// would let any visitor read the addresses of any customer whose id they could
// guess. The admin key is refused because the storefront is not where an
// operator works, and the anonymous caller for both reasons at once.
//
// What DOES open these is a customer identity the embedding application binds
// (ADR 0008, ADR 0043), which gobit deliberately does not issue. So this row
// asserts the refusal, and the acceptance is asserted where an identity can be
// faked — the customer module's own tests.
//
// # Where the 401 comes from, checked rather than assumed
//
// Not from a middleware: these routes carry none beyond the storefront guard.
// Not from [corehttp.ProvenCustomer]'s unbound branch either — this harness DOES
// bind an identity, as an embedder would. It comes from that identity refusing
// a request that carries no customer credential, and ProvenCustomer returning
// its error unchanged.
//
// That was found by mutation rather than by reading. Opening the unbound branch
// changed nothing and the row stayed green, which said the assertion was resting
// on a mechanism other than the one this comment first named; ignoring the bound
// identity's error made all seven patterns fail. A test whose failure message
// names the wrong cause sends the next person to the wrong file.
func assertCustomerRow(t *testing.T, route matrixRoute) {
	t.Helper()

	for name, decorate := range map[string]func(*http.Request){
		"an anonymous caller": nil,
		"a publishable key": func(r *http.Request) {
			r.Header.Set(corehttp.PublishableKeyHeader, publishableKey)
		},
		"an admin key": func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+secretKey)
		},
	} {
		assert.Equal(t, http.StatusUnauthorized, request(t, route, decorate),
			"%s reached %s, which names a PERSON. The publishable key is visible in "+
				"every shopper's browser; an endpoint it opens is one where a visitor "+
				"reads somebody else's addresses by guessing an id", name, route.pattern)
	}
}
