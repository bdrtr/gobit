package arch_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/modfile"
)

// gobitModule is the root module's path; a contrib module's path extends it.
const gobitModule = "github.com/bdrtr/gobit"

// TestEveryContribModuleRequiresTheRelease holds each module under contrib/ to
// the release it is tagged with (ADR 0441, D295): every gobit module it
// requires, the root and another contrib module alike, at the newest release
// the CHANGELOG names, and a replace to this tree beside it.
//
// The modules required gobit v0.0.0, a version that does not exist. A replace
// made that build here and is ignored in an embedder's graph, so a go get of
// contrib/identity-session in a module that had not pinned gobit first failed
// on gobit@v0.0.0, and one that had built against whatever gobit it pinned,
// with nothing in the module saying which release it was written for. The cut
// that adds a release heading has to move these lines, and this test is red
// until it does.
func TestEveryContribModuleRequiresTheRelease(t *testing.T) {
	changelog, err := os.ReadFile(filepath.Join(repoRoot, "CHANGELOG.md"))
	require.NoError(t, err)
	newest := regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\] — `).FindSubmatch(changelog)
	require.NotNil(t, newest, "the CHANGELOG names no release")
	release := "v" + string(newest[1])

	files, err := filepath.Glob(filepath.Join(repoRoot, "contrib", "*", "go.mod"))
	require.NoError(t, err)
	seen := map[string]bool{}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		mod, err := modfile.Parse(path, raw, nil)
		require.NoError(t, err)
		name := strings.TrimPrefix(mod.Module.Mod.Path, gobitModule+"/")
		seen[name] = true

		replaced := map[string]bool{}
		for _, r := range mod.Replace {
			replaced[r.Old.Path] = strings.HasPrefix(r.New.Path, "../")
		}
		for _, req := range mod.Require {
			if req.Mod.Path != gobitModule && !strings.HasPrefix(req.Mod.Path, gobitModule+"/") {
				continue
			}
			assert.Equal(t, release, req.Mod.Version,
				"%s requires %s at %s; a module tagged %s/%s has to require the release it is tagged with",
				name, req.Mod.Path, req.Mod.Version, name, release)
			assert.True(t, replaced[req.Mod.Path],
				"%s requires %s with no replace into this tree; it would build here against the published one",
				name, req.Mod.Path)
		}
	}

	// The two an embedder is pointed at, by name, so a glob that stopped
	// reaching them is not a pass.
	for _, want := range []string{"contrib/identity-session", "contrib/identity-passkey"} {
		assert.True(t, seen[want], "%s/go.mod was not read; the glob has gone blind", want)
	}
}
