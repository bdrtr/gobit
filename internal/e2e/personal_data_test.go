//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/personaldata"
	cartsvc "github.com/bdrtr/gobit/internal/modules/cart/service"
	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
	"github.com/bdrtr/gobit/internal/workflows/datasubject"
)

// This file proves what happens on the REAL ground when a data controller
// answers "forget me" (ADR 0029/0032).
//
// # What only this file can show
//
// Every holder has already been tested on its own: the order module, the cart
// module and the invoice module each have integration tests over a real
// database that check their own columns, their own outcome and their own
// idempotence, and the coordinator has unit tests over fake holders that check
// how it aggregates. All of those answer the question "does this holder do what
// it says". None of them can answer the question a controller actually has,
// which is "if I run ONE sweep for this person, is what comes back true of the
// whole installation".
//
// That question needs three things at once and they exist together only here:
//
//   - the holders have to be DISCOVERED rather than listed. The coordinator
//     finds them by type-asserting over the module registry, and the registry
//     on this ground is the production one — it carries the modules a plugin
//     brought and the holders that are not modules at all;
//   - the person's rows have to have been written by the real code paths. A
//     hand-inserted order is a row somebody chose the shape of; the order here
//     came out of the checkout saga, its address was copied from a cart by the
//     snapshot, and its invoice was drawn by the invoicing flow over HTTP;
//   - the answers have to be true SIDE BY SIDE. The one report says the sale
//     was anonymized and the document was kept, and both halves are re-read
//     afterwards from the modules that own them.
//
// # Why the order is COMPLETED first
//
// The order module refuses to anonymize an order that is still being performed,
// and a freshly placed order is PENDING — so a sweep run straight after
// checkout answers "retained", which is correct and is not what this scenario
// is about. An operator marking the sale done is the ordinary event that ends
// the refusal, and it is done here over the same endpoint an operator uses.

// The fixture values written on the person's cart.
//
// They are checked for AFTERWARDS, one by one: an erasure that reports success
// while leaving any of them behind is the exact failure ADR 0029 exists to
// prevent, and a blanket "the address is empty" assertion would pass on a
// column the fixture never filled.
const (
	erasureFirstName  = "Forgotten"
	erasureLastName   = "Person"
	erasureCompany    = "One Person Trading Ltd"
	erasureAddress1   = "Bagdat Caddesi 100"
	erasureAddress2   = "Daire 4"
	erasureCity       = "Istanbul"
	erasureProvince   = "Kadikoy"
	erasurePostalCode = "34710"
	erasurePhone      = "+90 555 000 00 00"
)

