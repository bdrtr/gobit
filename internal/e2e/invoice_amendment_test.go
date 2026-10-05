//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"

	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	paymentmodels "github.com/bdrtr/gobit/internal/modules/payment/models"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
	taxsvc "github.com/bdrtr/gobit/internal/modules/tax/service"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// The fixture's figures (ADR 0406): a line at 1% and a line at 20%, a sold
// delivery and the dearer one it was changed to.
const (
	amendedReducedPrice int64 = 10_000
	amendedReducedBps   int32 = 100
	amendedFullPrice    int64 = 5_000
	amendedFullQuantity int64 = 2
	amendedDearerBy     int64 = 1_500
	// amendedTotal is 10 000 + 100 tax, 2 x 5 000 + 2 000 tax, and the sold
	// delivery.
	amendedTotal int64 = 10_100 + 12_000 + soldDeliveryFee
)

// amendedOrder places an order with a 1% line and a 20% line on the sold
// delivery, then changes the delivery to a dearer one and pays for it. It
// returns the order and its two collections.
func amendedOrder(t *testing.T) (orderID string, collections []string) {
	t.Helper()

	ctx := t.Context()
	page, err := taxSvc.ListTaxRegions(ctx, taxedCountry, 0, 0)
	require.NoError(t, err)
	var rootID string
	for i := range page.Items {
		if page.Items[i].IsRoot() {
			rootID = page.Items[i].ID
		}
	}
	require.NotEmpty(t, rootID)

	customerID, email := newCustomer(ctx, t)
	reducedID, _ := newStockedVariant(ctx, t, "E2E Amended Reduced",
		map[string]int64{taxedCurrency: amendedReducedPrice}, 10)
	fullID, _ := newStockedVariant(ctx, t, "E2E Amended Full",
		map[string]int64{taxedCurrency: amendedFullPrice}, 10)
	reduced, err := productSvc.GetVariant(ctx, reducedID)
	require.NoError(t, err)
	rate, err := taxSvc.CreateTaxRate(ctx, taxsvc.CreateTaxRateInput{
		TaxRegionID: rootID, Name: fmt.Sprintf("E2E Reduced %d", fixtureCounter.Add(1)),
		RateBps: amendedReducedBps,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = taxSvc.DeleteTaxRate(ctx, rate.ID) })
	_, err = taxSvc.CreateRateRule(ctx, taxsvc.CreateRateRuleInput{
		TaxRateID: rate.ID, Reference: "product", ReferenceID: reduced.ProductID,
	})
	require.NoError(t, err)

	opened := openAdditionCart(t, customerID, email, "")
	require.Equal(t, http.StatusCreated, opened.Code, "body: %s", opened.Body.String())
	cartID, ok := storefrontData(t, opened)["id"].(string)
	require.True(t, ok)
	added := addAdminLine(t, cartID, testChannelID, reducedID, 1)
	require.Equal(t, http.StatusCreated, added.Code, "body: %s", added.Body.String())
	added = addAdminLine(t, cartID, testChannelID, fullID, amendedFullQuantity)
	require.Equal(t, http.StatusCreated, added.Code, "body: %s", added.Body.String())

	address := fmt.Sprintf(`{"first_name":"Ada","address_1":"12 Main St","city":"Springfield",`+
		`"postal_code":"62701","country_code":%q}`, taxedCountry)
	rec := storefrontRequest(t, http.MethodPut, "/store/v1/carts/"+cartID+"/shipping-address", address)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	sold := spyOptionPriced(t, soldDeliveryFee, false)
	rec = storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/shipping-methods",
		fmt.Sprintf(`{"shipping_option_id":%q}`, sold))
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID: cartID, LocationID: stockLocationID, PaymentProviderID: paymentmanual.ID,
		PaymentData: paymentBehavior(t, paymentmanual.OutcomeAuthorize), Email: email,
		ExpectedTotal: amendedTotal,
	})
	require.NoError(t, err)
	orderID = placed.OrderID

	method := onlyShippingMethod(t, orderID)
	methodID, _ := method["id"].(string)
	dearer := spyOptionPriced(t, soldDeliveryFee+amendedDearerBy, true)
	paid := collectFor(t, orderID, amendedDearerBy, amendedDearerBy)
	rec = adminCartRequest(t, http.MethodPut, "/admin/v1/orders/"+orderID+"/shipping-methods/"+methodID,
		fmt.Sprintf(`{"shipping_option_id":%q,"payment_collection_id":%q}`, dearer, paid))
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	return orderID, []string{placed.PaymentCollectionID, paid}
}

