package models

import "time"

// This file is the order module's side of the books (ADR 0188).
//
// As on the payment module's (ADR 0186), nothing here is written: every entry
// is derived, when it is read, from a record that is never deleted — an order
// placed, an order canceled, a credit line this module keeps, a refund the
// payment module keeps (ADR 0189), a document amending an order's invoice the
// invoice module keeps (ADR 0419).

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
	// AccountGiftCard is the gift cards the shop owes their holders: a sold
	// card's price is a debt, not a sale (ADR 0211). It is the SAME name as the
	// payment module's account, which a capture through a card debits.
	AccountGiftCard JournalAccount = "gift_card"
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
	// JournalDeliveryChanged is a delivery changed to a cheaper service, read
	// from the credit line the change wrote; its id is the change's (ADR 0199).
	JournalDeliveryChanged JournalKind = "delivery_changed"
	// JournalDeliveryUpgraded is a delivery changed to a dearer service and
	// paid for (ADR 0200); its id is the change's.
	JournalDeliveryUpgraded JournalKind = "delivery_upgraded"
	// JournalExchangeFunded is an exchange whose positive difference was
	// collected, at its funded_at (ADR 0203).
	JournalExchangeFunded JournalKind = "exchange_funded"
	// JournalExchangeRefunded is a refund whose cause is one of the order's
	// exchanges: the difference sent back (ADR 0203).
	JournalExchangeRefunded JournalKind = "exchange_refunded"
	// JournalTaxCorrected is an amending document that names one of the
	// order's acts, at its issued_at: the tax it gave back or charged moves
	// between tax_payable and the account the act was booked to (ADR 0419).
	// Its id is the document's.
	JournalTaxCorrected JournalKind = "tax_corrected"
	// JournalTaxCorrectionVoided is the same document at its voided_at, when it
	// was rejected or canceled: the correction's lines the other way
	// (ADR 0419). Its id is the document's.
	JournalTaxCorrectionVoided JournalKind = "tax_correction_voided"
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
	// delivery change for a changed delivery, the payment module's refund for
	// a refund, the invoice module's amending document for a tax correction.
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
// Amount is set for the others.
type JournalFact struct {
	ID           string
	Kind         JournalKind
	OrderID      string
	OccurredAt   time.Time
	CurrencyCode string

	Subtotal, DiscountTotal, TaxTotal, ShippingTotal, Total int64
	// GiftCardSubtotal is the part of Subtotal the order's gift card lines
	// sold (ADR 0211).
	GiftCardSubtotal int64

	Amount int64

	// ActKind and DocumentKind are set for a tax correction (ADR 0419): the
	// kind of the act the document names, which decides the account its tax
	// moves from or to, and the document's kind, "refund" for tax given back
	// and "sale" for tax charged.
	ActKind      JournalKind
	DocumentKind string
}

// JournalActOrder is the order a credit line or a delivery change belongs to,
// as a document naming it is booked (ADR 0419).
type JournalActOrder struct {
	ID string
	// Kind is "credit_line" or "delivery_change".
	Kind         string
	OrderID      string
	CurrencyCode string
}

// JournalCause is an order record a refund can name as its cause (ADR 0189).
type JournalCause struct {
	// ID is the return's, the claim's or the exchange's id, which the refund
	// carries as its reference (ADR 0187).
	ID string
	// Kind is "return", "claim" or "exchange".
	Kind         string
	OrderID      string
	CurrencyCode string
}

// AfterSaleCause is one of an order's returns, claims or exchanges, as its acts
// after the sale are read from it (ADR 0406).
type AfterSaleCause struct {
	// ID is the record's id, which a refund it caused carries as its
	// reference (ADR 0187).
	ID string
	// Kind is "return", "claim" or "exchange".
	Kind string
	// FundedAt is when an exchange's difference was funded; nil otherwise.
	FundedAt *time.Time
	// DifferenceDue is an exchange's difference; zero otherwise.
	DifferenceDue int64
}
