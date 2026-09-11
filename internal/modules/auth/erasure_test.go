package auth_test

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/auth"
)

// These tests need NO database and no setup. A declaration is a property of the
// code rather than of the data — the same sentences on an empty installation as
// on a full one — which is why [personaldata.Declarer] takes no context and returns
// no error, and why the audit of it is a plain unit test anybody can run.
//
// The audit is written against the MIGRATION and not against the declaration.
// A test that walked the declaration and checked it was well-formed would pass
// on the day somebody adds a "phone" column and declares nothing, which is the
// only failure worth catching here.

// upMigrations matches every forward migration the module ships.
//
// It used to be one file name in a constant, with a comment calling it "the
// module's only migration". The module gained a second one and this audit did not
// notice: its population was a NAME rather than the directory, so a table created
// by 000002 was invisible and the declaration covering it failed as "a column
// nobody can find". A list that decides what gets verified has to be checked
// against the world — the sixth time this repository has closed that shape.
var upMigrations = regexp.MustCompile(`\.up\.sql$`)

// notPersonalColumns lists, per table, the columns that hold nothing about a
// person, and it is the load-bearing half of
// [TestPersonalDataCoversEveryPersonalColumn]: every column NOT named here has
// to appear in the declaration. A column added to the schema tomorrow therefore
// fails this test until somebody decides which side of the line it falls on,
// and that moment of deciding is the whole point — a rule that inferred would
// wave a "date_of_birth" past on the grounds that it looked technical.
//
// The five tables all appear, including the ones with nothing personal in them,
// because the test also refuses a table that is missing from this map. A new
// table is exactly where an undeclared column would hide.
//
// Why each of these is not personal data:
//
//   - IDENTIFIERS (id, user_id, api_key_id, sales_channel_id, created_by,
//     revoked_by). A synthetic key names a ROW, not a person, and it is only a
//     person via the columns beside it — which are declared. Declaring the key
//     as well would double-count the same staff member and send an auditor to a
//     column with no name in it. api_key.created_by and .revoked_by are the same
//     case even though they point at people: they carry a "user_…" identifier
//     with no foreign key, and it resolves through auth_user, which is declared.
//   - ROW LIFECYCLE (created_at, updated_at, deleted_at). These timestamp a
//     WRITE to the row, not an act of the person. auth_identity.updated_at is
//     the honest boundary case, because some of those writes are the person's
//     own sign-ins; it is left out because last_login_at carries that fact
//     explicitly and declaring both would name the same event twice, once in a
//     column that means something else on every other table in the schema.
//   - PRIVILEGES (auth_user.scopes, api_key.scopes, api_key.type,
//     sales_channel.is_disabled). What an account may do is a fact about the
//     account. It identifies nobody, and it is the same value for everyone who
//     holds the same role.
//   - THE AUTHENTICATION METHOD (auth_identity.provider). The value is
//     "emailpass" or a provider's name and it is identical for every staff
//     member who signs in the same way; what identifies the person is
//     provider_identity beside it, which is declared and whose reason says so.
//   - MACHINE SECRETS (api_key.token_hash, api_key.redacted). Both are derived
//     from 256 random bits gobit generated itself, so they are derived from
//     nobody. This is the exact contrast that puts auth_identity.password_hash
//     on the other side of the line, where the input was a secret a person
//     chose.
//   - A MACHINE'S HISTORY (api_key.last_used_at, api_key.revoked_at). A key is
//     an identity that processes and any number of people hold, so its use
//     points at no one person — the contrast that makes
//     auth_identity.last_login_at personal, since an identity row belongs to
//     exactly one named human.
var notPersonalColumns = map[string][]string{
	"auth_user": {
		"id", "scopes", "created_at", "updated_at", "deleted_at",
	},
	"auth_identity": {
		"id", "user_id", "provider", "created_at", "updated_at", "deleted_at",
	},
	// token_hash is the digest of a secret and describes nobody; user_id and
	// invited_by are join keys, on the same side as auth_identity.user_id above;
	// expires_at is created_at plus a constant and says nothing created_at does
	// not. What IS declared is created_at, because the row's existence is the fact
	// about a person and a date is where that fact lives.
	"auth_user_invitation": {
		"token_hash", "user_id", "invited_by", "expires_at",
	},
	"sales_channel": {
		"id", "is_disabled", "created_at", "updated_at", "deleted_at",
	},
	"api_key": {
		"id", "type", "token_hash", "redacted", "scopes", "created_by",
		"last_used_at", "revoked_at", "revoked_by", "created_at", "updated_at",
		"deleted_at",
	},
	"api_key_sales_channel": {
		"api_key_id", "sales_channel_id", "created_at",
	},
}

