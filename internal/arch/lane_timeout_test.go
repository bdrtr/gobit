package arch_test

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheIntegrationLaneGivesThePackagesCIsTime is D210: the Makefile's
// integration lane gives every package the per-package timeout the CI's
// integration step gives it. Without one, go test's default ten minutes
// applied locally while CI allowed fifteen, and a package CI passed in a
// little under ten, as internal/arch is, was one slow run from failing the
// local lane.
func TestTheIntegrationLaneGivesThePackagesCIsTime(t *testing.T) {
	t.Parallel()

	timeout := regexp.MustCompile(`-timeout[ =](\S+)`)
	local := timeout.FindStringSubmatch(makeRecipe(t, readRepoFile(t, "Makefile"), "test-integration"))
	require.Len(t, local, 2,
		"the Makefile's integration lane names no -timeout, so go test's ten minutes apply, which are not CI's")

	step := regexp.MustCompile(`go test -race -tags=integration[^\n]*-timeout[ =](\S+)`)
	remote := step.FindStringSubmatch(readRepoFile(t, ".github/workflows/ci.yml"))
	require.Len(t, remote, 2, "the CI's integration step names no -timeout")

	assert.Equal(t, remote[1], local[1],
		"the local integration lane and the CI's integration step give a package different times")
}
