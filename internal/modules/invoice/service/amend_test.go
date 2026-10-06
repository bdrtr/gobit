package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// The rules ADR 0406 gives a document amending a sale, held against the fake;
// the lock, the sums over live documents and the index are held against
// Postgres in the module's integration tests.

// issuedSale issues validIssue and returns it.
func issuedSale(t *testing.T, svc *service.Service) models.Invoice {
	t.Helper()

	sale, err := svc.Issue(context.Background(), validIssue())
	require.NoError(t, err)

	return sale
}

// refundOf gives back total, tax included, on the sale's first row as one
// unit.
func refundOf(sale models.Invoice, total, tax int64) service.IssueInput {
	in := validIssue()
	in.Kind = models.KindRefund
	in.Buyer = models.Party{}
	in.Amends = sale.ID
	in.AmendmentReason = models.ReasonReturned
	in.Lines = []service.LineInput{{
		Description: "1 x Red T-Shirt", Quantity: 1, UnitPrice: total - tax, Subtotal: total - tax,
		TaxRateBps: sale.Lines[0].TaxRateBps, TaxTotal: tax, Total: total,
		AmendsLineID: sale.Lines[0].ID,
	}}
	in.Subtotal, in.TaxTotal, in.Total = total-tax, tax, total

	return in
}

// TestAnAmendmentNeedsNoBuyerOfItsOwn: an amendment sends no buyer, and the
// name check every other document passes is not asked of it.
func TestAnAmendmentNeedsNoBuyerOfItsOwn(t *testing.T) {
	t.Parallel()

	svc := newService(newFakeRepo())
	sale := issuedSale(t, svc)

	_, err := svc.Issue(context.Background(), refundOf(sale, 1200, 200))
	require.NoError(t, err, "an amendment is issued without a buyer of its own")
}

// TestAnAmendmentPrintsItsSalesBuyer: the buyer is the sale's, copied with the
// sale locked, and a buyer sent with an amendment is refused rather than
// printed in its place.
func TestAnAmendmentPrintsItsSalesBuyer(t *testing.T) {
	t.Parallel()

	svc := newService(newFakeRepo())
	sale := issuedSale(t, svc)

	refund, err := svc.Issue(context.Background(), refundOf(sale, 1200, 200))
	require.NoError(t, err)
	assert.Equal(t, sale.Buyer, refund.Buyer)

	other := refundOf(sale, 1200, 200)
	other.Buyer = models.Party{Name: "Somebody Else"}
	_, err = svc.Issue(context.Background(), other)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "got %v", err)
}

// TestARefundNamesItsSaleAndRows: a refund without a sale, a refund row
// without a sale row, and a row naming a sale row on a document that amends
// none are refused before a number is taken.
func TestARefundNamesItsSaleAndRows(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	svc := newService(repo)
	sale := issuedSale(t, svc)

	// A refund otherwise whole, so the sale it names is all it lacks.
	noSale := refundOf(sale, 1200, 200)
	noSale.Amends, noSale.AmendmentReason = "", ""
	noSale.Lines[0].AmendsLineID = ""
	noSale.Buyer = models.Party{Name: "A Customer"}
	noRow := refundOf(sale, 1200, 200)
	noRow.Lines[0].AmendsLineID = ""
	stray := validIssue()
	stray.Lines[0].AmendsLineID = sale.Lines[0].ID

	for name, in := range map[string]service.IssueInput{
		"a refund naming no sale": noSale, "a refund row naming no row": noRow,
		"a sale row naming a row of no amended sale": stray,
	} {
		_, err := svc.Issue(context.Background(), in)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalid(err), "%s: %v", name, err)
	}
	assert.Equal(t, 1, repo.documentCount(), "nothing but the sale was written")
}