// paidFor is what the buyer paid for the order and kept paid: what the
// collections captured less what they refunded, read off the payment journal.
func paidFor(t *testing.T, from time.Time, collections ...string) int64 {
	t.Helper()

	journal, err := paymentSvc.Journal(t.Context(), paymentsvc.JournalQuery{
		From: from, To: time.Now().UTC().Add(time.Minute),
	})
	require.NoError(t, err)
	var paid int64
	for _, entry := range journal.Entries {
		for _, collectionID := range collections {
			if entry.CollectionID != collectionID {
				continue
			}
			for _, line := range entry.Lines {
				if line.Account == paymentmodels.AccountReceivable {
					paid += line.Credit - line.Debit
				}
			}
		}
	}

	return paid
}

// saleOf is the id of the order's sale document.
func saleOf(t *testing.T, orderID string) string {
	t.Helper()

	rec := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+orderID+"/invoice", "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	saleID, _ := storefrontData(t, rec)["invoice_id"].(string)
	require.NotEmpty(t, saleID)

	return saleID
}

// amendingDocuments are the documents amending a sale, as the listing has
// them.
type amendingDocuments struct {
	Data []struct {
		ID              string `json:"id"`
		Kind            string `json:"kind"`
		Status          string `json:"status"`
		AmendmentReason string `json:"amendment_reason"`
		AmendmentKey    string `json:"amendment_key"`
		TaxTotal        int64  `json:"tax_total"`
		Total           int64  `json:"total"`
	} `json:"data"`
}

// documentedFor is what the order's documents say the buyer owes: the sale
// document, plus the live sales amending it, less the live refunds.
func documentedFor(t *testing.T, orderID string) int64 {
	t.Helper()

	saleID := saleOf(t, orderID)
	read := adminCartRequest(t, http.MethodGet, "/admin/v1/invoices/"+saleID, "")
	require.Equal(t, http.StatusOK, read.Code, "body: %s", read.Body.String())
	var sale invoiceDocumentResponse
	require.NoError(t, json.Unmarshal(read.Body.Bytes(), &sale))

	documented := sale.Data.Total
	for _, amendment := range amendmentsOf(t, saleID).Data {
		if amendment.Status == "canceled" || amendment.Status == "rejected" {
			continue
		}
		if amendment.Kind == "refund" {
			documented -= amendment.Total
		} else {
			documented += amendment.Total
		}
	}

	return documented
}

// amendmentsOf lists the documents amending the sale.
func amendmentsOf(t *testing.T, saleID string) amendingDocuments {
	t.Helper()

	rec := adminCartRequest(t, http.MethodGet, "/admin/v1/invoices?amends="+saleID, "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var listed amendingDocuments
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listed))

	return listed
}

// orderActs are the order's acts after its sale, as the order lists them.
type orderActs struct {
	Data []struct {
		Kind         string `json:"kind"`
		ID           string `json:"id"`
		Amount       int64  `json:"amount"`
		Documentable bool   `json:"documentable"`
		Document     *struct {
			InvoiceID string `json:"invoice_id"`
			Number    string `json:"number"`
		} `json:"document"`
	} `json:"data"`
}

// actOf finds the order's one act of the kind, failing when it has none.
func actOf(t *testing.T, orderID, kind string) (id string, amount int64, documented bool) {
	t.Helper()

	rec := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+orderID+"/invoice/amendments", "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var acts orderActs
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &acts))
	for _, act := range acts.Data {
		if act.Kind == kind {
			return act.ID, act.Amount, act.Document != nil
		}
	}
	t.Fatalf("order %s has no %s act: %s", orderID, kind, rec.Body.String())

	return "", 0, false
}

// documentAct documents the act on the E2E series and returns the status the
// endpoint answered.
func documentAct(t *testing.T, orderID, kind, id string) int {
	t.Helper()

	rec, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+orderID+"/invoice/amendments",
		map[string]any{"series_prefix": invoiceSeriesPrefix, "act": map[string]any{"kind": kind, "id": id}})
	require.NoError(t, err)
	require.Contains(t, []int{http.StatusCreated, http.StatusOK}, rec.Code, "body: %s", rec.Body.String())

	return rec.Code
}

