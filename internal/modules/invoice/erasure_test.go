package invoice_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/invoice"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
	"github.com/bdrtr/gobit/internal/schemaaudit"
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

// notPersonalColumns lists, per table, the columns that hold nothing about a
// person; with the declaration it has to judge every column the migrations
// leave (D189).
//
//   - IDENTIFIERS AND NUMBERING (id, series_id, invoice_id, invoice_line_id,
//     rate_id, number, position, the series' prefix, year and last_number,
//     the provider's id and its external id). They name a document, a line or
//     a series, and the person is in the party columns beside them.
//   - MONEY AND TAX (every amount, total, quantity, rate and compound flag,
//     currency_code, prices_include_tax). They describe the sale.
//   - STATE AND STAMPS (kind, status and every *_at). What the document is and
//     when it moved.
//   - AMENDMENT (amends_invoice_id, amendment_reason, amendment_key and
//     amends_line_id, ADR 0406). They name the sale a document amends, why,
//     the act it documents and the sale row a row moves; the key is a journal
//     kind and an act's id.
var notPersonalColumns = map[string][]string{
	"invoices": {
		"id", "number", "series_id", "kind", "status", "currency_code", "subtotal",
		"discount_total", "tax_total", "total", "issued_at", "provider_id", "external_id",
		"created_at", "updated_at", "prices_include_tax",
		"amends_invoice_id", "amendment_reason", "amendment_key",
	},
	"invoice_lines": {
		"id", "invoice_id", "position", "quantity", "unit_price", "subtotal", "discount_total",
		"tax_total", "total", "tax_rate_bps", "amends_line_id",
	},
	"invoice_line_taxes": {
		"id", "invoice_line_id", "position", "rate_id", "rate_bps", "compound",
		"taxable_amount", "tax_amount",
	},
	"invoice_series": {
		"id", "prefix", "year", "last_number", "created_at", "updated_at",
	},
}

// TestTheDeclarationCoversEveryColumnOfTheSchema holds the declaration to the
// columns the migrations leave, not to a list read off one of them: until D189
// the list below was the whole audit, and it had been read off 000001 while
// 000003 added the column Erase matches a person by.
func TestTheDeclarationCoversEveryColumnOfTheSchema(t *testing.T) {
	t.Parallel()

	module := invoice.New(invoice.Options{})
	schemaaudit.Cover(t, module.Migrations(), module.PersonalData(), notPersonalColumns)
}

// TestTheDeclarationNamesEveryPersonalColumnOfTheSchema pins the KIND of each
// declared column.
//
// The list is written out here on purpose. Deriving it from the same slice the
// declaration is built from would prove the declaration equals itself; which
// columns exist is held by [TestTheDeclarationCoversEveryColumnOfTheSchema],
// and what this adds is the judgement on each: the party columns and the
// folded address are Named, the free text is Open.
func TestTheDeclarationNamesEveryPersonalColumnOfTheSchema(t *testing.T) {
	t.Parallel()

	expected := map[string]personaldata.Kind{
		"invoices.buyer_name":          personaldata.Named,
		"invoices.buyer_tax_number":    personaldata.Named,
		"invoices.buyer_tax_office":    personaldata.Named,
		"invoices.buyer_email":         personaldata.Named,
		"invoices.buyer_email_folded":  personaldata.Named,
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
