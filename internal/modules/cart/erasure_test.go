package cart_test

import (
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/cart"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// These tests need NO database. A declaration is a property of the code and not
// of the data — the same sentence on an empty database and a full one — which
// is why [personaldata.Declarer] takes no context and returns no error, and why the
// audit of it belongs in a plain unit test that an auditor can run anywhere.

// migrationFile is the module's only migration; the declaration is checked
// against it because that file, not this package, decides what columns exist.
const migrationFile = "000001_cart_init.up.sql"

// notPersonalColumns are the columns that hold nothing about the person, table
// by table.
//
// EVERY table the migration creates has an entry, including the two whose whole
// row is exempt. That is not bookkeeping for its own sake: a table missing from
// this map would be a table nothing checks, and half the schema could grow a
// personal column without a single test noticing.
//
// The list is written down rather than inferred, and it is the load-bearing half
// of [TestPersonalDataCoversEveryPersonalColumn]: everything NOT in it has to
// appear in the declaration, so a column added tomorrow fails until somebody
// decides which side of the line it is on. Deciding is the whole point — a rule
// that guessed would let a "date_of_birth" column past on the grounds that it
// looked technical.
//
// The reasons, per table:
//
//   - The identifiers are here on purpose. A synthetic key names a row, not a
//     person, and after the columns beside it are anonymized it resolves to
//     nobody; that is exactly the fact the anonymization rests on. region_id,
//     variant_id and shipping_option_id name records in OTHER modules — a
//     region, a product, a delivery option — and each of those modules answers
//     for its own rows. carts.customer_id is the one identifier that IS
//     declared, because it is the installation's stable handle for the PERSON
//     rather than for a thing.
//   - The money, the quantities and the currency describe the basket. What
//     somebody put in it and what it came to is a fact about the sale, and a
//     line's title is a copy of the catalog's own words.
//   - cart_shipping_methods.name is the label of the delivery service the shop
//     offers ("standard", "next day"): written about the service, not about the
//     shopper. Its free-form data column is NOT exempt and is declared Open.
//   - revision, totals_revision and every stamp describe the record's state:
//     they are true of the row whether or not anybody is behind it. That
//     includes completed_at, which says the cart became a sale and not who
//     bought.
//   - cart_addresses.address_type says whether the address was for delivery or
//     for billing; it is true of the row with every name in it emptied.
var notPersonalColumns = map[string][]string{
	"carts": {
		"id", "region_id", "currency_code",
		"subtotal", "discount_total", "tax_total", "shipping_total", "total",
		"revision", "totals_revision",
		"completed_at", "created_at", "updated_at", "deleted_at",
	},
	"cart_line_items": {
		"id", "cart_id", "variant_id", "title", "quantity", "unit_price",
		"subtotal", "discount_total", "tax_total", "total",
		"created_at", "updated_at", "deleted_at",
	},
	"cart_addresses": {
		"id", "cart_id", "address_type",
		"created_at", "updated_at", "deleted_at",
	},
	"cart_shipping_methods": {
		"id", "cart_id", "name", "shipping_option_id", "amount",
		"created_at", "updated_at", "deleted_at",
	},
}

// TestPersonalDataCoversEveryPersonalColumn proves the declaration is
// EXHAUSTIVE against the schema it describes.
//
// A declaration is the only answer an embedder has to "where is this person in
// your database", so a missing row does not make the list shorter — it makes it
// FALSE, and the column it forgot is one no audit will ever look at. Comparing
// the list against the migration is what keeps the two from drifting: a column
// added to a table without a line here fails this test, which is the only moment
// anybody is thinking about that column at all.
//
// Both directions are checked. A column in the migration and not in the
// declaration is a hole; a holding naming a column the migration does not create
// sends an auditor searching for data that does not exist.
func TestPersonalDataCoversEveryPersonalColumn(t *testing.T) {
	declaration := cart.New().PersonalData()

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

// TestTheDeclarationLeavesTheHolderToTheSweep pins a field that is empty ON
// PURPOSE.
//
// The coordinator overwrites [personaldata.Declaration.Holder] with the name the
// registry knows this module by (internal/workflows/erasing), so a name written
// in here would be a second place to keep true with nothing comparing the two —
// and the report a controller reads would be attributed by whichever of them
// drifted. The name this module answers under is still pinned, one test below,
// where it is actually used.
func TestTheDeclarationLeavesTheHolderToTheSweep(t *testing.T) {
	assert.Empty(t, cart.New().PersonalData().Holder,
		"the registry's name is the authority on who answered; the sweep fills this in")
}

// TestTheHolderIsTheModuleName pins the one thing the service cannot check for
// itself.
//
// [service.ErasureHolder] cannot be read from the module package — that import
// would be a cycle — so it repeats the module name as a constant, and a repeated
// constant is exactly the kind that drifts. It is what a caller holding the
// service directly reads off the result, and what the module writes into its log
// line when it answers a request.
func TestTheHolderIsTheModuleName(t *testing.T) {
	assert.Equal(t, cart.ModuleName, service.ErasureHolder)
}

// readMigration reads the module's migration through the same embedded file
// system the migrator uses, so this test cannot pass against a file the module
// does not actually ship.
func readMigration(t *testing.T) string {
	t.Helper()

	raw, err := fs.ReadFile(cart.New().Migrations(), migrationFile)
	require.NoError(t, err)

	return string(raw)
}

// tableHeader is what a table's creation looks like in this repository's
// migrations; the audit finds its tables by it.
const tableHeader = "CREATE TABLE IF NOT EXISTS "

// tablesOf returns the names of the tables the migration creates.
//
// It is what makes the map above complete rather than merely correct: the test
// can only claim a table was looked at if it knows the table exists.
func tablesOf(t *testing.T, schema string) []string {
	t.Helper()

	var tables []string
	for _, line := range strings.Split(schema, "\n") {
		if !strings.HasPrefix(line, tableHeader) {
			continue
		}
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, tableHeader), "("))
		tables = append(tables, name)
	}
	require.NotEmpty(t, tables, "no table was read out of the migration; the scanner has gone blind")

	return tables
}

