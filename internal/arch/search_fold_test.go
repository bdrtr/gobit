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

// searchDefinition is one search function as a module's migrations leave it
// in effect: the file that last defines it, and that definition.
type searchDefinition struct {
	path, sql string
}

// searchFunction matches one definition of a search function, created or
// replaced, to the end of its body.
var searchFunction = regexp.MustCompile(
	`(?s)CREATE (?:OR REPLACE )?FUNCTION ([a-z]+)_search_(document|query)\(.*?\n\$\$;`)

// searchDefinitions reads, for every module, the definition of each of its
// two search functions that its migrations leave in effect: the last one in
// migration order. ADR 0460 replaced the document function in a later
// migration, so the first file to define it no longer says what folds. Every
// up-migration is read rather than three files named, so a fourth module's
// search is inside the gate the day it is written.
func searchDefinitions(t *testing.T) map[string]map[string]searchDefinition {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join(repoRoot, "internal", "modules", "*", "migrations", "*.up.sql"))
	require.NoError(t, err)
	sort.Strings(paths)

	found := map[string]map[string]searchDefinition{}
	for _, path := range paths {
		body, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		code := sqlCode(string(body))
		for _, m := range searchFunction.FindAllStringSubmatchIndex(code, -1) {
			module, kind := code[m[2]:m[3]], code[m[4]:m[5]]
			if found[module] == nil {
				found[module] = map[string]searchDefinition{}
			}
			found[module][kind] = searchDefinition{path: path, sql: code[m[0]:m[1]]}
		}
		if first := searchFunction.FindStringIndex(code); first != nil {
			for _, use := range []string{"CREATE INDEX", "REINDEX"} {
				if at := strings.Index(code, use); at >= 0 {
					assert.Greater(t, at, first[0], "%s: functions first, then the indexes", path)
				}
			}
		}
	}
	require.NotEmpty(t, found, "no search function was found; the gate would read nothing")
	for module, kinds := range found {
		require.Len(t, kinds, 2, "%s: a search has a document function and a query function", module)
	}

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

// TestEverySearchFoldIsTheGoFold holds each module's search document, as its
// migrations leave it, to the table internal/core/textfold folds by in Go. A
// letter left out of the SQL folds there only on a cluster whose CTYPE knows
// it, and one mapped to another base finds another word.
func TestEverySearchFoldIsTheGoFold(t *testing.T) {
	from, to := textfold.SQLTranslate()

	for module, kinds := range searchDefinitions(t) {
		document := kinds["document"]
		matches := translateArguments.FindAllStringSubmatch(document.sql, -1)
		require.Len(t, matches, 1, "%s (%s): the search document folds with exactly one translate()",
			module, document.path)
		assert.Equal(t, from, unicodeLiteral(t, matches[0][1]),
			"%s (%s): the letters translate() folds", module, document.path)
		assert.Equal(t, to, matches[0][2], "%s (%s): the bases translate() folds them to", module, document.path)
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
// functions as their migrations leave them. One module splitting words or
// reading a phone otherwise than another would answer one q two ways on one
// panel.
func TestEverySearchFunctionIsWrittenAlike(t *testing.T) {
	for _, kind := range []string{"document", "query"} {
		bodies := map[string][]string{}
		for module, kinds := range searchDefinitions(t) {
			definition := kinds[kind]
			written := strings.Replace(definition.sql, "CREATE OR REPLACE FUNCTION", "CREATE FUNCTION", 1)
			written = searchFunctionNames.ReplaceAllString(written, "x_search_$1")
			bodies[written] = append(bodies[written], module+" ("+definition.path+")")
		}

		if len(bodies) > 1 {
			var groups []string
			for _, modules := range bodies {
				sort.Strings(modules)
				groups = append(groups, strings.Join(modules, ", "))
			}
			sort.Strings(groups)
			t.Errorf("the search %s functions differ between modules; groups that read alike:\n%s",
				kind, strings.Join(groups, "\n"))
		}
	}
}
