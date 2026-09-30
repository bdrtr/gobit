package arch_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryDeclaringModuleIsAuditedAgainstItsSchema holds every package under
// internal/modules and plugins that declares personal data to a test that
// compares the declaration with its migrations through internal/schemaaudit
// (ADR 0278). contrib's modules are Go modules of their own and keep their
// own readers.
//
// A declaration no audit compares with the schema goes short the day a
// migration adds a column, and nothing says so: the order module went short
// that way (D188), and the settings module's shop country was never declared
// until its audit was written (D192), and webpush's audit judged only the
// columns whose names looked like a person. The population is the
// directories, so a package that starts to declare is held to the audit the
// same day.
func TestEveryDeclaringModuleIsAuditedAgainstItsSchema(t *testing.T) {
	t.Parallel()

	var dirs []string
	for _, tree := range []string{filepath.Join("internal", "modules"), "plugins"} {
		entries, err := os.ReadDir(filepath.Join(repoRoot, tree))
		require.NoError(t, err)
		for _, entry := range entries {
			if entry.IsDir() {
				dirs = append(dirs, filepath.Join(tree, entry.Name()))
			}
		}
	}

	declaring := 0
	for _, rel := range dirs {
		dir := filepath.Join(repoRoot, rel)
		files, err := os.ReadDir(dir)
		require.NoError(t, err)

		declares, audited := false, false
		for _, file := range files {
			name := file.Name()
			if file.IsDir() || !strings.HasSuffix(name, ".go") {
				continue
			}
			body, err := os.ReadFile(filepath.Join(dir, name))
			require.NoError(t, err)
			text := string(body)
			if strings.HasSuffix(name, "_test.go") {
				audited = audited || strings.Contains(text, "schemaaudit.Cover(")
			} else {
				declares = declares || strings.Contains(text, ") PersonalData() personaldata.Declaration")
			}
		}
		if !declares {
			continue
		}
		declaring++
		assert.True(t, audited,
			"%s declares personal data and no test in its package calls "+
				"schemaaudit.Cover: nothing compares the declaration with the columns its "+
				"migrations leave, so the next column a migration adds goes undeclared "+
				"without a failure", filepath.ToSlash(rel))
	}

	require.GreaterOrEqual(t, declaring, 14,
		"only %d declaring modules were found; the walk has gone blind", declaring)
}
