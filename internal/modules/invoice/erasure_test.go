package invoice_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/invoice"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// TestTheHolderNameIsTheModuleName pins two constants that cannot see each
// other.
//
// The service names the holder because the answer is built there, and it cannot
// read [invoice.ModuleName] because the module package imports the service —
// naming it in one place and reading it in the other would be an import cycle.
// So the two are held together here. A drift produces a report in which the
// invoice module never answered and a holder nobody registered did, and each
// half of that looks entirely normal on its own.
func TestTheHolderNameIsTheModuleName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, invoice.ModuleName, service.Holder)
}

// TestEveryRetainedColumnIsDeclared is the gate that keeps the refusal and the
// declaration from telling different stories.
//
// The two answers are built from separate lists in separate packages, and they
// have separate jobs: the refusal says what was kept about ONE person, the
// declaration says where this module keeps personal data at all. A column the
// refusal names and the declaration omits is the worse of the two failures —
// the declaration is what an embedder reads to decide where to look, so an
// omitted column is a place gobit has publicly admitted to keeping and that no
// audit will ever visit.
func TestEveryRetainedColumnIsDeclared(t *testing.T) {
	t.Parallel()

	declared := map[string]bool{}
	for _, holding := range invoice.New(invoice.Options{}).PersonalData().Holdings {
		declared[holding.Table+"."+holding.Column] = true
	}

	for _, column := range service.RetainedColumns() {
		assert.True(t, declared[column],
			"%s is reported as retained but is not declared in PersonalData", column)
	}
}

// TestADeclaredColumnTheRefusalNeverReachesSaysSo closes the direction
// TestEveryRetainedColumnIsDeclared leaves open.
//
// That test walks from the refusal to the declaration. This one walks the other
// way, and finds the six seller columns: declared as places a natural person is
// — for a sole trader the seller IS one — while [service.Service.Erase]
// resolves a subject through lower(buyer_email) and nothing else, so they are
// never searched and never appear in Kept. A sole trader whose address sits in
// seller_email is answered with zero rows and an empty Kept list, which is a
// true answer only if the declaration does not simultaneously imply that this
// module looks there.
//
// The fix that is being pinned here is a SENTENCE, so the test reads sentences:
// a declared column the refusal never names has to say in its own Why that
// Erase does not search it. Declaring without erasing is legitimate
// ([personaldata.Declarer] is a separate interface for exactly that reason);
// declaring without SAYING it is what leaves a reader believing in a reach this
// module does not have.
func TestADeclaredColumnTheRefusalNeverReachesSaysSo(t *testing.T) {
	t.Parallel()

	reported := map[string]bool{}
	for _, column := range service.RetainedColumns() {
		reported[column] = true
	}

	var unsearched []string

	for _, holding := range invoice.New(invoice.Options{}).PersonalData().Holdings {
		key := holding.Table + "." + holding.Column
		if reported[key] {
			continue
		}

		unsearched = append(unsearched, key)
		assert.Contains(t, holding.Why, "never searches",
			"%s is declared as a place a person is and the refusal never names it; "+
				"the declaration has to say that Erase does not look there", key)
	}

	assert.ElementsMatch(t, []string{
		"invoices.seller_name", "invoices.seller_tax_number", "invoices.seller_tax_office",
		"invoices.seller_email", "invoices.seller_address", "invoices.seller_country_code",
	}, unsearched, "the seller half is the only half this module cannot resolve a subject through")
}

// TestTheDeclarationNamesEveryPersonalColumnOfTheSchema reads the declaration
// against migration 000001 rather than against itself.
//
// The list is written out here on purpose. Deriving it from the same slice the
// declaration is built from would prove the declaration equals itself and
// nothing about the schema, which is the failure mode this repository has
// already met more than once. These fifteen are read off the CREATE TABLE
// statements in 000001: twelve party columns, the two jsonb-or-free-text fields
// and the operator's status_reason.
func TestTheDeclarationNamesEveryPersonalColumnOfTheSchema(t *testing.T) {
	t.Parallel()

	expected := map[string]personaldata.Kind{
		"invoices.buyer_name":          personaldata.Named,
		"invoices.buyer_tax_number":    personaldata.Named,
		"invoices.buyer_tax_office":    personaldata.Named,
		"invoices.buyer_email":         personaldata.Named,
		"invoices.buyer_address":       personaldata.Named,
		"invoices.buyer_country_code":  personaldata.Named,
		"invoices.seller_name":         personaldata.Named,
		"invoices.seller_tax_number":   personaldata.Named,
		"invoices.seller_tax_office":   personaldata.Named,
		"invoices.seller_email":        personaldata.Named,
		"invoices.seller_address":      personaldata.Named,
		"invoices.seller_country_code": personaldata.Named,
		"invoices.status_reason":       personaldata.Open,
		"invoices.metadata":            personaldata.Open,
		"invoice_lines.description":    personaldata.Open,
	}

	declaration := invoice.New(invoice.Options{}).PersonalData()
	assert.Equal(t, invoice.ModuleName, declaration.Holder)

	got := map[string]personaldata.Kind{}
	for _, holding := range declaration.Holdings {
		key := holding.Table + "." + holding.Column
		assert.NotEmpty(t, holding.Why, "%s is declared without saying what it holds", key)
		got[key] = holding.Kind
	}

	assert.Equal(t, expected, got)
}

// TestTheDeclarationDoesNotNeedRegister holds a property [personaldata.Declarer]
// states in its own words: a declaration is a property of the code rather than
// of the data.
//
// An audit reads it without a database, and a module whose Register failed must
// still be able to say what it holds. The module built here is never registered
// and has no pool, no service and no handler.
func TestTheDeclarationDoesNotNeedRegister(t *testing.T) {
	t.Parallel()

	assert.NotEmpty(t, invoice.New(invoice.Options{}).PersonalData().Holdings)
}

// TestAnUnregisteredModuleRefusesToEraseRatherThanAnswerZero draws the same
// line the service draws for a broken query.
//
// Without Register there is no service to ask. Answering Retained with a count
// of zero would be a well-formed report saying this person has no invoices —
// a claim a module that never opened a database is in no position to make.
func TestAnUnregisteredModuleRefusesToEraseRatherThanAnswerZero(t *testing.T) {
	t.Parallel()

	result, err := invoice.New(invoice.Options{}).
		Erase(context.Background(), personaldata.Subject{Email: "ada@example.com"})

	require.Error(t, err)
	assert.Equal(t, personaldata.Result{}, result)
}
