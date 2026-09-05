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

// unwrittenColumns are the columns nothing writes, with the REASON.
//
// The key is "<module>.<table>.<column>". As long as a column is not written
// here with a reason, one nothing writes is a bug: it is a field that is always
// its zero value, on a row every reader pays to filter by.
//
// The ten entries below are ONE finding stated ten times, and it is a real one:
// the order and payment modules never soft-delete anything. Every read in both
// carries "deleted_at IS NULL" — a predicate that has never once been false.
// Removing the column is a schema decision and taking the deletes on is a
// product one; recording it here is what keeps it from being rediscovered.
var unwrittenColumns = map[string]string{
	"order.orders.deleted_at": "the order module never soft-deletes; nothing sets this and " +
		"every read filters on it",
	"order.order_line_items.deleted_at":   "as orders.deleted_at",
	"order.order_returns.deleted_at":      "as orders.deleted_at",
	"order.order_return_items.deleted_at": "as orders.deleted_at",
	"order.order_claims.deleted_at":       "as orders.deleted_at",
	"order.order_exchanges.deleted_at":    "as orders.deleted_at",
	"payment.payment_collections.deleted_at": "the payment module never soft-deletes; money " +
		"records are kept, and a refund is a row rather than a deletion",
	"payment.payment_sessions.deleted_at": "as payment_collections.deleted_at",
	"payment.payments.deleted_at":         "as payment_collections.deleted_at",
	"payment.refunds.deleted_at":          "as payment_collections.deleted_at",
}

// The shapes read out of the SQL.
var (
	createTablePattern = regexp.MustCompile(`(?s)CREATE TABLE (?:IF NOT EXISTS )?(\w+)\s*\((.*?)\n\);`)
	columnPattern      = regexp.MustCompile(`(?i)^([a-z_][a-z0-9_]*)\s+` +
		`(TEXT|BIGINT|INTEGER|BOOLEAN|JSONB|TIMESTAMPTZ|NUMERIC|SMALLINT|BYTEA)\b(.*)$`)
	insertPattern = regexp.MustCompile(`(?is)INSERT\s+INTO\s+\w+\s*\(([^)]*)\)`)
	setPattern    = regexp.MustCompile(`(?is)\bSET\b(.*?)(?:\bWHERE\b|\bRETURNING\b|;)`)
	assignPattern = regexp.MustCompile(`([a-z_][a-z0-9_]*)\s*=`)
	// suppliedPattern marks a column whose value the DATABASE produces.
	suppliedPattern = regexp.MustCompile(`(?i)\b(DEFAULT|GENERATED)\b`)
	// bareName is a column name with nothing else on the line.
	bareName = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
)

// TestEveryColumnIsWrittenBySomething catches a column that exists and means
// nothing.
//
// # What the defect looks like
//
// A column is added, the model gains a field, the reader maps it back — and no
// INSERT or UPDATE ever names it. Everything compiles, every test passes, and
// the field is its zero value forever. It has happened here more than once:
// order_exchanges keeps completed_at and canceled_at and nothing writes either,
// so an exchange can never be recorded as finished (gaps.md D4).
//
// Nothing else catches it. The compiler sees a field that is read. The
// repository tests read back what they wrote and never notice the column they
// never wrote. Only the schema and the queries TOGETHER tell the story, which
// is why this audit reads both.
//
// # What counts as written
//
// A column named in an INSERT column list, or on the left of an assignment in
// an UPDATE's SET. A column the DATABASE supplies — DEFAULT or GENERATED — is
// out of scope: its value is the schema's answer, not a missing write.
//
// Migrations are read for their INSERTs too: a seed table (the currency list)
// is filled once by a migration and never by a query, and calling that dead
// would be the audit misreading the one legitimate case.
func TestEveryColumnIsWrittenBySomething(t *testing.T) {
	t.Parallel()

	modules := moduleNames(t)
	require.NotEmpty(t, modules)

	scanned, used := 0, map[string]bool{}
	for _, module := range modules {
		schema := readSQL(t, filepath.Join(repoRoot, modulesDir, module, migrationsDirName), ".up.sql")
		if schema == "" {
			continue
		}
		queries := readSQL(t, filepath.Join(repoRoot, modulesDir, module, "queries"), ".sql")
		written := writtenColumns(schema + "\n" + queries)

		for _, table := range createTablePattern.FindAllStringSubmatch(schema, -1) {
			for _, column := range declaredColumns(table[2]) {
				scanned++
				if written[column] {
					continue
				}

				key := module + "." + table[1] + "." + column
				if reason, exempt := unwrittenColumns[key]; exempt {
					used[key] = true
					require.NotEmpty(t, reason, "%s is exempt with no reason", key)

					continue
				}

				t.Errorf("%s is never written: no INSERT names it and no UPDATE sets it.\n"+
					"The column is its zero value on every row, and every read that filters "+
					"on it filters on nothing. Either write it, drop it, or — if the "+
					"database is meant to supply it — give it a DEFAULT. If it is "+
					"deliberately unwritten, record it in unwrittenColumns WITH ITS REASON.",
					key)
			}
		}
	}

	require.Positive(t, scanned,
		"no column was scanned; the audit has gone blind.\n"+
			"The migrations moved, or CREATE TABLE is no longer written the way the "+
			"pattern expects — and a scan that reads no column approves every dead one.")

	for _, key := range sortedKeys(unwrittenColumns) {
		assert.True(t, used[key],
			"%s is exempt in unwrittenColumns but is NOT unwritten any more.\n"+
				"Either the column is written now, or it is gone. Delete the entry — a dead "+
				"exemption covers up the next real one.", key)
	}
}

// declaredColumns returns the columns of a CREATE TABLE body that the database
// does not supply itself.
func declaredColumns(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSuffix(strings.TrimSpace(line), ",")
		// Three groups plus the whole match: a shorter result would mean the
		// pattern changed and match[3] would panic. Checking the length rather
		// than nil says which of the two failed.
		match := columnPattern.FindStringSubmatch(line)
		if len(match) < 4 || suppliedPattern.MatchString(match[3]) {
			continue
		}
		out = append(out, match[1])
	}

	return out
}

// writtenColumns returns every column an INSERT names or an UPDATE assigns.
func writtenColumns(sql string) map[string]bool {
	out := map[string]bool{}

	for _, insert := range insertPattern.FindAllStringSubmatch(sql, -1) {
		for _, name := range strings.Split(insert[1], ",") {
			name = strings.TrimSpace(name)
			if bareName.MatchString(name) {
				out[name] = true
			}
		}
	}
	for _, set := range setPattern.FindAllStringSubmatch(sql, -1) {
		for _, assignment := range assignPattern.FindAllStringSubmatch(set[1], -1) {
			out[assignment[1]] = true
		}
	}

	return out
}

// readSQL concatenates the SQL files of a directory; a missing directory is the
// empty string, because not every module has one.
func readSQL(t *testing.T, dir, suffix string) string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	var builder strings.Builder
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		content, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
		require.NoError(t, readErr, "%s could not be read", entry.Name())
		builder.Write(content)
		builder.WriteString("\n")
	}

	return builder.String()
}

// sortedKeys returns the map's keys in order, so the errors do not shuffle.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)

	return out
}
