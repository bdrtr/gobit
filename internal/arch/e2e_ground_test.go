package arch_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file compares two composition roots, and it exists because one of them
// fell behind the other without anything noticing.
//
// internal/app is what a running gobit wires. internal/e2e wires the same set by
// hand, deliberately — its own documentation says the order "FOLLOWS the order in
// internal/app/app.go" — because a ground that called the real setup would test
// the setup rather than the modules. The cost of that copy is that it is a copy:
// a flow added to one is not added to the other, and nothing said so.
//
// # What it cost
//
// The cancellation flow was wired in production and not on the ground, and it was
// the only one of the seven that was. It is also the only flow driven entirely by
// the BUS — nothing resolves it, nothing calls it — so an unsubscribed one does
// nothing and no request fails. Two faults lived there for weeks: a write-off that
// credited the shelf with goods sitting in a box, and a parcel cancellation that
// released nothing (D75, D76). Both were green against fakes, because a fake is
// written to the mechanism rather than to what the endpoints can produce.
//
// # Why the flows and not the modules
//
// A module that is not registered fails LOUDLY: its routes are missing and its
// container names do not resolve, so the first scenario that touches it goes red.
// A flow can be silent — this one is — and silence is what needs a gate.
const (
	// appRootDir is the production composition root.
	appRootDir = "internal/app"

	// e2eGroundDir is the end-to-end ground that mirrors it.
	e2eGroundDir = "internal/e2e"

	// workflowImportPrefix is how a flow package is named in an import.
	workflowImportPrefix = "github.com/bdrtr/gobit/internal/workflows/"

	// wiredFlowFloor is the smallest number of flows the production root must
	// import.
	//
	// A floor and not a count: flows get added. What it defends against is the
	// scan going blind — a walk that finds none would make the comparison below
	// vacuously true, and this gate would pass on a root it never read. The
	// repository wired seven when it was written.
	wiredFlowFloor = 5
)

// groundExemptFlows are flows the end-to-end ground deliberately does not wire,
// with the reason.
//
// It is empty, and that is the point: adding a name here is a deliberate,
// reviewable admission that one flow's behavior is proven by nothing but its
// own unit tests. The alternative — letting the ground quietly fall behind — is
// what this file exists to end.
var groundExemptFlows = map[string]string{}

// TestTheEndToEndGroundWiresEveryFlowProductionDoes keeps the copy honest.
func TestTheEndToEndGroundWiresEveryFlowProductionDoes(t *testing.T) {
	t.Parallel()

	wired := flowsImportedBy(t, appRootDir)
	ground := flowsImportedBy(t, e2eGroundDir)

	require.GreaterOrEqual(t, len(wired), wiredFlowFloor,
		"only %d flow(s) were found in %s, so this gate is comparing against almost "+
			"nothing. Either the composition root moved or the import scan stopped "+
			"working; both make every assertion below vacuous.",
		len(wired), appRootDir)

	var missing []string
	for _, flow := range wired.sorted() {
		if _, exempt := groundExemptFlows[flow]; exempt {
			t.Logf("%s is wired in production and exempt from the ground: %s",
				flow, groundExemptFlows[flow])

			continue
		}
		if !ground[flow] {
			missing = append(missing, flow)
		}
	}

	assert.Empty(t, missing,
		"%s wires these flows and %s does not: %v.\n"+
			"A flow missing from the ground is a flow no scenario can reach, and the "+
			"dangerous case is the one that fails SILENTLY: a flow driven by the bus "+
			"is not resolved by anything, so an unwired one breaks no request and "+
			"reddens no test. That is where D75 and D76 lived. Wire it there, or add "+
			"it to groundExemptFlows with the reason it is proven elsewhere.",
		appRootDir, e2eGroundDir, missing)
}

// flowImportPattern matches an import of a flow package.
var flowImportPattern = regexp.MustCompile(
	`"` + regexp.QuoteMeta(workflowImportPrefix) + `([a-z][a-z0-9]*)"`)

// flowsImportedBy returns the flow packages a directory's Go files import.
//
// Imports rather than constructor calls, and the difference matters: a flow is
// wired by whatever name its package chose, and a scan looking for
// `FromContainer` would miss one that named its entry point anything else. An
// import cannot be avoided.
//
// The returned value doubles as a set and as a sorted list, because both callers
// want one of the two and building it twice would be two chances to disagree.
func flowsImportedBy(t *testing.T, dir string) flowSet {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(repoRoot, dir))
	require.NoError(t, err, "%s could not be read; if it moved, this gate is auditing "+
		"a place nothing lives in", dir)

	found := flowSet{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}

		path := filepath.Join(repoRoot, dir, entry.Name())
		body, readErr := os.ReadFile(path)
		require.NoError(t, readErr, "%s could not be read", path)

		for _, match := range flowImportPattern.FindAllStringSubmatch(string(body), -1) {
			found[match[1]] = true
		}
	}

	return found
}

// flowSet is the set of flow package names a directory imports.
type flowSet map[string]bool

// sorted returns the names in a stable order, so a failure message does not
// change between runs.
func (s flowSet) sorted() []string {
	out := make([]string, 0, len(s))
	for name := range s {
		out = append(out, name)
	}
	sort.Strings(out)

	return out
}
