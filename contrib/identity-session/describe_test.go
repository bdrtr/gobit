package identitysession_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identitysession "github.com/bdrtr/gobit/contrib/identity-session"
	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/openapi"
)

// TestEveryRouteIsDescribedAndEveryDescriptionMatchesARoute is the loop the core
// already knows how to close, run here so it closes.
//
// The two directions fail differently and both are silent without this. A route
// nobody described is an endpoint an integrator can call and cannot find; a
// description matching no route is a promise of an endpoint that does not exist.
// The core reports both, and nothing asks it to unless somebody does.
func TestEveryRouteIsDescribedAndEveryDescriptionMatchesARoute(t *testing.T) {
	t.Parallel()

	m := identitysession.New(identitysession.Options{
		Secret:      []byte("a describing secret of at least thirty-two"),
		Credentials: nowhere{},
	})
	require.NoError(t, m.Register(t.Context(), emptyContainer(t)))

	r := chi.NewRouter()
	m.Routes(r)

	doc := openapi.New("identity-session", "v1")
	m.Describe(doc)
	_, err := doc.Build(r)
	require.NoError(t, err)

	assert.Empty(t, doc.UnmatchedDescriptions(),
		"a description matching no route promises an endpoint that does not exist")
	assert.Empty(t, doc.UndescribedRoutes(),
		"a route nobody described is an endpoint an integrator can call and cannot find")
}

// TestTheDescriptionsAreOnTheRealPaths guards the one thing the loop above
// cannot: a path spelled right in BOTH places and wrong in both.
func TestTheDescriptionsAreOnTheRealPaths(t *testing.T) {
	t.Parallel()

	m := identitysession.New(identitysession.Options{
		Secret:      []byte("a describing secret of at least thirty-two"),
		Credentials: nowhere{},
	})
	require.NoError(t, m.Register(t.Context(), emptyContainer(t)))

	r := chi.NewRouter()
	m.Routes(r)

	for _, want := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/store/v1/auth/sign-in"},
		{http.MethodPost, "/store/v1/auth/sign-out"},
		{http.MethodPut, "/admin/v1/customer-credentials"},
	} {
		rctx := chi.NewRouteContext()
		assert.True(t, r.Match(rctx, want.method, want.path),
			"%s %s is not bound; the description and the route agreeing on a path "+
				"NOBODY serves is what the loop test cannot see", want.method, want.path)
	}
}

// nowhere is a credential store nothing in this file reaches.
//
// The module needs one to register and these tests never sign anybody in, so the
// alternative would be a Postgres container for a document test.
type nowhere struct{}

// Credential refuses everything, the way an unknown address is refused.
func (nowhere) Credential(context.Context, string) (customerID, passwordHash string, err error) {
	return "", "", identitysession.ErrPasswordMismatch
}

// Put writes nothing.
func (nowhere) Put(context.Context, string, string, string) error { return nil }

// emptyContainer is a container with nothing in it.
//
// Register resolves the pool only when Options.Credentials is nil, which is why
// an empty one is enough here — and that is worth a test of its own rather than
// a comment, so see TestAStoreGivenInOptionsNeedsNoPool below.
func emptyContainer(t *testing.T) *container.Container {
	t.Helper()

	return container.New(nil)
}

// TestAStoreGivenInOptionsNeedsNoPool is the property the two tests above lean
// on, asserted instead of assumed.
//
// An installation keeping its passwords in a directory it already runs binds
// that store and should not have to hand this module a database as well.
func TestAStoreGivenInOptionsNeedsNoPool(t *testing.T) {
	t.Parallel()

	err := identitysession.New(identitysession.Options{
		Secret:      []byte("a secret of at least thirty-two bytes!!!"),
		Credentials: nowhere{},
	}).Register(t.Context(), container.New(nil))

	assert.NoError(t, err,
		"a store given in Options must not send Register looking for core.db")
}