// TestAnOrdersDocumentsCarryWhatItsBuyerPaidAfterTheSale is D247 on the
// production wiring (ADR 0406): an order with a 1% line and a 20% line is
// moved to a dearer delivery and paid for, invoiced, and then has its 20%
// line returned and refunded. The sale document alone prints less than the
// buyer paid; its amendments bring the documents to what the buyer paid and
// kept paid, and the return's document gives back the 20% row's own tax.
func TestAnOrdersDocumentsCarryWhatItsBuyerPaidAfterTheSale(t *testing.T) {
	ctx := t.Context()
	from := time.Now().UTC().Add(-time.Second)
	writeStoreProfile(t)
	orderID, collections := amendedOrder(t)

	issued, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+orderID+"/invoice",
		issueInvoiceBody())
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, issued.Code, "body: %s", issued.Body.String())
	require.Equal(t, amendedTotal, documentedFor(t, orderID), "the sale document prints the sale")
	require.Equal(t, amendedTotal+amendedDearerBy, paidFor(t, from, collections...))

	changeID, amount, documented := actOf(t, orderID, "delivery_upgraded")
	assert.Equal(t, amendedDearerBy, amount)
	assert.False(t, documented)

	// The dearer delivery is documented on the order's page, through the
	// order module's panel surface and the flow the API calls.
	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	pagePath := adminui.OrdersPath + "/" + orderID
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_accounts", Kind: "user", Scopes: []string{"order:read", "order:write", "invoice:read"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	page := send(http.MethodGet, pagePath, nil).Body.String()
	require.Contains(t, page, `name="act_id" value="`+changeID+`"`, "the page offers the act its document")
	documentedOnPage := send(http.MethodPost, pagePath+"/invoice/amendments", url.Values{
		"new_series": {strings.ToLower(invoiceSeriesPrefix)}, "act_kind": {"delivery_upgraded"},
		"act_id": {changeID},
	})
	require.Equal(t, http.StatusOK, documentedOnPage.Code, documentedOnPage.Body.String())
	assert.Regexp(t, `Document `+invoiceSeriesPrefix+`\d{13} was issued\.`, documentedOnPage.Body.String())
	assert.Equal(t, http.StatusOK, documentAct(t, orderID, "delivery_upgraded", changeID),
		"an act is documented once")
	assert.Equal(t, paidFor(t, from, collections...), documentedFor(t, orderID),
		"the dearer delivery is on a document")

	order, err := orderSvc.GetOrder(ctx, orderID)
	require.NoError(t, err)
	var fullLine string
	for _, item := range order.Items {
		if item.Quantity == amendedFullQuantity {
			fullLine = item.ID
		}
	}
	require.NotEmpty(t, fullLine)
	opened, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+orderID+"/returns",
		map[string]any{"reason": "both mugs came back", "lines": []map[string]any{{
			"order_line_item_id": fullLine, "quantity": amendedFullQuantity,
		}}})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, opened.Code, opened.Body.String())
	var record afterSalesRecordResponse
	require.NoError(t, json.Unmarshal(opened.Body.Bytes(), &record))
	received, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+orderID+"/returns/"+record.Data.ID+"/receive",
		map[string]any{"location_id": stockLocationID})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, received.Code, received.Body.String())
	refunded, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+orderID+"/returns/"+record.Data.ID+"/refund",
		map[string]any{"amount": 12_000, "reason": "both mugs came back"})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, refunded.Code, refunded.Body.String())

	refundID, amount, _ := actOf(t, orderID, "return_refunded")
	assert.Equal(t, int64(12_000), amount)
	assert.Equal(t, http.StatusCreated, documentAct(t, orderID, "return_refunded", refundID))
	assert.Equal(t, paidFor(t, from, collections...), documentedFor(t, orderID),
		"what the buyer paid and kept paid is on the documents")

	var returned bool
	for _, amendment := range amendmentsOf(t, saleOf(t, orderID)).Data {
		if amendment.AmendmentKey != "return_refunded:"+refundID {
			continue
		}
		returned = true
		assert.Equal(t, "refund", amendment.Kind)
		assert.Equal(t, "returned", amendment.AmendmentReason)
		assert.Equal(t, int64(2_000), amendment.TaxTotal, "the 20% row gives back the tax it charged")
	}
	assert.True(t, returned, "the return's document amends the sale")
	saleID := saleOf(t, orderID)
	for _, amendment := range amendmentsOf(t, saleID).Data {
		page := send(http.MethodGet, adminui.InvoicesPath+"/"+amendment.ID, nil).Body.String()
		assert.Contains(t, page, `Amends <a href="`+adminui.InvoicesPath+"/"+saleID+`">`,
			"the panel links the sale an amendment amends")
	}

	// The acts are the order journal's entries after the sale, by kind and id,
	// read on the real schema.
	journal, err := orderSvc.Journal(ctx, ordersvc.JournalQuery{From: from, To: time.Now().UTC().Add(time.Minute)})
	require.NoError(t, err)
	var booked, listed []string
	for _, entry := range journal.Entries {
		if entry.OrderID == orderID && entry.Kind != "order_placed" && entry.Kind != "order_canceled" {
			booked = append(booked, string(entry.Kind)+":"+entry.ID)
		}
	}
	rec := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+orderID+"/invoice/amendments", "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var acts orderActs
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &acts))
	for _, act := range acts.Data {
		listed = append(listed, act.Kind+":"+act.ID)
		assert.NotNil(t, act.Document, "%s %s is documented", act.Kind, act.ID)
	}
	assert.Equal(t, booked, listed)
}

