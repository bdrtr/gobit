package b2b_test

import (
	"io/fs"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/erasure"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/internal/modules/b2b"
)

// These tests need NO database and carry no build tag, which is the point: a
// declaration is a property of the code and not of the data — the same sentence
// on an empty installation as on a full one — so the audit of it has to run
// wherever the code is read, not only where Docker is.

// notPersonalColumns are the columns this module judged to hold nothing about a
// person, per table.
//
// The list is written down rather than inferred, and it is the load-bearing half
// of [TestPersonalDataCoversEveryPersonalColumn]: every column NOT named here
// has to appear in the declaration, so a column added tomorrow fails this test
// until somebody decides which side of the line it is on. Deciding is the whole
// point — a rule that guessed would wave a "date_of_birth" column through on the
// grounds that it looked technical.
//
// The reasons, column by column:
//
//   - id, company_id — a synthetic key names a ROW, not a person, and once the
//     columns beside it hold nothing the key resolves to nobody. company_id
//     points at a company besides. Declaring identifiers would drag every
//     foreign key in the deployment into the privacy notice.
//   - currency_code, spending_limit_reset_period — the company's accounting
//     settings. They are the same for every employee and describe the ledger,
//     not a person.
//   - created_at, updated_at, deleted_at — the row's own lifecycle. They say
//     when a record was written, which is a fact about this module's
//     bookkeeping; the person is in the columns beside them.
//
// spending_limit and is_company_admin are deliberately NOT here even though
// b2b_company_employee names no attribute of the person: each row is about one
// natural person and both columns state something about that person (what they
// may spend, whether they may administer the account). See
// [TestTheEmployeeFactsAreDeclaredEvenThoughTheTableNamesNobody].
var notPersonalColumns = map[string][]string{
	"b2b_company": {
		"id", "currency_code", "spending_limit_reset_period",
		"created_at", "updated_at", "deleted_at",
	},
	"b2b_company_employee": {
		"id", "company_id", "created_at", "updated_at", "deleted_at",
	},
}

// TestPersonalDataCoversEveryPersonalColumn proves the declaration is EXHAUSTIVE
// against the schema it describes, in BOTH directions.
//
// A declaration is the only answer an embedder has to "where is this person in
// your database", and the embedder publishes it as a privacy notice. A missing
// row does not make the list shorter — it makes it FALSE, and the column it
// forgot is one no audit will ever look at. A row pointing at a column that does
// not exist is the mirror failure: it sends whoever answers a data subject
// looking for data nobody holds.
//
// The tables are DISCOVERED from the migrations rather than listed here, so the
// audit also catches the failure the per-column check cannot see — a whole new
// table arriving with nobody asked whether it holds people.
func TestPersonalDataCoversEveryPersonalColumn(t *testing.T) {
	declaration := b2b.New(nil).PersonalData()

	declared := map[string]bool{}
	for _, holding := range declaration.Holdings {
		key := holding.Table + "." + holding.Column
		assert.False(t, declared[key], "%s is declared twice", key)
		declared[key] = true

		assert.Contains(t, []erasure.Kind{erasure.Named, erasure.Open}, holding.Kind,
			"%s: a holding with no kind says nothing about who wrote the value", key)
		require.NotEmpty(t, holding.Why,
			"%s: a column named without a reason cannot be repeated to a data subject", key)
		assert.Equal(t, strings.ToLower(holding.Why[:1]), holding.Why[:1],
			"%s: the reasons of every holder land side by side in one report, so this one "+
				"starts lower-case like the rest", key)
	}

	schema := readMigrations(t)
	tables := tablesOf(t, schema)
	require.Len(t, tables, len(notPersonalColumns),
		"the migrations create %d tables and %d are judged here; a table nobody judged is a "+
			"table nobody asked whether it holds people", len(tables), len(notPersonalColumns))

	for table, columns := range tables {
		exempt, judged := notPersonalColumns[table]
		require.True(t, judged, "%s is created by the migrations and appears in no judgement", table)

		for _, column := range columns {
			key := table + "." + column
			if slices.Contains(exempt, column) {
				assert.False(t, declared[key],
					"%s is declared as personal data and is also listed as holding nothing "+
						"about a person; the two lists disagree", key)
				continue
			}
			assert.True(t, declared[key],
				"%s is in the migrations and NOT in the declaration.\n"+
					"Either it holds something about the person — then it belongs in "+
					"Module.PersonalData with a sentence a controller can repeat to that "+
					"person — or it does not, and it belongs in notPersonalColumns with "+
					"the reason written down.", key)
			delete(declared, key)
		}
	}

	assert.Empty(t, declared,
		"these holdings name columns that no migration creates; a declaration that points at "+
			"a column nobody can find sends an auditor searching for data that does not exist")
}

