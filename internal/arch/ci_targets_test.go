package arch_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The lanes CI runs have to be the lanes a developer runs.
//
// Not "an equivalent command" — the SAME make target. A target carries guards a
// workflow step cannot: the loop over the separate modules, the floor that tells
// "nothing was wrong" apart from "nothing was looked at", and the `|| exit 1`
// inside a shell loop that would otherwise return only the last iteration's
// status. A `run:` line that reimplements a lane is a second copy of it, free to
// stay right while the target goes wrong — or, as happened here, to be narrower
// from the first day and stay that way for months.

// ciWorkflow is the workflow this audit reads.
const ciWorkflow = "ci.yml"

// lanesCIMustRun are the make targets whose absence from the workflow means a
// population goes unchecked, with the reason each one cannot be a bare command.
//
// It is a short hand-written list and that is its weakness; what keeps it honest
// is that each entry names a POPULATION, and the populations are derived
// elsewhere. `lint` and `vuln` both loop SEPARATE_MODULES, which
// [TestTheModuleListCoversEveryModuleOnDisk] holds to the go.mod files on disk.
var lanesCIMustRun = map[string]string{
	"make lint": "golangci-lint run ./... reaches ONE module. This repository is " +
		"six, and only the target loops the rest — with the same config, so a tree " +
		"cannot keep a style of its own, and with a floor, so a skipped module is " +
		"not a pass (D100)",
	"make vuln": "the same loop for govulncheck, plus the two guards the target's " +
		"own comment names: the `|| exit 1` inside the loop, without which a red " +
		"root and green examples exit 0, and the counter that tells an empty glob " +
		"from a clean scan",
	"make test-modules": "`go test ./...` from the root cannot reach a separate " +
		"module at all; without this target their tests are written and never run",
	"make gen": "the generated code is compared against the tree, and a source .sql " +
		"edit that never reached its .sql.go passes build, vet and both test lanes",
}

// runnableLines drops the workflow's comments.
//
// It is the difference between "CI runs this target" and "this target is
// MENTIONED in the file", and the first version of this gate did not make it:
// removing the `make vuln` STEP left the check green, because the comment above
// it explains why the step is a target rather than a bare command — and says
// "make vuln" while doing so. A gate that reads a file's prose audits the prose.
func runnableLines(body string) string {
	var kept []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}

	return strings.Join(kept, "\n")
}

// TestCIRunsTheLanesRatherThanReimplementingThem is the check.
func TestCIRunsTheLanesRatherThanReimplementingThem(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(filepath.Join(repoRoot, workflowDirName, ciWorkflow))
	require.NoErrorf(t, err, "%s/%s could not be read", workflowDirName, ciWorkflow)

	workflow := runnableLines(string(body))
	require.Contains(t, workflow, "runs-on:",
		"the workflow holds no job; the file was read and it is not the workflow")

	for target, why := range lanesCIMustRun {
		assert.Containsf(t, workflow, target,
			"CI does not run `%s`.\n%s\n\nA lane that runs only on a developer's "+
				"machine runs only for developers who did not pass --no-verify.",
			target, why)
	}

	// The linter action is refused BY NAME, and this is the half that would have
	// caught D100 on the day it was written. Its population is the module it
	// stands in; `make lint`'s is the list. Both present would be worse than
	// either alone — the fast one sets the expectation and the slow one looks
	// redundant.
	assert.NotContains(t, workflow, "golangci-lint-action",
		"the workflow uses the linting ACTION. It lints the module it is standing "+
			"in, which is the root, and the five separate modules are then linted by "+
			"the pre-push hook alone — which `git push --no-verify` skips. The target "+
			"`make lint` covers all six and pins one version instead of two.")
}
