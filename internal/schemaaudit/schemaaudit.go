// Package schemaaudit holds a module's personal-data declaration to the
// columns its migrations leave.
//
// A declaration is written by hand and its schema grows by migration, and the
// two came apart four times in one day: the auth audit read CREATE TABLE
// blocks alone and missed a column an ALTER TABLE added (D187), the order
// module declared the tables it began with and none added after (D188), the
// invoice audit compared the declaration with a list read off its first
// migration (D189), and the inventory audit's reader skipped every ALTER
// written across lines (D190). Each was the module's own copy of a reader.
// This package is one reader and one comparison, starting from the schema:
// every column the migrations leave is declared or judged to hold nobody,
// never both, and every name on either side exists. Every module audit under
// internal/modules uses it; contrib/identity-passkey, a Go module of its own,
// keeps its own reader.
package schemaaudit

import (
	"io/fs"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/personaldata"
)

// Cover fails t unless the declaration and notPersonal between them judge
// every column of every table the migrations leave, each exactly once.
//
// notPersonal lists, per table, the columns that hold nothing about a person;
// a table the migrations create must appear in it even when every column is
// declared, so a new table is judged by somebody.
func Cover(t testing.TB, migrations fs.FS, declaration personaldata.Declaration, notPersonal map[string][]string) {
	t.Helper()

	declared := map[string]bool{}
	for _, holding := range declaration.Holdings {
		key := holding.Table + "." + holding.Column
		assert.False(t, declared[key], "%s is declared twice", key)
		declared[key] = true
	}

	schema := Schema(t, migrations)
	for table, columns := range schema {
		exempt, judged := notPersonal[table]
		assert.True(t, judged,
			"%s is created by the migrations and judged nowhere: list the columns that hold "+
				"nobody with the reason, and declare the rest", table)

		for _, column := range exempt {
			assert.True(t, columns[column],
				"%s.%s is judged to hold nobody and the migrations leave no such column", table, column)
		}
		for column := range columns {
			key := table + "." + column
			if slices.Contains(exempt, column) {
				assert.False(t, declared[key], "%s is declared as personal data and also judged to hold nobody", key)
				delete(declared, key)
				continue
			}
			assert.True(t, declared[key],
				"%s is in the migrations and NOT in the declaration: declare it, or judge it "+
					"with the reason it holds nobody", key)
			delete(declared, key)
		}
	}
	for table := range notPersonal {
		assert.Contains(t, schema, table, "%s is judged and no migration leaves it", table)
	}

	remaining := make([]string, 0, len(declared))
	for key := range declared {
		remaining = append(remaining, key)
	}
	sort.Strings(remaining)
	assert.Empty(t, remaining, "these holdings name columns the migrations do not leave")
}

// Schema replays every up-migration in migrations, in name order, and returns
// the tables and columns they leave.
func Schema(t testing.TB, migrations fs.FS) map[string]map[string]bool {
	t.Helper()

	names, err := fs.Glob(migrations, "*.up.sql")
	require.NoError(t, err)
	require.NotEmpty(t, names, "no up-migration was found; the audit has gone blind")
	sort.Strings(names)

	var sql strings.Builder
	for _, name := range names {
		raw, err := fs.ReadFile(migrations, name)
		require.NoError(t, err)
		sql.Write(raw)
		sql.WriteString("\n")
	}

	schema := Replay(sql.String())
	require.NotEmpty(t, schema, "no table was read out of the migrations; the reader has gone blind")

	return schema
}

// The patterns the reader uses.
var (
	lineComment = regexp.MustCompile(`--[^\n]*`)
	stringLit   = regexp.MustCompile(`'[^']*'`)
	createTable = regexp.MustCompile(
		`(?is)^\s*create\s+table\s+(?:if\s+not\s+exists\s+)?([a-z0-9_]+)\s*\((.*)\)\s*$`)
	alterTable = regexp.MustCompile(`(?is)^\s*alter\s+table\s+(?:if\s+exists\s+)?([a-z0-9_]+)\s`)
	addColumn  = regexp.MustCompile(`(?is)\badd\s+column\s+(?:if\s+not\s+exists\s+)?([a-z0-9_]+)`)
	dropColumn = regexp.MustCompile(`(?is)\bdrop\s+column\s+(?:if\s+exists\s+)?([a-z0-9_]+)`)
	dropTable  = regexp.MustCompile(`(?is)^\s*drop\s+table\s+(?:if\s+exists\s+)?([a-z0-9_]+)`)
)

// constraintWords open a table constraint rather than a column.
var constraintWords = map[string]bool{
	"constraint": true, "primary": true, "unique": true, "foreign": true, "check": true, "exclude": true,
}

// Replay replays CREATE TABLE, ALTER TABLE ADD and DROP COLUMN and DROP TABLE
// in order, and returns the tables and columns they leave. A column is the
// first word of a top-level item of a CREATE TABLE body or the name after an
// ADD COLUMN, however many one statement carries.
func Replay(sql string) map[string]map[string]bool {
	sql = lineComment.ReplaceAllString(sql, " ")
	sql = stringLit.ReplaceAllString(sql, "''")

	schema := map[string]map[string]bool{}
	for _, statement := range splitTopLevel(sql, ';') {
		switch {
		case createTable.MatchString(statement):
			match := createTable.FindStringSubmatch(statement)
			columns := map[string]bool{}
			for _, item := range splitTopLevel(match[2], ',') {
				fields := strings.Fields(item)
				if len(fields) == 0 || constraintWords[strings.ToLower(fields[0])] {
					continue
				}
				columns[fields[0]] = true
			}
			schema[match[1]] = columns
		case alterTable.MatchString(statement):
			table := alterTable.FindStringSubmatch(statement)[1]
			for _, match := range addColumn.FindAllStringSubmatch(statement, -1) {
				if schema[table] == nil {
					schema[table] = map[string]bool{}
				}
				schema[table][match[1]] = true
			}
			for _, match := range dropColumn.FindAllStringSubmatch(statement, -1) {
				delete(schema[table], match[1])
			}
		case dropTable.MatchString(statement):
			delete(schema, dropTable.FindStringSubmatch(statement)[1])
		}
	}

	return schema
}

// splitTopLevel cuts text on sep where it is not inside parentheses.
func splitTopLevel(text string, sep rune) []string {
	var (
		out   []string
		depth int
		start int
	)
	for i, r := range text {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case sep:
			if depth == 0 {
				out = append(out, text[start:i])
				start = i + 1
			}
		}
	}

	return append(out, text[start:])
}
