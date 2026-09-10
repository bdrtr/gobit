package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/query"
)

// The payment topics this module listens to.
//
// The names are REPEATED here as literals rather than imported: the payment
// module and this one do not know each other (ADR 0006), and reaching for a
// constant across that line would tie them together at compile time. It is the
// same repetition the link names already take, for the same reason and at the
// same price — a rename on one side is caught by nothing but a test that runs
// both.
const (
	// TopicPaymentCaptured is emitted when money is collected.
	TopicPaymentCaptured = "payment.captured"
	// TopicPaymentRefunded is emitted when money is sent back.
	TopicPaymentRefunded = "payment.refunded"
)

// EventFieldPaymentCollectionID is the only field of those events this module
// reads.
//
// They carry no amount on purpose: a refund is not idempotent on the publishing
// side, so a figure in the payload would be an increment, and the bus delivers
// at least once. What this subscriber wants is the CUMULATIVE total, which it
// reads from the collection at the moment it is told.
const EventFieldPaymentCollectionID = "payment_collection_id"

// EventSubscriber is the NARROW surface this module needs to LISTEN.
//
// It stands beside [EventPublisher] rather than replacing it, and the split is
// the point: publishing and subscribing are two authorities, and a module that
// held the whole bus would look able to close it. This module now does both,
// and says so in two interfaces rather than one.
type EventSubscriber interface {
	// Subscribe registers the handler for the named event.
	Subscribe(eventName string, h eventbus.Handler) error
}

// CodeMoneyEventUnusable reports an event this module cannot act on.
const CodeMoneyEventUnusable = "order_money_event_unusable"

// HandleMoneyMoved brings the order's recorded money up to date after the
// payment module moved some.
//
// # Why this exists at all
//
// The order's summary is a REPORT of what the payment module holds, and until
// this subscriber only two flows ever wrote it — the checkout clearing a cart
// and the returns flow making a refund. The payment module publishes routes
// that capture and refund directly, neither flow is on those paths, and the
// report therefore went stale in silence (gap D55, and its sibling on the
// capture route). ADR 0022 named this subscriber as "the better home" for the
// write on the day the payment module published, and this is that day.
//
// # It reads rather than believes
//
// The event names a collection and carries no figures. This reads the
// collection's cumulative captured and refunded totals through the query layer
// and reports THOSE. That is what keeps ADR 0119 intact: the rule forbids an
// order row from holding a payment figure as its own truth, not from holding a
// report — and the report is produced by asking, at the moment it is written.
//
// # Why a missing binding is not an error
//
// A collection that no order is bound to is a real and ordinary thing: a cart
// that never became an order has one, and so does an exchange's difference. The
// handler returns nil for those. Returning an error would put the bus into a
// retry loop over an event that will never become actionable.
func (s *Service) HandleMoneyMoved(ctx context.Context, e eventbus.Event) error {
	collectionID, _ := e.Data[EventFieldPaymentCollectionID].(string)
	if collectionID == "" {
		return errors.Invalid(CodeMoneyEventUnusable,
			"the %q event carries no %s", e.Name, EventFieldPaymentCollectionID)
	}

	orderID, paid, refunded, found, err := s.moneyOfCollection(ctx, collectionID)
	if err != nil {
		return err
	}
	if !found {
		s.log.DebugContext(ctx, "a payment event named a collection no order is bound to",
			"event", e.Name, "payment_collection_id", collectionID)

		return nil
	}

	if _, err := s.SetOrderSummaryTotals(ctx, orderID, SummaryTotalsInput{
		PaidTotal: paid, RefundedTotal: refunded,
	}); err != nil {
		return errors.Wrap(err, errors.KindOf(err), CodeMoneyEventUnusable,
			"the order's recorded money could not be brought up to date: %s", orderID)
	}

	return nil
}

// moneyOfCollection reads a collection and the order bound to it in ONE query.
//
// # Why the collection is the root
//
// The order is reached BACKWARDS over `order_payment`, which is the query
// layer's own contract: with the root entity on the To end of a link the
// expansion goes the other way, and a one-to-one link puts exactly one record
// on the other side. Going forwards is not possible here — the handler starts
// from a collection and has no order to filter on.
//
// The collection's `reference` cannot stand in for the link. The checkout
// writes the CART's identifier there, which the payment module records as its
// own decision, so a reader that filtered on it would find carts and miss
// orders.
func (s *Service) moneyOfCollection(ctx context.Context, collectionID string) (
	orderID string, paid, refunded int64, found bool, err error,
) {
	if s.catalog == nil {
		return "", 0, 0, false, errors.Internal(CodeNotReady,
			"the query layer is not wired, so a payment event cannot be acted on")
	}

	records, err := s.catalog.Graph(ctx, query.GraphSpec{
		Entity:  EntityPaymentCollection,
		Fields:  []string{query.IDField, fieldPaymentCaptured, fieldPaymentRefunded},
		Filters: map[string]any{query.IDField: collectionID},
		Limit:   1,
		Expand: []query.Expansion{{
			Link:   LinkOrderPayment,
			As:     EntityName,
			Fields: []string{query.IDField},
		}},
	})
	if err != nil {
		return "", 0, 0, false, errors.Wrap(err, errors.KindOf(err), CodeCatalogReadFailed,
			"the collection of a payment event could not be read: %s", collectionID)
	}
	if len(records) == 0 {
		return "", 0, 0, false, nil
	}

	order, ok := firstExpanded(records[0][EntityName])
	if !ok {
		return "", 0, 0, false, nil
	}

	orderID = recordText(order, query.IDField)
	if orderID == "" {
		return "", 0, 0, false, nil
	}

	return orderID,
		recordInt(records[0], fieldPaymentCaptured),
		recordInt(records[0], fieldPaymentRefunded),
		true, nil
}