// TestAReasonFitsItsKind: a price raised is a sale, a return or a price
// lowered a refund, a reason comes with its sale and a sale with its reason.
func TestAReasonFitsItsKind(t *testing.T) {
	t.Parallel()

	svc := newService(newFakeRepo())
	sale := issuedSale(t, svc)
	charge := func(reason models.AmendmentReason) service.IssueInput {
		in := validIssue()
		in.Buyer = models.Party{}
		in.Amends, in.AmendmentReason = sale.ID, reason

		return in
	}
	refund := func(reason models.AmendmentReason) service.IssueInput {
		in := refundOf(sale, 120, 20)
		in.AmendmentReason = reason

		return in
	}

	for name, in := range map[string]service.IssueInput{
		"a refund raising a price": refund(models.ReasonPriceRaised),
		"a sale for a return":      charge(models.ReasonReturned),
		"a sale lowering a price":  charge(models.ReasonPriceLowered),
		"an unknown reason":        refund("discounted"),
		"a sale with no reason":    charge(""),
	} {
		_, err := svc.Issue(context.Background(), in)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalid(err), "%s: %v", name, err)
	}

	reasonAlone := validIssue()
	reasonAlone.AmendmentReason = models.ReasonPriceRaised
	_, err := svc.Issue(context.Background(), reasonAlone)
	require.Error(t, err, "a reason without a sale")
	assert.True(t, errors.IsInvalid(err), "got %v", err)

	for name, in := range map[string]service.IssueInput{
		"a sale raising a price": charge(models.ReasonPriceRaised),
		"a return":               refund(models.ReasonReturned),
		"a price lowered":        refund(models.ReasonPriceLowered),
	} {
		_, err := svc.Issue(context.Background(), in)
		require.NoError(t, err, name)
	}
}

// TestAKeyNeedsASale: an act's key is carried by an amendment, and a blank
// one names no act.
func TestAKeyNeedsASale(t *testing.T) {
	t.Parallel()

	svc := newService(newFakeRepo())
	sale := issuedSale(t, svc)

	alone := validIssue()
	alone.AmendmentKey = "credit_line:ocl_1"
	blank := refundOf(sale, 120, 20)
	blank.AmendmentKey = "  "

	for name, in := range map[string]service.IssueInput{"a key alone": alone, "a blank key": blank} {
		_, err := svc.Issue(context.Background(), in)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalid(err), "%s: %v", name, err)
	}
}

// TestAKeyNamesAnActTheJournalPlaces: the order journal books a document's tax
// against the act its key names and refuses every read of a window holding a
// document it cannot place (ADR 0419), so a key that is not "<kind>:<act id>"
// with a kind the journal places is refused before any document carries it,
// and every kind it places is taken.
func TestAKeyNamesAnActTheJournalPlaces(t *testing.T) {
	t.Parallel()

	for _, key := range []string{
		"exchange_funded:exch_1", "exchange_refunded:re_1", "order_placed:order_1", "credit_1",
		"credit_line:", "credit_line:  ", ":ocl_1", "Credit_line:ocl_1",
	} {
		svc := newService(newFakeRepo())
		in := refundOf(issuedSale(t, svc), 120, 20)
		in.AmendmentKey = key

		_, err := svc.Issue(context.Background(), in)
		require.Error(t, err, "%q is a key the journal cannot place", key)
		assert.True(t, errors.IsInvalid(err), "%q: %v", key, err)
		assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
	}

	for _, kind := range models.AmendmentActKinds {
		svc := newService(newFakeRepo())
		sale := issuedSale(t, svc)
		in := refundOf(sale, 120, 20)
		if kind == "delivery_upgraded" {
			in = chargeOf(sale, 120, 20, false)
		}
		in.AmendmentKey = kind + ":act_1"

		_, err := svc.Issue(context.Background(), in)
		require.NoError(t, err, "%s is a kind the journal places", kind)
	}
}