// TestTheEmployeeFactsAreDeclaredEvenThoughTheTableNamesNobody pins the module's
// one genuinely arguable judgement, so that changing it takes an argument.
//
// b2b_company_employee holds no name, no address and no customer id — the
// migration says the identity lives in a link instead — and the easy reading is
// that a table naming nobody holds nothing personal. That reading is wrong: every
// row is about one natural person, and what the row says is what that person may
// spend and whether they may administer the account. Those are facts ABOUT a
// person, and the person is reachable, just not from here.
//
// Without this test the two holdings would be the first thing a later reader
// deleted as noise, because the table they point at looks empty of people.
func TestTheEmployeeFactsAreDeclaredEvenThoughTheTableNamesNobody(t *testing.T) {
	declared := map[string]erasure.Holding{}
	for _, holding := range b2b.New(nil).PersonalData().Holdings {
		if holding.Table == "b2b_company_employee" {
			declared[holding.Column] = holding
		}
	}

	assert.Contains(t, declared, "spending_limit",
		"the limit is one person's purchasing authority and is held nowhere else")
	assert.Contains(t, declared, "is_company_admin",
		"whether a person may administer their employer's account is a fact about that person")
	assert.Len(t, declared, 2,
		"only the facts are declared here; the identifiers name rows, and the link that "+
			"carries the identity is a runtime table core/link declares for itself")

	columns := tablesOf(t, readMigrations(t))["b2b_company_employee"]
	require.NotEmpty(t, columns)
	assert.NotContains(t, columns, "customer_id",
		"the employee table gained a customer_id column, so this module now holds the identity "+
			"itself rather than reaching it through a link; the declaration above and the "+
			"argument beside it both have to be redone")
}

// TestTheHolderIsLeftForTheCoordinator pins that the module does NOT name itself.
//
// The coordinator overwrites Holder with the name the module is registered under
// (internal/workflows/erasing, Coordinator.PersonalData). Filling it in here
// would keep the same name in two places, and the day they disagree the report
// tells a controller that a module it cannot find holds somebody's data.
func TestTheHolderIsLeftForTheCoordinator(t *testing.T) {
	assert.Empty(t, b2b.New(nil).PersonalData().Holder)
}

// TestTheModuleIsFoundAsADeclarer walks the path the coordinator walks.
//
// The capability is optional and found by TYPE ASSERTION over the module
// registry, so a drifting method name or signature breaks nothing at build time
// — the module simply stops appearing in the sweep. The compile-time pin in
// module.go closes that for the concrete type; this closes it for the value the
// registry actually holds.
func TestTheModuleIsFoundAsADeclarer(t *testing.T) {
	var mod module.Module = b2b.New(nil)

	declarer, ok := mod.(erasure.Declarer)
	require.True(t, ok, "a module the sweep cannot recognize is silently missing from every report")
	assert.NotEmpty(t, declarer.PersonalData().Holdings,
		"a module that declares nothing produces no entry at all, and this one holds people")
}

// readMigrations reads every up-migration through the same embedded file system
// the migrator uses, so no test can pass against a file the module does not ship.
//
// All of them are read rather than the one that exists today: the second
// migration is where a column is most likely to be ADDED, and an audit that
// looked only at the first would go quiet exactly when the schema starts moving.
func readMigrations(t *testing.T) string {
	t.Helper()

	fsys := b2b.New(nil).Migrations()
	names, err := fs.Glob(fsys, "*.up.sql")
	require.NoError(t, err)
	require.NotEmpty(t, names, "the module ships no up-migration; the scanner has gone blind")

	var out strings.Builder
	for _, name := range names {
		raw, err := fs.ReadFile(fsys, path.Clean(name))
		require.NoError(t, err)
		out.Write(raw)
		out.WriteString("\n")
	}
	return out.String()
}

// tablesOf returns the columns of every table the schema creates, keyed by table.
//
// The parser is deliberately small: it reads what is between a CREATE TABLE's
// opening parenthesis and its closing one, drops comment and CONSTRAINT lines,
// and takes the first word of what is left. That is enough because this
// repository writes one column per line. It fails LOUDLY rather than quietly —
// a table whose body it cannot read yields no "id" column, and the require below
// stops the whole audit instead of reporting an empty schema as compliant.
func tablesOf(t *testing.T, schema string) map[string][]string {
	t.Helper()

	const header = "CREATE TABLE IF NOT EXISTS "

	tables := map[string][]string{}
	for _, block := range strings.Split(schema, header)[1:] {
		name, body, found := strings.Cut(block, " (")
		require.True(t, found, "the CREATE TABLE for %q has no column list", name)
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

		require.Contains(t, columns, "id",
			"no id column was read out of %s; every table in this module has one, so the "+
				"parser is reading nothing and every assertion below it is vacuous", name)
		tables[name] = columns
	}
	return tables
}
