package arch_test

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file is the second opinion ADR 0046 said it did not have.
//
// That record added eight modules to the graph and wrote the gap down in its own
// Consequences: "nothing in this repository audits third-party requires ... It is
// being taken on judgment, with no automated second opinion." The sentence is
// still true today, and this is the opinion.
//
// # Why a library audits its requires at all
//
// gobit is imported (ADR 0025), so every line of its go.mod is a line in the
// embedder's module graph, their `go list -m all`, their vulnerability scan and
// their legal review. A dependency here is not a private choice.
//
// # Why the INDIRECT requires are audited too, and it is not the obvious reason
//
// The obvious argument — "ADR 0046 pulled in seven indirect modules and a
// direct-only list would have missed six" — is wrong, and it was checked: those
// seven arrived BECAUSE two direct lines were added, so a direct-only ledger
// would have stopped the change and asked the question.
//
// The real case is the one no direct line marks: a version BUMP of a module
// already on the direct list can pull a new transitive module into every
// embedder's graph, and the direct line's path does not change. Nothing about
// that appears in a diff of direct requires. It appears here.
//
// # What this does NOT catch
//
// It reads go.mod, so it sees the module graph MVS computed and not what is
// linked: a require nothing imports is still listed, and a package pulled in by
// a build tag is not distinguished. It says nothing about licenses, and nothing
// about whether a module is maintained.

// directDependencyReasons is why an embedder inherits each direct require.
//
// A sentence per entry, and that is the point of the file: a new direct
// dependency is a promise made on somebody else's behalf, and this is where the
// person making it says why. An entry with no reason fails.
var directDependencyReasons = map[string]string{
	"github.com/99designs/gqlgen": "the GraphQL server the storefront read endpoint is " +
		"generated from; it is also the `tool` directive above the require block",
	"github.com/caarlos0/env/v11": "reads the configuration out of the environment, which " +
		"is the only configuration source this framework has",
	"github.com/go-chi/chi/v5": "the router every surface is mounted on, and the one whose " +
		"Walk this repository's route audits depend on",
	"github.com/golang-jwt/jwt/v5": "the admin session token's format; the alternative was " +
		"minting and parsing a JWT by hand, which is cryptography written in-house",
	"github.com/golang-migrate/migrate/v4": "runs the migration sets each module owns " +
		"(ADR 0003 records what its cancellation costs)",
	"github.com/jackc/pgx/v5": "the PostgreSQL driver, and PostgreSQL is a foundation " +
		"rather than a supported option (ADR 0015)",
	"github.com/prometheus/client_golang": "the metrics registry the scrape endpoint " +
		"serves; ADR 0046 chose scrape over push and this is what it cost",
	"github.com/redis/go-redis/v9": "the event bus's and the rate limiter's backend",
	"github.com/stretchr/testify": "assertions in tests. It reaches an embedder's graph " +
		"even so, because a module graph does not separate test dependencies — which is " +
		"the argument core/providertest uses for asserting by hand instead",
	"github.com/testcontainers/testcontainers-go": "starts the real PostgreSQL and Redis " +
		"the integration lane measures against; a fake would prove the fake",
	"github.com/testcontainers/testcontainers-go/modules/postgres": "the PostgreSQL half " +
		"of the same",
	"github.com/testcontainers/testcontainers-go/modules/redis": "the Redis half",
	"github.com/vektah/gqlparser/v2": "gqlgen's parser, required directly because the " +
		"generated code names it",
	"go.opentelemetry.io/otel":        "the tracing API the core is instrumented with",
	"go.opentelemetry.io/otel/metric": "the metrics API of the same",
	"go.opentelemetry.io/otel/trace":  "the trace API of the same",
	"go.opentelemetry.io/otel/sdk":    "the SDK that exports what the API records",
	"go.opentelemetry.io/otel/sdk/metric": "the metric SDK, needed by the Prometheus " +
		"exporter below",
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc": "sends TRACES to a " +
		"collector; ADR 0046 kept this path and moved metrics off it",
	"go.opentelemetry.io/otel/exporters/prometheus": "turns the metric SDK's readings into " +
		"the scrape endpoint's body (ADR 0046)",
	"golang.org/x/crypto": "bcrypt for the admin password hash; the standard library has " +
		"no password hash",
}

// indirectDependencyFile lists the modules MVS pulls in behind the direct ones.
//
// Names only, and no reason each. Seventy-five sentences about somebody else's
// transitive graph would be seventy-five sentences nobody maintains, and a
// ledger that rots is worse than a list that is merely long. What the list buys
// is the DIFF: a module entering the graph appears here, in a change somebody
// reviews, whatever caused it.
const indirectDependencyFile = "testdata/indirect-dependencies.txt"

// goModDirectives are the top-level directives go.mod is known to carry.
//
// An unrecognized one FAILS rather than being skipped, and the reason is a
// semantic inversion the first draft of this gate had: a scan not scoped to the
// require blocks reads a tab-indented line inside `exclude (` as a require, so a
// module deliberately kept OUT of the graph would be demanded in the allowlist —
// the exact opposite of the truth. `replace` and `retract` blocks mislead in
// their own ways.
var goModDirectives = []string{"module", "go", "toolchain", "require", "tool"}

