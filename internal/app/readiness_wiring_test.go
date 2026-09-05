package app

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReadinessMapsUseTheNamedTypes proves the composition root never builds a
// readiness check map out of the UNNAMED map type.
//
// corehttp.GatingChecks and corehttp.DegradingChecks are distinct named types so
// that moving a dependency from one side to the other cannot be done by
// accident: a variable of one will not compile where the other is expected, and
// which side Redis lands on is the difference between "a Redis failover degrades
// the storefront" and "a Redis failover empties the load balancer".
//
// Go's assignability rules leave exactly one hole in that guarantee, and it is
// the shape this file's history had: an UNNAMED map[string]corehttp.HealthCheck
// is assignable to BOTH named types, so
//
//	checks := map[string]corehttp.HealthCheck{"postgres": pool.Ping}
//	setupRedis(ctx, c, cfg, checks, log)          // compiles, redis now GATES
//	NewRouter(RouterOptions{ReadinessChecks: checks, ...})
//
// compiles, puts Redis back on the gating side and passes every test in the
// repository — verified by mutation before this test was written. The compiler
// cannot see it because nothing is ill-typed; only the absence of the unnamed
// type can be checked, so that is what is checked here.
//
// An explicit conversion (corehttp.DegradingChecks(checks)) would also slip
// through. That is deliberate: a conversion is a sentence someone had to write
// on purpose, not a default that happens while looking away.
func TestReadinessMapsUseTheNamedTypes(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	fset := gotoken.NewFileSet()
	var offenders []string

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, parseErr := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.SkipObjectResolution)
		require.NoError(t, parseErr, "%s could not be parsed", name)

		ast.Inspect(file, func(n ast.Node) bool {
			mapType, ok := n.(*ast.MapType)
			if !ok {
				return true
			}
			if !isHealthCheckMap(mapType) {
				return true
			}

			offenders = append(offenders, fset.Position(mapType.Pos()).String())

			return true
		})
	}

	require.Empty(t, offenders,
		"the composition root writes an unnamed map[string]HealthCheck; use corehttp.GatingChecks "+
			"or corehttp.DegradingChecks so the two sides cannot be swapped by assignment: %v", offenders)
}

// isHealthCheckMap reports whether the map type is keyed by string and carries
// HealthCheck values, whatever the import is aliased to.
func isHealthCheckMap(m *ast.MapType) bool {
	key, ok := m.Key.(*ast.Ident)
	if !ok || key.Name != "string" {
		return false
	}

	value, ok := m.Value.(*ast.SelectorExpr)

	return ok && value.Sel.Name == "HealthCheck"
}
