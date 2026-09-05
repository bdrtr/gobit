package service

import (
	"context"
	"sort"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// LinkOrderFulfillment binds an order to the shipments opened for it.
//
// The definition is declared by the FULFILLMENT module (ADR 0005). The name is
// repeated here as a literal for the same reason [LinkOrderPayment] is: reaching
// into another module for a string would tie this one to it at compile time.
const LinkOrderFulfillment = "order_fulfillment"

// EntityFulfillment is the shipment's entity name in the read layer.
const EntityFulfillment = "fulfillment"

// Shipment fields read through the Query layer.
const (
	fieldShipmentStatus    = "status"
	fieldShipmentCreated   = "created_at"
	fieldShipmentShipped   = "shipped_at"
	fieldShipmentDelivered = "delivered_at"
	fieldShipmentCanceled  = "canceled_at"
	fieldShipmentTracking  = "tracking_number"
)

// The kinds a timeline entry can have.
//
// They are dotted "<source>.<what happened>" so a client can group by prefix
// without a second field, and they are CONSTANTS because a support screen will
// switch on them: a kind invented at a call site is one nobody can render.
const (
	KindOrderPlaced        = "order.placed"
	KindOrderCompleted     = "order.completed"
	KindOrderCanceled      = "order.canceled"
	KindPaymentCaptured    = "payment.captured"
	KindPaymentRefunded    = "payment.refunded"
	KindShipmentOpened     = "shipment.opened"
	KindShipmentShipped    = "shipment.shipped"
	KindShipmentDelivered  = "shipment.delivered"
	KindShipmentCanceled   = "shipment.canceled"
	KindReturnOpened       = "return.opened"
	KindReturnReceived     = "return.received"
	KindReturnCanceled     = "return.canceled"
	KindClaimOpened        = "claim.opened"
	KindClaimCompleted     = "claim.completed"
	KindClaimCanceled      = "claim.canceled"
	KindExchangeOpened     = "exchange.opened"
	KindExchangeUnfinished = "exchange.unfinished"
)

// The clock that stamped a moment.
//
// # Why this is on every entry
//
// The moments do NOT share one axis and it was measured. The order's own
// stamps, the return's and the claim's come from the DATABASE (`now()` in the
// query, which is the transaction's start on one cluster). The capture and the
// shipment transitions come from the APPLICATION — whichever process ran the
// write, with whatever its clock said.
//
// On one machine the two agree and nothing shows. Across machines they can
// disagree by more than the gap between two events, and then a timeline sorted
// by time prints a capture BEFORE the order it paid for. Hiding that behind a
// single sorted list would be presenting a guess as a fact; naming the clock on
// each entry lets whoever reads it see why two lines look out of order.
const (
	// ClockDatabase means the moment came from the database's now().
	ClockDatabase = "database"
	// ClockApplication means the moment came from the process that wrote it.
	ClockApplication = "application"
)

// TimelineEntry is one thing that happened to an order.
type TimelineEntry struct {
	// At is when it happened. NIL means the fact is real and its moment was
	// never recorded — see [Service.Timeline].
	At *time.Time
	// Kind is what happened, as one of the Kind constants.
	Kind string
	// RefID is the record the moment belongs to: the order, the shipment, the
	// return, the claim or the payment collection.
	RefID string
	// Clock says which clock stamped At; it is empty when At is nil.
	Clock string
	// Detail is a short human-facing extra — a status, a tracking number. It is
	// empty when there is nothing to add.
	Detail string
	// Amount and Currency are filled in on the money entries only.
	Amount   int64
	Currency string
}

// Timeline is everything that happened to an order, newest first.
//
// # Why it is composed and not a table
//
// Every moment it shows is ALREADY a stamped row — the order's own columns, the
// return's and the claim's, the shipment's three transition stamps, the
// capture and the refund. A timeline table would be a second copy of rows that
// exist, and it would need a writer inside every one of those modules'
// transactions: as many places for the copy to drift from the row. This
// repository keeps exactly one such duplication (the order's recorded money
// beside the live payment view) and only because the two are CROSS-CHECKED so a
// divergence becomes visible. A mirror with no cross-check has no such defense.
//
// What a table would buy is the fleet-wide question — "everything that shipped
// yesterday" — which no provider can answer today because none offers a time
// FILTER. That is a different feature.
//
// # Facts with no moment are kept, not dropped
//
// Two things are known to have happened and carry no stamp: an exchange that
// was completed or canceled (the columns exist and nothing writes them) and an
// archived order (the status flips and no moment is recorded). They come back
// with a nil At, LAST. Dropping them would make the timeline silently shorter
// than the truth, which is the failure this repository keeps writing
// constraints against.
func (s *Service) Timeline(ctx context.Context, orderID string) ([]TimelineEntry, error) {
	order, err := s.GetOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}

	entries := orderEntries(order)

	money, shipments, err := s.timelineGraph(ctx, orderID)
	if err != nil {
		return nil, err
	}
	entries = append(entries, money...)
	entries = append(entries, shipments...)

	afterSales, err := s.afterSalesEntries(ctx, orderID)
	if err != nil {
		return nil, err
	}
	entries = append(entries, afterSales...)

	sortTimeline(entries)

	return entries, nil
}