// TestAnAmendmentAmendsOnlyALiveSale walks every refusal of the sale named and
// of the rows named, and every status an amendment may follow.
func TestAnAmendmentAmendsOnlyALiveSale(t *testing.T) {
	t.Parallel()

	for _, status := range []models.Status{models.StatusIssued, models.StatusSent, models.StatusAccepted} {
		repo := newFakeRepo()
		svc := newService(repo)
		sale := issuedSale(t, svc)
		sale.Status = status
		repo.seed(sale)

		_, err := svc.Issue(context.Background(), refundOf(sale, 1200, 200))
		require.NoError(t, err, "a %s sale stands and is amended", status)
	}

	for _, status := range []models.Status{models.StatusRejected, models.StatusCanceled} {
		repo := newFakeRepo()
		svc := newService(repo)
		sale := issuedSale(t, svc)
		sale.Status = status
		repo.seed(sale)

		_, err := svc.Issue(context.Background(), refundOf(sale, 1200, 200))
		require.Error(t, err, "a %s sale took no effect", status)
		assert.Equal(t, service.CodeAmendsVoid, errors.CodeOf(err))
	}

	repo := newFakeRepo()
	svc := newService(repo)
	sale := issuedSale(t, svc)
	stacked, err := svc.Issue(context.Background(), stackedIssue())
	require.NoError(t, err)
	// A sale amending the sale is a sale too, so only its amending one
	// refuses it.
	raise := validIssue()
	raise.Buyer = models.Party{}
	raise.Amends, raise.AmendmentReason = sale.ID, models.ReasonPriceRaised
	raise.Lines[0].AmendsLineID = sale.Lines[0].ID
	amending, err := svc.Issue(context.Background(), raise)
	require.NoError(t, err)

	ofAmendment := refundOf(amending, 120, 20)
	otherCurrency := refundOf(sale, 120, 20)
	otherCurrency.CurrencyCode = "EUR"
	otherConvention := refundOf(sale, 120, 20)
	otherConvention.PricesIncludeTax = true
	otherConvention.Lines[0].UnitPrice = 120
	otherRate := refundOf(sale, 120, 20)
	otherRate.Lines[0].TaxRateBps = 1000
	notItsRow := refundOf(sale, 120, 20)
	notItsRow.Lines[0].AmendsLineID = stacked.Lines[0].ID
	otherComponents := refundOf(stacked, 113, 13)
	otherComponents.Lines[0].TaxRateBps = 500
	otherComponents.Lines[0].TaxComponents = []service.LineTaxInput{
		{RateBps: 500, TaxableAmount: 100, TaxAmount: 5},
		{RateBps: 900, Compound: true, TaxableAmount: 105, TaxAmount: 8},
	}
	otherCompound := refundOf(stacked, 113, 13)
	otherCompound.Lines[0].TaxRateBps = 500
	otherCompound.Lines[0].TaxComponents = []service.LineTaxInput{
		{RateBps: 500, TaxableAmount: 100, TaxAmount: 5},
		{RateBps: 800, TaxableAmount: 100, TaxAmount: 8},
	}
	sameComponents := refundOf(stacked, 113, 13)
	sameComponents.Lines[0].TaxRateBps = 500
	sameComponents.Lines[0].TaxComponents = []service.LineTaxInput{
		{RateBps: 500, TaxableAmount: 100, TaxAmount: 5},
		{RateBps: 800, Compound: true, TaxableAmount: 105, TaxAmount: 8},
	}

	for name, in := range map[string]service.IssueInput{
		"an amendment of an amendment": ofAmendment, "another currency": otherCurrency,
		"another price convention": otherConvention, "another rate": otherRate,
		"a row of another document": notItsRow, "another rate in the stack": otherComponents,
		"a compound rate printed simple": otherCompound,
	} {
		_, err := svc.Issue(context.Background(), in)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalid(err), "%s: %v", name, err)
	}

	_, err = svc.Issue(context.Background(), sameComponents)
	require.NoError(t, err, "a stacked row is given back at its own rates")
}