// TestOneSweepForgetsAPersonAcrossEveryHolderAndTheInvoiceSaysWhyItCannot is
// the whole legal answer in one scenario.
//
// A data controller who receives an erasure request has to be able to say one
// sentence to the person: here is what was destroyed, here is what was kept,
// and here is why. The claim under test is that a single sweep produces that
// sentence truthfully across modules that know nothing about each other — the
// sale is stripped of the buyer, the shopping session with it, and the tax
// document is kept with its reason attached.
//
// If it stops holding, the failure is not a broken page. The report is what the
// controller repeats to the data subject and to a regulator, so a holder that
// answers "anonymized" while a column still names the person turns the
// framework into the source of a false statement — which is what the
// coordinator's own package doc says it must never be.
func TestOneSweepForgetsAPersonAcrossEveryHolderAndTheInvoiceSaysWhyItCannot(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Forgotten Product", map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, happyInitialStock)

	cartID, _ := prepareCart(ctx, t, customerID, variantID, happyQuantity)
	seedPersonalDataOnCart(ctx, t, cartID, email)

	placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: paymentmanual.ID,
		PaymentData:       paymentBehavior(t, paymentmanual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     happyTotal,
	})
	require.NoError(t, err, "the fixture order could not be placed")

	// The sale is finished, which is what ends the order module's refusal; see
	// the file header.
	completed, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+placed.OrderID+"/complete", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, completed.Code,
		"the fixture order could not be marked complete; body: %s", completed.Body.String())

	writeStoreProfile(t)

	issued, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+placed.OrderID+"/invoice", issueInvoiceBody())
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, issued.Code,
		"the fixture invoice could not be issued; body: %s", issued.Body.String())

	invoiceID := decodeIssuedInvoice(t, issued.Body.Bytes())

	// The sale really does carry the person before the sweep. Without this the
	// assertions afterwards would pass on an order that never held anything.
	before, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err, "the fixture order could not be read back")
	require.Equal(t, email, before.Email, "precondition: the order carries the buyer's address")
	require.NotNil(t, before.ShippingAddress,
		"precondition: the cart's address has to have traveled to the order, or there is "+
			"nothing here for the erasure to blank")
	require.Equal(t, erasureAddress1, before.ShippingAddress.Address1,
		"precondition: the order's address is the one written on the cart")

	// --- the sweep ---
	//
	// The coordinator is built with the PRODUCTION wiring, from the same
	// registry the router was built from. A hand-written holder list here would
	// prove only that the holders somebody remembered were asked.
	co := personalDataCoordinator(t)

	report, err := co.Erase(ctx, personaldata.Subject{CustomerID: customerID, Email: email})
	require.NoError(t, err,
		"the sweep must finish; an error means at least one holder could not act and the "+
			"controller must NOT tell the data subject the work is done")

	assert.Equal(t, customerID, report.Subject.CustomerID,
		"the report has to echo who it was about; filed away on its own it is unreadable otherwise")
	assert.Equal(t, email, report.Subject.Email)
	assert.False(t, report.At.IsZero(), "the report has to say when the sweep ran")

	answers := erasureAnswers(t, report)

	// --- the sale: anonymized, and it says what it left ---
	sale := requireAnswer(t, answers, "order")
	assert.Equal(t, personaldata.Anonymized, sale.Outcome,
		"a settled sale has to be ANONYMIZED: the lines and the totals are the shop's own "+
			"books and cannot evaporate, but nothing on the row may still name the buyer")
	assert.Positive(t, sale.Rows,
		"zero rows would mean the sweep never found this person's order; the report would "+
			"then be a truthful-looking answer about nobody")
	assert.NotEmpty(t, sale.Kept,
		"an anonymized answer has to name the free-form columns gobit never rewrites; "+
			"without that list the word 'anonymized' covers fields nobody looked at")
	assert.NotEmpty(t, sale.Why,
		"a kept list with no sentence beside it is not something a controller can pass on")

	// --- the shopping session: anonymized too ---
	session := requireAnswer(t, answers, "cart")
	assert.Equal(t, personaldata.Anonymized, session.Outcome,
		"the cart the sale came out of has to be anonymized as well; it is joinable from the "+
			"order and would otherwise hold the name the order just dropped")
	assert.Positive(t, session.Rows,
		"zero rows would mean the cart holder never found this person's session")

	// --- the document: retained, with a reason ---
	document := requireAnswer(t, answers, "invoice")
	assert.Equal(t, personaldata.Retained, document.Outcome,
		"an issued invoice is a legal document and the honest answer is RETAINED; "+
			"'deleted' or 'anonymized' here would be a false statement made to a data subject")
	assert.Equal(t, 1, document.Rows,
		"the one document this person has must be counted; zero would send the controller "+
			"away believing there is nothing to disclose")
	assert.NotEmpty(t, document.Kept,
		"a refusal that cannot say WHAT it kept is not a refusal a controller can repeat")
	assert.NotEmpty(t, document.Why,
		"the sentence is the whole point of the third outcome: the person is owed a reason")

	// --- the modules are re-read, because the report is only a claim ---
	after, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err, "the order must still exist; an erasure does not destroy the sale")
	assert.Empty(t, after.Email,
		"the buyer's address must be gone from the sale; the report said anonymized and this "+
			"is the column that decides whether that was true")
	assert.Equal(t, customerID, after.CustomerID,
		"the customer id STAYS: it is the only indexed handle a second sweep has, and nulling "+
			"it would leave the rows it already erased unfindable")
	assert.Equal(t, happyTotal, after.Total,
		"the sale itself must be untouched; a shop whose past year evaporates when somebody "+
			"asks to be forgotten has lost its own books rather than the person's data")

	require.NotNil(t, after.ShippingAddress,
		"the address ROW stays and is blanked; deleting it would take the country the tax "+
			"was computed under with it")
	for _, field := range []struct{ label, value string }{
		{"first name", after.ShippingAddress.FirstName},
		{"last name", after.ShippingAddress.LastName},
		{"company", after.ShippingAddress.Company},
		{"address line 1", after.ShippingAddress.Address1},
		{"address line 2", after.ShippingAddress.Address2},
		{"city", after.ShippingAddress.City},
		{"province", after.ShippingAddress.Province},
		{"postal code", after.ShippingAddress.PostalCode},
		{"phone", after.ShippingAddress.Phone},
	} {
		assert.Empty(t, field.value,
			"the order address still carries the person's %s; the sweep reported the sale as "+
				"anonymized, so this column left behind makes that report a lie", field.label)
	}
	assert.Equal(t, taxedCountry, after.ShippingAddress.CountryCode,
		"the COUNTRY stays on purpose: it is what the tax on this sale was computed under, "+
			"and a country alone names nobody")

	cart, err := cartSvc.GetCart(ctx, cartID)
	require.NoError(t, err, "the cart must still exist; the order rests on it")
	assert.Empty(t, cart.Email, "the shopper's address must be gone from the cart")
	require.NotNil(t, cart.ShippingAddress, "the cart's address row stays and is blanked")
	assert.Empty(t, cart.ShippingAddress.Address1,
		"the cart still carries the street the person typed")
	assert.Empty(t, cart.ShippingAddress.Phone,
		"the cart still carries the person's phone number")

	// The document was reported as kept, so it has to BE kept — whole, with the
	// buyer still on it. A module that answers "retained" and quietly blanks
	// the paper is worse than one that erased it openly.
	stored, err := adminRequestWithBody(http.MethodGet, "/admin/v1/invoices/"+invoiceID, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, stored.Code, stored.Body.String())

	var invoice invoiceDocumentResponse
	require.NoError(t, json.Unmarshal(stored.Body.Bytes(), &invoice),
		"the stored invoice could not be decoded; body: %s", stored.Body.String())
	assert.Equal(t, email, invoice.Data.Buyer.Email,
		"the invoice reported RETAINED must still name its buyer; a document quietly "+
			"stripped of the party it was issued to is no longer the document that was filed")

	// --- the second sweep answers the same thing ---
	//
	// Idempotence is what makes an erasure safe to repeat, and a controller
	// WILL repeat it: the same request arrives twice, or a colleague runs it
	// again to produce the evidence. An answer that changes on the second run
	// means one of the two reports is wrong and there is no way to tell which.
	repeat, err := co.Erase(ctx, personaldata.Subject{CustomerID: customerID, Email: email})
	require.NoError(t, err, "the second sweep must finish as well")

	assert.Equal(t, outcomesOf(erasureAnswers(t, repeat)), outcomesOf(answers),
		"the second sweep has to answer the same OUTCOME for every holder; a holder that "+
			"changes its mind on the second run makes both reports unusable, because "+
			"nothing says which of the two the controller should have filed")

	secondSale := requireAnswer(t, erasureAnswers(t, repeat), "order")
	assert.Equal(t, sale.Rows, secondSale.Rows,
		"the sale's anonymizing statements are unconditional over the same rows, so the "+
			"second run touches the same count; a different number means the second sweep "+
			"reached a different set of rows than the first")
}

