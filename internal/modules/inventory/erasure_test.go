// This file is English because ADR 0012 makes language a property of the FILE
// and every new file is English; module.go next to it stays Turkish.
//
// These tests need NO database and carry no build tag. A declaration is a
// property of the code rather than of the data — the same sentence on an empty
// installation and a full one — which is why [personaldata.Declarer] takes no
// context and returns no error, and why the audit of it has to be runnable by
// somebody who has the repository and nothing else.
package inventory_test

import (
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/inventory"
)

// notPersonalColumns are the columns that hold nothing about a person, table by
// table, as the schema stands AFTER every migration has run.
//
// EVERY table the module creates has an entry, including inventory_levels which
// the declaration does not touch at all. That is the load-bearing half of
// [TestTheDeclarationCoversEveryPersonalColumn]: everything not named here has
// to appear in the declaration, so a column added tomorrow fails this test until
// somebody decides which side of the line it is on, and a whole new table fails
// it until somebody has read the table. A rule that guessed instead would wave a
// "collected_by" column through on the grounds that it looked technical.
//
// The reasons, per table:
//
//   - The identifiers are exempt everywhere. A synthetic key names a row, not a
//     person; declaring one would drag every foreign id in the deployment in
//     behind it. inventory_reservations.line_item_id is the one worth arguing:
//     it is the module's ONLY tie to a shopper, it points at a cart line that
//     lives in another module, and once the columns beside that line are
//     anonymized it resolves to nobody.
//   - inventory_levels is exempt WHOLE. It is two ids and two counts — how many
//     units of a thing sit at a place — and it carries no text at all, so there
//     is nowhere for a person to be in it.
//   - inventory_items.sku is a goods code. It is required and unique among
//     living items, which is to say gobit uses it to tell one product from
//     another, not one person from another; title and description, the two free
//     text fields beside it, ARE declared.
//   - requires_shipping, quantity and status are a boolean, a count and a
//     CHECK-constrained enum: a sentence about somebody cannot land in any of
//     them.
//   - The timestamps describe the record's state and are true of the row whether
//     or not anybody is behind it.
var notPersonalColumns = map[string][]string{
	"stock_locations": {
		"id", "created_at", "updated_at", "closed_at",
	},
	"inventory_items": {
		"id", "sku", "requires_shipping", "created_at", "updated_at", "deleted_at",
	},
	"inventory_levels": {
		"id", "inventory_item_id", "location_id", "stocked_quantity",
		"reserved_quantity", "created_at", "updated_at", "deleted_at",
	},
	"inventory_reservations": {
		"id", "inventory_item_id", "location_id", "quantity", "line_item_id",
		"status", "created_at", "updated_at",
	},
}

// TestTheDeclarationCoversEveryPersonalColumn proves the declaration EXHAUSTIVE
// against the schema it describes, in both directions.
//
// A declaration is the only answer an embedder has to "where in your database is
// this person", and it is what the embedder publishes as its privacy notice. A
// missing row therefore does not make the list shorter, it makes it FALSE, and
// the column it forgot is one no audit will ever visit. The opposite direction
// matters as much: a holding naming a column that is not in the schema sends an
// auditor looking for data that does not exist.
func TestTheDeclarationCoversEveryPersonalColumn(t *testing.T) {
	t.Parallel()

	declared := map[string]bool{}
	for _, holding := range inventory.New().PersonalData().Holdings {
		key := holding.Table + "." + holding.Column
		require.False(t, declared[key], "%s is declared twice", key)
		declared[key] = true
	}

	schema := currentSchema(t)

	for table, columns := range schema {
		exempt, judged := notPersonalColumns[table]
		require.True(t, judged,
			"the migrations create %s and nobody has said whether it holds personal "+
				"data. Add it to notPersonalColumns — every column of it if it holds "+
				"nothing, the rest to Module.PersonalData.", table)

		for _, column := range columns {
			key := table + "." + column
			if slices.Contains(exempt, column) {
				assert.False(t, declared[key],
					"%s is declared as personal data and is also listed as holding "+
						"nothing; the two lists disagree about the same column", key)
				continue
			}

			assert.True(t, declared[key],
				"%s is in the migrations and NOT in the declaration.\n"+
					"Either it holds something about a person — then it belongs in "+
					"Module.PersonalData with a Why an embedder can repeat to that "+
					"person — or it does not, and it belongs in notPersonalColumns "+
					"with the reason written down.", key)
			delete(declared, key)
		}

		for _, column := range exempt {
			assert.Contains(t, columns, column,
				"%s.%s is exempted from the declaration and is not in the schema; an "+
					"exemption for a column that no longer exists is a judgement "+
					"nobody has to make again", table, column)
		}
	}

	assert.Empty(t, declared,
		"these holdings name columns the migrations do not create; a declaration "+
			"that points at a column nobody can find sends an auditor searching for "+
			"data that is not there")
}

