package plugin

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// repositoryRoot is the tree this package sits in, two levels up.
const repositoryRoot = "../.."

// exampleRange is a RequiresCore written out for a plugin author to copy.
var exampleRange = regexp.MustCompile(`RequiresCore\(\) string \{ return "([^"]*)" \}`)

// releaseHeading is a released section of the CHANGELOG.
var releaseHeading = regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\] — `)

// TestEveryExampleRangeAdmitsTheCurrentRelease installs a plugin naming each
// range the documentation and the examples show a plugin author, at the
// newest release the CHANGELOG names, through [Registry.Install] (D289).
//
// docs/extending.md showed ">=v0.9.0 <v0.11.0" through v0.11.0 and into the
// v0.12.0 cut: a plugin that copied it was refused by every release since. A
// range there has an upper bound on purpose, since a minor version may break a
// plugin throughout 0.x, so the bound is not removed; the cut that adds a
// release heading has to move it, and this test is red until it does.
func TestEveryExampleRangeAdmitsTheCurrentRelease(t *testing.T) {
	changelog, err := os.ReadFile(filepath.Join(repositoryRoot, "CHANGELOG.md"))
	require.NoError(t, err)
	newest := releaseHeading.FindSubmatch(changelog)
	require.NotNil(t, newest, "the CHANGELOG names no release")
	release := "v" + string(newest[1])

	ranges := map[string]string{}
	for _, dir := range []string{"docs", "examples", "README.md"} {
		require.NoError(t, filepath.WalkDir(filepath.Join(repositoryRoot, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(repositoryRoot, path)
			// The records keep the ranges they were written with.
			if d.IsDir() && (rel == filepath.Join("docs", "adr") || rel == filepath.Join("docs", "measurements")) {
				return filepath.SkipDir
			}
			if d.IsDir() || (!strings.HasSuffix(path, ".md") && !strings.HasSuffix(path, ".go")) {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range exampleRange.FindAllSubmatch(body, -1) {
				ranges[rel+": "+string(m[1])] = string(m[1])
			}
			return nil
		}))
	}

	// The two places a plugin author is pointed at, by name, so a walk that
	// stopped reaching them is not a pass.
	for _, want := range []string{filepath.Join("docs", "extending.md"), filepath.Join("examples", "plugin", "plugin.go")} {
		found := false
		for site := range ranges {
			found = found || strings.HasPrefix(site, want+": ")
		}
		assert.True(t, found, "%s shows no RequiresCore range; the walk did not reach it", want)
	}

	for site, requires := range ranges {
		_, err := installAt(t, release, ranged{name: "example", requires: requires})
		assert.NoError(t, err, "%s: a plugin copying this range is refused by %s, the newest release", site, release)
	}
}