// TestARowGivesBackNoMoreThanItCarriedOnTheFake holds the ceiling the service
// computes: per row, in amount and in tax, equality fitting and a charge
// raising it. The sums over live documents are the integration test's.
func TestARowGivesBackNoMoreThanItCarriedOnTheFake(t *testing.T) {
	t.Parallel()

	svc := newService(newFakeRepo())
	sale := issuedSale(t, svc)

	_, err := svc.Issue(context.Background(), refundOf(sale, 1200, 200))
	require.NoError(t, err)
	_, err = svc.Issue(context.Background(), refundOf(sale, 1201, 200))
	require.Error(t, err, "the row has 1200 left")
	assert.Equal(t, service.CodeAmendmentExceedsSale, errors.CodeOf(err))
	_, err = svc.Issue(context.Background(), refundOf(sale, 1200, 201))
	require.Error(t, err, "the row has 200 tax left")
	assert.Equal(t, service.CodeAmendmentExceedsSale, errors.CodeOf(err))
	_, err = svc.Issue(context.Background(), refundOf(sale, 1200, 200))
	require.NoError(t, err, "what is left fits exactly")

	charge := validIssue()
	charge.Buyer = models.Party{}
	charge.Amends, charge.AmendmentReason = sale.ID, models.ReasonPriceRaised
	charge.Lines[0].Quantity, charge.Lines[0].UnitPrice, charge.Lines[0].Subtotal = 1, 100, 100
	charge.Lines[0].TaxTotal, charge.Lines[0].Total = 20, 120
	charge.Lines[0].AmendsLineID = sale.Lines[0].ID
	charge.Subtotal, charge.TaxTotal, charge.Total = 100, 20, 120
	_, err = svc.Issue(context.Background(), charge)
	require.NoError(t, err)
	_, err = svc.Issue(context.Background(), refundOf(sale, 120, 20))
	require.NoError(t, err, "a charge on the row raises what it can give back")

	fresh := issuedSale(t, svc)
	twice := refundOf(fresh, 1300, 200)
	twice.Lines = append(twice.Lines, twice.Lines[0])
	twice.Subtotal, twice.TaxTotal, twice.Total = 2200, 400, 2600
	_, err = svc.Issue(context.Background(), twice)
	require.Error(t, err, "two rows of one document moving one sale row are summed: 2600 on 2400")
	assert.Equal(t, service.CodeAmendmentExceedsSale, errors.CodeOf(err))
}

// TestAStackedRowGivesBackNoMoreUnderOneRate: the row's tax can fit while one
// of its rates gives back more than it charged.
func TestAStackedRowGivesBackNoMoreUnderOneRate(t *testing.T) {
	t.Parallel()

	svc := newService(newFakeRepo())
	sale, err := svc.Issue(context.Background(), stackedIssue())
	require.NoError(t, err)

	skewed := refundOf(sale, 368, 268)
	skewed.Lines[0].TaxRateBps = 500
	skewed.Lines[0].TaxComponents = []service.LineTaxInput{
		{RateBps: 500, TaxableAmount: 2000, TaxAmount: 101},
		{RateBps: 800, Compound: true, TaxableAmount: 2100, TaxAmount: 167},
	}
	_, err = svc.Issue(context.Background(), skewed)
	require.Error(t, err)
	assert.Equal(t, service.CodeAmendmentExceedsSale, errors.CodeOf(err))
}

// TestASaleWithLiveAmendmentsIsNotCanceled: a sale is canceled once nothing
// amending it stands, and a rejection is recorded whatever amends it.
func TestASaleWithLiveAmendmentsIsNotCanceled(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newService(newFakeRepo())
	sale := issuedSale(t, svc)
	refund, err := svc.Issue(ctx, refundOf(sale, 120, 20))
	require.NoError(t, err)

	_, err = svc.MoveStatus(ctx, sale.ID, service.MoveInput{To: models.StatusCanceled, Reason: "mistake"})
	require.Error(t, err)
	assert.Equal(t, service.CodeHasLiveAmendments, errors.CodeOf(err))

	_, err = svc.MoveStatus(ctx, refund.ID, service.MoveInput{To: models.StatusCanceled, Reason: "mistake"})
	require.NoError(t, err, "an amendment itself is canceled like any document")
	_, err = svc.MoveStatus(ctx, sale.ID, service.MoveInput{To: models.StatusCanceled, Reason: "mistake"})
	require.NoError(t, err, "nothing amending it stands")
}