// TestTheDeclarationLeavesTheHolderToTheCoordinator pins an EMPTY field.
//
// The coordinator overwrites Holder with the name the module is registered
// under, so a name hard-coded here would be dead the moment it disagreed with
// the registry — and it would disagree silently, because both halves look
// entirely normal on their own. The empty value is the assertion that this
// module is not the one that decides what it is called.
func TestTheDeclarationLeavesTheHolderToTheCoordinator(t *testing.T) {
	t.Parallel()

	assert.Empty(t, inventory.New().PersonalData().Holder,
		"the holder comes from the registry; setting it here creates a second "+
			"name that can drift from the one the report prints")
}

// TestEveryHoldingCanBeReadOffOneReport checks the shape of the strings rather
// than their content.
//
// One report puts these sentences side by side with the other modules', which is
// what makes the shape a contract and not a style preference (ADR 0033): a Why
// that starts with a capital or wraps onto a second line reads as a different
// document inside the same list. The Kind matters for the same reason — it is
// what tells the reader whether gobit knows what is in the column or the
// embedder does, and a holding without one says neither.
func TestEveryHoldingCanBeReadOffOneReport(t *testing.T) {
	t.Parallel()

	for _, holding := range inventory.New().PersonalData().Holdings {
		key := holding.Table + "." + holding.Column

		assert.Contains(t, []personaldata.Kind{personaldata.Named, personaldata.Open}, holding.Kind,
			"%s: a holding with no kind says nothing about who wrote the column", key)

		require.NotEmpty(t, holding.Why,
			"%s: a column named without a reason cannot be repeated to a data subject", key)
		assert.Equal(t, strings.TrimSpace(holding.Why), holding.Why,
			"%s: the Why is padded and lands in a list of other modules' sentences", key)
		assert.NotContains(t, holding.Why, "\n",
			"%s: the Why is one clause, not a paragraph", key)
		first, _ := utf8.DecodeRuneInString(holding.Why)
		assert.True(t, unicode.IsLower(first),
			"%s: the Why starts with a capital; these clauses sit side by side in one "+
				"report and one of them shouting is how a reader tells them apart", key)
	}
}

// TestTheDeclarationDoesNotNeedRegister holds the property [personaldata.Declarer]
// states in its own words.
//
// A declaration is a property of the code, so an audit reads it with no database
// and a module whose Register failed must still be able to say what it holds.
// The module built here has no pool, no service and no routes.
func TestTheDeclarationDoesNotNeedRegister(t *testing.T) {
	t.Parallel()

	assert.NotEmpty(t, inventory.New().PersonalData().Holdings)
}

// TestTheSchemaScannerFollowedEveryMigration is the audit auditing itself.
//
// The scanner reads CREATE TABLE blocks, and a scanner that stopped there would
// be describing the schema of 000001 while the module ships two more: 000002
// DROPS inventory_reservations.deleted_at, and 000003 replaces
// stock_locations.deleted_at with closed_at (ADR 0055). This module has already
// paid for exactly that mistake once: the audit that was supposed to catch the
// never-written column matched writes by bare column name and could not see it
// for as long as another table wrote a column of the same name (the reasoning
// is in the head of 000002). So the facts the scanner has to get right are
// asserted directly rather than left to be implied by the tests above passing.
//
// Each drop is paired with a column that SURVIVES it, because "the schema has
// this column" and "the scanner reads this table at all" fail the same way
// otherwise: a table it stopped reading contains nothing, and every NotContains
// about it passes.
func TestTheSchemaScannerFollowedEveryMigration(t *testing.T) {
	t.Parallel()

	schema := currentSchema(t)

	assert.NotContains(t, schema["inventory_reservations"], "deleted_at",
		"000002 drops this column; a scanner that still sees it is auditing a "+
			"schema the module does not ship")
	assert.NotContains(t, schema["stock_locations"], "deleted_at",
		"000003 drops this column; a scanner that still sees it is auditing a "+
			"schema the module does not ship")
	assert.Contains(t, schema["stock_locations"], "closed_at",
		"000003 adds the column that replaces it; without it the drop above reads "+
			"as a table the scanner stopped following")
	assert.Contains(t, schema["inventory_items"], "deleted_at",
		"the drops applied to the wrong table, or to all of them")
	assert.Contains(t, schema["inventory_reservations"], "description",
		"the reservation columns stopped being read at all, which would make every "+
			"assertion about them vacuously true")
}

