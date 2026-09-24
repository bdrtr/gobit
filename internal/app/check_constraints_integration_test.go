//go:build integration

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
)

// A CHECK constraint answers true or false (ADR 0169).
//
// PostgreSQL passes a row whose CHECK expression is NULL, not only one whose
// expression is TRUE. A disjunct that compares a nullable column before it
// tests the column for NULL turns "refuse" into "unknown", and so does a
// function that answers NULL for a value the column can hold — array_length of
// an empty array. ADR 0168's first draft admitted three shapes it was written
// to refuse the first way, with the service's own validation hiding it from
// every unit test, and the promotion rules' constraint admitted an empty array
// the second way for as long as it existed.

// answersNullByIntent are the constraints whose NULL answer is the point, each
// with the reason. An entry that stops being reachable fails the test, so the
// list cannot outlive what it excuses.
var answersNullByIntent = map[string]string{
	"workflow_executions.workflow_executions_idempotency_key_not_blank": "NULL is an execution " +
		"with no key; the constraint only rules out the empty string, and its migration says so",
}

// checkConstraint is one CHECK read back from the catalog.
type checkConstraint struct {
	table, name, expr string
	columns           []checkColumn
}

// checkColumn is a column the constraint reads.
type checkColumn struct {
	name, typ, category string
	nullable            bool
}

// TestEveryCheckConstraintAnswersTrueOrFalse applies every migration in the
// repository and evaluates each CHECK over every combination of NULL (where the
// column admits it), the literals the constraint names, and a few values of the
// column's type. A combination for which the expression is NULL is a row the
// constraint cannot refuse.
//
// The literals are what make it bite: `type IN ('sale', 'override')` is FALSE
// for an arbitrary string, and FALSE absorbs a NULL beside it in an AND, so
// only the value the constraint names exposes the NULL next to it.
//
// The population is every migrations directory in the tree rather than the
// sources this binary migrates at startup: two catalog plugins that ship tables
// will not install without credentials, and the contrib modules are separate
// Go modules nothing here can import. Their SQL is read as files.
func TestEveryCheckConstraintAnswersTrueOrFalse(t *testing.T) {
	ctx := t.Context()
	dsn := migrateDSN(t)

	dirs := migrationDirectories(t)
	for _, dir := range dirs {
		require.NoError(t, db.Migrate(ctx, dsn, os.DirFS(dir), migrationOwner(dir)), dir)
	}

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	// A domain's CHECK has no table and no columns to vary; none exists today,
	// and one that arrives is refused here rather than skipped.
	var domainChecks int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint
WHERE contype = 'c' AND conrelid = 0 AND connamespace = current_schema()::regnamespace`).Scan(&domainChecks))
	require.Zero(t, domainChecks, "a CHECK on a domain is outside what this test can evaluate")

	constraints := readCheckConstraints(ctx, t, pool)
	require.NotEmpty(t, constraints, "a schema with no CHECK constraint proves nothing here")
	t.Logf("%d migrations directories, %d CHECK constraints", len(dirs), len(constraints))

	reached := map[string]bool{}
	for i := range constraints {
		c := &constraints[i]
		key := c.table + "." + c.name
		row, ok := nullRow(ctx, t, pool, c)
		if !ok {
			continue
		}
		if _, excused := answersNullByIntent[key]; excused {
			reached[key] = true
			continue
		}
		assert.Failf(t, "a CHECK constraint answers NULL",
			"%s on %s is NULL for %s, so the row passes; test each column IS NOT NULL "+
				"before comparing it.\n  %s", c.name, c.table, row, c.expr)
	}

	for key := range answersNullByIntent {
		assert.True(t, reached[key],
			"%s is excused for answering NULL and no longer does (or no longer exists); drop the entry", key)
	}
}

// migrationDirectories is every directory named migrations that holds an up
// migration, outside test data, in the order startup applies them: the core's
// schemas first, then the modules, then what plugs into them.
func migrationDirectories(t *testing.T) []string {
	t.Helper()

	const root = "../.."
	rank := func(dir string) int {
		rel, _ := filepath.Rel(root, dir)
		for i, prefix := range []string{"core/", "internal/core/", "internal/modules/", "plugins/", "contrib/"} {
			if strings.HasPrefix(filepath.ToSlash(rel), prefix) {
				return i
			}
		}
		return 99
	}

	var dirs []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(path, ".up.sql") && filepath.Base(filepath.Dir(path)) == "migrations" {
			if dir := filepath.Dir(path); !slices.Contains(dirs, dir) {
				dirs = append(dirs, dir)
			}
		}
		return nil
	})
	require.NoError(t, err)

	owners := map[string]string{}
	for _, dir := range dirs {
		require.Less(t, rank(dir), 99, "%s is a migrations directory outside every known root", dir)
		owner := migrationOwner(dir)
		require.NotContains(t, owners, owner, "%s and %s share an owner name", dir, owners[owner])
		owners[owner] = dir
	}
	slices.SortStableFunc(dirs, func(a, b string) int { return rank(a) - rank(b) })

	return dirs
}

// migrationOwner is the ledger name a migrations directory is applied under:
// the name of the directory that holds it, in the characters an owner allows.
func migrationOwner(dir string) string {
	return strings.ReplaceAll(filepath.Base(filepath.Dir(dir)), "-", "_")
}

// readCheckConstraints reads every table CHECK with the columns it reads.
func readCheckConstraints(ctx context.Context, t *testing.T, pool *pgxpool.Pool) []checkConstraint {
	t.Helper()

	rows, err := pool.Query(ctx, `
SELECT c.conrelid::regclass::text, c.conname, pg_get_expr(c.conbin, c.conrelid),
       array_agg(a.attname ORDER BY a.attnum),
       array_agg(format_type(a.atttypid, a.atttypmod) ORDER BY a.attnum),
       array_agg(ty.typcategory::text ORDER BY a.attnum),
       array_agg(NOT a.attnotnull ORDER BY a.attnum)
FROM pg_constraint c
JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
JOIN pg_type ty ON ty.oid = a.atttypid
WHERE c.contype = 'c' AND c.conrelid <> 0 AND c.connamespace = current_schema()::regnamespace
GROUP BY c.oid, c.conrelid, c.conname, c.conbin
ORDER BY 1, 2`)
	require.NoError(t, err)
	defer rows.Close()

	var out []checkConstraint
	for rows.Next() {
		var (
			c                  checkConstraint
			names, types, cats []string
			nullable           []bool
		)
		require.NoError(t, rows.Scan(&c.table, &c.name, &c.expr, &names, &types, &cats, &nullable))
		for i := range names {
			c.columns = append(c.columns, checkColumn{
				name: names[i], typ: types[i], category: cats[i], nullable: nullable[i],
			})
		}
		out = append(out, c)
	}
	require.NoError(t, rows.Err())

	return out
}

var (
	quotedLiteral = regexp.MustCompile(`'((?:[^']|'')*)'`)
	bareNumber    = regexp.MustCompile(`(?:^|[^\w.'])(-?\d+(?:\.\d+)?)`)
)

