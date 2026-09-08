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

	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/review"
)

// The tests in this file need NO database and carry no build tag on purpose.
// A declaration is a property of the code rather than of the data — the same
// sentence on an empty installation and a full one — which is why
// [personaldata.Declarer] takes no context and returns no error, and why the audit
// of it has to be runnable by anybody, anywhere, without Docker.

// The declaration is checked against the MIGRATIONS because those files decide
// which columns exist; a test that checked the declaration against a list typed
// in this package would prove the declaration equals itself.
//
// Every up-migration is read, and it is worth saying why that is not the
// obvious "read the file that creates the table". This audit was written when
// the module had one migration and it named that file as a constant. A column
// added by a SECOND migration would then have been invisible to it — present in
// the database, absent from the population, and therefore never asked which
// side of the personal-data line it falls on. The bug is not that the constant
// was wrong; it is that the population was pinned to a LITERAL instead of
// derived from the property being audited, and the property is "the columns
// this module creates", which no single filename can name. ADR 0038 records the
// same defect found in the repository-wide column audit, where a column arriving
// by ALTER TABLE was equally invisible.

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
//   - suggested_status, suggested_at and suggestion_model are a MACHINE's
//     proposal, its moment and the name of the model that made it. None of the
//     three describes anybody: the first is one of two words, the second is a
//     clock reading, and the third names a model rather than a person. The
//     reason the model gave is a different matter and IS declared, for the same
//     reason the moderation note is — it is free text about the author's own
//     free text, and a reason for rejecting a review quotes the review.
var notPersonalColumns = []string{
	"id", "product_id", "rating", "status", "moderated_at",
	"created_at", "updated_at",
	"suggested_status", "suggested_at", "suggestion_model",
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

	declared := map[string]personaldata.Kind{}

	for _, holding := range review.New(review.Options{}).PersonalData().Holdings {
		key := holding.Table + "." + holding.Column

		_, twice := declared[key]
		assert.False(t, twice, "%s is declared twice", key)

		assert.Equal(t, reviewsTable, holding.Table,
			"%s: this module owns one table and can only be declaring about that one", key)
		assert.Contains(t, []personaldata.Kind{personaldata.Named, personaldata.Open}, holding.Kind,
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
// into core/personaldata and read by the embedder, not prose about the code, so it
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

	_, declares := any(m).(personaldata.Declarer)
	assert.True(t, declares, "the module has to declare, or it is absent from every erasure report")

	_, erases := any(m).(personaldata.Eraser)
	assert.False(t, erases,
		"this module gained an Eraser; it cannot resolve a subject, so read ADR 0033 and "+
			"the migration header before deciding this is right")
}

// TestTheDeclarationDoesNotNeedRegister holds the property [personaldata.Declarer]
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

	migrations := review.New(review.Options{}).Migrations()

	entries, err := fs.ReadDir(migrations, ".")
	require.NoError(t, err)

	var (
		columns []string
		created bool
		read    int
	)

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}

		read++

		raw, err := fs.ReadFile(migrations, entry.Name())
		require.NoError(t, err)

		schema := string(raw)

		if header := "CREATE TABLE " + reviewsTable + " ("; strings.Contains(schema, header) {
			created = true
			columns = append(columns, declaredColumns(schema, header)...)
		}

		columns = append(columns, addedColumns(schema)...)
	}

	require.Positive(t, read, "no up-migration was read at all; the scanner has gone blind")
	require.True(t, created, "%s is created by no migration", reviewsTable)
	require.NotEmpty(t, columns, "no column was read out of %s; the scanner has gone blind", reviewsTable)

	return columns
}

// declaredColumns reads the columns out of a CREATE TABLE body.
func declaredColumns(schema, header string) []string {
	body := schema[strings.Index(schema, header)+len(header):]
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

// addedColumns reads the columns an ALTER TABLE adds.
//
// It matches on the two words rather than on the statement, so it does not care
// whether the ALTER names one column or five, and ADD CONSTRAINT lines fall out
// because they do not begin with those two words.
//
// A column DROPPED by a later migration is deliberately not subtracted. The
// audit would then report a column that no longer exists, the declaration would
// be asked to account for it, and the test would fail loudly — which is the
// safe direction. Subtracting silently is the direction that loses a column.
func addedColumns(schema string) []string {
	var columns []string

	for _, line := range strings.Split(schema, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || !strings.EqualFold(fields[0], "ADD") ||
			!strings.EqualFold(fields[1], "COLUMN") {
			continue
		}

		columns = append(columns, strings.TrimSuffix(fields[2], ","))
	}

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