// TestAFreeDeliveryChangedUpAndBackIsDocumented is the order that shipped
// free (ADR 0406): its invoice has no carriage row, its dearer delivery is
// documented on a row of its own, and the delivery changed back is given back
// from that row, so the documents end at what the order owes.
func TestAFreeDeliveryChangedUpAndBackIsDocumented(t *testing.T) {
	ctx := t.Context()
	writeStoreProfile(t)
	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Shipped Free",
		map[string]int64{taxedCurrency: amendedFullPrice}, 10)
	opened := openAdditionCart(t, customerID, email, "")
	require.Equal(t, http.StatusCreated, opened.Code, "body: %s", opened.Body.String())
	cartID, ok := storefrontData(t, opened)["id"].(string)
	require.True(t, ok)
	added := addAdminLine(t, cartID, testChannelID, variantID, 1)
	require.Equal(t, http.StatusCreated, added.Code, "body: %s", added.Body.String())
	address := fmt.Sprintf(`{"first_name":"Ada","address_1":"12 Main St","city":"Springfield",`+
		`"postal_code":"62701","country_code":%q}`, taxedCountry)
	rec := storefrontRequest(t, http.MethodPut, "/store/v1/carts/"+cartID+"/shipping-address", address)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	free := spyOptionPriced(t, 0, false)
	rec = storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/shipping-methods",
		fmt.Sprintf(`{"shipping_option_id":%q}`, free))
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	const owed int64 = 6_000
	placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID: cartID, LocationID: stockLocationID, PaymentProviderID: paymentmanual.ID,
		PaymentData: paymentBehavior(t, paymentmanual.OutcomeAuthorize), Email: email, ExpectedTotal: owed,
	})
	require.NoError(t, err)
	orderID := placed.OrderID

	methodID, _ := onlyShippingMethod(t, orderID)["id"].(string)
	express := spyOptionPriced(t, amendedDearerBy, true)
	paid := collectFor(t, orderID, amendedDearerBy, amendedDearerBy)
	rec = adminCartRequest(t, http.MethodPut, "/admin/v1/orders/"+orderID+"/shipping-methods/"+methodID,
		fmt.Sprintf(`{"shipping_option_id":%q,"payment_collection_id":%q}`, express, paid))
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	issued, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+orderID+"/invoice", issueInvoiceBody())
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, issued.Code, "body: %s", issued.Body.String())
	upgradeID, _, _ := actOf(t, orderID, "delivery_upgraded")
	assert.Equal(t, http.StatusCreated, documentAct(t, orderID, "delivery_upgraded", upgradeID))
	assert.Equal(t, owed+amendedDearerBy, documentedFor(t, orderID))

	rec = changeDelivery(t, orderID, methodID, free)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	backID, amount, _ := actOf(t, orderID, "delivery_changed")
	assert.Equal(t, amendedDearerBy, amount)
	assert.Equal(t, http.StatusCreated, documentAct(t, orderID, "delivery_changed", backID),
		"the delivery changed back is given back from the row its dearer one added")
	assert.Equal(t, owed, documentedFor(t, orderID), "the documents end at what the order owes")
}