// nullRow evaluates the constraint over its domain and returns one
// combination for which it is NULL.
func nullRow(ctx context.Context, t *testing.T, pool *pgxpool.Pool, c *checkConstraint) (row string, found bool) {
	t.Helper()

	literals := []string{}
	for _, m := range quotedLiteral.FindAllStringSubmatch(c.expr, -1) {
		literals = append(literals, strings.ReplaceAll(m[1], "''", "'"))
	}
	for _, m := range bareNumber.FindAllStringSubmatch(c.expr, -1) {
		literals = append(literals, m[1])
	}

	from := make([]string, 0, len(c.columns))
	selected := make([]string, 0, len(c.columns))
	for i, col := range c.columns {
		values := columnDomain(ctx, t, pool, col, literals)
		ident := pgx.Identifier{col.name}.Sanitize()
		from = append(from, fmt.Sprintf("(VALUES %s) AS d%d(%s)", strings.Join(values, ", "), i, ident))
		selected = append(selected, fmt.Sprintf("%s || '=' || coalesce(%s::text, 'NULL')",
			quoteLiteral(col.name), ident))
	}

	query := fmt.Sprintf("SELECT concat_ws(', ', %s) FROM %s WHERE (%s) IS NULL LIMIT 1",
		strings.Join(selected, ", "), strings.Join(from, " CROSS JOIN "), c.expr)

	err := pool.QueryRow(ctx, query).Scan(&row)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false
	}
	require.NoError(t, err, "evaluating %s on %s:\n%s", c.name, c.table, query)

	return row, true
}

// columnDomain is NULL when the column admits it, every literal of the
// constraint that casts to the column's type, and a few values of the type.
func columnDomain(ctx context.Context, t *testing.T, pool *pgxpool.Pool, col checkColumn, literals []string) []string {
	t.Helper()

	candidates := slices.Clone(literals)
	switch col.category {
	case "S":
		candidates = append(candidates, "", "x", " ")
	case "N":
		candidates = append(candidates, "0", "1", "-1")
	case "B":
		candidates = append(candidates, "true", "false")
	case "D":
		candidates = append(candidates, "2026-01-01 00:00:00+00", "2026-01-02 00:00:00+00")
	case "A":
		// An array takes the constraint's literals as its elements: a CHECK
		// on an array names the elements it allows.
		candidates = []string{"{}", "{x}"}
		for _, literal := range literals {
			candidates = append(candidates, `{"`+strings.ReplaceAll(literal, `"`, `\"`)+`"}`)
		}
	case "U":
		candidates = append(candidates, "{}", "[]", "null", `"x"`, "1")
	}
	slices.Sort(candidates)
	candidates = slices.Compact(candidates)

	values := []string{}
	if col.nullable {
		values = append(values, "(NULL::"+col.typ+")")
	}
	for _, v := range candidates {
		value := quoteLiteral(v) + "::" + col.typ
		// A literal of another column's type does not cast to this one; the
		// failed cast is how it is sorted out.
		if _, err := pool.Exec(ctx, "SELECT "+value); err != nil {
			continue
		}
		values = append(values, "("+value+")")
	}
	require.NotEmpty(t, values, "%s (%s) has no value to try", col.name, col.typ)

	return values
}

// quoteLiteral quotes a string as an SQL literal.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
