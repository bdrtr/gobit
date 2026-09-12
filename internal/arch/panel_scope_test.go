package arch_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The panel's privileges have to be privileges that EXIST (ADR 0156).
//
// The panel imports no module (Principle 2.4) and core knows none either, so the
// scope a screen requires is a string written in one tree and a constant declared
// in another. Nothing connects them at compile time: misspell the panel's copy
// and the tree still builds, every test of that screen still passes — they sign
// in as an operator carrying the admin scope, which satisfies anything — and what
// breaks is the one case nobody runs, an operator granted exactly that module's
// read privilege, who is refused a screen they are entitled to.
//
// So both sides are read from SOURCE and compared. Importing the panel's table
// would be the other shape of this mistake: the check would then agree with
// whatever the table says, which is the thing under suspicion.

// scopeConstant matches a scope constant's declaration, e.g.
//
//	ScopeRead = "product:read"
var scopeConstant = regexp.MustCompile(`"([a-z0-9_]+:[a-z0-9_]+)"`)

// panelScopeConstant matches the panel's own scope declarations in scope.go.
var panelScopeConstant = regexp.MustCompile(`\n\tscope[A-Za-z]+\s+= "([^"]+)"`)

// TestThePanelAsksForPrivilegesTheModulesDeclare compares the two spellings.
func TestThePanelAsksForPrivilegesTheModulesDeclare(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile(filepath.Join(repoRoot, "internal", "adminui", "scope.go"))
	require.NoError(t, err, "the panel's scope table is the subject of this check")

	matches := panelScopeConstant.FindAllStringSubmatch(string(source), -1)
	require.GreaterOrEqual(t, len(matches), 8,
		"only %d scope constants were read out of the panel's table. The pattern has gone "+
			"BLIND, and a blind read compares an empty set against the modules and passes",
		len(matches))

	declared := declaredScopes(t)
	require.GreaterOrEqual(t, len(declared), 20,
		"only %d scopes were found across the modules and the plugins; the other side of "+
			"this comparison has gone blind", len(declared))

	for _, match := range matches {
		scope := match[1]

		assert.True(t, declared[scope],
			"the panel's screens ask for %q, and no module or plugin declares it as a "+
				"scope constant.\nA privilege nothing grants is a screen an operator can "+
				"never open unless they hold the admin scope — and the tree still builds, "+
				"because the two spellings meet nowhere but here.", scope)
	}
}

// declaredScopes is every scope constant the api packages and the plugins
// declare.
//
// The population is derived from the value's SHAPE (a colon-separated pair in a
// string literal assigned to a Scope-prefixed constant) rather than from a list
// kept here: a module added next year declares its scopes the same way and joins
// this set without anybody remembering to.
func declaredScopes(t *testing.T) map[string]bool {
	t.Helper()

	out := map[string]bool{}
	for _, tree := range []string{"internal/modules", "plugins"} {
		for _, path := range goFiles(t, filepath.Join(repoRoot, tree)) {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}

			source, err := os.ReadFile(path)
			require.NoError(t, err)

			for _, line := range strings.Split(string(source), "\n") {
				if !strings.Contains(line, "Scope") || !strings.Contains(line, "=") {
					continue
				}
				if match := scopeConstant.FindStringSubmatch(line); match != nil {
					out[match[1]] = true
				}
			}
		}
	}

	return out
}
