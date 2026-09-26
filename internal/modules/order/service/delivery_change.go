package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// The refusals of a delivery change (ADR 0199).
const (
	// CodeDeliveryNotChangeable refuses a change on an order that is not
	// pending.
	CodeDeliveryNotChangeable = "order_delivery_not_changeable"
	// CodeDeliveryMissing refuses a change that names a shipping method the
	// order does not have.
	CodeDeliveryMissing = "order_delivery_missing"
	// CodeDeliveryCostsMore refuses a change that costs more than the delivery
	// it replaces and names no payment for the difference. Its details say
	// what to collect (ADR 0200).
	CodeDeliveryCostsMore = "order_delivery_costs_more"
	// CodeDeliveryPaymentMismatch refuses a payment that does not hold exactly
	// the difference.
	CodeDeliveryPaymentMismatch = "order_delivery_payment_mismatch"
	// CodeDeliveryTakesNoPayment refuses a payment named for a change that
	// costs no more: the money would pay for nothing.
	CodeDeliveryTakesNoPayment = "order_delivery_takes_no_payment"
	// CodeCollectionTaken refuses a payment collection that already paid for a
	// delivery change or funded an exchange (ADR 0200, D142).
	CodeCollectionTaken = "order_payment_collection_taken"
)

// CreditReasonDeliveryChange is the reason on the credit line a cheaper
// delivery writes (ADR 0199).
const CreditReasonDeliveryChange = "delivery_change"

// ChangeDeliveryInput is a new service for one of the order's deliveries, as
// the fulfillment module quoted it.
type ChangeDeliveryInput struct {
	// ShippingMethodID is the method to change.
	ShippingMethodID string
	// ShippingOptionID, Name and Amount are the quoted service. This module
	// takes the quote as given: pricing an option is the fulfillment module's,
	// and the caller that asked it is the one that can.
	ShippingOptionID string
	Name             string
	Amount           int64
	// PaymentCollectionID and Paid are the payment collection that took the
	// difference of a dearer change and what it holds, as the caller read it
	// from the payment module (ADR 0200). A change that costs no more names
	// neither.
	PaymentCollectionID string
	Paid                int64
}

// ChangeDelivery changes one of the order's deliveries and returns the change
// it wrote, or nil when the method is already on that option (ADR 0199).
//
// The difference is the quoted amount less the amount of the delivery being
// replaced — the method's latest change, or the method itself — and it is
// computed under the order's lock, so two changes run one after the other and
// each is priced against the one before it.
//
//   - A difference of zero writes the change and nothing else.
//   - A negative one also writes a credit line for the difference in the same
//     transaction (ADR 0105), and the change names it.
//   - A positive one is written only with a payment collection that holds
//     exactly the difference and paid for nothing else, and the change names
//     it (ADR 0200). Without one it is refused, and the refusal's details say
//     what to collect.
//
// A change is refused on an order that is not pending, and on a method the
// order does not have. Whether a parcel is already on its way is not this
// module's to know; the caller that reads the parcels asks that first.
func (s *Service) ChangeDelivery(
	ctx context.Context, orderID string, in ChangeDeliveryInput,
) (*models.DeliveryChange, error) {
	if err := requireID("order_id", orderID); err != nil {
		return nil, err
	}
	if err := requireID("shipping_method_id", in.ShippingMethodID); err != nil {
		return nil, err
	}
	in.ShippingOptionID = strings.TrimSpace(in.ShippingOptionID)
	in.Name = strings.TrimSpace(in.Name)
	if err := requireText("shipping_option_id", in.ShippingOptionID); err != nil {
		return nil, err
	}
	if err := requireText("name", in.Name); err != nil {
		return nil, err
	}
	if err := checkAmount("amount", in.Amount, models.MaxTotal); err != nil {
		return nil, err
	}
	if in.PaymentCollectionID != "" {
		if err := requireID("payment_collection_id", in.PaymentCollectionID); err != nil {
			return nil, err
		}
	}
	if err := checkAmount("paid", in.Paid, models.MaxTotal); err != nil {
		return nil, err
	}

	var written *models.DeliveryChange
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		order, err := s.store.LockOrder(ctx, orderID)
		if err != nil {
			return err
		}
		if order.Status != models.OrderPending {
			return errors.Conflict(CodeDeliveryNotChangeable,
				"order %s is %s; only a pending order's delivery can be changed", orderID, order.Status)
		}

		methods, err := s.store.OrderShippingMethodsByOrderIDs(ctx, []string{orderID})
		if err != nil {
			return err
		}
		changes, err := s.store.DeliveryChangesByOrderIDs(ctx, []string{orderID})
		if err != nil {
			return err
		}
		current, err := deliveryToChange(orderID, in.ShippingMethodID,
			models.CurrentDeliveries(methods[orderID], changes[orderID]))
		if err != nil {
			return err
		}
		if current.ShippingOptionID == in.ShippingOptionID {
			// A repeated request writes nothing. A payment named with it has
			// to be the one the change already took, or it paid for nothing.
			if in.PaymentCollectionID != "" &&
				latestPayment(changes[orderID], current.ID) != in.PaymentCollectionID {
				return errors.Conflict(CodeDeliveryTakesNoPayment,
					"order %s's delivery is already on %s; collection %s pays for no change",
					orderID, in.ShippingOptionID, in.PaymentCollectionID)
			}

			return nil
		}

		change := models.DeliveryChange{
			ID:               models.NewDeliveryChangeID(),
			OrderID:          orderID,
			ShippingMethodID: current.ID,
			ShippingOptionID: in.ShippingOptionID,
			Name:             in.Name,
			Amount:           in.Amount,
			Difference:       in.Amount - current.Amount,
		}
		if err := s.checkDeliveryPayment(ctx, order, current, in, change.Difference); err != nil {
			return err
		}
		change.PaymentCollectionID = in.PaymentCollectionID
		if change.Difference < 0 {
			credit, err := s.CreateCreditLine(ctx, orderID, CreateCreditLineInput{
				Amount: -change.Difference,
				Reason: CreditReasonDeliveryChange,
				Note:   change.ID,
			})
			if err != nil {
				return err
			}
			change.CreditLineID = credit.ID
		}

		created, err := s.store.CreateDeliveryChange(ctx, change)
		if err != nil {
			return err
		}
		written = &created

		return nil
	})
	if err != nil {
		return nil, err
	}
	if written != nil {
		s.log.InfoContext(ctx, "an order's delivery was changed",
			"order_id", orderID, "delivery_change_id", written.ID,
			"shipping_option_id", written.ShippingOptionID, "difference", written.Difference)
	}

	return written, nil
}

