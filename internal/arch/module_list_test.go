package arch_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file enforces ONE invariant: EVERY GO MODULE IN THIS REPOSITORY IS ON THE
// MAKEFILE'S LIST.
//
// `go test ./...` and `golangci-lint run ./...` do not reach a separate module —
// a separate go.mod is a separate build unit — so four lanes walk a list the
// Makefile keeps by hand: the tests, the integration tests, the linter and the
// vulnerability scan. A module missing from that list is verified by NOTHING, and
// nothing says so.
//
// # Why the target's own counter is not enough
//
// Each of those targets carries a floor: it counts what it ran and fails if the
// count came up short. That catches a glob matching nothing. It cannot catch a
// SHORTENED list, because the floor's population IS the list — trimming an entry
// trims the floor with it and everything passes.
//
// It is the same shape [TestTheProductionTreeListCoversTheRepository] closes for
// the audits in this package and [TestTheReferencedTreeListCoversTheContribModules]
// closes for the documentation scan. The population here comes from DISK: a
// directory with a go.mod is a module whatever any list says.

// separateModules matches the Makefile's list and its floor.
var (
	separateModulesPattern = regexp.MustCompile(`(?m)^SEPARATE_MODULES\s*:=\s*(.+)$`)
	separateCountPattern   = regexp.MustCompile(`(?m)^SEPARATE_MODULE_COUNT\s*:=\s*(\d+)\s*$`)
)

// TestTheModuleListCoversEveryModuleOnDisk keeps a module from being verified by
// nothing.
func TestTheModuleListCoversEveryModuleOnDisk(t *testing.T) {
	t.Parallel()

	listed, count := declaredModules(t)
	onDisk := modulesOnDisk(t)

	require.Greater(t, len(onDisk), 1,
		"only %d go.mod was found, so this audit compared almost nothing; the walk is "+
			"looking in the wrong place", len(onDisk))

	for _, dir := range onDisk {
		assert.Contains(t, listed, dir,
			"%s declares a Go module and is not in the Makefile's SEPARATE_MODULES.\n"+
				"`go test ./...` and `golangci-lint run ./...` cannot reach a separate "+
				"module, so four lanes read that list: test-modules, "+
				"test-modules-integration, lint and vuln. A module missing from it is "+
				"verified by NOTHING, and each target's own floor cannot notice — the "+
				"floor's population is the list.", dir)
	}

	for _, dir := range listed {
		assert.Contains(t, onDisk, dir,
			"the Makefile's SEPARATE_MODULES names %q and there is no go.mod there.\n"+
				"Each lane skips an entry it cannot find, silently, and the floor then "+
				"counts one fewer than the list promised — which reads as a broken glob "+
				"rather than as a name that moved.", dir)
	}

	assert.Equal(t, len(listed), count,
		"SEPARATE_MODULES names %d modules and SEPARATE_MODULE_COUNT says %d.\n"+
			"The count is the FLOOR every lane checks what it ran against; a floor lower "+
			"than the list lets a lane skip a module and still pass.", len(listed), count)
}

// declaredModules reads the list and the floor out of the Makefile.
//
// It parses the Makefile rather than taking the values from a Go constant, and
// that is the point: the Makefile is where the lanes read them, and a second
// copy here would be two sides of one comparison coming from the same place —
// which proves the copies agree and nothing about the lanes.
func declaredModules(t *testing.T) (dirs []string, count int) {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(repoRoot, makefileName))
	require.NoError(t, err, "the %s could not be read", makefileName)

	list := separateModulesPattern.FindStringSubmatch(string(body))
	require.Len(t, list, 2,
		"%s declares no SEPARATE_MODULES; either the variable was renamed and this "+
			"audit has to follow it, or the lanes stopped reading a list and this audit "+
			"is now about nothing", makefileName)

	dirs = append(dirs, strings.Fields(list[1])...)

	floor := separateCountPattern.FindStringSubmatch(string(body))
	require.Len(t, floor, 2, "%s declares no SEPARATE_MODULE_COUNT", makefileName)
	count, err = strconv.Atoi(floor[1])
	require.NoError(t, err)

	return dirs, count
}

// modulesOnDisk finds every directory declaring a module, written the way the
// Makefile writes them.
//
// The repository root is "." there, which is also what [treeOf] means by it, so
// the two vocabularies line up without a translation table.
func modulesOnDisk(t *testing.T) []string {
	t.Helper()

	var out []string
	err := filepath.WalkDir(repoRoot, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && slices.Contains(skippedDirs, entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() || entry.Name() != goModFileName {
			return nil
		}

		rel, relErr := filepath.Rel(repoRoot, filepath.Dir(current))
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(rel))

		return nil
	})
	require.NoError(t, err, "the repository could not be walked")

	return out
}
