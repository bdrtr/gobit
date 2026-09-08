package customer

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// This file pins the JOIN, and nothing else here does.
//
// ADR 0043's seam is a chain of three links and each one was already held on
// its own: core/http/identity_test.go proves that an implementation registers
// under corehttp.IdentityName and resolves as the interface, the tests beside
// this one prove that [identityBinding] resolves that name lazily and refuses
// when it is absent, and internal/modules/customer/api/identity_test.go proves
// that a handler holding an identity compares it against the path. What none of
// them touched is [Module.Register] handing the handler a REAL binding: with
// `api.New(m.svc, nil)` written there instead, every one of those tests stayed
// green — the wrapper over an empty container and a nil field answer the same
// 401, and no test anywhere both registered an identity in a container and
// drove a storefront route. A published contract whose wiring nothing pins is a
// contract that can be unplugged silently.
//
// So the tests below build the module the way the composition root does —
// Register, then Routes — and drive the router that comes out. Both of them go
// red under that exact mutation: the first because an identity that was never
// asked cannot refuse a stranger, the second because the refusal changes the
// party it blames.

// strangerID is a customer identifier a caller may name in the path without
// being able to prove it. It is not a secret: it travels in every order
// response.
const strangerID = "cust_SOMEBODY_ELSE"

// recordingIdentity is an embedder's implementation reduced to its answer, plus
// a count of how often it was ASKED.
//
// The count is the half that matters here. A status can be produced by a chain
// that skipped the identity entirely; "it was asked exactly once" cannot.
type recordingIdentity struct {
	id    string
	calls int
}

var _ corehttp.Identity = (*recordingIdentity)(nil)

func (i *recordingIdentity) CustomerID(*http.Request) (string, error) {
	i.calls++

	return i.id, nil
}

// wiredRouter builds the module through its own [Module.Register] and returns
// the router its storefront requests really travel through.
//
// # Why the pool is a zero value rather than a live one
//
// Register needs a *db.Pool to build the repository, and nothing in this file
// reaches the database: every request either stops at the identity gate or is
// answered by the repository's own "not configured" refusal. Standing a
// container up in front of an assertion about WIRING would buy nothing and cost
// Docker. The served path over a real database is proved next door, in
// customer_integration_test.go.
func wiredRouter(t *testing.T, c *container.Container) chi.Router {
	t.Helper()

	require.NoError(t, c.Provide(dbServiceName, &db.Pool{}))

	m := New(slog.New(slog.DiscardHandler))
	require.NoError(t, m.Register(context.Background(), c),
		"the module could not be registered, so nothing below is about the wiring")

	r := chi.NewRouter()
	m.Routes(r)

	return r
}

// drive runs one request against the wired router.
func drive(t *testing.T, r chi.Router, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(
		context.Background(), method, path, http.NoBody))

	return rec
}

// refusalCode reads the machine-readable code out of an error response. It is
// the part a client branches on, and the part that says WHICH refusal happened.
func refusalCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body),
		"the error body could not be read: %s", rec.Body.String())

	return body.Error.Code
}

// TestRegisterHandsTheStorefrontTheIdentityTheEmbedderProvided is the pin.
//
// An embedder Provides an implementation under corehttp.IdentityName from its
// own module, and the property ADR 0043 exists for is that the storefront then
// asks it. Nothing but a test that starts at [Module.Register] and ends at a
// registered route can say so: the handler, the wrapper and the container name
// are each correct in isolation while the line that joins them is a nil.
func TestRegisterHandsTheStorefrontTheIdentityTheEmbedderProvided(t *testing.T) {
	t.Parallel()

	identity := &recordingIdentity{id: provenID}
	c := container.New(nil)
	require.NoError(t, c.Provide(corehttp.IdentityName, identity))

	rec := drive(t, wiredRouter(t, c), http.MethodGet, "/store/v1/customers/"+strangerID)

	require.Equal(t, http.StatusForbidden, rec.Code,
		"the module registered an identity proving %q and then answered %d for %q.\n"+
			"That is the mutation ADR 0043's seam has no other guard against: the "+
			"embedder's implementation is in the container and the handler never sees "+
			"it.\nbody: %s", provenID, rec.Code, strangerID, rec.Body.String())
	assert.Equal(t, corehttp.CodeIdentityMismatch, refusalCode(t, rec),
		"the refusal has to blame the PROOF. A 401 naming the missing binding would "+
			"mean the wiring dropped an identity that was registered")
	assert.Equal(t, 1, identity.calls,
		"the embedder's implementation was asked %d times. A status can be produced "+
			"by a chain that never reached it; this count cannot", identity.calls)
}

