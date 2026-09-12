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

// A lane hands the tests no database of the developer's.
//
// The configuration defaults to localhost:5432 and localhost:6379, and a
// development machine usually has both. A test that forgets to start its own
// installation therefore reaches one, passes on the machine that wrote it, and
// fails on a runner where nothing listens — which is how a green local suite
// and a red main happened on the same commit (D107, ADR 0163).
//
// What this audits is the LANE rather than any test: the targets below must set
// the two variables to something other than the default, so the forgetting
// fails here instead of one push later.

// laneEnvironmentVars are the settings that decide which services a test
// reaches.
var laneEnvironmentVars = []string{"DATABASE_URL", "REDIS_URL"}

// lanesThatRunTests are the make targets that start a Go test binary.
//
// `lint`, `vuln` and `gen` are not here because they run no test: nothing in
// them can reach a database by accident.
var lanesThatRunTests = []string{"test", "test-integration", "smoke"}

func TestNoLaneHandsATestTheAmbientServices(t *testing.T) {
	makefile := readRepoFile(t, "Makefile")
	defaults := configuredDefaults(t)

	for _, lane := range lanesThatRunTests {
		t.Run(lane, func(t *testing.T) {
			recipe := expandMakeVariables(makefile, makeRecipe(t, makefile, lane))

			for _, name := range laneEnvironmentVars {
				value, found := assignedInRecipe(recipe, name)
				require.Truef(t, found,
					"the %q lane does not set %s, so a test that starts no installation of "+
						"its own reaches whatever is listening on this machine. It then passes "+
						"here and fails on the runner", lane, name)
				assert.NotEqualf(t, defaults[name], value,
					"the %q lane sets %s to the configuration's own default, which is the "+
						"address a forgotten test would have reached anyway", lane, name)
			}
		})
	}
}

// configuredDefaults reads what a test would reach if a lane handed it nothing.
//
// The values are READ from the configuration rather than written here. A copy
// kept in this file would agree with itself after somebody moved the real
// default, and the whole assertion is that the lane's value DIFFERS from it.
func configuredDefaults(t *testing.T) map[string]string {
	t.Helper()

	source := readRepoFile(t, filepath.Join("internal", "core", "config", "config.go"))
	found := map[string]string{}

	for _, name := range laneEnvironmentVars {
		pattern := regexp.MustCompile(`env:"` + name + `"\s+envDefault:"([^"]+)"`)
		match := pattern.FindStringSubmatch(source)
		require.Lenf(t, match, 2,
			"internal/core/config/config.go declares no envDefault for %s. This audit is "+
				"written on the assumption that a forgotten test reaches a DEFAULT address; "+
				"if the default is gone, the rule it protects has changed", name)
		found[name] = match[1]
	}

	return found
}

// makeRecipe returns the lines a target runs, with the comments dropped.
//
// The comments are dropped because a recipe explains itself: the unit lane's own
// comment discusses coverage and could as easily discuss DATABASE_URL, and a
// check that read it would be auditing the prose rather than the command. The
// same mistake is recorded against the CI lane audit, which found it the hard
// way.
func makeRecipe(t *testing.T, makefile, target string) string {
	t.Helper()

	lines := strings.Split(makefile, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, target+":") {
			start = i + 1
			break
		}
	}
	require.GreaterOrEqualf(t, start, 0, "the Makefile has no %q target", target)

	var recipe []string
	for _, line := range lines[start:] {
		if !strings.HasPrefix(line, "\t") {
			break
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		recipe = append(recipe, line)
	}
	require.NotEmptyf(t, recipe, "the %q target runs nothing", target)

	return strings.Join(recipe, "\n")
}

// expandMakeVariables substitutes the simply-expanded variables a recipe names.
//
// Without this the audit would accept a recipe that mentions
// $(NO_AMBIENT_SERVICES) whatever that variable holds, which is where the value
// actually lives.
func expandMakeVariables(makefile, recipe string) string {
	assignment := regexp.MustCompile(`(?m)^([A-Z_][A-Z0-9_]*)\s*:=\s*(.*)$`)
	for _, match := range assignment.FindAllStringSubmatch(makefile, -1) {
		recipe = strings.ReplaceAll(recipe, "$("+match[1]+")", match[2])
	}

	return recipe
}

// assignedInRecipe returns the value the recipe gives an environment variable.
func assignedInRecipe(recipe, name string) (string, bool) {
	pattern := regexp.MustCompile(name + `='([^']*)'`)
	match := pattern.FindStringSubmatch(recipe)
	if len(match) != 2 {
		return "", false
	}

	return match[1], true
}

// readRepoFile reads a file relative to the repository root.
func readRepoFile(t *testing.T, name string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("..", "..", name))
	require.NoErrorf(t, err, "%s could not be read", name)

	return string(body)
}
