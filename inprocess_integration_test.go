//go:build integration

package gobit_test

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit"
	"github.com/bdrtr/gobit/core/container"
)

// This file is the PROOF of the facade's in-process entry point (ADR 0150), and
// it is deliberately written the way an EMBEDDER would write it: it imports
// `github.com/bdrtr/gobit` and nothing else of the framework except the module
// contract it adds a module through.
//
// # Why that constraint matters more than the assertions
//
// The claim is "a program that embeds gobit can bring a real installation up in
// its own test". A test that reached into internal/ to do it would prove nothing
// about that claim — the whole difficulty is that an outside program cannot. So
// the imports here are the assertion, and the rest is what one does with the
// handler afterwards.

// postgresImage is the database the harness runs against.
//
// It is the SAME pin the module integration tests use. A second version here
// would mean the framework was proven to assemble against a Postgres no other
// lane runs.
const postgresImage = "postgres:16-alpine"

// testDSN is the connection string of the container every test in this file
// shares.
var testDSN string

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres brings one Postgres up and runs the tests against it.
//
// It is a separate function because os.Exit skips deferred calls.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_inprocess"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	defer func() {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			fmt.Fprintf(os.Stderr, "the postgres container could not be stopped: %v\n", termErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "the postgres container could not be started: %v\n", err)

		return 1
	}

	testDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection string could not be read: %v\n", err)

		return 1
	}

	return m.Run()
}

// configureInstallation sets the environment an installation needs.
//
// The configuration comes from the environment because that is where the real
// binary reads it (ADR 0150), so a test sets what its scenario needs and nothing
// else — every other value is the production default, which is the point: a
// harness with defaults of its own would exercise a configuration no deployment
// has.
//
// t.Setenv forbids a parallel test, which is not a limitation of the framework
// but of an environment shared by one process. It is what makes these tests
// serial and it is worth knowing before somebody adds t.Parallel and watches two
// installations fight over DATABASE_URL.
func configureInstallation(t *testing.T) {
	t.Helper()

	t.Setenv("DATABASE_URL", testDSN)
	t.Setenv("JWT_SECRET", "an-in-process-signing-secret-long-enough")
	t.Setenv("APP_ENV", "development")
	// The event bus is the in-memory one: nothing here asserts about an event and
	// Redis is not running.
	t.Setenv("EVENT_BUS", "inmemory")
}

// TestAnEmbedderCanBringAnInstallationUpInItsOwnProcess is the decision.
//
// Two requests and neither is decoration. The schema endpoint proves the whole
// assembly ran — it is BUILT from the router tree, so a document with paths in it
// means the modules registered and bound their routes. The storefront read proves
// the guard rings are attached: it answers 401 without a publishable key, which is
// the production behaviour and the one a harness that skipped the rings would get
// wrong in the friendliest possible way.
func TestAnEmbedderCanBringAnInstallationUpInItsOwnProcess(t *testing.T) {
	configureInstallation(t)

	handler, stop, err := gobit.New().Version("in-process-test").InProcess(t.Context())
	require.NoError(t, err, "the installation must come up")
	require.NotNil(t, handler)

	defer stop()

	schema := request(t, handler, http.MethodGet, "/openapi.json", nil)
	require.Equal(t, http.StatusOK, schema.Code,
		"the generated schema must be served; body: %s", schema.Body.String())
	// The CHANNEL-SCOPED listing, which is where the storefront's catalog really
	// lives (ADR 0044). Naming it rather than a shorter guess is the point: a
	// substring that no route binds would make this assertion pass on a document
	// that happens to mention the word.
	assert.Contains(t, schema.Body.String(),
		"/store/v1/sales-channels/{sales_channel_id}/products",
		"the document is built FROM the router, so the catalog's own path in it means "+
			"the product module registered and bound its routes")

	unkeyed := request(t, handler, http.MethodGet,
		"/store/v1/sales-channels/sc_1/products", nil)
	assert.Equal(t, http.StatusUnauthorized, unkeyed.Code,
		"the storefront must refuse a request with no publishable key; a harness that "+
			"assembled the routes WITHOUT the guard rings would answer 200 here and every "+
			"authorization test written against it would pass for the wrong reason")

	health := request(t, handler, http.MethodGet, "/health", nil)
	assert.Equal(t, http.StatusOK, health.Code,
		"the health endpoint is outside the rings and must answer")
}

// TestTheInstallationCarriesTheEmbeddersOwnModule is the other half of the claim.
//
// An installation that came up without the caller's module would pass every
// assertion above: the framework's own endpoints are all there. The module added
// here binds one route, and asking for it is the only way to see that Add reached
// the same registry the assembly walks.
//
// # Why the route is not under /store/v1
//
// Because the guard ring answers 401 for an unknown /store/v1 path exactly as it
// does for a known one without a key — so a 401 there would prove nothing about
// whether the module is present. The module binds its own prefix, which is what a
// flat router allows, and the 200 is then unambiguous. What the guarded prefixes
// do is asserted in the test above, where its subject is the ring rather than the
// module.
func TestTheInstallationCarriesTheEmbeddersOwnModule(t *testing.T) {
	configureInstallation(t)

	handler, stop, err := gobit.New().Add(&loyaltyModule{}).InProcess(t.Context())
	require.NoError(t, err, "the installation must come up with an added module")

	defer stop()

	own := request(t, handler, http.MethodGet, "/loyalty/points", nil)
	assert.Equal(t, http.StatusOK, own.Code,
		"the embedder's own route must answer; body: %s", own.Body.String())
	assert.Equal(t, `{"points":42}`, own.Body.String())
}

// TestTheHandlerIsUsableAfterTheContextThatBuiltItIsDone is the trap a harness
// invites.
//
// The context passed to InProcess belongs to the BOOT: it migrates, it opens the
// pool, it resolves. If any of that were kept as the request context's parent,
// every request after the test's own context expired would fail — and in a test
// that is exactly when the assertions run.
func TestTheHandlerIsUsableAfterTheContextThatBuiltItIsDone(t *testing.T) {
	configureInstallation(t)

	boot, cancel := context.WithCancel(t.Context())

	handler, stop, err := gobit.New().InProcess(boot)
	require.NoError(t, err)

	defer stop()

	// The boot is over; the installation is not.
	cancel()

	health := request(t, handler, http.MethodGet, "/health", nil)
	assert.Equal(t, http.StatusOK, health.Code,
		"a request made after the boot context was canceled must still be served")
}

// request sends one request to the assembled handler.
func request(
	t *testing.T, handler http.Handler, method, path string, body []byte,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// loyaltyModule is an embedder's module: one store route and no schema.
//
// It is as small as a module can be, because what it proves is not what a module
// can do but that the installation carried it.
type loyaltyModule struct{}

func (m *loyaltyModule) Name() string { return "loyalty" }

func (m *loyaltyModule) Migrations() fs.FS { return nil }

func (m *loyaltyModule) Register(context.Context, *container.Container) error { return nil }

func (m *loyaltyModule) Routes(r chi.Router) {
	r.Get("/loyalty/points", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"points":42}`))
	})
}
