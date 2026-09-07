package service

import (
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// These tests are INSIDE the package because what they hold shut is unexported:
// the accessor tables of service/disclosure.go and the declaration they are
// derived from. ADR 0034 says a holder should derive the disclosure's field list
// from its declaration and, where it cannot, owes a test that keeps the two
// equal. This module derives the COLUMNS and writes the ACCESSORS by hand, so
// the seam that is left is exactly one — a declared column with nobody to read
// it, or a reader for a column nobody declares — and it is the seam below.

// tableAccessors is the accessor table of every table this module discloses,
// reduced to the column names it can read.
//
// It is written out here rather than derived because deriving it is the thing
// under test: a helper that walked the declaration to build this list would
// agree with the declaration by construction and would prove nothing.
func tableAccessors() map[string][]string {
	return map[string][]string{
		tableOrders:         columnsOf(orderValues),
		tableOrderLineItems: columnsOf(lineItemValues),
		tableOrderAddresses: columnsOf(addressValues),
		tableOrderReturns:   columnsOf(returnValues),
		tableOrderExchanges: columnsOf(exchangeValues),
		tableOrderClaims:    columnsOf(claimValues),
	}
}

// columnsOf lists the columns one accessor table can read, sorted.
func columnsOf[T any](values disclosureValues[T]) []string {
	return slices.Sorted(maps.Keys(values))
}

// TestTheDisclosureCanReadEveryDeclaredColumn is the test ADR 0034 asks for.
//
// It fails in both directions and both matter. A declared column with no
// accessor makes the dossier SHORT of something the declaration promised the
// controller, and the person receiving it cannot tell that from a column of hers
// that is empty. An accessor for a column nobody declares is the other half:
// were it ever reached it would hand over data the declaration said was not
// there, and a reader with no declaration behind it is that mistake waiting for
// somebody to add the record type.
func TestTheDisclosureCanReadEveryDeclaredColumn(t *testing.T) {
	t.Parallel()

	accessors := tableAccessors()

	declared := make(map[string][]string)
	for i := range personalColumns {
		holding := personalColumns[i].holding
		declared[holding.Table] = append(declared[holding.Table], holding.Column)
	}
	require.NotEmpty(t, declared, "the declaration is empty and this test approves anything")

	for table, columns := range declared {
		readable, known := accessors[table]
		require.True(t, known,
			"the declaration names table %q and the disclosure has no accessor table for it; "+
				"every row of it would be missing from the dossier", table)

		slices.Sort(columns)
		assert.Equal(t, columns, readable,
			"the declared columns of %s and the ones the disclosure can read have come apart", table)
	}

	for table := range accessors {
		assert.Contains(t, declared, table,
			"the disclosure can read %s and the declaration does not mention it; a disclosure "+
				"may not reach past the declaration", table)
	}
}

// TestARecordCarriesTheDeclaredColumnsInDeclarationOrder pins the shape one row
// turns into.
//
// The ORDER is asserted as well as the set. It is the order the declaration
// itself lists the columns in, which is what a controller holding the
// declaration beside the dossier reads down, and an order that moved between
// runs would make two documents about one person impossible to compare.
func TestARecordCarriesTheDeclaredColumnsInDeclarationOrder(t *testing.T) {
	t.Parallel()

	record, disclosed, err := recordOf(tableOrders, "order_1", models.Order{
		CustomerID: "cus_1",
		Email:      "person@example.com",
	}, orderValues)
	require.NoError(t, err)
	require.True(t, disclosed)

	columns := make([]string, 0, len(record.Fields))
	for _, field := range record.Fields {
		columns = append(columns, field.Column)
	}

	expected := make([]string, 0, len(columns))
	for i := range personalColumns {
		if personalColumns[i].holding.Table == tableOrders {
			expected = append(expected, personalColumns[i].holding.Column)
		}
	}

	assert.Equal(t, expected, columns)
	assert.Equal(t, "order_1", record.ID)
	assert.Equal(t, tableOrders, record.Table)
}

// TestAFieldCarriesTheDeclaredKind verifies the fact ADR 0034 puts ON the value
// rather than beside it.
//
// An Open value is one gobit has never inspected. If the Kind on the field could
// disagree with the declaration, a controller reviewing the dossier would vouch
// for a metadata blob nobody read.
func TestAFieldCarriesTheDeclaredKind(t *testing.T) {
	t.Parallel()

	record, disclosed, err := recordOf(tableOrders, "order_1", models.Order{
		Email:    "person@example.com",
		Metadata: map[string]any{"channel": "web"},
	}, orderValues)
	require.NoError(t, err)
	require.True(t, disclosed)

	kinds := make(map[string]personaldata.Kind, len(record.Fields))
	for _, field := range record.Fields {
		kinds[field.Column] = field.Kind
	}

	assert.Equal(t, personaldata.Named, kinds["email"],
		"gobit writes the buyer's address there itself")
	assert.Equal(t, personaldata.Open, kinds[columnMetadata],
		"the metadata is the embedder's and gobit does not look inside it (ADR 0029)")
}

// TestADeclaredColumnWithNoAccessorIsRefused proves the guard can fire.
//
// It is worth a test of its own because a protection that CANNOT fire is worse
// than none — this repository has already removed one such guard, defended by a
// claim that turned out to be false (ADR 0033's amendment). This one fires the
// day somebody adds a holding to the declaration and forgets the accessor
// beside it, and what it does then is refuse rather than quietly hand over a
// dossier that is one column short of what the declaration promises.
func TestADeclaredColumnWithNoAccessorIsRefused(t *testing.T) {
	t.Parallel()

	// Everything the orders table declares, minus the e-mail: the shape of a
	// forgotten accessor.
	crippled := maps.Clone(orderValues)
	delete(crippled, "email")

	_, _, err := recordOf(tableOrders, "order_1", models.Order{CustomerID: "cus_1"}, crippled)

	require.Error(t, err)
	assert.Equal(t, CodeDisclosureColumnUnread, errors.CodeOf(err))
	assert.Equal(t, errors.KindInternal, errors.KindOf(err),
		"the declaration and the disclosure are both this module's code; their disagreement is "+
			"a fault of ours and not of the caller: %v", err)
	assert.Contains(t, err.Error(), "orders.email",
		"the fault has to name the column, or nobody can fix it")
}

// TestAnEmptyRowIsDisclosedOnlyWhereGobitWritesThePersonItself is the rule that
// decides which rows become records, checked against the declaration it is read
// off.
//
// The two halves are one decision. An emptied ORDER or ADDRESS row is what an
// erased order looks like, and dropping it would answer "there is nothing here"
// about a row that exists and is hers. An after-sales row or a line whose whole
// declaration is free text somebody never typed holds nothing about anybody, and
// twenty of them would bury the one note that has content.
func TestAnEmptyRowIsDisclosedOnlyWhereGobitWritesThePersonItself(t *testing.T) {
	t.Parallel()

	assert.True(t, rowIsDisclosed(tableOrders))
	assert.True(t, rowIsDisclosed(tableOrderAddresses))
	assert.False(t, rowIsDisclosed(tableOrderLineItems))
	assert.False(t, rowIsDisclosed(tableOrderReturns))
	assert.False(t, rowIsDisclosed(tableOrderExchanges))
	assert.False(t, rowIsDisclosed(tableOrderClaims))

	// An address with every declared column empty is still a record, and every
	// one of its fields says "nothing is held here" rather than being absent.
	record, disclosed, err := recordOf(tableOrderAddresses, "order_1/oaddr_1",
		models.OrderAddress{}, addressValues)
	require.NoError(t, err)
	require.True(t, disclosed)
	require.NotEmpty(t, record.Fields)
	for _, field := range record.Fields {
		assert.Nil(t, field.Value, "%s holds nothing and must not be reported as holding \"\"",
			field.Column)
	}

	// An untouched return record is not a record at all.
	_, disclosed, err = recordOf(tableOrderReturns, "order_1/ret_1", models.Return{}, returnValues)
	require.NoError(t, err)
	assert.False(t, disclosed)
}

// TestAnEmptyValueIsNilAndNotTheEmptyString covers the two shapes "nothing" can
// arrive in.
//
// NULL and "" are one answer to the person — this is not kept about you — and a
// field carrying "" would say the opposite. The jsonb half is the one that is
// easy to miss: the column is NOT NULL with a '{}' default, so a caller who
// wrote nothing arrives as an empty map rather than as a missing value.
func TestAnEmptyValueIsNilAndNotTheEmptyString(t *testing.T) {
	t.Parallel()

	assert.Nil(t, textValue(""))
	assert.Equal(t, "Ayse", textValue("Ayse"))
	assert.Nil(t, jsonValue(nil))
	assert.Nil(t, jsonValue(map[string]any{}))
	assert.Equal(t, map[string]any{"channel": "web"}, jsonValue(map[string]any{"channel": "web"}))
}
