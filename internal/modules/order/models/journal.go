package models

import "time"

// This file is the order module's side of the books (ADR 0188).
//
// As on the payment module's (ADR 0186), nothing here is written: every entry
// is derived, when it is read, from a record this module keeps and never
// deletes — an order placed, an order canceled, a credit line.

// JournalAccount is one side of the order module's books.
type JournalAccount string

// The accounts.
const (
	// AccountReceivable is what the buyer owes for the order. It is the SAME
	// account as the payment module's, which credits it when money is captured
	// and debits it when money goes back; internal/arch binds the two
	// spellings, because the books close only if both modules name it alike.
	AccountReceivable JournalAccount = "receivable"
	// AccountSales is the goods sold, before discount and without tax.
	AccountSales JournalAccount = "sales"
	// AccountSalesDiscounts is the discount taken off the goods: a debit
	// against sales.
	AccountSalesDiscounts JournalAccount = "sales_discounts"
	// AccountTaxPayable is the tax the shop collected for the authority.
	AccountTaxPayable JournalAccount = "tax_payable"
	// AccountShipping is what the buyer was charged for shipping.
	AccountShipping JournalAccount = "shipping"
	// AccountCreditAllowances is what the shop wrote off an order's amount
	// after it was placed: a credit line.
	AccountCreditAllowances JournalAccount = "credit_allowances"
	// AccountSalesReturns is the revenue given back for goods returned: what a
	// return's refunds sent back (ADR 0189).
	AccountSalesReturns JournalAccount = "sales_returns"
	// AccountClaimAllowances is what a claim's refunds sent back for goods that
	// arrived wrong or damaged and were not returned (ADR 0189).
	AccountClaimAllowances JournalAccount = "claim_allowances"
)

// JournalKind is the fact an entry is read from.
type JournalKind string

// The kinds.
const (
	// JournalOrderPlaced is an order at its placed_at.
	JournalOrderPlaced JournalKind = "order_placed"
	// JournalOrderCanceled is an order at its canceled_at.
	JournalOrderCanceled JournalKind = "order_canceled"
	// JournalCreditLine is a row of order_credit_lines.
	JournalCreditLine JournalKind = "credit_line"
	// JournalReturnRefunded is a refund whose cause is one of the order's
	// returns (ADR 0189).
	JournalReturnRefunded JournalKind = "return_refunded"
	// JournalClaimRefunded is a refund whose cause is one of the order's claims
	// (ADR 0189).
	JournalClaimRefunded JournalKind = "claim_refunded"
)

// JournalLine is one side of an entry: exactly one of Debit and Credit is
// positive.
type JournalLine struct {
	Account JournalAccount `json:"account"`
	Debit   int64          `json:"debit"`
	Credit  int64          `json:"credit"`
}

// JournalEntry is one balanced fact: its lines' debits equal their credits.
type JournalEntry struct {
	// ID is the id of the record the entry is read from: the order for a
	// placement or a cancellation, the credit line for a credit line, the
	// payment module's refund for a refund.
	ID           string        `json:"id"`
	Kind         JournalKind   `json:"kind"`
	OrderID      string        `json:"order_id"`
	OccurredAt   time.Time     `json:"occurred_at"`
	CurrencyCode string        `json:"currency_code"`
	Lines        []JournalLine `json:"lines"`
}

// JournalBalance is one account's sums over a window, in one currency.
type JournalBalance struct {
	CurrencyCode string         `json:"currency_code"`
	Account      JournalAccount `json:"account"`
	Debit        int64          `json:"debit"`
	Credit       int64          `json:"credit"`
}

// JournalFact is one record the journal is derived from, before any account is
// named. An order's amounts are set for a placement and a cancellation;
// Amount is set for a credit line.
type JournalFact struct {
	ID           string
	Kind         JournalKind
	OrderID      string
	OccurredAt   time.Time
	CurrencyCode string

	Subtotal, DiscountTotal, TaxTotal, ShippingTotal, Total int64

	Amount int64
}

// JournalCause is an order record a refund can name as its cause (ADR 0189).
type JournalCause struct {
	// ID is the return's or the claim's id, which the refund carries as its
	// reference (ADR 0187).
	ID string
	// Kind is "return" or "claim".
	Kind         string
	OrderID      string
	CurrencyCode string
}
