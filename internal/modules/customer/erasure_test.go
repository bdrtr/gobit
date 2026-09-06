package customer_test

import (
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/customer"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// These tests need NO database. A declaration is a property of the code and not
// of the data — the same sentence on an empty database and a full one — which
// is why [personaldata.Declarer] takes no context and returns no error, and why the
// audit of it belongs in a plain unit test that an auditor can run anywhere.

// migrationFile is the module's only migration; the declaration is checked
// against it because that file, not this package, decides what columns exist.
const migrationFile = "000001_customer_init.up.sql"

// notPersonalColumns are the columns that hold nothing about the person, table
// by table.
//
// EVERY table the migration creates has an entry, including the two the
// declaration barely touches. That is not bookkeeping for its own sake: a table
// missing from this map used to be a table nobody checked, so half the schema
// could grow a personal column without a single test noticing. The map is now
// the answer to "which tables were looked at", and
// [TestPersonalDataCoversEveryPersonalColumn] fails if the migration creates a
// table this map does not name.
//
// The list is written down rather than inferred, and it is the load-bearing
// half of that test: everything NOT in it has to appear in the declaration, so
// a column added tomorrow fails until somebody decides which side of the line
// it is on. Deciding is the whole point — a rule that guessed would let a
// "date_of_birth" column past on the grounds that it looked technical.
//
// The reasons, per table:
//
//   - The identifiers are here on purpose, in all four tables. A synthetic key
//     names a row, not a person, and after the row beside it is anonymized it
//     resolves to nobody; that is exactly the fact the anonymization rests on.
//     Declaring one would also drag in every foreign id in the deployment.
//     customer_group_customer is nothing BUT identifiers plus the moment the
//     membership was made, which is why the whole row is exempt: it says that
//     some row belongs to some segment, and once the customer row is anonymous
//     it no longer says that about anybody.
//   - customer_group.name is the segment's own label ("wholesalers"), written
//     by the shop about a category rather than about a customer. Its metadata
//     is NOT exempt and is declared Open, because it is the same free-form
//     jsonb as customer.metadata and gobit refuses to look inside either one.
//   - The timestamps and the flags (has_account, is_default_shipping,
//     is_default_billing) describe the record's state, not the person: they are
//     true of the row whether or not anybody is behind it.
var notPersonalColumns = map[string][]string{
	"customer": {
		"id", "has_account", "created_at", "updated_at", "deleted_at",
	},
	"customer_address": {
		"id", "customer_id", "is_default_shipping", "is_default_billing",
		"created_at", "updated_at", "deleted_at",
	},
	"customer_group": {
		"id", "name", "created_at", "updated_at", "deleted_at",
	},
	"customer_group_customer": {
		"customer_id", "customer_group_id", "created_at",
	},
}

// TestPersonalDataCoversEveryPersonalColumn proves the declaration is
// EXHAUSTIVE against the schema it describes.
//
// A declaration is the only answer an embedder has to "where is this person in
// your database", so a missing row does not make the list shorter — it makes it
// FALSE, and the column it forgot is one no audit will ever look at. Comparing
// the list against the migration is what keeps the two from drifting: a column
// added to the table without a line here fails this test, which is the only
// moment anybody is thinking about that column at all.
func TestPersonalDataCoversEveryPersonalColumn(t *testing.T) {
	declaration := customer.New(nil).PersonalData()
	assert.Equal(t, customer.ModuleName, declaration.Holder,
		"the holder is what a controller reads off the report to know who still holds something")

	declared := map[string]bool{}
	for _, holding := range declaration.Holdings {
		key := holding.Table + "." + holding.Column
		assert.False(t, declared[key], "%s is declared twice", key)
		declared[key] = true

		assert.Contains(t, []personaldata.Kind{personaldata.Named, personaldata.Open}, holding.Kind,
			"%s: a holding with no kind says nothing about who wrote it", key)
		assert.NotEmpty(t, holding.Why,
			"%s: a column named without a reason cannot be repeated to a data subject", key)
	}

	schema := readMigration(t)
	tables := tablesOf(t, schema)
	require.Len(t, tables, len(notPersonalColumns),
		"the audit reads %v; the migration creates %v. A table the map does not name "+
			"is a table nothing checks, and the migration→declaration direction is "+
			"unaudited for every column in it",
		keysOf(notPersonalColumns), tables)

	for _, table := range tables {
		exempt, known := notPersonalColumns[table]
		require.True(t, known,
			"%s is created by the migration and is not in notPersonalColumns; every "+
				"table has to be looked at, even one whose columns all turn out to be "+
				"exempt — that is what the next column added to it will be measured against", table)
		columns := columnsOf(t, schema, table)
		require.NotEmpty(t, columns, "no column was read out of %s; the scanner has gone blind", table)

		for _, column := range columns {
			key := table + "." + column
			if slicesContains(exempt, column) {
				assert.False(t, declared[key],
					"%s is declared as personal data but is listed as holding nothing", key)
				continue
			}
			assert.True(t, declared[key],
				"%s is in the migration and NOT in the declaration.\n"+
					"Either it holds something about the person — then it belongs in "+
					"Module.PersonalData, and Service.Erase has to decide whether it is "+
					"overwritten or reported in Result.Kept — or it does not, and it "+
					"belongs in notPersonalColumns with the reason written down.", key)
			delete(declared, key)
		}
	}

	assert.Empty(t, declared,
		"these holdings name columns that are not in the migration; a declaration "+
			"that points at a column nobody can find sends an auditor searching for "+
			"data that does not exist")
}

// TestTheHolderIsTheModuleName pins the one thing the service cannot check for
// itself.
//
// [service.ErasureHolder] cannot be read from the module package — that import
// would be a cycle — so it repeats the module name as a constant, and a
// repeated constant is exactly the kind that drifts. The report is where the
// drift would show up: a controller reading "customer" off one holder and
// looking for a module by that name has to find this one.
func TestTheHolderIsTheModuleName(t *testing.T) {
	assert.Equal(t, customer.ModuleName, service.ErasureHolder)
}

// readMigration reads the module's migration through the same embedded file
// system the migrator uses, so this test cannot pass against a file the module
// does not actually ship.
func readMigration(t *testing.T) string {
	t.Helper()

	raw, err := fs.ReadFile(customer.New(nil).Migrations(), migrationFile)
	require.NoError(t, err)
	return string(raw)
}

// tablesOf returns the name of every table the migration creates, in the order
// the file creates them.
//
// It is read out of the SQL rather than listed here for the same reason the
// columns are: a list kept by hand is a list that stops being true the day
// somebody adds a table, and the table nobody added to the list is exactly the
// one whose columns no audit would ever visit.
func tablesOf(t *testing.T, schema string) []string {
	t.Helper()

	const header = "CREATE TABLE IF NOT EXISTS "

	var tables []string
	for _, line := range strings.Split(schema, "\n") {
		rest, found := strings.CutPrefix(line, header)
		if !found {
			continue
		}
		name, _, _ := strings.Cut(rest, " ")
		tables = append(tables, strings.TrimSuffix(name, "("))
	}
	require.NotEmpty(t, tables, "no table was read out of the migration; the scanner has gone blind")
	return tables
}

// keysOf keeps the failure message above readable; the order is sorted so two
// runs of a failing test print the same sentence.
func keysOf(m map[string][]string) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// columnsOf returns the column names of one CREATE TABLE block.
//
// The parser is deliberately small: it reads the lines between the table's
// opening parenthesis and its closing one, drops comments and CONSTRAINT lines,
// and takes the first word of what is left. It is enough because this
// repository writes one column per line, and it fails loudly rather than
// quietly — a block it cannot find yields no columns at all, which the caller
// asserts against.
func columnsOf(t *testing.T, schema, table string) []string {
	t.Helper()

	header := "CREATE TABLE IF NOT EXISTS " + table + " ("
	start := strings.Index(schema, header)
	require.GreaterOrEqual(t, start, 0, "%s is not created in the migration", table)

	body := schema[start+len(header):]
	if end := strings.Index(body, "\n);"); end >= 0 {
		body = body[:end]
	}

	var columns []string
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || strings.HasPrefix(fields[0], "--") ||
			fields[0] == "CONSTRAINT" || fields[0] == "PRIMARY" || fields[0] == "UNIQUE" {
			continue
		}
		columns = append(columns, strings.TrimSuffix(fields[0], ","))
	}
	return columns
}

// slicesContains keeps the assertions above readable.
func slicesContains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
