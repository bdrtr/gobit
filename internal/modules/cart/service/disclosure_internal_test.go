package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// This file audits the two pairs the disclosure cannot check for itself.
//
// The first is the SQL. [personalColumns] says which columns this module holds
// about a person and the disclosure may carry exactly those; the statements
// that fetch them are TEXT, so a SELECT list that dropped a column would answer
// a person with a document short of what the declaration promised, and one that
// added a column would hand over data the declaration said was not there.
// Neither is a compile error and neither shows up in a service test, because
// the fake store answers from a map somebody filled in by hand.
//
// The second is the readers. The field lists are built by walking the
// declaration (see declaredFields), which makes a forgotten column an ERROR
// rather than a silent omission — but only if something ever asks for that
// column with a row that actually holds a value. The test at the bottom is that
// something.
//
// The generated file is audited beside its source because the generated file is
// the one that RUNS: sqlc turns queries/disclosure.sql into it, and a change
// made to one without the other leaves the executed statement and the reviewed
// statement different documents.
var (
	disclosureStatements = map[string]string{
		tableCarts:               "ListCartsForDisclosure",
		tableCartAddresses:       "ListCartAddressesForDisclosure",
		tableCartLineItems:       "ListCartLineItemNotesForDisclosure",
		tableCartShippingMethods: "ListCartShippingNotesForDisclosure",
	}
	disclosureSources = []string{
		"../queries/disclosure.sql",
		"../repository/cartdb/disclosure.sql.go",
	}
)

// structuralColumns are the selected expressions that are NOT personal data and
// are allowed to appear anyway.
//
// The list is short and closed on purpose: it is what lets the audit below be
// an equality rather than a one-sided check. id and cart_id are the row's
// identity and its tie back to the cart, which is what makes a dossier
// readable as a sequence of sessions; created_at is quoted by the truncation
// notice; count(*) is how many carts matched before the bound cut. Anything
// else appearing in a SELECT list is a column somebody added to a disclosure
// without adding it to the declaration.
var structuralColumns = map[string]bool{
	"id":         true,
	"cart_id":    true,
	"created_at": true,
	"count(*)":   true,
}

// TestTheStatementsSelectExactlyTheDeclaredColumns proves the declaration and
// the SQL describe the same disclosure.
func TestTheStatementsSelectExactlyTheDeclaredColumns(t *testing.T) {
	for _, source := range disclosureSources {
		t.Run(source, func(t *testing.T) {
			text := readSource(t, source)

			selected := map[string]map[string]bool{}
			for table, statement := range disclosureStatements {
				selected[table] = selectedColumns(t, text, statement)
			}

			declared := map[string]map[string]bool{}
			for i := range personalColumns {
				holding := personalColumns[i].holding
				if declared[holding.Table] == nil {
					declared[holding.Table] = map[string]bool{}
				}
				declared[holding.Table][holding.Column] = true

				assert.True(t, selected[holding.Table][holding.Column],
					"%s is declared and %s does not select it; the person would be handed a "+
						"document short of what the declaration promised",
					columnPath(holding), disclosureStatements[holding.Table])
			}

			for table, columns := range selected {
				for column := range columns {
					if structuralColumns[column] {
						continue
					}
					assert.True(t, declared[table][column],
						"%s.%s is selected by the disclosure and is not a declared holding; a "+
							"disclosure may not reach past the declaration, and it is not a "+
							"structural column either", table, column)
				}
			}
		})
	}
}

// TestNoDisclosureStatementWrites is the "this is a READ" claim, made against
// the statements rather than against the code that calls them.
//
// A disclosure is triggered by an administrator answering a person, at an
// arbitrary moment, possibly twice. A write that crept into one of these
// statements would change the data the document describes while describing it,
// and FOR UPDATE would make an answer about a cart queue behind a checkout that
// is still holding it.
func TestNoDisclosureStatementWrites(t *testing.T) {
	for _, source := range disclosureSources {
		t.Run(source, func(t *testing.T) {
			text := readSource(t, source)

			for table, statement := range disclosureStatements {
				body := statementBody(t, text, statement)
				for _, forbidden := range []string{"UPDATE", "INSERT", "DELETE"} {
					assert.NotContains(t, body, forbidden,
						"%s (%s) contains %s; a disclosure reads and does nothing else",
						statement, table, forbidden)
				}
			}
		})
	}
}

