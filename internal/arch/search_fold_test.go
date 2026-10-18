package arch_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/textfold"
)

// The admin lists' search (ADR 0456) folds in SQL, in one function each module
// carries in its own migration, and the query is folded by the same function,
// so the two sides of one module cannot disagree. What can drift is the SQL
// from the Go fold, and one module's function from another's. These gates
// hold both.

// searchMigrations finds every up-migration that defines a search document,
// rather than naming three files: a fourth module's search would otherwise be
// outside the gate the day it was written.
func searchMigrations(t *testing.T) map[string]string {
	t.Helper()

	found := map[string]string{}
	paths, err := filepath.Glob(filepath.Join(repoRoot, "internal", "modules", "*", "migrations", "*.up.sql"))
	require.NoError(t, err)
	for _, path := range paths {
		body, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		if strings.Contains(string(body), "_search_document(phone text, VARIADIC words text[])") {
			found[path] = string(body)
		}
	}
	require.NotEmpty(t, found, "no search migration was found; the gate would read nothing")

	return found
}

// translateArguments matches the two strings a search fold passes to
// translate(): the letters, as the file's one U& literal, and their bases.
var translateArguments = regexp.MustCompile(`U&'([^']*)',\s*'([^']*)'\)`)

// unicodeLiteral decodes the \XXXX escapes of a U& string.
func unicodeLiteral(t *testing.T, escaped string) string {
	t.Helper()

	var b strings.Builder
	for i := 0; i < len(escaped); {
		if escaped[i] == '\\' && i+5 <= len(escaped) {
			code, err := strconv.ParseUint(escaped[i+1:i+5], 16, 32)
			require.NoError(t, err, "a U& escape is four hex digits: %q", escaped[i:i+5])
			b.WriteRune(rune(code))
			i += 5

			continue
		}
		b.WriteByte(escaped[i])
		i++
	}

	return b.String()
}

// TestEverySearchFoldIsTheGoFold holds each search document's translate() to
// the table internal/core/textfold folds by in Go. A letter left out of the
// SQL folds there only on a cluster whose CTYPE knows it, and one mapped to
// another base finds another word.
func TestEverySearchFoldIsTheGoFold(t *testing.T) {
	from, to := textfold.SQLTranslate()

	for path, body := range searchMigrations(t) {
		matches := translateArguments.FindAllStringSubmatch(body, -1)
		require.Len(t, matches, 1, "%s: the search document folds with exactly one translate()", path)
		assert.Equal(t, from, unicodeLiteral(t, matches[0][1]), "%s: the letters translate() folds", path)
		assert.Equal(t, to, matches[0][2], "%s: the bases translate() folds them to", path)
	}
}

// searchFunctionNames finds a search function's name, whose module prefix is
// taken out before the modules' functions are compared.
var searchFunctionNames = regexp.MustCompile(`\b[a-z]+_search_(document|query)\b`)

// sqlCode is SQL with its comment lines taken out; the comments may speak of
// the module.
func sqlCode(sql string) string {
	var code []string
	for _, line := range strings.Split(sql, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			code = append(code, line)
		}
	}

	return strings.Join(code, "\n")
}

// TestEverySearchFunctionIsWrittenAlike compares the modules' search
// functions. One module splitting words or reading a phone otherwise than
// another would answer one q two ways on one panel.
func TestEverySearchFunctionIsWrittenAlike(t *testing.T) {
	bodies := map[string][]string{}
	for path, body := range searchMigrations(t) {
		start := strings.Index(body, "CREATE FUNCTION")
		end := strings.Index(body, "CREATE INDEX")
		require.True(t, start >= 0 && end > start, "%s: functions first, then the indexes", path)
		functions := searchFunctionNames.ReplaceAllString(sqlCode(body[start:end]), "x_search_$1")
		bodies[functions] = append(bodies[functions], path)
	}

	if len(bodies) > 1 {
		var groups []string
		for _, paths := range bodies {
			sort.Strings(paths)
			groups = append(groups, strings.Join(paths, ", "))
		}
		sort.Strings(groups)
		t.Errorf("the search functions differ between modules; groups that read alike:\n%s",
			strings.Join(groups, "\n"))
	}
}
