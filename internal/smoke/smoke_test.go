//go:build smoke

// Package smoke exercises the application's REAL startup, real migrations, real
// configuration loading and real signal handling.
//
// # Why this package exists
//
// While the repository passed its unit and integration tests (~76% coverage) and
// lint spotlessly, running the application BY HAND turned up four faults the tests
// did NOT see, and all four were of the same class:
//
//  1. When three instances started against an empty database at the same time, two
//     of them died with "admin_bootstrap_failed" (a crash loop with replicas:3).
//  2. The spec-compliant form of OTEL_EXPORTER_OTLP_ENDPOINT (http://host:4317) was
//     silently swallowed: "telemetry is set up" was logged, yet not a single span
//     went out.
//  3. The name of the metric interval variable clashed with the OpenTelemetry spec;
//     the spec-compliant value (60000) brought the application down AT STARTUP.
//  4. make migrate-up was still holding back a feature from nine phases earlier.
//
// What they have in common is that none of them lives INSIDE a package: all four sit
// in internal/app's wiring, in the startup sequence, or in process behavior.
//
// internal/e2e cannot see them, and should not be expected to: it drives the router
// with httptest, which means it SKIPS main.go's wiring, the migrations at startup,
// config loading and signal handling. This package closes exactly that gap.
//
// # What is tested and what is not
//
// The criterion is NOT "business logic or infrastructure"; the criterion is this: can
// the claim be verified only by the decisions of the REAL PROCESS? Whether an endpoint
// is mounted, whether a module is registered, whether a flow is wired, whether a
// migration ran at startup — main() settles all of these and no module test can see
// them. The body of such a claim inevitably passes through business logic (a cart is
// opened, a price is read, an order is born), but what is being tested is not the
// arithmetic itself, it is THAT THE PATH IS OPEN.
//
// The correctness of the arithmetic does not belong here: a claim that tests the same
// total runs far more cheaply in internal/e2e and in the module tests, and that is
// where it runs. Because every scenario in this package pays the cost of a container
// plus a startup plus a real process, it asks only the question that is worth that
// cost.
//
// # Which scenario guards which fault
//
//   - acilis_test.go: proves that a fresh installation becomes usable with no manual
//     step. It is also today's answer to the fourth fault ("migrate-up was holding a
//     feature back as a separate command"): migrations are applied AT STARTUP, and
//     what verifies that is no longer a sentence in the Makefile but a process that
//     boots against an empty database and can be logged into.
//   - yaris_test.go: the first fault, the seeding race.
//   - izleme_test.go: the second and third faults, the OTLP address format and the
//     name clash of the metric interval variable.
//   - yapilandirma_test.go and kapanis_test.go: the two unclosed wings of the same
//     class — STARTING UP with a flawed configuration and FAILING TO SHUT DOWN on a
//     signal.
//   - b2b_test.go and graphql_test.go: two surfaces that had NEVER run in a real
//     process; both exist solely thanks to a single registration line in the
//     composition root.
//   - anahtar_test.go: the SETUP TRAP a developer following the documentation falls
//     into — a publishable key created without a channel gets a 201 but always gets a
//     401 on the storefront surface, and the diagnostic code is not in the response
//     but in the server's log.
//   - vitrin_test.go: that the path from cart to order really is OPEN. The static
//     invariant in internal/arch sees that the flows are WIRED in the composition
//     root but cannot see that the wiring RUNS; the proof of that half lives here.
//
// # Setup
//
// The tests share ONE Postgres and ONE Redis container; scenarios get their isolation
// from a SEPARATE DATABASE (see [scenarioDatabase]). The server binary is likewise
// compiled ONCE (see [buildBinary]) and every scenario runs that one.
//
// # Why a compiled binary and not go run
//
// "go run" puts a parent process in between and may not forward SIGTERM to the child;
// one of the very things we want to test is signal behavior (see kapanis_test.go).
// Running the compiled binary directly guarantees that the signal the test sends lands
// in the same place as the signal the orchestrator sends in production.
package smoke

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/bdrtr/gobit/core/db"
)

// postgresImage and redisImage are the images the tests share.
//
// The versions are the SAME as in the integration tests (see internal/e2e and
// core/http/redisguard): the behavior of the migrations that run at startup
// must not diverge between the two runs.
const (
	postgresImage = "postgres:16-alpine"
	redisImage    = "redis:7-alpine"
)

// maintenanceDatabase is the container's first database and is used for nothing but
// CREATING the scenarios' own; a scenario never connects to it.
const maintenanceDatabase = "gobit_smoke"

// buildTimeout is the longest the binary is allowed to take to compile.
//
// It is generous because on a cold CI cache the whole dependency tree is compiled; a
// timeout here would mean nothing but a slow runner, not a real fault.
const buildTimeout = 5 * time.Minute

// Container and build output; TestMain fills them in, the scenarios read them.
var (
	// maintenanceDSN is the connection address of the maintenance database.
	maintenanceDSN string
	// maintenancePool is the pool that creates the scenario databases.
	maintenancePool *db.Pool
	// redisURL is the connection address of the Redis that was brought up.
	redisURL string
	// binaryPath is the full path of the compiled server binary.
	binaryPath string
)

