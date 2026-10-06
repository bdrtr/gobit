package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// journalKinds reads every JournalKind the models declare, from their source,
// so a kind added there is audited without anyone listing it here.
func journalKinds(t *testing.T) []models.JournalKind {
	t.Helper()

	const dir = "../models"
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "the order module's models could not be listed")

	var kinds []models.JournalKind
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, 0)
		require.NoError(t, parseErr, "%s could not be parsed", name)
		kinds = append(kinds, declaredKinds(t, file)...)
	}

	return kinds
}

// declaredKinds is the JournalKind constants one file declares.
func declaredKinds(t *testing.T, file *ast.File) []models.JournalKind {
	t.Helper()

	var kinds []models.JournalKind
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if typ, ok := value.Type.(*ast.Ident); !ok || typ.Name != "JournalKind" {
				continue
			}
			for _, expr := range value.Values {
				literal, ok := expr.(*ast.BasicLit)
				require.True(t, ok && literal.Kind == token.STRING,
					"a JournalKind is declared with something other than a string literal")
				kind, unquoteErr := strconv.Unquote(literal.Value)
				require.NoError(t, unquoteErr)
				kinds = append(kinds, models.JournalKind(kind))
			}
		}
	}

	return kinds
}

// TestOnlyADearerDeliveryAndAnExchangeChargeAfterTheSale holds ADR 0394 in
// the order journal: after the sale, revenue is credited, and the buyer's
// receivable debited, for a dearer delivery (ADR 0200) and an exchange's
// funding (ADR 0203) alone, so no kind books a sold line's price raised.
//
// The population is every JournalKind the models declare, read from their
// source, and each is booked through [journalEntry] itself; a kind the
// journal cannot book fails here rather than passing unread. The sale and its
// cancellation are the two kinds left out: they ARE the sale.
//
// A document's tax correction and its voiding (ADR 0419) are booked for every
// act and document kind a document can carry, and held to touching neither the
// buyer's receivable nor any account but tax_payable and the act's own: they
// move tax a document printed between the two, and charge nobody.
func TestOnlyADearerDeliveryAndAnExchangeChargeAfterTheSale(t *testing.T) {
	t.Parallel()

	kinds := journalKinds(t)
	for _, floor := range []models.JournalKind{
		models.JournalOrderPlaced, models.JournalOrderCanceled,
		models.JournalDeliveryUpgraded, models.JournalExchangeFunded,
	} {
		require.Contains(t, kinds, floor, "the kinds were not read from the models; the audit is BLIND")
	}

	var credited, charged []models.JournalKind
	for _, kind := range kinds {
		if kind == models.JournalOrderPlaced || kind == models.JournalOrderCanceled {
			continue
		}
		if kind == models.JournalTaxCorrected || kind == models.JournalTaxCorrectionVoided {
			auditCorrection(t, kind)
			continue
		}
		entry, err := journalEntry(&models.JournalFact{
			ID: "fact_1", Kind: kind, OrderID: "order_1", CurrencyCode: "eur", Amount: 100,
		})
		require.NoError(t, err, "the journal cannot book %q, so this audit cannot read it", kind)
		require.NotEmpty(t, entry.Lines, "the journal booked %q with no lines", kind)

		for _, line := range entry.Lines {
			if line.Credit > 0 && line.Account != models.AccountReceivable && !slices.Contains(credited, kind) {
				credited = append(credited, kind)
			}
			if line.Debit > 0 && line.Account == models.AccountReceivable && !slices.Contains(charged, kind) {
				charged = append(charged, kind)
			}
		}
	}
	slices.Sort(credited)
	slices.Sort(charged)

	want := []models.JournalKind{models.JournalDeliveryUpgraded, models.JournalExchangeFunded}
	const why = "A placed order is not charged more for a line it sold (ADR 0394): the buyer " +
		"buys the line again at checkout, or the shop bears it."
	assert.Equal(t, want, credited,
		"the order journal credits an account other than receivable after the sale for a kind "+
			"other than a dearer delivery and an exchange's funding. "+why)
	assert.Equal(t, want, charged,
		"the order journal debits the buyer's receivable after the sale for a kind other than "+
			"a dearer delivery and an exchange's funding. "+why)
	assert.Equal(t, models.AccountShipping, chargedTo[models.JournalDeliveryUpgraded])
	assert.Equal(t, models.AccountSales, chargedTo[models.JournalExchangeFunded])
}

// auditCorrection books a tax correction of the kind for every act a document
// can name, as the document kind that act is documented with (ADR 0406), and
// holds each to the two accounts it moves tax between.
func auditCorrection(t *testing.T, kind models.JournalKind) {
	t.Helper()

	for _, act := range DocumentedActs {
		document, account := documentRefund, givenBackTo[act]
		if act == models.JournalDeliveryUpgraded {
			document, account = documentSale, chargedTo[act]
		}
		entry, err := journalEntry(&models.JournalFact{
			ID: "fact_1", Kind: kind, OrderID: "order_1", CurrencyCode: "eur", Amount: 100,
			ActKind: act, DocumentKind: document,
		})
		require.NoError(t, err, "the journal cannot book %q on a %s document for %q", kind, document, act)
		require.Len(t, entry.Lines, 2, "%q for %q", kind, act)
		for _, line := range entry.Lines {
			assert.NotEqual(t, models.AccountReceivable, line.Account,
				"%q for %q touches the buyer's receivable: a correction charges nobody", kind, act)
			assert.Contains(t, []models.JournalAccount{models.AccountTaxPayable, account}, line.Account,
				"%q for %q moves tax to an account that is neither tax_payable nor the act's", kind, act)
		}
	}
}