// deliveryToChange finds the method a change applies to, as it stands after
// its earlier changes.
func deliveryToChange(
	orderID, methodID string, deliveries []models.OrderShippingMethod,
) (models.OrderShippingMethod, error) {
	for i := range deliveries {
		if deliveries[i].ID == methodID {
			return deliveries[i], nil
		}
	}

	return models.OrderShippingMethod{}, errors.NotFound(CodeDeliveryMissing,
		"order %s has no shipping method %s", orderID, methodID)
}

// checkDeliveryPayment holds a change's payment to its difference: a dearer
// change names a collection that holds exactly the difference and paid for
// nothing else, and a change that costs no more names none (ADR 0200).
func (s *Service) checkDeliveryPayment(
	ctx context.Context, order models.Order, current models.OrderShippingMethod,
	in ChangeDeliveryInput, difference int64,
) error {
	if difference <= 0 {
		if in.PaymentCollectionID != "" {
			return errors.Conflict(CodeDeliveryTakesNoPayment,
				"%s costs %d against %s's %d, so collection %s would pay for nothing; send "+
					"its money back", in.Name, in.Amount, current.Name, current.Amount,
				in.PaymentCollectionID)
		}

		return nil
	}

	details := map[string]any{
		"shipping_option_id": in.ShippingOptionID,
		"amount":             in.Amount,
		"difference":         difference,
		"currency_code":      order.CurrencyCode,
	}
	if in.PaymentCollectionID == "" {
		return errors.Conflict(CodeDeliveryCostsMore,
			"%s costs %d and order %s's %s costs %d; collect the difference of %d on a "+
				"payment collection opened for the order and name it",
			in.Name, in.Amount, order.ID, current.Name, current.Amount, difference).
			WithDetails(details)
	}
	if in.Paid != difference {
		return errors.Conflict(CodeDeliveryPaymentMismatch,
			"collection %s holds %d and the change costs %d more",
			in.PaymentCollectionID, in.Paid, difference).WithDetails(details)
	}
	taken, err := s.store.CollectionTakenBy(ctx, in.PaymentCollectionID)
	if err != nil {
		return err
	}
	if taken != "" {
		return errors.Conflict(CodeCollectionTaken,
			"collection %s already paid for %s", in.PaymentCollectionID, taken)
	}

	return nil
}

// latestPayment is the collection the method's latest change took, or "".
func latestPayment(changes []models.DeliveryChange, methodID string) string {
	paid := ""
	for i := range changes {
		if changes[i].ShippingMethodID == methodID {
			paid = changes[i].PaymentCollectionID
		}
	}

	return paid
}