// databaseCounter keeps scenario database names unique.
//
// A counter is used instead of a timestamp: two database names created in the same
// millisecond colliding was a fragility that would surface in exactly the concurrency
// scenario and would pin the fault on the test itself.
var databaseCounter atomic.Int64

// TestMain brings the containers up, compiles the binary and runs the scenarios.
func TestMain(m *testing.M) {
	os.Exit(runWithHarness(m))
}

// runWithHarness sets the harness up and returns the exit code.
//
// It lives in a separate function because os.Exit skips defers: the containers, the
// pool and the build directory can only be torn down safely in here.
func runWithHarness(m *testing.M) int {
	ctx := context.Background()

	pgCtr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase(maintenanceDatabase),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	defer func() {
		if termErr := testcontainers.TerminateContainer(pgCtr); termErr != nil {
			fmt.Fprintf(os.Stderr, "could not stop the postgres container: %v\n", termErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not start the postgres container: %v\n", err)
		return 1
	}

	maintenanceDSN, err = pgCtr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not obtain the postgres connection address: %v\n", err)
		return 1
	}

	redisCtr, err := tcredis.Run(ctx, redisImage)
	defer func() {
		if termErr := testcontainers.TerminateContainer(redisCtr); termErr != nil {
			fmt.Fprintf(os.Stderr, "could not stop the redis container: %v\n", termErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not start the redis container: %v\n", err)
		return 1
	}

	redisURL, err = redisCtr.ConnectionString(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not obtain the redis connection address: %v\n", err)
		return 1
	}

	maintenancePool, err = db.New(ctx, db.DefaultConfig(maintenanceDSN), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not open the maintenance pool: %v\n", err)
		return 1
	}
	defer maintenancePool.Close()

	dir, err := os.MkdirTemp("", "gobit-smoke-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not create the build directory: %v\n", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()

	binaryPath = filepath.Join(dir, "gobit")
	if err := buildBinary(ctx, binaryPath); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}

	return m.Run()
}

// buildBinary compiles the server ONCE.
//
// Doing the build here and not INSIDE the tests is deliberate: compiling in every
// scenario would multiply the slowest step by the number of scenarios. A single build
// also guarantees that every scenario drives the SAME binary — and since the sources
// do not change between scenarios, separate builds would buy nothing either.
func buildBinary(ctx context.Context, target string) error {
	root, err := filepath.Abs("../..")
	if err != nil {
		return fmt.Errorf("could not locate the repository root: %w", err)
	}

	buildCtx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()

	cmd := exec.CommandContext(buildCtx, "go", "build", "-o", target, "./cmd/server")
	cmd.Dir = root
	// The environment IS INHERITED: the build needs GOCACHE, GOMODCACHE and GOPATH.
	// The environment of the server PROCESS, by contrast, is built from scratch
	// (see surec_test.go).
	cmd.Env = os.Environ()

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("could not compile the server binary: %w\n%s", err, output)
	}

	return nil
}

// scenarioDatabase creates an empty database SPECIFIC to the scenario and returns its
// DSN.
//
// # Why a database per scenario and not a container per scenario
//
// Most scenarios want an EMPTY database: a cold start can only prove that the seeding
// ran, and a concurrent start can only prove that three instances raced, when there is
// no user at all. In a single shared database the administrator created by the first
// scenario silently skips the next one's seeding step, and the test stays green while
// proving nothing.
//
// Drawing the boundary per CONTAINER would do the same job, but bringing up one
// Postgres per scenario would add the cost of pulling the image plus starting it to
// the test's duration as many times as there are scenarios. CREATE DATABASE gives the
// same boundary within milliseconds; the migrations run from scratch in every database
// anyway, so the schema the scenario sees really is fresh.
func scenarioDatabase(t *testing.T) string {
	t.Helper()

	name := fmt.Sprintf("smoke_%s_%d", sanitizeDatabaseName(t.Name()), databaseCounter.Add(1))

	// pgx.Identifier does the quoting by the driver's own rules; even though the name
	// has already been filtered, quoting it by hand would silently leave an injection
	// surface should the filter ever loosen.
	_, err := maintenancePool.Pool().Exec(t.Context(),
		"CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	require.NoError(t, err, "could not create the scenario database: %s", name)

	addr, err := url.Parse(maintenanceDSN)
	require.NoError(t, err, "could not parse the maintenance DSN")
	addr.Path = "/" + name

	return addr.String()
}

// sanitizeDatabaseName turns a test name into a Postgres identifier.
//
// Subtests put a '/' into t.Name(), and names left over in Turkish can carry non-ASCII
// characters; both are invalid in an unquoted identifier. The name is for DIAGNOSTICS
// only (which database belongs to which scenario) and uniqueness comes from the
// counter, so a lossless conversion is not required.
func sanitizeDatabaseName(name string) string {
	const maxLength = 40

	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= maxLength {
			break
		}
	}

	return b.String()
}