// TestEveryStorefrontRouteNamingACustomerRefusesWhenTheContainerHoldsNothing is
// the closed-by-default row, held on the object PRODUCTION builds.
//
// Its counterpart in internal/modules/customer/api walks the same routes with a
// literal nil, which exercises the handler's own guard; production never passes
// nil, so the refusal it really makes comes from [identityBinding] finding
// nothing registered. That branch was proved once, at wrapper level, and never
// across the routes it closes. Here it is the wired module over an empty
// container — an installation that read no ADR and bound nothing — and every
// registered route naming a customer has to refuse.
func TestEveryStorefrontRouteNamingACustomerRefusesWhenTheContainerHoldsNothing(t *testing.T) {
	t.Parallel()

	r := wiredRouter(t, container.New(nil))

	for _, route := range storefrontRoutesNamingACustomer(t, r) {
		t.Run(route.method+" "+route.pattern, func(t *testing.T) {
			rec := drive(t, r, route.method, route.path)

			require.Equal(t, http.StatusUnauthorized, rec.Code,
				"%s %s answered %d in an installation that bound no identity.\n"+
					"ADR 0043: with nothing bound these routes are CLOSED, not open — a "+
					"route that answers hands a person's name, e-mail and street address "+
					"to anybody holding an identifier.\nbody: %s",
				route.method, route.pattern, rec.Code, rec.Body.String())
			assert.Equal(t, corehttp.CodeIdentityNotBound, refusalCode(t, rec),
				"the refusal has to NAME the missing binding; a bare 401 sends an "+
					"operator looking for a credential the client never had to send")
		})
	}
}

// customerRoute is one registered storefront route that names a customer.
type customerRoute struct {
	method string
	// pattern is the route as chi registered it, kept for the failure message:
	// the concrete path says which request was sent, the pattern says which
	// registration is at fault.
	pattern string
	path    string
}

// storefrontRoutesNamingACustomer walks the REAL route tree and returns every
// storefront route whose path carries a customer id.
//
// A list of eight paths would pass the day somebody adds a ninth, and pinning
// the COUNT would fail the day somebody adds a ninth that behaves perfectly.
// The property is "every one of them refuses", so the routes are read from the
// tree the module just built.
func storefrontRoutesNamingACustomer(t *testing.T, r chi.Router) []customerRoute {
	t.Helper()

	const prefix = "/store/v1/customers/{id}"

	var routes []customerRoute
	err := chi.Walk(r, func(method, route string, _ http.Handler,
		_ ...func(http.Handler) http.Handler,
	) error {
		if !strings.HasPrefix(route, prefix) {
			return nil
		}
		path := strings.NewReplacer(
			"{id}", strangerID,
			"{address_id}", "addr_1",
		).Replace(strings.TrimSuffix(route, "/"))
		routes = append(routes, customerRoute{method: method, pattern: route, path: path})

		return nil
	})
	require.NoError(t, err, "the route tree could not be walked")
	require.NotEmpty(t, routes,
		"no storefront route naming a customer was found, which cannot be true while "+
			"the address book alone registers six. The walk has gone BLIND and every "+
			"assertion made from it is being made about nothing")

	return routes
}