// TestPersonalDataCoversEveryPersonalColumn proves the declaration is
// EXHAUSTIVE against the schema it describes, in BOTH directions.
//
// The declaration is the only answer an embedder has to "where is this person
// in your database", and it is what that embedder publishes as its privacy
// notice. A missing row does not make the list shorter — it makes it FALSE, and
// the column it forgot is one no audit will ever look at. A row pointing at a
// column that does not exist is the mirror fault: it sends somebody answering a
// data-subject request hunting for data nobody holds.
func TestPersonalDataCoversEveryPersonalColumn(t *testing.T) {
	declaration := auth.New(auth.Options{}).PersonalData()

	declared := map[string]bool{}
	for _, holding := range declaration.Holdings {
		key := holding.Table + "." + holding.Column
		assert.False(t, declared[key], "%s is declared twice", key)
		declared[key] = true
	}

	schema := readMigration(t)
	tables := tablesOf(t, schema)
	require.NotEmpty(t, tables, "no table was read out of the migration; the scanner has gone blind")

	for _, table := range tables {
		exempt, known := notPersonalColumns[table]
		require.True(t, known,
			"%s is created in the migration and this test knows nothing about it. "+
				"A new table is where an undeclared personal column hides: list its "+
				"columns in notPersonalColumns with the reason each one holds nothing "+
				"about a person, and put the rest in Module.PersonalData.", table)

		columns := columnsOf(t, schema, table)
		require.NotEmpty(t, columns, "no column was read out of %s; the scanner has gone blind", table)

		for _, column := range columns {
			key := table + "." + column
			if contains(exempt, column) {
				assert.False(t, declared[key],
					"%s is declared as personal data and is at the same time listed as "+
						"holding nothing about a person; the two cannot both be true", key)
				continue
			}
			assert.True(t, declared[key],
				"%s is in the migration and NOT in the declaration.\n"+
					"Either it holds something about the staff member — then it belongs "+
					"in Module.PersonalData with a sentence a controller can repeat to "+
					"them — or it does not, and it belongs in notPersonalColumns with "+
					"the reason written down.", key)
			delete(declared, key)
		}
	}

	assert.Empty(t, declared,
		"these holdings name columns that are not in the migration; a declaration "+
			"pointing at a column nobody can find sends an auditor searching for data "+
			"that does not exist")
}

// TestTheDeclarationReadsAsAnAnswerToAPerson checks the shape of the text
// rather than the list.
//
// Every holding lands in one report beside the other modules' — the invoice's,
// the customer's, the saga store's — and a controller reads that report out to
// a person. So a holding with no Kind says nothing about who wrote the column,
// a holding with no Why cannot be repeated to anybody, and a Why that opens
// with a capital letter is a sentence that was written for a Go file rather
// than for the answer it ends up inside (ADR 0033).
func TestTheDeclarationReadsAsAnAnswerToAPerson(t *testing.T) {
	for _, holding := range auth.New(auth.Options{}).PersonalData().Holdings {
		key := holding.Table + "." + holding.Column

		assert.Contains(t, []personaldata.Kind{personaldata.Named, personaldata.Open}, holding.Kind,
			"%s: a holding with no kind says nothing about whether gobit or the shop wrote it", key)

		require.NotEmpty(t, holding.Why,
			"%s: a column named without a reason cannot be repeated to a data subject", key)
		first, _ := utf8.DecodeRuneInString(holding.Why)
		assert.True(t, unicode.IsLower(first),
			"%s: the reason opens with %q; these clauses are read side by side inside "+
				"one report and start lower-case", key, first)
	}
}