// chargeOf raises the sale's first row by total, tax included, as one unit;
// with added set it is a row the sale did not have.
func chargeOf(sale models.Invoice, total, tax int64, added bool) service.IssueInput {
	in := refundOf(sale, total, tax)
	in.Kind, in.AmendmentReason = models.KindSale, models.ReasonPriceRaised
	if added {
		in.Lines[0].AmendsLineID, in.Lines[0].TaxRateBps = "", 0
	}

	return in
}

// TestACanceledChargeLeavesNoRefundAboveItsRow: a charge a refund relies on
// is withdrawn only once the refund is, a charge whose added row a refund
// gave back is held the same way, and a rejection is recorded whatever it
// uncovers.
func TestACanceledChargeLeavesNoRefundAboveItsRow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newService(newFakeRepo())
	sale := issuedSale(t, svc)
	_, err := svc.Issue(ctx, refundOf(sale, 1200, 200))
	require.NoError(t, err)
	charge, err := svc.Issue(ctx, chargeOf(sale, 120, 20, false))
	require.NoError(t, err)
	relying, err := svc.Issue(ctx, refundOf(sale, 1320, 220))
	require.NoError(t, err, "the row and its charge: 2520 with 420 tax")

	cancel := service.MoveInput{To: models.StatusCanceled, Reason: "issued twice"}
	_, err = svc.MoveStatus(ctx, charge.ID, cancel)
	require.Error(t, err, "without the charge the row would have given back 2520 of 2400")
	assert.Equal(t, service.CodeAmendmentExceedsSale, errors.CodeOf(err))

	_, err = svc.MoveStatus(ctx, relying.ID, cancel)
	require.NoError(t, err)
	_, err = svc.MoveStatus(ctx, charge.ID, cancel)
	require.NoError(t, err, "what is left fits the row alone")

	added, err := svc.Issue(ctx, chargeOf(sale, 1500, 0, true))
	require.NoError(t, err)
	_, err = svc.Issue(ctx, refundOf(added, 1, 0))
	require.Error(t, err, "a refund amends the sale, not the charge")
	given := refundOf(sale, 1500, 0)
	given.Lines[0].AmendsLineID, given.Lines[0].TaxRateBps = added.Lines[0].ID, 0
	_, err = svc.Issue(ctx, given)
	require.NoError(t, err)
	_, err = svc.MoveStatus(ctx, added.ID, cancel)
	require.Error(t, err, "a refund gave back on the row the charge added")
	assert.Equal(t, service.CodeAmendmentExceedsSale, errors.CodeOf(err))

	_, err = svc.MoveStatus(ctx, added.ID, service.MoveInput{To: models.StatusSent})
	require.NoError(t, err)
	_, err = svc.MoveStatus(ctx, added.ID, service.MoveInput{To: models.StatusRejected, Reason: "refused"})
	require.NoError(t, err, "a rejection is the receiving side's fact and is recorded")
}

// TestARefundGivesBackFromARowAChargeAdded: a row a live charge added is the
// refund's to name and holds its own ceiling; a row of a charge no longer
// live is no row at all.
func TestARefundGivesBackFromARowAChargeAdded(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newService(newFakeRepo())
	sale := issuedSale(t, svc)
	added, err := svc.Issue(ctx, chargeOf(sale, 1500, 0, true))
	require.NoError(t, err)
	fromAdded := func(total int64) service.IssueInput {
		in := refundOf(sale, total, 0)
		in.Lines[0].AmendsLineID, in.Lines[0].TaxRateBps = added.Lines[0].ID, 0
		in.AmendmentReason = models.ReasonPriceLowered

		return in
	}

	_, err = svc.Issue(ctx, fromAdded(1500))
	require.NoError(t, err, "the delivery changed back is given back from the row its dearer one added")
	_, err = svc.Issue(ctx, fromAdded(1))
	require.Error(t, err)
	assert.Equal(t, service.CodeAmendmentExceedsSale, errors.CodeOf(err))

	other, err := svc.Issue(ctx, chargeOf(sale, 700, 0, true))
	require.NoError(t, err)
	_, err = svc.MoveStatus(ctx, other.ID, service.MoveInput{To: models.StatusCanceled, Reason: "typed twice"})
	require.NoError(t, err)
	dead := refundOf(sale, 700, 0)
	dead.Lines[0].AmendsLineID, dead.Lines[0].TaxRateBps = other.Lines[0].ID, 0
	_, err = svc.Issue(ctx, dead)
	require.Error(t, err, "a canceled charge's row is no row")
	assert.True(t, errors.IsInvalid(err), "got %v", err)

	given, err := svc.Issue(ctx, refundOf(sale, 120, 20))
	require.NoError(t, err)
	ofRefund := refundOf(sale, 120, 20)
	ofRefund.Lines[0].AmendsLineID = given.Lines[0].ID
	_, err = svc.Issue(ctx, ofRefund)
	require.Error(t, err, "a refund's row is no row to give back from")
	assert.True(t, errors.IsInvalid(err), "got %v", err)
}