// TestTheDeclarationReachesHoldersThatAreNoModule audits the map an embedder
// publishes its privacy notice from.
//
// The declaration is ADR 0029's third obligation and the one that makes the
// other two checkable: without it nobody can say where to look. Its dangerous
// blind spot is not a module that forgot to declare — internal/app already
// audits every module for that — but the personal data that sits OUTSIDE the
// module tree entirely. The saga store keeps whole workflow payloads, the link
// tables bind one module's rows to another's by bare identifier, and the audit
// log records which member of staff touched which admin path. None of the three
// is a module, so a walk over the registry misses all of them, and they can
// only be reached through the container — which exists in this shape only here.
//
// If it stops holding, an installation publishes a privacy notice that is
// silently short by three stores, and a disclosure request assembled from that
// notice hands the data subject an incomplete file while looking complete.
func TestTheDeclarationReachesHoldersThatAreNoModule(t *testing.T) {
	co := personalDataCoordinator(t)

	declared := make(map[string][]personaldata.Holding)
	for _, d := range co.PersonalData() {
		require.NotEmpty(t, d.Holder,
			"every declaration has to be attributed; the sweep overwrites the holder name "+
				"with the one the registry knows, and a blank one means that never happened")
		declared[d.Holder] = d.Holdings
	}

	// The three holders that no module list contains. They are named as string
	// literals because that is exactly what an embedder reading the published
	// declaration sees; taking them from the package that defines them would
	// make the assertion true by construction.
	for _, holder := range []string{
		"internal/core/workflow/pgstore",
		"core/link",
		"core/audit",
	} {
		holdings, found := declared[holder]
		assert.True(t, found,
			"%q declares personal data and is not a module, so nothing that walks the "+
				"module registry can find it; missing here means an installation's privacy "+
				"notice is short by a whole store", holder)
		assert.NotEmpty(t, holdings,
			"%q is in the declaration and names no table; a holder that declares nothing "+
				"tells a controller to look nowhere", holder)
	}

	// A module holder is required alongside them, because the assertion above
	// would also pass on a coordinator that somehow reached ONLY the outside
	// holders and no module at all.
	assert.NotEmpty(t, declared["order"],
		"the order module has to be in the same declaration; if it is not, the coordinator "+
			"was built from something other than the registry the router was built from")

	// Every holding has to be readable as a place to look. A blank table or
	// column is a line in a privacy notice that points at nothing.
	for holder, holdings := range declared {
		for _, h := range holdings {
			assert.NotEmpty(t, h.Table, "%s declares a holding with no table", holder)
			assert.NotEmpty(t, h.Column, "%s declares a holding with no column", holder)
			assert.NotEmpty(t, h.Why,
				"%s declares %s.%s and does not say what it holds about the person; the "+
					"WHY is the part a controller copies into an answer",
				holder, h.Table, h.Column)
			assert.Contains(t, []personaldata.Kind{personaldata.Named, personaldata.Open}, h.Kind,
				"%s declares %s.%s with kind %q; a reader cannot tell whether gobit wrote "+
					"the person there or the embedder might have",
				holder, h.Table, h.Column, h.Kind)
		}
	}
}