// columnsOf returns the column names of one CREATE TABLE block.
//
// The parser is deliberately small: it reads the lines between the table's
// opening parenthesis and its closing one, drops comments and the lines that
// open or continue a table constraint, and takes the first word of what is left.
// It is enough because this repository writes one column per line, and it fails
// loudly rather than quietly — a block it cannot find yields no columns at all,
// which the caller asserts against.
func columnsOf(t *testing.T, schema, table string) []string {
	t.Helper()

	header := tableHeader + table + " ("
	start := strings.Index(schema, header)
	require.GreaterOrEqual(t, start, 0, "%s is not created in the migration", table)

	body := schema[start+len(header):]
	if end := strings.Index(body, "\n);"); end >= 0 {
		body = body[:end]
	}

	var columns []string
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || strings.HasPrefix(fields[0], "--") || isConstraintWord(fields[0]) {
			continue
		}
		columns = append(columns, strings.TrimSuffix(fields[0], ","))
	}

	return columns
}

// isConstraintWord reports whether the line begins a table constraint rather
// than a column.
//
// CHECK is in the list because this module's constraints are written over two
// lines — the CONSTRAINT and its name, then the CHECK — and without it the audit
// would invent a column called "CHECK", fail to find it in the declaration and
// send whoever reads the failure looking for a column that does not exist.
func isConstraintWord(word string) bool {
	switch word {
	case "CONSTRAINT", "CHECK", "PRIMARY", "UNIQUE", "FOREIGN":
		return true
	default:
		return false
	}
}

// keysOf returns the map's keys in a stable order, so a failure message reads
// the same on every run.
func keysOf(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)

	return out
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
