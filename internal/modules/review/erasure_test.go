package review_test

import (
	"io/fs"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/erasure"
	"github.com/bdrtr/gobit/internal/modules/review"
)

// The tests in this file need NO database and carry no build tag on purpose.
// A declaration is a property of the code rather than of the data — the same
// sentence on an empty installation and a full one — which is why
// [erasure.Declarer] takes no context and returns no error, and why the audit
// of it has to be runnable by anybody, anywhere, without Docker.

// migrationFile is the module's only migration. The declaration is checked
// against it because THAT file decides which columns exist; a test that checked
// the declaration against a list typed in this package would prove the
// declaration equals itself.
const migrationFile = "000001_review_init.up.sql"

// reviewsTable is the module's only table.
const reviewsTable = "reviews"

// notPersonalColumns are the columns of the reviews table that hold nothing
// about any person, each one a judgement somebody made rather than a pattern
// somebody matched.
//
// It is the load-bearing half of [TestTheDeclarationCoversEveryPersonalColumn]:
// everything NOT listed here has to appear in the declaration, so a column
// added to the table tomorrow fails this test until somebody decides which side
// of the line it falls on. Deciding is the point — a rule that guessed from the
// name would wave a "reviewer_email" column through for looking technical.
//
// The reasoning, column by column:
//
//   - id is a synthetic key. It names a row, not a person, and once the byline
//     and the text beside it are gone it resolves to nobody. Declaring bare
//     identifiers would also drag every foreign id in the deployment into the
//     declaration and tell an auditor to look where there is nothing to find.
//   - product_id is what the review is ABOUT. It is another module's product,
//     stored and never validated (Principle 2.2), and it says nothing about who
//     wrote the row.
//   - rating is a number from 1 to 5.
//   - status and moderated_at record that a decision was made and when. The
//     table stores no column saying WHICH operator made it, so neither points
//     at anybody. The note they were written with is a different matter and IS
//     declared: it is free text, and a person can be described in free text.
//   - created_at and updated_at are row bookkeeping.
var notPersonalColumns = []string{
	"id", "product_id", "rating", "status", "moderated_at",
	"created_at", "updated_at",
}

// TestTheDeclarationCoversEveryPersonalColumn walks the declaration and the
// migration against each other, in both directions.
//
// The declaration is the only answer an embedder has to "where is this person
// in your database", so a column missing from it does not make the list shorter
// — it makes the list FALSE, and the forgotten column is one no audit will ever
// visit. The reverse direction matters for the opposite reason: a holding
// naming a column that does not exist sends whoever is answering a data subject
// looking for data nobody stores.
func TestTheDeclarationCoversEveryPersonalColumn(t *testing.T) {
	t.Parallel()

	declared := map[string]erasure.Kind{}

	for _, holding := range review.New(review.Options{}).PersonalData().Holdings {
		key := holding.Table + "." + holding.Column

		_, twice := declared[key]
		assert.False(t, twice, "%s is declared twice", key)

		assert.Equal(t, reviewsTable, holding.Table,
			"%s: this module owns one table and can only be declaring about that one", key)
		assert.Contains(t, []erasure.Kind{erasure.Named, erasure.Open}, holding.Kind,
			"%s: a holding with no kind says nothing about whether gobit knows what is in it", key)

		declared[key] = holding.Kind
	}

	for _, column := range columnsOfReviews(t) {
		key := reviewsTable + "." + column

		if slices.Contains(notPersonalColumns, column) {
			_, found := declared[key]
			assert.False(t, found,
				"%s is declared as personal data and is also listed as holding nothing about anybody; "+
					"one of the two is wrong", key)

			continue
		}

		_, found := declared[key]
		assert.True(t, found,
			"%s is in the migration and NOT in the declaration.\n"+
				"Either it holds something about a person — then it belongs in "+
				"Module.PersonalData, with a Why a controller could repeat to a data "+
				"subject — or it does not, and it belongs in notPersonalColumns with "+
				"the reason written down there.", key)

		delete(declared, key)
	}

	assert.Empty(t, declared,
		"these holdings name columns the migration does not create; a declaration that "+
			"points at a column nobody can find sends an auditor searching for data that "+
			"does not exist")
}