// goModRequire matches one line of a require block: a module path and a version.
var goModRequire = regexp.MustCompile(`^\s+(\S+)\s+v\S+`)

// The floors, one per block.
//
// Two rather than one, because a single floor over the total is satisfied by
// either block alone: with 21 direct and 75 indirect, a scan that lost every
// direct line still counts 75 and passes a floor of 60. Each is well under
// today's count for the usual reason — a floor that tracks the number has to be
// edited on every ordinary change.
const (
	directRequireFloor   = 15
	indirectRequireFloor = 50
)

// TestEveryDependencyAnEmbedderInheritsIsWrittenDown is the audit.
func TestEveryDependencyAnEmbedderInheritsIsWrittenDown(t *testing.T) {
	t.Parallel()

	direct, indirect := goModRequires(t)

	require.GreaterOrEqual(t, len(direct), directRequireFloor,
		"only %d direct requires were read out of go.mod; the parser has gone blind and "+
			"an empty reading agrees with an empty allowlist", len(direct))
	require.GreaterOrEqual(t, len(indirect), indirectRequireFloor,
		"only %d indirect requires were read out of go.mod; the parser has gone blind",
		len(indirect))

	for _, module := range direct {
		reason, listed := directDependencyReasons[module]
		assert.True(t, listed,
			"%s is a DIRECT require and is not in directDependencyReasons.\n"+
				"gobit is a library: this module is now in every embedder's graph, their "+
				"`go list -m all` and their vulnerability scan. Write the sentence saying "+
				"why they inherit it, or remove the require.", module)
		assert.NotEmpty(t, strings.TrimSpace(reason),
			"%s is listed with an empty reason, which is the same as not being listed",
			module)
	}

	for _, module := range slices.Sorted(maps.Keys(directDependencyReasons)) {
		assert.Contains(t, direct, module,
			"%s has a reason written for it and is no longer a direct require of go.mod. "+
				"A ledger naming a dependency nobody has sends a reader looking for a "+
				"module that is not there.", module)
	}

	// The two sides are compared as SETS and only the difference is reported.
	// assert.Contains against a 75-element slice prints the whole slice for a
	// one-module drift, and a failure nobody reads to the end gets skimmed for
	// the exit code.
	declared := map[string]bool{}
	for _, module := range declaredIndirect(t) {
		declared[module] = true
	}

	present := map[string]bool{}
	for _, module := range indirect {
		present[module] = true

		if !declared[module] {
			t.Errorf("%s entered the module graph as an INDIRECT require and is not in %s.\n"+
				"It reaches every embedder whether or not anybody chose it, and the usual "+
				"cause is a version bump of a direct require — which changes no direct "+
				"line, so nothing else in a diff would show it.", module, indirectDependencyFile)
		}
	}

	for _, module := range slices.Sorted(maps.Keys(declared)) {
		if !present[module] {
			t.Errorf("%s is in %s and go.mod no longer requires it indirectly; the list "+
				"has outlived the graph", module, indirectDependencyFile)
		}
	}
}

// goModRequires parses go.mod's require blocks.
func goModRequires(t *testing.T) (direct, indirect []string) {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	require.NoError(t, err, "go.mod could not be read")

	inRequire := false

	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)

		switch {
		case trimmed == ")":
			inRequire = false

			continue
		case strings.HasPrefix(trimmed, "//") || trimmed == "":
			continue
		}

		// A top-level directive is one that starts at column zero.
		if !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, " ") {
			directive, _, _ := strings.Cut(trimmed, " ")
			require.Contains(t, goModDirectives, directive,
				"go.mod carries the %q directive, which this audit does not know how to "+
					"read. A block it walked into blindly would be scanned as requires: an "+
					"`exclude` entry would then be DEMANDED in the allowlist, which is the "+
					"opposite of what it means.", directive)

			inRequire = directive == "require" && strings.HasSuffix(trimmed, "(")
			if directive == "require" && !inRequire {
				// A single-line require, outside any block.
				if match := goModRequire.FindStringSubmatch(" " + strings.TrimPrefix(trimmed, "require ")); match != nil {
					direct = append(direct, match[1])
				}
			}

			continue
		}

		if !inRequire {
			continue
		}

		match := goModRequire.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		if strings.Contains(line, "// indirect") {
			indirect = append(indirect, match[1])

			continue
		}

		direct = append(direct, match[1])
	}

	return direct, indirect
}

// declaredIndirect reads the indirect allowlist.
func declaredIndirect(t *testing.T) []string {
	t.Helper()

	body, err := os.ReadFile(indirectDependencyFile)
	require.NoError(t, err, "%s could not be read", indirectDependencyFile)

	var modules []string
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		modules = append(modules, trimmed)
	}

	return modules
}
