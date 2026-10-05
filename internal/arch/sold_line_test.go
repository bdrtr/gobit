package arch_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// soldLineTables are the tables that hold what an order sold: its lines, their
// taxes and its shipping methods. No query rewrites or removes a row of them.
var soldLineTables = []string{"order_line_items", "order_line_taxes", "order_shipping_methods"}

var (
	// sqlWhitespace is collapsed to one space, so a statement is read the same
	// however it is laid out over lines.
	sqlWhitespace = regexp.MustCompile(`\s+`)
	// sqlNamedQuery is the sqlc annotation that opens every statement of a
	// query file.
	sqlNamedQuery = regexp.MustCompile(`(?m)^-- name:`)

	// rewritesSoldLine is a statement that changes or removes a sold line's
	// rows, under any of the spellings PostgreSQL accepts.
	rewritesSoldLine = regexp.MustCompile(
		`\b(?:update(?: only)?|delete from(?: only)?|merge into|truncate(?: table)?(?: only)?) ` +
			`(?:order_line_items|order_line_taxes|order_shipping_methods)\b`)
	// removesAnOrder is a statement that removes an order, which removes its
	// lines and their taxes with it through ON DELETE CASCADE.
	removesAnOrder = regexp.MustCompile(`\b(?:delete from(?: only)?|truncate(?: table)?(?: only)?) orders\b`)
	// upsertsASale is an insert that rewrites the row it meets. An insert
	// that does nothing on a conflict rewrites nothing and is not one.
	upsertsASale = regexp.MustCompile(
		`\binsert into (?:order_line_items|order_line_taxes|order_shipping_methods|orders|order_exchanges)\b` +
			`.*\bon conflict\b.*\bdo update\b`)

	// updatesOrders and updatesExchanges are an UPDATE or a MERGE of the
	// table, aliased or not; the whole statement is then read, not a clause cut
	// out of it.
	updatesOrders    = regexp.MustCompile(`\b(?:update|merge into)(?: only)? orders\b`)
	updatesExchanges = regexp.MustCompile(`\b(?:update|merge into)(?: only)? order_exchanges\b`)

	// orderAmountAssigned is an assignment to one of the order's sold amounts;
	// the separator before the name keeps paid_total and the like out of it.
	orderAmountAssigned = regexp.MustCompile(
		`(?:^|[ ,])(?:subtotal|discount_total|tax_total|shipping_total|total) ?=`)
	orderAmountName = regexp.MustCompile(`\b(?:subtotal|discount_total|tax_total|shipping_total|total)\b`)
	// differenceAssigned is an assignment to an exchange's difference.
	differenceAssigned = regexp.MustCompile(`(?:^|[ ,])difference_due ?=`)
	differenceName     = regexp.MustCompile(`\bdifference_due\b`)
	// rowAssigned is the column list of SET (a, b) = (...), which assigns
	// every column it names.
	rowAssigned = regexp.MustCompile(`\bset ?\(([^)]*)\) ?=`)
)

// assigns reports whether a statement assigns a column the two patterns name,
// as SET name = or as one of the columns of SET (...) =.
func assigns(statement string, assigned, name *regexp.Regexp) bool {
	if assigned.MatchString(statement) {
		return true
	}
	for _, columns := range rowAssigned.FindAllStringSubmatch(statement, -1) {
		if name.MatchString(columns[1]) {
			return true
		}
	}

	return false
}

