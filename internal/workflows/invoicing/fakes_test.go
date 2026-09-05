package invoicing_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/workflows/invoicing"
)

// fakeOrders is the order surface the flow reads.
type fakeOrders struct {
	order fakeOrder
	err   error
}

// fakeOrder is the body the surface returns.
type fakeOrder struct {
	OrderID       string     `json:"order_id"`
	DisplayID     int64      `json:"display_id"`
	CurrencyCode  string     `json:"currency_code"`
	Email         string     `json:"email"`
	Status        string     `json:"status"`
	Subtotal      int64      `json:"subtotal"`
	DiscountTotal int64      `json:"discount_total"`
	TaxTotal      int64      `json:"tax_total"`
	ShippingTotal int64      `json:"shipping_total"`
	Total         int64      `json:"total"`
	Items         []fakeItem `json:"items"`
}

// fakeItem is one line of that order.
type fakeItem struct {
	Title         string `json:"title"`
	Quantity      int64  `json:"quantity"`
	UnitPrice     int64  `json:"unit_price"`
	Subtotal      int64  `json:"subtotal"`
	DiscountTotal int64  `json:"discount_total"`
	TaxRateBps    int32  `json:"tax_rate_bps"`
	TaxTotal      int64  `json:"tax_total"`
	Total         int64  `json:"total"`
}

// OrderInvoiceJSON returns the prepared order.
func (f *fakeOrders) OrderInvoiceJSON(_ context.Context, _ string) (json.RawMessage, error) {
	if f.err != nil {
		return nil, f.err
	}

	return json.Marshal(f.order)
}

// fakeInvoices is the invoice surface the flow writes to.
//
// It imitates the one behavior the flow depends on: a number is handed out per
// issue and never twice, so a test can tell "returned the existing document"
// apart from "issued a second one" by the counter alone.
type fakeInvoices struct {
	// issues counts how many documents were really issued.
	issues int
	// documents holds the bodies the flow sent, in order.
	documents []json.RawMessage
	// issued maps an id to the identity the flow will read back.
	issued map[string]json.RawMessage
	// err scripts a failure.
	err error
}

// newFakeInvoices builds an empty fake.
func newFakeInvoices() *fakeInvoices {
	return &fakeInvoices{issued: map[string]json.RawMessage{}}
}

// IssueJSON records the document and hands out the next number.
func (f *fakeInvoices) IssueJSON(
	_ context.Context, document json.RawMessage,
) (invoiceID, number string, err error) {
	if f.err != nil {
		return "", "", f.err
	}

	f.issues++
	f.documents = append(f.documents, document)

	id := "inv_1"
	serial := "GBT2026000000001"

	if f.issues > 1 {
		id = "inv_2"
		serial = "GBT2026000000002"
	}

	identity, err := json.Marshal(map[string]string{
		"id": id, "number": serial, "status": "issued",
	})
	if err != nil {
		return "", "", err
	}

	f.issued[id] = identity

	return id, serial, nil
}

// InvoiceIdentityJSON returns what was recorded for the id.
func (f *fakeInvoices) InvoiceIdentityJSON(
	_ context.Context, id string,
) (json.RawMessage, error) {
	identity, ok := f.issued[id]
	if !ok {
		return nil, errors.NotFound("fake_invoice_missing", "no such invoice")
	}

	return identity, nil
}

// lastDocument decodes the most recent document the flow sent.
func (f *fakeInvoices) lastDocument(t *testing.T) issuedDocument {
	t.Helper()

	require.NotEmpty(t, f.documents, "the flow sent no document")

	return decodeDocument(t, f.documents[len(f.documents)-1])
}

// fakeLinks is the core's link service.
type fakeLinks struct {
	// bound maps an order id to the invoice ids bound to it.
	bound map[string][]string
	// linkErr scripts a failure of the write.
	linkErr error
	// listErr scripts a failure of the read.
	listErr error
}

// newFakeLinks builds an empty fake.
func newFakeLinks() *fakeLinks { return &fakeLinks{bound: map[string][]string{}} }

// Create binds the two records.
func (f *fakeLinks) Create(_ context.Context, _, fromID, toID string) error {
	if f.linkErr != nil {
		return f.linkErr
	}

	f.bound[fromID] = append(f.bound[fromID], toID)

	return nil
}

// ListMany returns what is bound to each of the given ids.
func (f *fakeLinks) ListMany(
	_ context.Context, _ string, fromIDs []string,
) (map[string][]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}

	out := map[string][]string{}
	for _, id := range fromIDs {
		if bound, ok := f.bound[id]; ok {
			out[id] = bound
		}
	}

	return out, nil
}

// harness holds the flow and its fakes.
type harness struct {
	flow     *invoicing.Workflows
	orders   *fakeOrders
	invoices *fakeInvoices
	links    *fakeLinks
}

// newHarness builds a flow over fakes carrying one ordinary order.
//
// The order has ONE product line and a carriage total, because that is the
// combination the assembling has to get right: the carriage becomes a line and
// the totals have to survive the move.
func newHarness(t *testing.T) *harness {
	t.Helper()

	orders := &fakeOrders{order: fakeOrder{
		OrderID:      "order_1",
		DisplayID:    1042,
		CurrencyCode: "TRY",
		Email:        "customer@example.test",
		Status:       "pending",
		Subtotal:     2000,
		TaxTotal:     400,
		// Carriage is not taxed, the same as the cart leaves it today.
		ShippingTotal: 2500,
		Total:         4900,
		Items: []fakeItem{{
			Title:      "Red T-Shirt",
			Quantity:   2,
			UnitPrice:  1000,
			Subtotal:   2000,
			TaxRateBps: 2000,
			TaxTotal:   400,
			Total:      2400,
		}},
	}}

	invoices := newFakeInvoices()
	links := newFakeLinks()

	flow, err := invoicing.New(invoicing.Deps{
		Orders:   orders,
		Invoices: invoices,
		Links:    links,
	})
	require.NoError(t, err)

	return &harness{flow: flow, orders: orders, invoices: invoices, links: links}
}
