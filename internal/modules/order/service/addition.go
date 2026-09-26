package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// The refusals of an order that names an order it adds to (ADR 0192).
const (
	// CodeAdditionNeedsCustomer refuses an addition that names no customer: a
	// guest proves nothing that ties a second purchase to the first.
	CodeAdditionNeedsCustomer = "order_addition_needs_customer"
	// CodeAdditionCustomerMismatch refuses an addition whose customer is not
	// the parent's.
	CodeAdditionCustomerMismatch = "order_addition_customer_mismatch"
	// CodeAdditionCurrencyMismatch refuses an addition in another currency than
	// the parent's.
	CodeAdditionCurrencyMismatch = "order_addition_currency_mismatch"
	// CodeAdditionParentNotPending refuses an addition to an order that is
	// completed, archived or canceled.
	CodeAdditionParentNotPending = "order_addition_parent_not_pending"
	// CodeAdditionParentIsAddition refuses an addition to an order that is an
	// addition itself; every addition names the order it adds to directly.
	CodeAdditionParentIsAddition = "order_addition_parent_is_addition"
)

// CheckAddition answers whether an order of customerID in currencyCode may add
// to orderID now, and returns nil when it may.
//
// It is the question a cart asks when it is opened for an addition, so a
// mistyped or closed order is refused before anything is bought. The answer
// can change before the addition is placed, and the write asks again under a
// lock on the parent ([Service.writeOrder]); this read takes none.
func (s *Service) CheckAddition(ctx context.Context, orderID, customerID, currencyCode string) error {
	if err := requireID("adds_to_order_id", orderID); err != nil {
		return err
	}
	if err := optionalID("customer_id", customerID); err != nil {
		return err
	}
	currency, err := normalizeCurrency(currencyCode)
	if err != nil {
		return err
	}

	parent, err := s.store.GetOrder(ctx, orderID)
	if err != nil {
		return err
	}
	return additionRefusal(parent, customerID, currency)
}

// additionRefusal says why an order of customerID in currencyCode may not add
// to parent, or returns nil.
//
// The rules need the parent's row, so no CHECK can hold them; the write holds
// them by reading the parent under a share lock in its own transaction. The
// parent's customer and currency never change, and whether it is an addition
// is written once when it is placed, so only its status can move under a
// reader, and the lock is what keeps that still.
func additionRefusal(parent models.Order, customerID, currencyCode string) error {
	if customerID == "" {
		return errors.Conflict(CodeAdditionNeedsCustomer,
			"an order adding to %s has to name its customer; a guest's order adds to nothing",
			parent.ID)
	}
	if parent.AddsToOrderID != "" {
		return errors.Conflict(CodeAdditionParentIsAddition,
			"order %s adds to %s; an addition names that order instead",
			parent.ID, parent.AddsToOrderID)
	}
	if parent.Status != models.OrderPending {
		return errors.Conflict(CodeAdditionParentNotPending,
			"order %s is %s; only a pending order can be added to", parent.ID, parent.Status)
	}
	if parent.CustomerID != customerID {
		return errors.Conflict(CodeAdditionCustomerMismatch,
			"order %s belongs to another customer than %s", parent.ID, customerID)
	}
	if parent.CurrencyCode != currencyCode {
		return errors.Conflict(CodeAdditionCurrencyMismatch,
			"order %s is in %s and the addition in %s", parent.ID, parent.CurrencyCode, currencyCode)
	}
	return nil
}
