package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// Store credit: what the shop owes a customer, and the customer spending it
// (ADR 0152).
//
// # Why it lives in the payment module
//
// Because store credit is a PAYMENT METHOD. It is spent through the same provider
// slot a card is spent through, so the module that owns sessions, captures and
// refunds owns this too. Had it gone into the customer module, that module would
// have become a party to money movements and the checkout saga would have had two
// modules to compensate instead of one.
//
// # The service WRITES the ledger, the provider SPENDS it
//
// The two methods here are the operator's acts: give credit, read the balance.
// The hold, release and refund rows are written by the store-credit PROVIDER
// (payment/storecredit), because what produces them is the payment session's state
// machine and that machine belongs to the core contract.

// Store credit error codes.
const (
	// CodeStoreCreditInvalidInput reports a credit input that makes no sense.
	CodeStoreCreditInvalidInput = "payment_store_credit_invalid_input"
	// CodeStoreCreditInsufficient reports a balance that does not cover the amount.
	//
	// The provider answers with it as a DECLINE rather than an error: not enough
	// balance, like a refused card, is an ordinary outcome of paying.
	CodeStoreCreditInsufficient = "payment_store_credit_insufficient"
)

// IssueCreditInput is the input for putting credit on a customer's account.
type IssueCreditInput struct {
	// CustomerID is who receives the credit; it is required and is NOT A FOREIGN
	// KEY (Principle 2.2): the customer record belongs to the customer module.
	CustomerID string
	// CurrencyCode is the ISO 4217 code; it is required.
	//
	// Credit in one currency is not credit in another, and the balance is read per
	// currency as well: converting is not this module's job, and converting
	// silently would hand the customer an amount other than the one promised.
	CurrencyCode string
	// Amount is what is given (minor unit) and has to be POSITIVE.
	//
	// There is no "take credit back" through a negative amount: taking it back is a
	// separate decision, and allowing it here would leave nothing between an
	// operator and a customer balance below zero.
	Amount int64
	// Reason is why the credit was given; it is required.
	//
	// This is the half a balance column cannot hold, and it may not be left empty:
	// the answer to "why does this customer have 200 lira" will never exist again
	// if it is not in the record the moment it is asked.
	Reason string
	// Reference is the identifier of the operator's own record (a ticket, a
	// return); it may be empty.
	Reference string
}

// IssueCredit puts store credit on a customer's account and returns the new row.
//
// The write is ONE ROW and needs no lock: the ledger is append-only, and one
// append does not race another. What needs the lock is READING the balance and
// acting on it — and that is on the spending side (see the storecredit provider).
func (s *Service) IssueCredit(
	ctx context.Context, in IssueCreditInput,
) (models.StoreCreditEntry, error) {
	customerID := strings.TrimSpace(in.CustomerID)
	if customerID == "" {
		return models.StoreCreditEntry{}, errors.Invalid(CodeStoreCreditInvalidInput,
			"the credit needs an owner: customer_id cannot be empty")
	}

	currency, err := normalizeCurrency(in.CurrencyCode)
	if err != nil {
		return models.StoreCreditEntry{}, err
	}

	if in.Amount <= 0 {
		return models.StoreCreditEntry{}, errors.Invalid(CodeStoreCreditInvalidInput,
			"the credit amount has to be positive, %d given; taking credit back is a "+
				"separate decision and a negative row would push the balance below zero",
			in.Amount)
	}

	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return models.StoreCreditEntry{}, errors.Invalid(CodeStoreCreditInvalidInput,
			"the credit needs a reason: WHY the balance exists will never exist again "+
				"if it is not in the record the moment it is asked")
	}

	entry, err := s.store.AppendStoreCreditEntry(ctx, models.StoreCreditEntry{
		ID:           models.NewStoreCreditEntryID(),
		CustomerID:   customerID,
		CurrencyCode: currency,
		Amount:       in.Amount,
		Kind:         models.StoreCreditIssue,
		Reference:    strings.TrimSpace(in.Reference),
		Reason:       reason,
	})
	if err != nil {
		return models.StoreCreditEntry{}, err
	}

	s.log.InfoContext(ctx, "store credit issued",
		"customer", customerID, "amount", in.Amount, "currency", currency,
		"entry", entry.ID)

	return entry, nil
}

// StoreCreditBalance returns a customer's balance in ONE currency.
//
// A customer with no rows gets ZERO, and that is not an absence: somebody who was
// never given credit and somebody who was given it and spent all of it hold the
// same amount of money, and what tells them apart is the ledger itself.
func (s *Service) StoreCreditBalance(
	ctx context.Context, customerID, currencyCode string,
) (int64, error) {
	customerID = strings.TrimSpace(customerID)
	if customerID == "" {
		return 0, errors.Invalid(CodeStoreCreditInvalidInput,
			"the balance needs an owner: customer_id cannot be empty")
	}

	currency, err := normalizeCurrency(currencyCode)
	if err != nil {
		return 0, err
	}

	return s.store.StoreCreditBalance(ctx, customerID, currency)
}

// ListStoreCreditInput is the input for listing a credit history.
type ListStoreCreditInput struct {
	// CustomerID and CurrencyCode say which ledger is read.
	CustomerID   string
	CurrencyCode string
	// Page holds the paging parameters.
	Page Page
}

// ListStoreCredit returns a customer's credit history, newest first.
//
// The history is published alongside the balance because the operator's question
// is never only "how much is left" but "why" — and that answer is in the rows.
func (s *Service) ListStoreCredit(
	ctx context.Context, in ListStoreCreditInput,
) ([]models.StoreCreditEntry, int64, error) {
	customerID := strings.TrimSpace(in.CustomerID)
	if customerID == "" {
		return nil, 0, errors.Invalid(CodeStoreCreditInvalidInput,
			"the history needs an owner: customer_id cannot be empty")
	}

	currency, err := normalizeCurrency(in.CurrencyCode)
	if err != nil {
		return nil, 0, err
	}

	page, err := in.Page.normalize()
	if err != nil {
		return nil, 0, err
	}

	return s.store.ListStoreCreditEntries(ctx, customerID, currency,
		page.Limit, page.Offset)
}