// orderStatements reads the order module's query files into their statements:
// comments gone, lower case, whitespace collapsed and identifiers unquoted.
// Each file has to split into as many statements as it names, or the reading
// is cut somewhere this audit cannot see.
func orderStatements(t *testing.T) []string {
	t.Helper()

	dir := filepath.Join(repoRoot, modulesDir, "order", "queries")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "the order module's queries could not be listed")

	var statements []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
		require.NoError(t, readErr, "%s could not be read", entry.Name())

		// The comments go first: they name the tables and the columns in prose.
		text := sqlLineComment.ReplaceAllString(string(raw), "")
		text = strings.ReplaceAll(strings.ToLower(text), `"`, "")
		text = sqlWhitespace.ReplaceAllString(text, " ")

		var inFile []string
		for _, statement := range strings.Split(text, ";") {
			if statement = strings.TrimSpace(statement); statement != "" {
				inFile = append(inFile, statement)
			}
		}
		// Counts rather than require.Len, so a failure prints the file's name
		// and not every statement in it.
		require.Equal(t, len(sqlNamedQuery.FindAllStringIndex(string(raw), -1)), len(inFile),
			"%s did not split into one statement per named query; the audit is BLIND to some",
			entry.Name())
		statements = append(statements, inFile...)
	}

	return statements
}

// TestASoldLineIsNotRewrittenInSQL holds ADR 0394 in the order module's
// queries: no statement rewrites a sold line, its taxes, its shipping method,
// an order's amounts or an exchange's difference, and none removes an order.
// It speaks for the queries alone; an exchange that sends the line's own
// variant at a difference raises a sold line with none of these, and nothing
// refuses it (ADR 0394).
//
// It reads the query files as TEXT, as the stock ledger's gate does: the only
// way to reach these tables from Go is a named query in that directory. Every
// statement that updates orders or an exchange is read whole, so a subquery or
// an alias cannot hide an assignment. The floor is that every table it audits
// is found at all, since a scan that finds no statement approves every one of
// them.
func TestASoldLineIsNotRewrittenInSQL(t *testing.T) {
	t.Parallel()

	statements := orderStatements(t)
	text := strings.Join(statements, ";\n")

	for _, floor := range append([]string{"orders", "order_exchanges"}, soldLineTables...) {
		require.True(t, regexp.MustCompile(`\b`+floor+`\b`).MatchString(text),
			"no order query names %q, so this audit read nothing of it.\n"+
				"The queries moved or the table was renamed; the audit has to follow it.", floor)
	}

	var orderUpdates, exchangeUpdates int
	for _, statement := range statements {
		// Booleans rather than assert.NotRegexp, so a failure prints the
		// statement and not the module's whole query directory.
		assert.False(t, rewritesSoldLine.MatchString(statement),
			"an order query rewrites a sold line: %q.\n"+
				"A sold line, its taxes and its shipping method are what the buyer agreed "+
				"to pay (ADR 0394): a price charged too low is bought again at checkout, "+
				"not rewritten.", statement)
		assert.False(t, removesAnOrder.MatchString(statement),
			"an order query removes an order: %q.\n"+
				"An order retires by status and is never deleted (000010), and removing it "+
				"takes its sold lines with it (ADR 0394).", statement)
		assert.False(t, upsertsASale.MatchString(statement),
			"an order query upserts a sold line, an order or an exchange: %q.\n"+
				"ON CONFLICT DO UPDATE rewrites the row it meets, which is what was sold "+
				"(ADR 0394); DO NOTHING is the idempotent insert.", statement)

		if updatesOrders.MatchString(statement) {
			orderUpdates++
			assert.False(t, assigns(statement, orderAmountAssigned, orderAmountName),
				"an UPDATE of orders sets an amount: %q.\n"+
					"An order's amounts are what it sold (ADR 0394); a credit lowers what is "+
					"owed beside them (ADR 0105) and nothing raises them.", statement)
		}
		if updatesExchanges.MatchString(statement) {
			exchangeUpdates++
			assert.False(t, assigns(statement, differenceAssigned, differenceName),
				"an UPDATE of order_exchanges sets difference_due: %q.\n"+
					"The difference is fixed when the exchange is opened; rewritten afterwards "+
					"it would charge for goods nobody named (ADR 0394).", statement)
		}
	}

	// The statements are found by pattern, and a pattern that finds nothing
	// passes every statement it never read.
	require.Positive(t, orderUpdates, "no statement read as an UPDATE of orders; the audit is BLIND to them")
	require.Positive(t, exchangeUpdates,
		"no statement read as an UPDATE of order_exchanges; the audit is BLIND to them")
}
