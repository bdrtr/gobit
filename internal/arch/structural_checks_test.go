package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// skipCalls are the names of the testing methods that show a test as "passed"
// WITHOUT RUNNING it.
//
// All three give the same result: the package prints "ok" in the run output and
// the check has not run at all. They differ only in the shape of the call, not in
// its outcome.
var skipCalls = map[string]bool{
	"Skip":    true,
	"Skipf":   true,
	"SkipNow": true,
}

// TestStructuralChecksAreNotSkipped enforces that no check under internal/arch
// passes by SKIPPING itself.
//
// # Which failure class
//
// The common way the tests in this package die is not missing a violation but
// losing THE INPUT: the walk finds nothing one day and the test stays silently
// green. The cheapest and most invisible form of that disappearance is a skip —
// "skip if there is no module", "skip if there is no tree" — because a skipped
// test cannot be told apart from a passing one in the run summary (without
// go test -v the SKIP line is never printed) and its intent is innocent: on the
// day it is written that tree really does not exist yet. After the tree arrives
// nobody deletes the line; and because it is not deleted, the tree being MOVED one
// day becomes the same thing as the rule being removed.
//
// This repository had three instances and all three were written exactly that way:
// three import invariants skipping while the module, workflow and plugin trees did
// not exist. After the trees arrived all three lines stayed in place — because they
// looked harmless. Yet those lines were going to silence three checks at once on
// the day a directory name drifted; the rule would not be removed, only nobody
// would be looking.
//
// # Why forbidden, why not a "justified exemption"
//
// The input of this package is THE REPOSITORY ITSELF and the repository always
// exists. There is no situation in which a structural check cannot run: if what it
// looks for is not there, the answer is not "skip" but "lost" — because what it
// looks for disappearing is exactly the event these tests are supposed to report.
// That is why no door was opened either: an exemption mechanism without a single
// legitimate instance today would only leave a ready way out for the first person
// who gets stuck. If a genuinely legitimate skip is ever needed, the right move is
// to EXTEND this check with a justified exemption list; the justification is then
// visible in code review.
//
// # What this invariant does NOT GUARANTEE
//
// It does NOT say "every newly written structural test has a blindness guard", and
// it cannot. It closes one escape route only — the skip. A `for` loop that finishes
// doing nothing once the input set empties out passes through here without trouble;
// the only way to catch that is to KNOW what the walk counts, and that knowledge
// sits in the test itself and cannot be read from outside. The syntactic proxies
// one might try (LOOKING FOR a require.NotEmpty in the body, say) do not measure
// what they claim to, because they can be silenced with a single line; a rule that
// satisfies itself is the very class this package is trying to close. The real
// assurance is in mutation: that a guard works is known only by blinding the walk
// and seeing the test FAIL, and the way to automate that goes through writing a
// harness that deliberately breaks the tests — which is the job of a separate tool,
// not of this package.
//
// # Its false positive
//
// The match looks at THE NAME: were another type with a method called "Skip" (a
// parser, a scanner) used inside this package, it would fail unfairly. Type
// resolution is not done because no check in this package depends on go/types, and
// binding to it for this single case would make the scan cost larger than the value
// of the rule. The false positive is NOISY — somebody has to look and extend the
// rule; it does not pass silently.
func TestStructuralChecksAreNotSkipped(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(repoRoot, archDirName)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "%s could not be read", archDirName)

	fset := token.NewFileSet()
	scanned, callsSeen := 0, 0

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}

		// Build tags are IGNORED: a check behind the integration tag is subject to this
		// rule as well, and a scan looking at the tag would overlook it in an untagged
		// run.
		path := filepath.Join(dir, entry.Name())
		tree, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		require.NoError(t, parseErr, "%s could not be parsed", path)
		scanned++

		ast.Inspect(tree, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			callsSeen++

			sec, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !skipCalls[sec.Sel.Name] {
				return true
			}
			t.Errorf("%s: a %s.%s call — a structural check is SKIPPING itself.\n"+
				"A skipped test cannot be told apart from a passing one in the run summary: the "+
				"SKIP line is only visible with -v and the package still prints \"ok\". If the tree, "+
				"file or constant being looked for cannot be found, that is NOT a reason to skip "+
				"but the very event the check is supposed to report — on the day the condition "+
				"comes true the rule is not removed, only the check is over.\n"+
				"Turn the condition into a require and write in its message WHAT may have been "+
				"lost (a directory moved, a signature changed, a convention drifted). If the skip "+
				"really is legitimate, this check has to be extended with a justified exemption.",
				fset.Position(sec.Sel.Pos()), receiverName(sec.X), sec.Sel.Name)

			return true
		})
	}

	require.Positive(t, scanned,
		"no _test.go file was found under %s; this check must have gone BLIND — "+
			"if the structural tests moved to another package, archDirName has to move too.",
		archDirName)
	require.Positive(t, callsSeen,
		"no function call at all was found in the test files under %s; the walk must be "+
			"broken. A scan that cannot see a call cannot see a skip call either and reports "+
			"clean on every run.", archDirName)
}

// receiverName turns a call's receiver expression into short text for the error
// message; it returns "?" for an expression it cannot read.
//
// It is for diagnosis only: the receiver's name ("t", "b") does not change the rule
// but makes the message searchable in the source.
func receiverName(expr ast.Expr) string {
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}

	return "?"
}
