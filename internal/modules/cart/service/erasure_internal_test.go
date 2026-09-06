package service

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file audits the pair the declaration cannot check for itself: whether
// [personalColumns]'s erased flags describe what the SQL actually does.
//
// The service file says nothing in Go can prove it, because the statements are
// text — and that is exactly why the audit reads the text. A flag and a
// statement that disagree fail in one of two directions and both are silent
// otherwise: a column flagged erased and left out of the UPDATE is reported as
// cleaned while it still holds a name, and a column nulled by the UPDATE
// without being declared is data destroyed that no report ever mentioned.
//
// It is not a substitute for the integration test the module does not have. A
// statement can be correct in its SET clause and wrong in its WHERE, and only a
// database can say so.

// erasureStatements maps a table to the statement that rewrites it, and the two
// files that have to agree about it.
//
// The generated file is audited alongside its source because the generated file
// is the one that RUNS: sqlc turns queries/erasure.sql into it, and a change
// made to one of the two without the other would leave the executed statement
// and the reviewed statement different documents.
var (
	erasureStatements = map[string]string{
		tableCarts:         "AnonymizeCartContacts",
		tableCartAddresses: "AnonymizeCartAddresses",
	}
	erasureSources = []string{
		"../queries/erasure.sql",
		"../repository/cartdb/erasure.sql.go",
	}
)

// TestTheStatementsNullExactlyTheColumnsDeclaredErased proves the declaration
// and the SQL describe the same erasure.
func TestTheStatementsNullExactlyTheColumnsDeclaredErased(t *testing.T) {
	for _, source := range erasureSources {
		t.Run(source, func(t *testing.T) {
			text := readSource(t, source)

			nulled := map[string]map[string]bool{}
			for table, statement := range erasureStatements {
				nulled[table] = nulledColumns(t, text, statement)
			}

			for i := range personalColumns {
				holding := personalColumns[i].holding
				key := columnPath(holding)
				columns, hasStatement := nulled[holding.Table]

				if !personalColumns[i].erased {
					assert.False(t, columns[holding.Column],
						"%s is declared as KEPT and the statement nulls it: the report would "+
							"name a column as still holding the person after it had been emptied", key)

					continue
				}

				require.True(t, hasStatement,
					"%s is declared as erased and %s has no anonymizing statement at all",
					key, holding.Table)
				assert.True(t, columns[holding.Column],
					"%s is declared as erased and the statement does NOT null it; the report "+
						"would leave it out of Result.Kept and tell a controller it was cleaned", key)
			}

			// The other direction: nothing may be rewritten that the
			// declaration does not know about. A column emptied in silence is
			// the embedder's data destroyed with no line in any report saying
			// where it went.
			declared := map[string]bool{}
			for i := range personalColumns {
				if personalColumns[i].erased {
					declared[columnPath(personalColumns[i].holding)] = true
				}
			}
			for table, columns := range nulled {
				for column := range columns {
					assert.True(t, declared[table+"."+column],
						"%s.%s is set to NULL by the erasure and is not declared as an erased "+
							"holding; every column this module rewrites has to be one it said it held",
						table, column)
				}
			}
		})
	}
}

// TestNoStatementTouchesATableWithoutOne guards the tables the erasure leaves
// alone entirely.
//
// cart_line_items and cart_shipping_methods carry no column gobit writes a
// person into, so nothing in them is flagged erased; a flag appearing there
// would be a promise no statement keeps.
func TestNoStatementTouchesATableWithoutOne(t *testing.T) {
	for i := range personalColumns {
		if _, ok := erasureStatements[personalColumns[i].holding.Table]; ok {
			continue
		}
		assert.False(t, personalColumns[i].erased,
			"%s is flagged erased and its table has no anonymizing statement",
			columnPath(personalColumns[i].holding))
	}
}

// readSource reads one of the files the statements live in.
func readSource(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(raw)
}

// nulledColumns returns the columns one named statement sets to NULL.
//
// The parser is deliberately small and STRICT: it cuts the statement out by its
// sqlc name, takes what stands between SET and WHERE, and reads one assignment
// per comma. An assignment whose value is not NULL is allowed only for
// updated_at — anything else means a column is being rewritten to a value
// rather than emptied, and the audit above, which asks only "is it NULL", would
// wave it through.
func nulledColumns(t *testing.T, text, statement string) map[string]bool {
	t.Helper()

	marker := "-- name: " + statement + " "
	start := strings.Index(text, marker)
	require.GreaterOrEqual(t, start, 0, "%s is not in this file", statement)

	body := text[start+len(marker):]
	if end := strings.IndexAny(body, ";`"); end >= 0 {
		body = body[:end]
	}

	set := strings.Index(body, "\nSET ")
	require.GreaterOrEqual(t, set, 0, "%s has no SET clause", statement)
	where := strings.Index(body, "\nWHERE ")
	require.Greater(t, where, set, "%s has no WHERE clause after its SET clause", statement)

	out := map[string]bool{}
	for _, assignment := range strings.Split(body[set+len("\nSET "):where], ",") {
		fields := strings.Fields(assignment)
		require.Len(t, fields, 3, "%s: %q is not a plain <column> = <value> assignment",
			statement, strings.TrimSpace(assignment))
		require.Equal(t, "=", fields[1])

		if fields[2] != "NULL" {
			assert.Equal(t, "updated_at", fields[0],
				"%s writes %s = %s; a personal column rewritten to a VALUE is not covered "+
					"by an audit that looks for NULL", statement, fields[0], fields[2])

			continue
		}
		out[fields[0]] = true
	}

	return out
}