// orderEntries are the moments the order's own row carries.
func orderEntries(order models.OrderDetail) []TimelineEntry {
	entries := []TimelineEntry{{
		At:     &order.PlacedAt,
		Kind:   KindOrderPlaced,
		RefID:  order.ID,
		Clock:  ClockDatabase,
		Detail: order.Status.String(),
	}}

	if order.CompletedAt != nil {
		entries = append(entries, TimelineEntry{
			At: order.CompletedAt, Kind: KindOrderCompleted, RefID: order.ID,
			Clock: ClockDatabase,
		})
	}
	if order.CanceledAt != nil {
		entries = append(entries, TimelineEntry{
			At: order.CanceledAt, Kind: KindOrderCanceled, RefID: order.ID,
			Clock: ClockDatabase,
		})
	}

	return entries
}

// sortTimeline puts the newest first and the undated last.
//
// The undated are not sorted among themselves: there is nothing to sort them
// by, and inventing an order would suggest one.
func sortTimeline(entries []TimelineEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		switch {
		case entries[i].At == nil:
			return false
		case entries[j].At == nil:
			return true
		default:
			return entries[i].At.After(*entries[j].At)
		}
	})
}

// timelineGraph reads the money and the shipments in ONE cross-module request.
//
// Both hang off the order through links the far sides declared, so one Graph
// with two expansions answers both — and the read layer resolves each expansion
// in batch, which is the reason this is one round trip rather than one per
// shipment.
func (s *Service) timelineGraph(
	ctx context.Context, orderID string,
) (money, shipments []TimelineEntry, err error) {
	if s.catalog == nil {
		return nil, nil, errors.Internal(CodeNotReady,
			"the query layer is not wired, so an order's timeline cannot be assembled")
	}

	records, err := s.catalog.Graph(ctx, query.GraphSpec{
		Entity:  EntityName,
		Fields:  []string{query.IDField},
		Filters: map[string]any{query.IDField: orderID},
		Limit:   1,
		Expand: []query.Expansion{
			{
				Link: LinkOrderPayment,
				As:   EntityPaymentCollection,
				Fields: []string{
					query.IDField, fieldPaymentCurrency,
					fieldPaymentCaptured, fieldPaymentRefunded,
					fieldPaymentFirstCaptured, fieldPaymentLastRefunded,
				},
			},
			{
				Link: LinkOrderFulfillment,
				As:   EntityFulfillment,
				Fields: []string{
					query.IDField, fieldShipmentStatus, fieldShipmentTracking,
					fieldShipmentCreated, fieldShipmentShipped,
					fieldShipmentDelivered, fieldShipmentCanceled,
				},
			},
		},
	})
	if err != nil {
		return nil, nil, errors.Wrap(err, errors.KindOf(err), CodeCatalogReadFailed,
			"the timeline of order %s could not be read", orderID)
	}
	if len(records) == 0 {
		return nil, nil, errors.NotFound(CodeOrderNotFound, "order not found: %s", orderID)
	}

	if collection, ok := firstExpanded(records[0][EntityPaymentCollection]); ok {
		money = moneyEntries(collection)
	}

	return money, shipmentEntries(records[0][EntityFulfillment]), nil
}

// moneyEntries are the two money moments, when they happened.
func moneyEntries(collection query.Record) []TimelineEntry {
	collectionID := recordText(collection, query.IDField)
	currency := recordText(collection, fieldPaymentCurrency)

	var entries []TimelineEntry
	if at := recordTime(collection, fieldPaymentFirstCaptured); at != nil {
		entries = append(entries, TimelineEntry{
			At: at, Kind: KindPaymentCaptured, RefID: collectionID,
			// The capture's moment is stamped by the process that captured, not
			// by the database — see the Clock constants.
			Clock:    ClockApplication,
			Amount:   recordInt(collection, fieldPaymentCaptured),
			Currency: currency,
		})
	}
	if at := recordTime(collection, fieldPaymentLastRefunded); at != nil {
		entries = append(entries, TimelineEntry{
			At: at, Kind: KindPaymentRefunded, RefID: collectionID,
			Clock:    ClockDatabase,
			Amount:   recordInt(collection, fieldPaymentRefunded),
			Currency: currency,
		})
	}

	return entries
}