// TestAnAmendmentMovesNoNegativeAmount: an amending row of either kind and on
// every route moves money one way, so a refund cannot charge its row.
func TestAnAmendmentMovesNoNegativeAmount(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	svc := newService(repo)
	sale := issuedSale(t, svc)

	negative := refundOf(sale, 0, 0)
	negative.Lines[0].UnitPrice, negative.Lines[0].Subtotal = 0, 0
	negative.Lines[0].TaxTotal, negative.Lines[0].Total = -1000, -1000
	negative.Subtotal, negative.TaxTotal, negative.Total = 0, -1000, -1000
	discounted := refundOf(sale, 0, 0)
	discounted.Lines[0].UnitPrice, discounted.Lines[0].Subtotal = 100, 100
	discounted.Lines[0].DiscountTotal, discounted.Lines[0].TaxTotal, discounted.Lines[0].Total = 150, 60, 10
	discounted.Subtotal, discounted.DiscountTotal, discounted.TaxTotal, discounted.Total = 100, 150, 60, 10
	price := refundOf(sale, 0, 0)
	price.Lines[0].UnitPrice, price.Lines[0].Subtotal, price.Lines[0].Total = -50, -50, -50
	price.Subtotal, price.Total = -50, -50
	stacked, err := svc.Issue(context.Background(), stackedIssue())
	require.NoError(t, err)
	rate := refundOf(stacked, 113, 13)
	rate.Lines[0].TaxRateBps = 500
	rate.Lines[0].TaxComponents = []service.LineTaxInput{
		{RateBps: 500, TaxableAmount: -100, TaxAmount: 0},
		{RateBps: 800, Compound: true, TaxableAmount: 105, TaxAmount: 13},
	}
	charge := chargeOf(sale, 0, 0, false)
	charge.Lines[0].TaxTotal, charge.Lines[0].Total = -1000, -1000
	charge.TaxTotal, charge.Total = -1000, -1000

	for name, in := range map[string]service.IssueInput{
		"a negative tax and total": negative, "a discount above the subtotal": discounted,
		"a negative unit price": price, "a negative component base": rate, "a charge of a negative amount": charge,
	} {
		_, err := svc.Issue(context.Background(), in)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalid(err), "%s: %v", name, err)
	}
	assert.Equal(t, 2, repo.documentCount(), "nothing but the two sales was written")
}

// TestASaleNamedBySpacesIsRefused: a blank sale named is no amendment, one of
// spaces alone names nothing and is refused before any read, and the name is
// read trimmed.
func TestASaleNamedBySpacesIsRefused(t *testing.T) {
	t.Parallel()

	svc := newService(newFakeRepo())
	sale := issuedSale(t, svc)

	spaces := refundOf(sale, 120, 20)
	spaces.Amends = "   "
	_, err := svc.Issue(context.Background(), spaces)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "refused as input, not looked up: %v", err)
	plain := validIssue()
	plain.Amends = "   "
	_, err = svc.Issue(context.Background(), plain)
	require.Error(t, err, "a sale naming spaces is refused, not issued as one that amends nothing")
	assert.True(t, errors.IsInvalid(err), "got %v", err)

	padded := refundOf(sale, 120, 20)
	padded.Amends = " " + sale.ID + " "
	refund, err := svc.Issue(context.Background(), padded)
	require.NoError(t, err)
	assert.Equal(t, sale.ID, refund.AmendsInvoiceID)
}