// personalDataCoordinator builds the sweep with the PRODUCTION wiring.
//
// It takes the registry's full module list ([testModules]) and the container,
// which is what internal/app does. Both matter: the modules a plugin brings are
// only in that list, and the holders that are not modules are only reachable
// through the container.
func personalDataCoordinator(t *testing.T) *datasubject.Coordinator {
	t.Helper()

	co, err := datasubject.FromContainer(ctr, testModules)
	require.NoError(t, err, "the erasure coordinator could not be built from the ground")
	require.NotNil(t, co)

	return co
}

// erasureAnswers indexes a report by holder.
//
// It fails on a duplicate holder rather than letting the later entry win: two
// answers under one name mean a controller reading the report cannot tell which
// of them describes the installation.
func erasureAnswers(t *testing.T, report personaldata.Report) map[string]personaldata.Result {
	t.Helper()

	require.NotEmpty(t, report.Results,
		"the sweep asked nobody; an empty report is not an answer to a data subject")

	out := make(map[string]personaldata.Result, len(report.Results))
	for _, result := range report.Results {
		require.NotContains(t, out, result.Holder,
			"the holder %q answered twice in one report", result.Holder)
		out[result.Holder] = result
	}

	return out
}

// requireAnswer returns one holder's answer and fails naming the holders that
// did answer, so a renamed module is diagnosable from the failure alone.
func requireAnswer(
	t *testing.T, answers map[string]personaldata.Result, holder string,
) personaldata.Result {
	t.Helper()

	result, found := answers[holder]
	require.True(t, found,
		"the %q holder did not answer the sweep at all. It holds personal data, so its "+
			"silence is invisible in the report and the controller would file an answer "+
			"that never asked it. Holders that did answer: %v",
		holder, anahtarlar(answers))

	return result
}

// outcomesOf reduces a report to holder -> outcome, which is the part
// idempotence is about.
func outcomesOf(answers map[string]personaldata.Result) map[string]personaldata.Outcome {
	out := make(map[string]personaldata.Outcome, len(answers))
	for holder, result := range answers {
		out[holder] = result.Outcome
	}

	return out
}

// seedPersonalDataOnCart writes the person onto the cart, contact and address
// both.
//
// The address is written with EVERY name and location field filled in, because
// the erasure is checked field by field afterwards: a column the fixture left
// empty would pass the "it is empty now" assertion without the erasure ever
// touching it.
func seedPersonalDataOnCart(ctx context.Context, t *testing.T, cartID, email string) {
	t.Helper()

	_, err := cartSvc.UpdateCart(ctx, cartID, cartsvc.UpdateCartInput{Email: &email})
	require.NoError(t, err, "the fixture cart's contact address could not be written")

	_, err = cartSvc.SetShippingAddress(ctx, cartID, cartsvc.AddressInput{
		FirstName:  erasureFirstName,
		LastName:   erasureLastName,
		Company:    erasureCompany,
		Address1:   erasureAddress1,
		Address2:   erasureAddress2,
		City:       erasureCity,
		Province:   erasureProvince,
		PostalCode: erasurePostalCode,
		// The country is the taxed region's, so the fixture's amounts stay the
		// hand-computed ones of the happy path.
		CountryCode: taxedCountry,
		Phone:       erasurePhone,
	})
	require.NoError(t, err, "the fixture cart's shipping address could not be written")
}

// decodeIssuedInvoice reads the invoice id out of the issue endpoint's answer.
func decodeIssuedInvoice(t *testing.T, body []byte) string {
	t.Helper()

	var issued invoiceIssueResponse
	require.NoError(t, json.Unmarshal(body, &issued),
		"the issued invoice could not be decoded; body: %s", string(body))
	require.NotEmpty(t, issued.Data.InvoiceID, "the issued invoice must carry an identity")

	return issued.Data.InvoiceID
}