// shipmentEntries are every moment of every parcel.
func shipmentEntries(raw any) []TimelineEntry {
	records, ok := raw.([]query.Record)
	if !ok {
		return nil
	}

	var entries []TimelineEntry
	for _, record := range records {
		id := recordText(record, query.IDField)
		tracking := recordText(record, fieldShipmentTracking)

		for _, moment := range []struct {
			field string
			kind  string
		}{
			{fieldShipmentCreated, KindShipmentOpened},
			{fieldShipmentShipped, KindShipmentShipped},
			{fieldShipmentDelivered, KindShipmentDelivered},
			{fieldShipmentCanceled, KindShipmentCanceled},
		} {
			at := recordTime(record, moment.field)
			if at == nil {
				continue
			}
			entries = append(entries, TimelineEntry{
				At: at, Kind: moment.kind, RefID: id,
				// created_at is the database's; the three transitions are
				// stamped by the fulfillment service's own clock.
				Clock:  shipmentClock(moment.field),
				Detail: tracking,
			})
		}
	}

	return entries
}

// shipmentClock says which clock stamped a shipment field.
func shipmentClock(field string) string {
	if field == fieldShipmentCreated {
		return ClockDatabase
	}

	return ClockApplication
}

// afterSalesEntries are the returns, claims and exchanges of the order.
//
// They are read from this module's OWN tables rather than through the read
// layer: they belong to the order, and going out through Query to fetch a
// module's own rows would be a round trip to reach the desk it is sitting at.
func (s *Service) afterSalesEntries(ctx context.Context, orderID string) ([]TimelineEntry, error) {
	var entries []TimelineEntry

	returns, _, err := s.ListReturns(ctx, orderID, Page{Limit: timelinePageLimit})
	if err != nil {
		return nil, err
	}
	for i := range returns {
		entries = append(entries, TimelineEntry{
			At: &returns[i].CreatedAt, Kind: KindReturnOpened, RefID: returns[i].ID,
			Clock: ClockDatabase, Detail: returns[i].Status.String(),
			Amount: returns[i].RefundAmount,
		})
		entries = appendMoment(entries, returns[i].ReceivedAt, KindReturnReceived, returns[i].ID)
		entries = appendMoment(entries, returns[i].CanceledAt, KindReturnCanceled, returns[i].ID)
	}

	claims, _, err := s.ListClaims(ctx, orderID, Page{Limit: timelinePageLimit})
	if err != nil {
		return nil, err
	}
	for i := range claims {
		entries = append(entries, TimelineEntry{
			At: &claims[i].CreatedAt, Kind: KindClaimOpened, RefID: claims[i].ID,
			Clock: ClockDatabase, Detail: claims[i].Status.String(),
			Amount: claims[i].RefundAmount,
		})
		entries = appendMoment(entries, claims[i].CompletedAt, KindClaimCompleted, claims[i].ID)
		entries = appendMoment(entries, claims[i].CanceledAt, KindClaimCanceled, claims[i].ID)
	}

	exchanges, _, err := s.ListExchanges(ctx, orderID, Page{Limit: timelinePageLimit})
	if err != nil {
		return nil, err
	}
	for i := range exchanges {
		entries = append(entries, TimelineEntry{
			At: &exchanges[i].CreatedAt, Kind: KindExchangeOpened, RefID: exchanges[i].ID,
			Clock: ClockDatabase, Detail: exchanges[i].Status.String(),
		})
		// An exchange that is finished carries NO moment: the columns exist and
		// nothing writes them (gaps.md D4). The fact is reported with a nil At
		// rather than dropped, because a timeline shorter than the truth is the
		// failure that hides a bug instead of showing it.
		if exchanges[i].Status != models.ExchangeRequested {
			entries = append(entries, TimelineEntry{
				Kind: KindExchangeUnfinished, RefID: exchanges[i].ID,
				Detail: exchanges[i].Status.String(),
			})
		}
	}

	return entries, nil
}

// appendMoment adds an entry when the moment happened.
func appendMoment(entries []TimelineEntry, at *time.Time, kind, refID string) []TimelineEntry {
	if at == nil {
		return entries
	}

	return append(entries, TimelineEntry{At: at, Kind: kind, RefID: refID, Clock: ClockDatabase})
}

// timelinePageLimit bounds each after-sales read.
//
// An order with more returns than this has a bigger problem than a truncated
// timeline, and the alternative — an unbounded read — would let one order pull
// an unbounded number of rows into memory.
const timelinePageLimit = 100