// TestARateRemembersWhatItGaveBack: a stacked row's rate is held to what the
// live documents before gave back under it, not only to this document's.
func TestARateRemembersWhatItGaveBack(t *testing.T) {
	t.Parallel()

	svc := newService(newFakeRepo())
	sale, err := svc.Issue(context.Background(), stackedIssue())
	require.NoError(t, err)
	refund := func(total, tax, base, top int64) error {
		in := refundOf(sale, total, tax)
		in.Lines[0].TaxRateBps = 500
		in.Lines[0].TaxComponents = []service.LineTaxInput{
			{RateBps: 500, TaxableAmount: 1000, TaxAmount: base},
			{RateBps: 800, Compound: true, TaxableAmount: 1000, TaxAmount: top},
		}
		_, err := svc.Issue(context.Background(), in)

		return err
	}

	require.NoError(t, refund(1100, 100, 100, 0), "the base rate gives back all it charged")
	err = refund(1134, 134, 1, 133)
	require.Error(t, err, "the row's tax fits, the base rate has nothing left")
	assert.Equal(t, service.CodeAmendmentExceedsSale, errors.CodeOf(err))
	require.NoError(t, refund(1134, 134, 0, 134))
}

// TestARefundNamesTheSaleRowAChargeRaised: a charge's line that raises a sale
// row is that row's headroom, not a row of its own, so a refund naming it is
// refused and the charge is given back once, from the sale row.
func TestARefundNamesTheSaleRowAChargeRaised(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newService(newFakeRepo())
	sale := issuedSale(t, svc)
	charge, err := svc.Issue(ctx, chargeOf(sale, 1200, 200, false))
	require.NoError(t, err)

	_, err = svc.Issue(ctx, refundOf(sale, 3600, 600))
	require.NoError(t, err, "the sale row and the charge raising it: 3600 with 600 tax")
	again := refundOf(sale, 1200, 200)
	again.Lines[0].AmendsLineID = charge.Lines[0].ID
	_, err = svc.Issue(ctx, again)
	require.Error(t, err, "the charge was given back with its row; naming its line would give it back twice")
	assert.True(t, errors.IsInvalid(err), "got %v", err)
}

// TestACanceledChargeIsHeldOnlyByTheRowsItCovers: a row a rejected charge left
// over its ceiling does not hold another charge's cancellation, and a row the
// charge's removal would put over is named in the refusal.
func TestACanceledChargeIsHeldOnlyByTheRowsItCovers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newService(newFakeRepo())
	sale := issuedSale(t, svc)
	raise, err := svc.Issue(ctx, chargeOf(sale, 120, 20, false))
	require.NoError(t, err)
	added, err := svc.Issue(ctx, chargeOf(sale, 1500, 0, true))
	require.NoError(t, err)
	_, err = svc.Issue(ctx, refundOf(sale, 2520, 420))
	require.NoError(t, err)
	_, err = svc.MoveStatus(ctx, raise.ID, service.MoveInput{To: models.StatusSent})
	require.NoError(t, err)
	_, err = svc.MoveStatus(ctx, raise.ID, service.MoveInput{To: models.StatusRejected, Reason: "refused"})
	require.NoError(t, err, "the rejection leaves the row over its ceiling")

	_, err = svc.MoveStatus(ctx, added.ID, service.MoveInput{To: models.StatusCanceled, Reason: "typed twice"})
	require.NoError(t, err, "no refund relies on this charge; the row already over stays as it is")

	other := issuedSale(t, svc)
	held, err := svc.Issue(ctx, chargeOf(other, 120, 20, false))
	require.NoError(t, err)
	_, err = svc.Issue(ctx, refundOf(other, 2520, 420))
	require.NoError(t, err)
	_, err = svc.MoveStatus(ctx, held.ID, service.MoveInput{To: models.StatusCanceled, Reason: "typed twice"})
	require.Error(t, err)
	assert.Equal(t, service.CodeAmendmentExceedsSale, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "row 1 of invoice "+other.ID, "the refusal names the row")
}
