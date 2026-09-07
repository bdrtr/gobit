package customer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/customer"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// These tests need NO database either, and for the same reason the declaration
// audit beside them does not: what they check is the agreement between two
// statements the code makes, and both are true on an empty installation.

// undisclosedTables are the declared tables a person's file deliberately
// carries no record from, with the reason written down.
//
// The map is the second half of an argument the code makes in one direction
// only. [service.Service.PersonalDataOf] derives a record's FIELDS from the
// declaration, so a declared column can never be missed inside a table the
// module discloses; nothing in the code says anything about a declared table it
// discloses NOTHING from. That silence is what this map breaks — every declared
// table is either disclosed or listed here with a sentence, and
// [TestEveryDeclaredTableIsDisclosedOrExempt] fails the day a declaration names
// a table neither list mentions.
//
// It matters because the failure it guards is invisible from the outside. A
// person receives a file, the file says Disclosed, and the table nobody decided
// about is simply not in it — no state, no sentence, nothing to notice.
var undisclosedTables = map[string]string{
	// A group's metadata is written by the shop about a SEGMENT ("wholesalers")
	// and is shared by everybody in it. It is declared because gobit never looks
	// inside it and therefore cannot claim it holds nobody — that judgement is
	// the controller's (ADR 0029) — but the same not-looking is why it cannot be
	// attributed to one member: handing it to whoever asked would disclose to
	// this person whatever the shop happened to write about the others. The
	// declaration is what points a controller at the column; one person's file
	// is not the place it can be answered from.
	service.TableGroup: "a segment's free-form blob is not one member's data and may name the others in it",
}

// TestTheDisclosureDerivesFromTheDeclaration pins the arrangement that makes
// every other claim in this file worth making.
//
// The module is the declarer and the list it declares lives in the service, so
// that [service.Service.PersonalDataOf] can build a person's records out of it
// instead of out of a second list of columns (ADR 0034, fourth point). This test
// is what stops that from being quietly undone: the day somebody writes a
// literal list back into Module.PersonalData, the declaration and the disclosure
// become two documents again, and nothing else in the repository would notice —
// the declaration audit would still pass, the disclosure tests would still pass,
// and only a person reading their own file would find a column the map promised
// and the file never contained.
func TestTheDisclosureDerivesFromTheDeclaration(t *testing.T) {
	declaration := customer.New(nil).PersonalData()

	assert.Equal(t, service.PersonalDataHoldings(), declaration.Holdings,
		"what the module declares and what the disclosure is built from have to be the "+
			"same list, not two lists that agree today")
	// The declaration hands out a COPY. What comes back is gobit's own statement
	// about where a person is kept, and a caller that sorted or truncated it
	// would be editing that statement for every later reader.
	declaration.Holdings[0].Column = "tampered"
	assert.NotEqual(t, "tampered", customer.New(nil).PersonalData().Holdings[0].Column,
		"a caller was able to edit the declaration through the slice it was handed")
}

// TestEveryDeclaredTableIsDisclosedOrExempt proves that no declared table can
// fall out of a person's file unnoticed.
//
// The two lists are written by hand on purpose, exactly as notPersonalColumns
// is. A rule that inferred the answer would let the next table past on the
// grounds that it looked like the last one, and deciding is the entire point:
// the one table this module declares and does not disclose took an argument to
// settle, and the next one will too.
func TestEveryDeclaredTableIsDisclosedOrExempt(t *testing.T) {
	disclosed := map[string]bool{
		service.TableCustomer: true,
		service.TableAddress:  true,
	}

	declared := map[string]bool{}
	for _, holding := range customer.New(nil).PersonalData().Holdings {
		declared[holding.Table] = true
	}
	require.NotEmpty(t, declared, "the declaration is empty; the audit has gone blind")

	for table := range declared {
		_, exempt := undisclosedTables[table]
		assert.NotEqual(t, disclosed[table], exempt,
			"%s is declared and is either in both lists or in neither.\n"+
				"Either a person's file carries records from it — then it belongs in "+
				"disclosed, and Service.PersonalDataOf has to produce them — or it does "+
				"not, and it belongs in undisclosedTables with the reason written down.",
			table)
	}

	for table := range disclosed {
		assert.True(t, declared[table],
			"%s is disclosed and declares no column; a record with no declared field is "+
				"an empty row in somebody's file", table)
	}
	for table := range undisclosedTables {
		assert.True(t, declared[table],
			"%s is exempted from disclosure and is not declared at all; the exemption "+
				"answers a question nobody asked", table)
	}
}

// TestTheModuleIsFoundAsADiscloser mirrors the lookup the coordinator actually
// performs.
//
// The capability is OPTIONAL and found by type assertion over a
// [module.Module] value taken out of the registry (see
// internal/workflows/datasubject, FromContainer). A method whose name or
// signature drifted would break nothing: the module would keep compiling, the
// sweep would keep running, and the coordinator would enter this module in every
// dossier as Unresolvable — "we hold this and cannot tell whether it is yours" —
// about a person it can resolve by two handles. That answer is worse than an
// error because it reads like a considered one.
func TestTheModuleIsFoundAsADiscloser(t *testing.T) {
	var mod module.Module = customer.New(nil)

	_, ok := mod.(personaldata.Discloser)
	assert.True(t, ok, "the coordinator finds this capability by type assertion and by nothing else")

	_, ok = mod.(personaldata.Declarer)
	assert.True(t, ok, "a discloser that does not declare has nothing to derive its records from")
}

// TestTheDisclosedTablesAreRealTables holds the table constants against the
// migration, the way the declaration audit holds the columns against it.
//
// A table name is a string in three places — the declaration, the record a
// person reads and the SQL — and only the SQL fails loudly when it is wrong. A
// misspelled name in the other two produces a file that names a table nobody can
// find, which sends an auditor looking for data that does not exist.
func TestTheDisclosedTablesAreRealTables(t *testing.T) {
	tables := tablesOf(t, readMigration(t))

	for _, name := range []string{service.TableCustomer, service.TableAddress, service.TableGroup} {
		assert.Contains(t, tables, name, "%s is not created by the migration", name)
	}
}
