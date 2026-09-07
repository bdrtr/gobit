package arch_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// lintConfigFile is the golangci-lint configuration.
const lintConfigFile = ".golangci.yml"

// TestTheDepguardMatrixNamesEveryModule keeps the lint half of the
// module-isolation rule from going stale.
//
// # The defect this was written for, and it was live
//
// [TestModulesDoNotImportEachOther] enforces Principle 2.1/2.4 by walking the
// real module directories, so it covers every module the moment one is added.
// Its own godoc says depguard applies the same rule on the lint side and that
// the test is "the second line of defense... if the rule list in .golangci.yml
// is FORGOTTEN while a module is being added".
//
// It was forgotten. Measured on 2026-09-07, the matrix named fifteen modules;
// `invoice` and `review` appeared nowhere in the file. So lint did not stop
// either of them from importing another module, and it did not stop any of the
// other fifteen from importing THEM — 211 entries where 273 were needed. The
// arch test still held the line, which is exactly why nothing noticed: the
// second line of defense was doing all the work while the first quietly covered
// less of the tree every time a module was added.
//
// # Why a text scan and not a YAML parse
//
// The shape being checked is a set of names, and what the check has to survive
// is somebody adding a module — not somebody restructuring the config. A parse
// would bring a dependency into internal/arch for no extra certainty, and the
// blindness guard below is what actually protects against reading nothing: if
// the section header ever changes shape, the count drops to zero and this test
// fails rather than passing over a file it no longer understands.
//
// # Both directions, because each misses a different module
//
// A module needs a section of its own (so IT cannot import the others) AND an
// entry in every other module's section (so the others cannot import IT). A
// matrix with only the first is silently one-way.
func TestTheDepguardMatrixNamesEveryModule(t *testing.T) {
	t.Parallel()

	modules := moduleNames(t)
	require.NotEmpty(t, modules, "no module directory was found; the audit has gone blind")

	body, err := os.ReadFile(filepath.Join(repoRoot, lintConfigFile))
	require.NoError(t, err, "%s could not be read", lintConfigFile)

	config := string(body)

	sections := 0

	for _, mod := range modules {
		header := fmt.Sprintf("module-isolation-%s:", mod)
		if !strings.Contains(config, header) {
			t.Errorf("%s has no %q section, so lint does not stop the %s module from importing another one.\n"+
				"TestModulesDoNotImportEachOther still catches it, which is why this is easy to miss: "+
				"the lint rule quietly covers less of the tree every time a module is added.",
				lintConfigFile, header, mod)

			continue
		}

		sections++

		for _, other := range modules {
			if other == mod {
				continue
			}

			entry := fmt.Sprintf("github.com/bdrtr/gobit/internal/modules/%s\"", other)
			// The entry has to appear at least as many times as there are
			// sections that must deny it — every module except the one that
			// owns it, plus its own files: line.
			if strings.Count(config, entry) < len(modules)-1 {
				t.Errorf("%s names the %s module fewer times than the matrix needs.\n"+
					"Every module's section must deny every OTHER module; a matrix that only "+
					"gives a module its own section is one-way, and the modules added before it "+
					"can still import it.", lintConfigFile, other)

				break
			}
		}
	}

	require.Equal(t, len(modules), sections,
		"the matrix covers %d of the repository's %d modules", sections, len(modules))
}