// TestEveryDeclaredColumnHasAReader is the Go half of the same audit.
//
// declaredFields walks [personalColumns] and asks a switch for each column's
// value, so a column added to the declaration and forgotten in disclosure.go
// produces [CodeDisclosureColumnUnreadable] — but only when something asks. The
// rows below are filled in every field, so each declared column has a value to
// find; a reader wired to the WRONG field of the row would come back empty and
// fail here just as loudly as a missing one.
func TestEveryDeclaredColumnHasAReader(t *testing.T) {
	note := map[string]any{"typed": "by somebody"}

	cartFieldList, err := cartFields(models.PersonalCart{
		ID: "cart_1", CustomerID: "cust_1", Email: "person@example.com", Metadata: note,
	})
	require.NoError(t, err)
	assertCoversTable(t, tableCarts, columnsOf(cartFieldList))

	addressFieldList, err := addressFields(models.PersonalAddress{
		ID: "addr_1", CartID: "cart_1",
		SourceAddressID: "book_1", FirstName: "A", LastName: "B", Company: "C",
		Address1: "D", Address2: "E", City: "F", Province: "G", PostalCode: "H",
		CountryCode: "TR", Phone: "I", Metadata: note,
	})
	require.NoError(t, err)
	assertCoversTable(t, tableCartAddresses, columnsOf(addressFieldList))

	lineNote, err := noteRecord(tableCartLineItems, columnMetadata,
		models.PersonalNote{ID: "li_1", CartID: "cart_1", Data: note})
	require.NoError(t, err)
	assertCoversTable(t, tableCartLineItems, columnsOf(lineNote.Fields))

	shippingNote, err := noteRecord(tableCartShippingMethods, columnShippingData,
		models.PersonalNote{ID: "csm_1", CartID: "cart_1", Data: note})
	require.NoError(t, err)
	assertCoversTable(t, tableCartShippingMethods, columnsOf(shippingNote.Fields))
}

// columnsOf lists the columns a field slice carries.
func columnsOf(fields []personaldata.Field) []string {
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		out = append(out, field.Column)
	}

	return out
}

// assertCoversTable requires the produced columns to be exactly the declared
// ones for that table, in the declaration's own order.
//
// The ORDER is part of the claim and not incidental: [personalColumns] is
// documented as the order the entries appear in a report, so a dossier's fields
// read table by table in the order the declaration lists them. A disclosure that
// produced the same set in another order would make two documents about the same
// person hard to compare.
func assertCoversTable(t *testing.T, table string, produced []string) {
	t.Helper()

	expected := make([]string, 0, len(personalColumns))
	for i := range personalColumns {
		if personalColumns[i].holding.Table == table {
			expected = append(expected, personalColumns[i].holding.Column)
		}
	}
	assert.Equal(t, expected, produced,
		"the disclosure of a %s row has to produce every declared column of that table", table)
}

// statementBody cuts one named statement out of a file.
//
// It is the same shape as the erasure audit's parser and stops at the first ';'
// or backtick for the same reason: in the .sql file a statement ends with a
// semicolon, and in the generated .go file it ends where the raw string literal
// closes.
func statementBody(t *testing.T, text, statement string) string {
	t.Helper()

	marker := "-- name: " + statement + " "
	start := strings.Index(text, marker)
	require.GreaterOrEqual(t, start, 0, "%s is not in this file", statement)

	body := text[start+len(marker):]
	if end := strings.IndexAny(body, ";`"); end >= 0 {
		body = body[:end]
	}

	return body
}

// selectedColumns returns the columns one named statement selects.
//
// The parser is deliberately small and STRICT: it takes what stands between
// SELECT and FROM and reads one expression per comma, keeping the first word of
// each. That is enough for the lists in queries/disclosure.sql, which are
// written one column per line for exactly this reason, and it is not enough for
// a clever expression — which is the point. A SELECT list this parser cannot
// read is a SELECT list a reviewer cannot check against a declaration either.
func selectedColumns(t *testing.T, text, statement string) map[string]bool {
	t.Helper()

	body := statementBody(t, text, statement)

	from := strings.Index(body, "\nFROM ")
	require.Greater(t, from, 0, "%s has no FROM clause", statement)
	list := strings.Index(body, "SELECT ")
	require.GreaterOrEqual(t, list, 0, "%s has no SELECT clause", statement)
	require.Less(t, list, from, "%s selects nothing before its FROM", statement)

	out := map[string]bool{}
	for _, expression := range strings.Split(body[list+len("SELECT "):from], ",") {
		fields := strings.Fields(expression)
		require.NotEmpty(t, fields, "%s has an empty entry in its SELECT list", statement)
		out[fields[0]] = true
	}

	return out
}
