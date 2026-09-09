package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// caseFoldingProbeFile and caseFoldingProbeConst locate the startup probe.
const (
	caseFoldingProbeFile  = "core/db/casefold.go"
	caseFoldingProbeConst = "caseFoldingProbe"
)

// probeAlias matches the name the probe gives each column it measures.
var probeAlias = regexp.MustCompile(`(?i)\bAS\s+([a-z_]+)`)

// clusterContractWitnesses maps each path the startup probe measures to the
// test that FAILS when the cluster cannot fold it.
//
// The keys are not a list somebody typed: they are read out of the probe's own
// SQL, so a fourth path added there arrives here as a failure rather than as
// silence. The values are written, because "which test covers this" is a
// judgment and not something the tree can answer.
var clusterContractWitnesses = map[string]string{
	"pattern":    "TestTheStorefrontSearchFoldsTheClustersNonAsciiCase",
	"fulltext":   "TestTheIndexFoldsTheClustersNonAsciiCase",
	"case_lower": "TestTheEmailGuardRefusesANonAsciiCapitalOnAContractCluster",
}

// TestEveryProbedFoldPathHasATestThatFails is the gate behind a measurement.
//
// # What was measured
//
// ADR 0015's contract has one hard requirement — a ctype that folds more than
// ASCII — and gobit checks it at startup and WARNS. On 2026-09-09 the three
// packages that depend on that fold were run against a `--locale=C` cluster,
// which breaks the requirement, and ALL THREE PASSED. The warning was the only
// thing that changed, because every fixture in the suite was ASCII, and the
// reason each of them is ASCII is written beside it and is correct: a module
// test with a non-ASCII fixture would be testing the container.
//
// So the suite could not tell a cluster that satisfies the contract from one
// that breaks it, on the very paths the contract exists for.
//
// # What this gate holds
//
// Each probed path now has ONE test whose declared subject is the cluster, and
// which is therefore allowed to depend on how the container was created. This
// gate keeps them: deleting one restores the blindness, and a blind suite looks
// exactly like a passing one.
//
// It does NOT check that the named test still fails on a bad cluster. Nothing
// in a build can check that without starting a second, deliberately broken
// cluster for every path; what stands in for it is the record — ADR 0081 — and
// the reproduction in each test's own file.
func TestEveryProbedFoldPathHasATestThatFails(t *testing.T) {
	probed := probedFoldPaths(t)

	require.GreaterOrEqual(t, len(probed), 3,
		"only %d probed fold paths were read out of %s; the probe is a single "+
			"constant and reading fewer than the three it has means the scan went blind",
		len(probed), caseFoldingProbeFile)

	names := testFunctionNames(t)

	for _, path := range probed {
		witness, named := clusterContractWitnesses[path]
		assert.Truef(t, named,
			"%s probes the %q fold and no test is named for it.\n"+
				"A path the startup probe measures is a path the suite has to be able to "+
				"tell apart; add the witness test and name it here, or write down why the "+
				"warning alone is enough for this one.", caseFoldingProbeFile, path)
		if !named {
			continue
		}

		assert.Containsf(t, names, witness,
			"the witness named for the %q fold, %s, is not in the tree.\n"+
				"Deleting it makes the suite green on a cluster that breaks ADR 0015's "+
				"contract, which is the state this gate was written out of.", path, witness)
	}

	for _, path := range slices.Sorted(maps.Keys(clusterContractWitnesses)) {
		assert.Containsf(t, probed, path,
			"a witness is named for the %q fold, which %s no longer probes; a witness for "+
				"a path that is gone hides the next path that takes its name",
			path, caseFoldingProbeFile)
	}
}

// probedFoldPaths returns the column names the startup probe gives its answers.
//
// The constant is read as a VALUE — the parser folds the concatenation the
// source is written in — rather than matched over the file text. A text match
// would also read the comments above each line, and every one of them names the
// path it introduces.
func probedFoldPaths(t *testing.T) []string {
	t.Helper()

	path := filepath.Join(repoRoot, caseFoldingProbeFile)
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoErrorf(t, err, "%s could not be parsed", caseFoldingProbeFile)

	var sql string
	ast.Inspect(parsed, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || spec.Names[0].Name != caseFoldingProbeConst {
			return true
		}
		require.Len(t, spec.Values, 1, "%s is not a single expression", caseFoldingProbeConst)
		sql = concatenatedString(t, spec.Values[0])

		return false
	})
	require.NotEmptyf(t, sql, "%s was not found in %s", caseFoldingProbeConst, caseFoldingProbeFile)

	var aliases []string
	for _, match := range probeAlias.FindAllStringSubmatch(sql, -1) {
		aliases = append(aliases, strings.ToLower(match[1]))
	}

	return aliases
}

// concatenatedString evaluates a constant expression built from string literals
// joined with "+".
func concatenatedString(t *testing.T, expr ast.Expr) string {
	t.Helper()

	switch node := expr.(type) {
	case *ast.BasicLit:
		if node.Kind != token.STRING {
			return ""
		}
		value, err := strconv.Unquote(node.Value)
		require.NoError(t, err, "a literal inside %s could not be read", caseFoldingProbeConst)

		return value
	case *ast.BinaryExpr:
		if node.Op != token.ADD {
			return ""
		}

		return concatenatedString(t, node.X) + concatenatedString(t, node.Y)
	case *ast.ParenExpr:
		return concatenatedString(t, node.X)
	default:
		return ""
	}
}
