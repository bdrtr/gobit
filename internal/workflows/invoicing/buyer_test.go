package invoicing_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/workflows/invoicing"
)

// billedTo is a billing address as the order module sends it.
func billedTo() map[string]any {
	return map[string]any{
		"first_name":   "Ada",
		"last_name":    "Lovelace",
		"address_1":    "12 Main St",
		"address_2":    "Flat 3",
		"city":         "Springfield",
		"province":     "IL",
		"postal_code":  "62701",
		"country_code": "US",
	}
}

// TestTheBuyerIsWhomTheOrderWasBilledTo fills an empty buyer from the order's
// billing address: the person's name, the address in lines, the country
// (ADR 0193).
func TestTheBuyerIsWhomTheOrderWasBilledTo(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.orders.order.BillingAddress = billedTo()

	in := validIssue()
	in.Buyer = invoicing.Party{TaxNumber: "11111111111"}
	_, err := h.flow.IssueForOrder(context.Background(), in)
	require.NoError(t, err)

	buyer := h.invoices.lastDocument(t).Buyer
	assert.Equal(t, "Ada Lovelace", buyer.Name)
	assert.Equal(t, "12 Main St\nFlat 3\n62701 Springfield IL", buyer.Address)
	assert.Equal(t, "US", buyer.CountryCode)
	assert.Equal(t, "11111111111", buyer.TaxNumber, "what the order does not know stays the caller's")
	assert.Equal(t, "customer@example.test", buyer.Email)
}

// TestACompanyIsBilledByItsName prints the company, not the person, when the
// billing address names one.
func TestACompanyIsBilledByItsName(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	billing := billedTo()
	billing["company"] = "Analytical Engines Ltd"
	h.orders.order.BillingAddress = billing

	in := validIssue()
	in.Buyer = invoicing.Party{}
	_, err := h.flow.IssueForOrder(context.Background(), in)
	require.NoError(t, err)

	assert.Equal(t, "Analytical Engines Ltd", h.invoices.lastDocument(t).Buyer.Name)
}

// TestWhatTheCallerSendsIsNeverOverruled keeps every field the request filled,
// and fills only the rest.
func TestWhatTheCallerSendsIsNeverOverruled(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.orders.order.BillingAddress = billedTo()

	in := validIssue()
	in.Buyer = invoicing.Party{Name: "A Customer", CountryCode: "TR"}
	_, err := h.flow.IssueForOrder(context.Background(), in)
	require.NoError(t, err)

	buyer := h.invoices.lastDocument(t).Buyer
	assert.Equal(t, "A Customer", buyer.Name)
	assert.Equal(t, "TR", buyer.CountryCode)
	assert.Equal(t, "12 Main St\nFlat 3\n62701 Springfield IL", buyer.Address,
		"the field the caller left empty is still the order's")
}

// TestAnOrderBilledToNobodyLeavesTheBuyerAsSent is the rule's edge: without a
// billing address nothing is filled, and the invoice module's own refusal of a
// nameless buyer stands.
func TestAnOrderBilledToNobodyLeavesTheBuyerAsSent(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	in := validIssue()
	in.Buyer = invoicing.Party{CountryCode: "TR"}
	_, err := h.flow.IssueForOrder(context.Background(), in)
	require.NoError(t, err)

	buyer := h.invoices.lastDocument(t).Buyer
	assert.Empty(t, buyer.Name)
	assert.Empty(t, buyer.Address)
	assert.Equal(t, "TR", buyer.CountryCode)
}

// TestAnErasedBillingAddressFillsOnlyItsCountry reads what an erasure keeps:
// the country, and no name to print (ADR 0033).
func TestAnErasedBillingAddressFillsOnlyItsCountry(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.orders.order.BillingAddress = map[string]any{"country_code": "US"}

	in := validIssue()
	in.Buyer = invoicing.Party{}
	_, err := h.flow.IssueForOrder(context.Background(), in)
	require.NoError(t, err)

	buyer := h.invoices.lastDocument(t).Buyer
	assert.Empty(t, buyer.Name)
	assert.Empty(t, buyer.Address)
	assert.Equal(t, "US", buyer.CountryCode)
}
