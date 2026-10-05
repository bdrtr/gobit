package app

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/audit"
	"github.com/bdrtr/gobit/core/openapi"
)

// TestTheRootBoundRoutesAreDescribedAndTheDescriptionsMatch checks, without a
// database, that the root's binder and describer agree; the e2e ratchet sees
// the same routes on the opened installation (ADR 0398).
//
// # What the ratchet covers, and why this test stays
//
// TestEveryRealRouteIsDescribed in internal/e2e walks a running server's router
// and fails on any route with no body. internal/e2e walks the installation
// app.Open assembles, which carries the root-bound routes too (ADR 0398); this
// test stays because it checks the agreement between the binder and the
// describer without a database. Both halves are checked here:
//
//   - a described path that matches no route means the description never enters
//     the document, so the endpoint ships bodiless;
//   - a bound route with no description is the debt the ratchet exists for.
//
// # Why a bare router rather than the real one
//
// The real router needs a database, a container and every module. What is being
// checked is the agreement between two functions in this package — the one that
// BINDS and the one that DESCRIBES — and that agreement is complete without any
// of it. The store is real but never queried: binding only reads whether it is
// nil.
func TestTheRootBoundRoutesAreDescribedAndTheDescriptionsMatch(t *testing.T) {
	t.Parallel()

	router := chi.NewRouter()
	registerAuditLog(router, audit.NewStore(nil))

	doc := openapi.New("root", "test")
	describeAuditLog(doc)

	built, err := doc.Build(router)
	require.NoError(t, err, "the document has to build before its coverage can be read")

	assert.Empty(t, doc.UnmatchedDescriptions(),
		"a description that matches no route never enters the document: the endpoint "+
			"ships with a path, a method and NO BODY")

	assert.Empty(t, doc.UndescribedRoutes(),
		"a route bound at the composition root has no description. That is the debt "+
			"internal/e2e's ledger exists for, and this test sees it without a database")

	paths, ok := built["paths"].(map[string]any)
	require.True(t, ok, "the document has to carry a paths object")
	require.Contains(t, paths, auditLogPath,
		"the audit log's path is missing from the document; the audit above compared "+
			"two empty sets and proved nothing")
}

// TestTheAuditLogIsNotBoundWithoutAStore pins the refusal that keeps a lie off
// the surface.
//
// An installation can run with no audit writer at all, and then there is no
// table to read. An endpoint that existed anyway and answered with an empty page
// would be worse than none: a client cannot tell "there is no log here" from
// "nothing has happened", and during an incident those are opposite facts.
func TestTheAuditLogIsNotBoundWithoutAStore(t *testing.T) {
	t.Parallel()

	router := chi.NewRouter()
	registerAuditLog(router, nil)

	var routes []string

	require.NoError(t, chi.Walk(router,
		func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			routes = append(routes, method+" "+route)

			return nil
		}))

	assert.Empty(t, routes,
		"with no store the endpoint must not exist; an empty page would be "+
			"indistinguishable from a log that recorded nothing")
}