// TestTheHolderIsLeftForTheCoordinator pins the empty Holder.
//
// The coordinator overwrites [personaldata.Declaration.Holder] with the name the
// module registry knows this module by, so writing "auth" here would be a
// second copy of that name with nothing keeping the two together. The failure
// it would eventually produce is quiet: a report naming a holder no registry
// entry matches, read by somebody trying to find out who still holds the data.
func TestTheHolderIsLeftForTheCoordinator(t *testing.T) {
	assert.Empty(t, auth.New(auth.Options{}).PersonalData().Holder)
}

// TestTheAuthModuleOffersNoErasure pins the decision, not the omission.
//
// This module's data subject is a STAFF MEMBER. A shopper asking to be
// forgotten must never reach an administrator's account, and an eraser here is
// all it would take for a colliding e-mail address to delete an operator — the
// customer and auth tables are joined nowhere that would catch it (ADR 0033).
// The sweep finds an eraser BY TYPE ASSERTION, so adding the method would be
// enough to wire that up with no other edit anywhere; this assertion is what
// makes that edit stop and read the argument first.
func TestTheAuthModuleOffersNoErasure(t *testing.T) {
	var module any = auth.New(auth.Options{})

	_, ok := module.(personaldata.Eraser)
	assert.False(t, ok,
		"the auth module has grown an Erase method. Its rows belong to staff, not "+
			"to shoppers, so a customer's erasure request would now delete an "+
			"administrator; declaring is how this module answers, and the coordinator "+
			"reports it as RETAINED on every sweep.")

	_, ok = module.(personaldata.Declarer)
	assert.True(t, ok, "the module has to declare, or its staff accounts are invisible in every report")
}

// readMigration reads the migration through the same embedded file system the
// migrator uses, so this test cannot pass against a file the module does not
// actually ship.
func readMigration(t *testing.T) string {
	t.Helper()

	migrations := auth.New(auth.Options{}).Migrations()

	entries, err := fs.ReadDir(migrations, ".")
	require.NoError(t, err)

	var schema strings.Builder
	read := 0
	for _, entry := range entries {
		if entry.IsDir() || !upMigrations.MatchString(entry.Name()) {
			continue
		}
		raw, readErr := fs.ReadFile(migrations, entry.Name())
		require.NoError(t, readErr)
		schema.Write(raw)
		schema.WriteString("\n")
		read++
	}

	require.NotZero(t, read,
		"no forward migration was read, so the audit has gone blind and would pass "+
			"whatever this module's schema held")

	return schema.String()
}

// createTable matches every table the migration creates.
var createTable = regexp.MustCompile(`(?m)^CREATE TABLE IF NOT EXISTS (\w+) \(`)

// tablesOf returns every table name the migration creates.
//
// The audit walks THIS list rather than the exemption map's keys, which is the
// difference between a test that notices a new table and one that does not.
func tablesOf(t *testing.T, schema string) []string {
	t.Helper()

	var tables []string
	for _, match := range createTable.FindAllStringSubmatch(schema, -1) {
		tables = append(tables, match[1])
	}
	return tables
}

// columnsOf returns the column names of one CREATE TABLE block.
//
// The parser is deliberately small: it reads the lines between the table's
// opening parenthesis and its closing one, drops comments and constraint lines,
// and takes the first word of what is left. That is enough because this
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

// contains keeps the assertions above readable.
func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