// TestEveryWhyIsASentenceThatLandsInSomebodyElsesReport checks the text rather
// than the structure, because the text is what leaves this repository.
//
// One report assembles the sentences of every holder side by side, so each one
// has to read as a clause in that list: present, English, and starting
// lower-case. ADR 0033 writes the language rule down — the Why is DATA crossing
// into core/erasure and read by the embedder, not prose about the code, so it
// stays English even where the file around it is not.
func TestEveryWhyIsASentenceThatLandsInSomebodyElsesReport(t *testing.T) {
	t.Parallel()

	for _, holding := range review.New(review.Options{}).PersonalData().Holdings {
		key := holding.Table + "." + holding.Column

		require.NotEmpty(t, holding.Why,
			"%s is named without a reason, and a column named without a reason cannot be "+
				"repeated to a data subject", key)

		first, _ := utf8.DecodeRuneInString(holding.Why)
		assert.False(t, unicode.IsUpper(first),
			"%s: the Why starts upper-case; these clauses are printed next to the other "+
				"modules' in one report", key)
	}
}

// TestTheHolderIsLeftToTheCoordinator pins an emptiness, which is worth a test
// precisely because it looks like an omission.
//
// The coordinator overwrites Holder with the name the module registry knows this
// module by. Filling it in here would create a second copy of that name, free to
// drift from the registry's — and the drift shows up as a report in which the
// review module never answered and a holder nobody registered did, each half of
// which looks entirely normal on its own.
func TestTheHolderIsLeftToTheCoordinator(t *testing.T) {
	t.Parallel()

	assert.Empty(t, review.New(review.Options{}).PersonalData().Holder)
}

// TestTheModuleDeclaresAndCannotErase pins the decision this module exists to
// demonstrate (ADR 0033).
//
// It holds personal data and has no handle on WHICH person: no customer id, no
// e-mail, no order id, only a byline two shoppers can share. An Eraser here
// could only report zero rows for everybody — a well-formed lie — or match on
// the name and erase strangers. So the day somebody adds one, this test asks
// them which of the two they built.
func TestTheModuleDeclaresAndCannotErase(t *testing.T) {
	t.Parallel()

	m := review.New(review.Options{})

	_, declares := any(m).(erasure.Declarer)
	assert.True(t, declares, "the module has to declare, or it is absent from every erasure report")

	_, erases := any(m).(erasure.Eraser)
	assert.False(t, erases,
		"this module gained an Eraser; it cannot resolve a subject, so read ADR 0033 and "+
			"the migration header before deciding this is right")
}

// TestTheDeclarationDoesNotNeedRegister holds the property [erasure.Declarer]
// states in its own words.
//
// The module built here never registers: it has no pool, no service and no
// handler. It still answers, because an audit reads the declaration without a
// database and a module whose Register failed is still holding its rows.
func TestTheDeclarationDoesNotNeedRegister(t *testing.T) {
	t.Parallel()

	assert.NotEmpty(t, review.New(review.Options{}).PersonalData().Holdings)
}

// columnsOfReviews reads the column names of the reviews table out of the
// migration, through the same embedded file system the migrator uses — so this
// test cannot pass against a file the module does not actually ship.
//
// The parser is deliberately small: it takes the lines between the CREATE TABLE
// and its closing parenthesis, drops comments and the constraint clauses, and
// keeps the first word of what is left. That is enough because this repository
// writes one column per line, and it fails loudly rather than quietly — a table
// it cannot find yields no columns at all, which the caller requires against.
func columnsOfReviews(t *testing.T) []string {
	t.Helper()

	raw, err := fs.ReadFile(review.New(review.Options{}).Migrations(), migrationFile)
	require.NoError(t, err)

	schema := string(raw)

	header := "CREATE TABLE " + reviewsTable + " ("
	start := strings.Index(schema, header)
	require.GreaterOrEqual(t, start, 0, "%s is not created in the migration", reviewsTable)

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

	require.NotEmpty(t, columns, "no column was read out of %s; the scanner has gone blind", reviewsTable)

	return columns
}

// isConstraintWord reports whether a line opens a table constraint rather than a
// column. The CHECK entry is not decorative: this table writes two of them on
// their own lines, and without it the audit would go looking for a column called
// "CHECK".
func isConstraintWord(word string) bool {
	switch word {
	case "CONSTRAINT", "CHECK", "PRIMARY", "UNIQUE", "FOREIGN", "EXCLUDE":
		return true
	default:
		return false
	}
}