// The statement forms the scanner understands. Anything else in a migration
// makes it fail loudly instead of quietly describing a schema that has moved.
var (
	createTableRE = regexp.MustCompile(`(?m)^CREATE TABLE (?:IF NOT EXISTS )?([a-z_]+) \(`)
	alterTableRE  = regexp.MustCompile(`(?m)^ALTER TABLE ([a-z_]+) ([^;]+);`)
	dropColumnRE  = regexp.MustCompile(`^DROP COLUMN (?:IF EXISTS )?([a-z_]+)$`)
	addColumnRE   = regexp.MustCompile(`^ADD COLUMN (?:IF NOT EXISTS )?([a-z_]+)\b`)
)

// currentSchema returns the module's tables and their columns as they stand
// after every up migration has been applied, in file order.
//
// It reads through the same embedded file system the migrator uses, so this
// audit cannot pass against a file the module does not actually ship. The parser
// is deliberately small — this repository writes one column per line — and it is
// built to fail rather than to shrug: a table whose block it cannot read yields
// no columns and trips the caller's assertions, and an ALTER TABLE in a form it
// does not know stops the test with a message saying so.
func currentSchema(t *testing.T) map[string][]string {
	t.Helper()

	migrations := inventory.New().Migrations()

	files, err := fs.Glob(migrations, "*.up.sql")
	require.NoError(t, err)
	require.NotEmpty(t, files, "the module ships no up migration; the scanner has gone blind")
	slices.Sort(files)

	schema := map[string][]string{}

	for _, name := range files {
		raw, err := fs.ReadFile(migrations, name)
		require.NoError(t, err)
		sql := string(raw)

		for _, match := range createTableRE.FindAllStringSubmatchIndex(sql, -1) {
			table := sql[match[2]:match[3]]
			require.NotContains(t, schema, table, "%s: %s is created twice", name, table)
			schema[table] = columnsOfBlock(t, sql[match[1]:])
			require.NotEmpty(t, schema[table], "%s: no column was read out of %s", name, table)
		}

		for _, match := range alterTableRE.FindAllStringSubmatch(sql, -1) {
			table, action := match[1], strings.TrimSpace(match[2])
			require.Contains(t, schema, table, "%s: %s is altered before it is created", name, table)

			switch {
			case dropColumnRE.MatchString(action):
				column := dropColumnRE.FindStringSubmatch(action)[1]
				schema[table] = slices.DeleteFunc(schema[table],
					func(existing string) bool { return existing == column })
			case addColumnRE.MatchString(action):
				schema[table] = append(schema[table], addColumnRE.FindStringSubmatch(action)[1])
			case strings.HasPrefix(action, "ADD CONSTRAINT"),
				strings.HasPrefix(action, "DROP CONSTRAINT"):
				// A constraint changes what may be written, not where a person is.
			default:
				require.Fail(t, "the schema scanner cannot follow this migration",
					"%s: %q is a statement this audit does not understand, so the "+
						"columns it is about to check are not the columns the module "+
						"ships. Teach the scanner before trusting the result.", name, action)
			}
		}
	}

	return schema
}

// columnsOfBlock returns the column names of one CREATE TABLE body.
//
// It reads from the opening parenthesis to the closing one, drops comment and
// constraint lines, and takes the first word of what is left.
func columnsOfBlock(t *testing.T, body string) []string {
	t.Helper()

	if end := strings.Index(body, "\n);"); end >= 0 {
		body = body[:end]
	}

	var columns []string
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || strings.HasPrefix(fields[0], "--") {
			continue
		}
		switch fields[0] {
		case "CONSTRAINT", "PRIMARY", "UNIQUE", "FOREIGN", "CHECK":
			continue
		}
		columns = append(columns, strings.TrimSuffix(fields[0], ","))
	}

	return columns
}
