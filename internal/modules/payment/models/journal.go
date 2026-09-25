package models

import "time"

// This file is the payment module's side of the books (ADR 0186).
//
// Nothing here is WRITTEN. Every entry is derived, when it is read, from a row
// this module already keeps and never deletes: a capture, a refund, a store
// credit grant, a loyalty grant. A second table recording the same money would be
// the mirror ADR 0119 refused, and two records of one movement can disagree; a
// derived entry cannot disagree with the row it is read from.

// JournalAccount is one side of the module's books.
//
// The set is closed and small, because each account answers one question an
// accountant asks of a payment module. A provider or a customer is a DIMENSION
// on a line ([JournalLine.ProviderID], [JournalLine.CustomerID]), not an account
// of its own: the chart stays six names however many providers and customers
// there are.
type JournalAccount string

// The accounts.
const (
	// AccountReceivable is what buyers owe the shop for their payment
	// collections. A capture credits it and a refund debits it. Its debit side,
	// the order that created the obligation, is the order module's to publish,
	// so on this module's books alone it runs negative: it is the net money
	// received.
	AccountReceivable JournalAccount = "receivable"
	// AccountProviderClearing is money a payment provider holds or has paid out
	// for the shop, one line per provider.
	AccountProviderClearing JournalAccount = "provider_clearing"
	// AccountStoreCredit is the store credit the shop owes its customers.
	AccountStoreCredit JournalAccount = "store_credit"
	// AccountStoreCreditGranted is what the store credit the shop gave cost it.
	AccountStoreCreditGranted JournalAccount = "store_credit_granted"
	// AccountLoyalty is the loyalty points the shop owes, at one minor unit a
	// point (ADR 0165).
	AccountLoyalty JournalAccount = "loyalty"
	// AccountLoyaltyGranted is what the points the shop gave cost it.
	AccountLoyaltyGranted JournalAccount = "loyalty_granted"
)

// JournalKind is the movement an entry is read from.
type JournalKind string

// The kinds.
const (
	// JournalCapture is a row of payments.
	JournalCapture JournalKind = "capture"
	// JournalRefund is a row of refunds.
	JournalRefund JournalKind = "refund"
	// JournalStoreCreditIssue is an issue row of the store credit ledger.
	JournalStoreCreditIssue JournalKind = "store_credit_issue"
	// JournalLoyaltyEarn is an earn row of the loyalty ledger.
	JournalLoyaltyEarn JournalKind = "loyalty_earn"
	// JournalLoyaltyReverse is a reverse row of the loyalty ledger.
	JournalLoyaltyReverse JournalKind = "loyalty_reverse"
)

// JournalLine is one side of an entry. Exactly one of Debit and Credit is
// positive, and the other is zero.
type JournalLine struct {
	Account JournalAccount `json:"account"`
	// ProviderID names the provider on a provider clearing line.
	ProviderID string `json:"provider_id,omitempty"`
	// CustomerID names whose balance a store credit or loyalty line moves.
	CustomerID string `json:"customer_id,omitempty"`
	Debit      int64  `json:"debit"`
	Credit     int64  `json:"credit"`
}

// JournalEntry is one balanced movement: its lines' debits equal their credits.
type JournalEntry struct {
	// ID is the id of the row the entry is read from; the kind says which table.
	ID   string      `json:"id"`
	Kind JournalKind `json:"kind"`
	// OccurredAt is the moment the row records: a capture's captured_at, and the
	// created_at of the others.
	OccurredAt   time.Time `json:"occurred_at"`
	CurrencyCode string    `json:"currency_code"`
	// CollectionID is the payment collection of a capture or a refund.
	CollectionID string        `json:"collection_id,omitempty"`
	Lines        []JournalLine `json:"lines"`
}

// JournalBalance is one account's sums over a window, in one currency.
type JournalBalance struct {
	CurrencyCode string         `json:"currency_code"`
	Account      JournalAccount `json:"account"`
	ProviderID   string         `json:"provider_id,omitempty"`
	Debit        int64          `json:"debit"`
	Credit       int64          `json:"credit"`
}

// JournalMovement is one row the journal is derived from, as the repository
// read it and before any account is named.
//
// Amount carries the row's own sign: a loyalty reverse row's points are
// negative, every other kind is positive. Which accounts a movement touches is
// the service's decision, not the repository's.
type JournalMovement struct {
	ID           string
	Kind         JournalKind
	OccurredAt   time.Time
	CurrencyCode string
	Amount       int64
	// CollectionID is set on a capture and a refund.
	CollectionID string
	// ProviderID is the provider of the session a capture or refund came from.
	ProviderID string
	// CustomerID is the collection's customer on a capture or refund, and the
	// ledger's customer on a grant; empty for a collection that names nobody.
	CustomerID string
}
