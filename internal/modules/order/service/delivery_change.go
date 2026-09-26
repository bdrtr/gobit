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
	// it replaces: nothing can take the difference yet.
	CodeDeliveryCostsMore = "order_delivery_costs_more"
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
//   - A positive one is refused: the customer would owe more, and no record
//     can take the money yet.
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
		if change.Difference > 0 {
			return errors.Conflict(CodeDeliveryCostsMore,
				"%s costs %d and order %s's %s costs %d; a change that costs more is not taken "+
					"until the difference can be charged",
				in.Name, in.Amount, orderID, current.Name, current.Amount)
		}
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
