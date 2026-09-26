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
	fieldShipmentReturned  = "returned_at"
	fieldShipmentTracking  = "tracking_number"
)

// The kinds a timeline entry can have.
//
// They are dotted "<source>.<what happened>" so a client can group by prefix
// without a second field, and they are CONSTANTS because a support screen will
// switch on them: a kind invented at a call site is one nobody can render.
const (
	KindOrderPlaced       = "order.placed"
	KindOrderCompleted    = "order.completed"
	KindOrderCanceled     = "order.canceled"
	KindOrderArchived     = "order.archived"
	KindPaymentCaptured   = "payment.captured"
	KindPaymentRefunded   = "payment.refunded"
	KindShipmentOpened    = "shipment.opened"
	KindShipmentShipped   = "shipment.shipped"
	KindShipmentDelivered = "shipment.delivered"
	KindShipmentCanceled  = "shipment.canceled"
	KindShipmentReturned  = "shipment.returned"
	KindReturnOpened      = "return.opened"
	KindReturnReceived    = "return.received"
	KindReturnCanceled    = "return.canceled"
	KindClaimOpened       = "claim.opened"
	KindClaimCompleted    = "claim.completed"
	KindClaimCanceled     = "claim.canceled"
	KindExchangeOpened    = "exchange.opened"
	KindExchangeCompleted = "exchange.completed"
	KindExchangeCanceled  = "exchange.canceled"

	// The kinds ADR 0170 added: facts every one of which was a dated row
	// before, and none of which the timeline showed.
	KindOrderLineCanceled     = "order.line_canceled"
	KindOrderCredited         = "order.credited"
	KindOrderDataErased       = "order.personal_data_erased"
	KindReplacementOpened     = "replacement.opened"
	KindReplacementDispatched = "replacement.dispatched"
	KindReplacementCanceled   = "replacement.canceled"
	KindExchangeFunded        = "exchange.funded"

	// KindShippingAddressCorrected is a correction of where the order ships
	// (ADR 0195), dated by the moment the address it replaced was closed. It
	// names the replaced row and carries no address.
	KindShippingAddressCorrected = "order.shipping_address_corrected"

	// KindDeliveryChanged is a delivery put on another service (ADR 0199). Its
	// detail is the new service's name and its amount the new service's price;
	// a difference written off is the credit entry beside it.
	KindDeliveryChanged = "order.delivery_changed"
)

// The payment collection's movements, read through the Query layer (ADR 0170).
//
// The payment module declares them; the names are repeated here as literals for
// the reason [LinkOrderPayment] is.
const (
	fieldPaymentMovements = "movements"
	movementID            = "id"
	movementKind          = "kind"
	movementAmount        = "amount"
	movementAt            = "at"
	movementCapture       = "capture"
	movementRefund        = "refund"
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
	// Amount and Currency are filled in on the money entries only. An amount
	// is what moved AT that moment, never a running total (ADR 0170).
	Amount   int64
	Currency string
	// Quantity is the number of units a goods entry moved; it is set on a line
	// cancellation, and zero elsewhere.
	Quantity int64
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
// The machinery for an undated entry stays — [sortTimeline] puts a nil At last
// and [TimelineEntry.At] is a pointer for that reason — but nothing produces
// one any more, and the two things that used to are worth recording because the
// text here described them WRONGLY.
//
// It claimed an archived order came back with a nil At. It did not come back at
// all: [orderEntries] emitted placed, completed and canceled, and no archived
// entry of any kind existed, so the one moment the paragraph was written to
// defend was the one the code dropped. It is dated now (migration 000007) and
// emitted as [KindOrderArchived].
//
// It also claimed an exchange that was completed or canceled came back undated.
// That entry could never fire: nothing wrote the exchange's status either, so
// every exchange was "requested" and the branch testing for anything else was
// unreachable. Both endings are dated now and both are reported
// ([KindExchangeCompleted], [KindExchangeCanceled]).
//
// The completion half was missing until ADR 0117 for the reason the paragraph
// below names: this godoc said "completion is gone from the record entirely"
// and stayed saying it after ADR 0114 put the status and its column back, so
// the timeline went silent on an ending the record could reach.
//
// The lesson is kept rather than the code: a timeline shorter than the truth is
// the failure that hides a bug instead of showing it, and a comment claiming
// otherwise hides it twice.
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

	facts, err := s.orderFactEntries(ctx, order)
	if err != nil {
		return nil, err
	}
	entries = append(entries, facts...)

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
	// An archived order that carries no moment was archived before the column
	// existed (migration 000007), and it produces no entry: there is nothing to
	// place on a timeline and inventing a position would be worse than the
	// absence. The status still says "archived" on the placed entry's Detail,
	// so the fact is not lost, only undated — which is exactly what it is.
	if order.ArchivedAt != nil {
		entries = append(entries, TimelineEntry{
			At: order.ArchivedAt, Kind: KindOrderArchived, RefID: order.ID,
			Clock: ClockDatabase,
		})
	}
	// The erasure is dated because what the order held before it is not what
	// it holds after: a reading of the order as it stood at an earlier moment
	// has to know the contact it shows was not the contact then.
	if order.PersonalDataErasedAt != nil {
		entries = append(entries, TimelineEntry{
			At: order.PersonalDataErasedAt, Kind: KindOrderDataErased, RefID: order.ID,
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

// orderGraph reads the order's payment collection and its shipments in ONE
// cross-module request; the timeline and the reading at a moment (ADR 0171)
// both start from it.
//
// Both hang off the order through links the far sides declared, so one Graph
// with two expansions answers both — and the read layer resolves each expansion
// in batch, which is the reason this is one round trip rather than one per
// shipment.
func (s *Service) orderGraph(
	ctx context.Context, orderID string,
) (collection query.Record, hasCollection bool, shipments any, err error) {
	if s.catalog == nil {
		return nil, false, nil, errors.Internal(CodeNotReady,
			"the query layer is not wired, so an order's history cannot be assembled")
	}

	records, err := s.catalog.Graph(ctx, query.GraphSpec{
		Entity:  EntityName,
		Fields:  []string{query.IDField},
		Filters: map[string]any{query.IDField: orderID},
		Limit:   1,
		Expand: []query.Expansion{
			{
				Link:   LinkOrderPayment,
				As:     EntityPaymentCollection,
				Fields: []string{query.IDField, fieldPaymentCurrency, fieldPaymentMovements},
			},
			{
				Link: LinkOrderFulfillment,
				As:   EntityFulfillment,
				Fields: []string{
					query.IDField, fieldShipmentStatus, fieldShipmentTracking,
					fieldShipmentCreated, fieldShipmentShipped,
					fieldShipmentDelivered, fieldShipmentCanceled,
					fieldShipmentReturned,
				},
			},
		},
	})
	if err != nil {
		return nil, false, nil, errors.Wrap(err, errors.KindOf(err), CodeCatalogReadFailed,
			"the history of order %s could not be read", orderID)
	}
	if len(records) == 0 {
		return nil, false, nil, errors.NotFound(CodeOrderNotFound, "order not found: %s", orderID)
	}

	collection, hasCollection = firstExpanded(records[0][EntityPaymentCollection])

	return collection, hasCollection, records[0][EntityFulfillment], nil
}

// timelineGraph turns the graph into the money and the shipment entries.
func (s *Service) timelineGraph(
	ctx context.Context, orderID string,
) (money, shipments []TimelineEntry, err error) {
	collection, hasCollection, parcels, err := s.orderGraph(ctx, orderID)
	if err != nil {
		return nil, nil, err
	}
	if hasCollection {
		if money, err = moneyEntries(collection); err != nil {
			return nil, nil, err
		}
	}

	return money, shipmentEntries(parcels), nil
}

// moneyEntries are every capture and every refund of the collection, each
// with what it moved (ADR 0170).
//
// They used to be two entries: the FIRST capture and the LAST refund, each
// carrying the collection's lifetime total. A refund of 1,000 on Monday and one
// of 500 on Friday read as a single refund of 1,500 on Friday, and Monday's did
// not exist. The movements are the payment module's own rows, so a history
// needs nothing this module does not already reach.
//
// A movement this module cannot read is an error, not a skipped entry: a
// timeline shorter than the truth is the failure this file exists to avoid.
func moneyEntries(collection query.Record) ([]TimelineEntry, error) {
	movements, err := readMovements(collection)
	if err != nil {
		return nil, err
	}
	currency := recordText(collection, fieldPaymentCurrency)

	entries := make([]TimelineEntry, 0, len(movements))
	for i := range movements {
		entry := TimelineEntry{
			At: &movements[i].At, RefID: movements[i].ID,
			Amount: movements[i].Amount, Currency: currency,
		}
		if movements[i].Kind == movementCapture {
			// A capture's moment is stamped by the process that captured, not
			// by the database — see the Clock constants.
			entry.Kind, entry.Clock = KindPaymentCaptured, ClockApplication
		} else {
			entry.Kind, entry.Clock = KindPaymentRefunded, ClockDatabase
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// paymentMovement is one capture or refund as this module reads it.
type paymentMovement struct {
	ID     string
	Kind   string
	Amount int64
	At     time.Time
}

// readMovements reads and checks the collection's movements. Every one it
// returns has an id, a moment, a positive amount and a known kind; anything
// else is an error rather than a movement quietly left out.
func readMovements(collection query.Record) ([]paymentMovement, error) {
	collectionID := recordText(collection, query.IDField)

	list, ok := movementList(collection[fieldPaymentMovements])
	if !ok {
		return nil, errors.Internal(CodeCatalogReadFailed,
			"payment collection %s answered its movements as %T", collectionID,
			collection[fieldPaymentMovements])
	}

	out := make([]paymentMovement, 0, len(list))
	for _, record := range list {
		movement := paymentMovement{
			ID: recordText(record, movementID), Kind: recordText(record, movementKind),
			Amount: recordInt(record, movementAmount),
		}
		at := recordTime(record, movementAt)
		if movement.ID == "" || at == nil || movement.Amount <= 0 {
			return nil, errors.Internal(CodeCatalogReadFailed,
				"payment collection %s answered a movement with no id, moment or amount", collectionID)
		}
		if movement.Kind != movementCapture && movement.Kind != movementRefund {
			return nil, errors.Internal(CodeCatalogReadFailed,
				"payment collection %s answered a movement of kind %q", collectionID, movement.Kind)
		}
		movement.At = *at
		out = append(out, movement)
	}

	return out, nil
}

// movementList reads the movements field: a list of records, which the
// provider hands over as either map type.
func movementList(raw any) ([]query.Record, bool) {
	switch value := raw.(type) {
	case []map[string]any:
		out := make([]query.Record, 0, len(value))
		for _, movement := range value {
			out = append(out, movement)
		}

		return out, true
	case []query.Record:
		return value, true
	default:
		return nil, false
	}
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
			{fieldShipmentReturned, KindShipmentReturned},
		} {
			at := recordTime(record, moment.field)
			if at == nil {
				continue
			}
			entries = append(entries, TimelineEntry{
				At: at, Kind: moment.kind, RefID: id,
				// created_at is the database's; the four transitions are
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
	return append(entries, exchangeEntries(exchanges)...), nil
}

// exchangeEntries are the moments an exchange record carries.
//
// It is split out of [Service.afterSalesEntries] for the reason [orderEntries]
// is: the mapping is the part that goes wrong and the part a store cannot be
// wired for. The composed [Service.Timeline] needs a query layer, so it can
// only run in the end-to-end lane, and the entry this function was missing
// would have gone unnoticed there for exactly as long as it went unnoticed
// here.
//
// The two endings both go through [appendMoment] rather than through a status
// test: the record's own column answers "when", so nothing here has to infer a
// moment from a status. The pair mirrors the claim's, which has carried both
// since it had both.
func exchangeEntries(exchanges []models.Exchange) []TimelineEntry {
	var entries []TimelineEntry

	for i := range exchanges {
		entries = append(entries, TimelineEntry{
			At: &exchanges[i].CreatedAt, Kind: KindExchangeOpened, RefID: exchanges[i].ID,
			Clock: ClockDatabase, Detail: exchanges[i].Status.String(),
		})
		// The funding is money: the difference the exchange collected, taken on
		// its own collection and not on the order's, so it is not also among
		// the order's captures.
		if exchanges[i].FundedAt != nil {
			entries = append(entries, TimelineEntry{
				At: exchanges[i].FundedAt, Kind: KindExchangeFunded, RefID: exchanges[i].ID,
				Clock: ClockDatabase, Detail: exchanges[i].PaymentCollectionID,
				Amount: exchanges[i].DifferenceDue,
			})
		}
		entries = appendMoment(entries, exchanges[i].CompletedAt,
			KindExchangeCompleted, exchanges[i].ID)
		entries = appendMoment(entries, exchanges[i].CanceledAt,
			KindExchangeCanceled, exchanges[i].ID)
	}

	return entries
}

// orderFactEntries are the order's own dated facts outside its row and its
// after-sales records: the lines canceled, the credits given and the goods a
// claim or an exchange promised to send (ADR 0170).
func (s *Service) orderFactEntries(ctx context.Context, order models.OrderDetail) ([]TimelineEntry, error) {
	cancellations, err := s.ListLineCancellations(ctx, order.ID)
	if err != nil {
		return nil, err
	}
	credits, err := s.ListCreditLines(ctx, order.ID)
	if err != nil {
		return nil, err
	}
	replacements, err := s.store.ListReplacementsByOrder(ctx, order.ID, timelinePageLimit)
	if err != nil {
		return nil, err
	}
	addresses, err := s.store.OrderAddressesByOrderIDs(ctx, []string{order.ID})
	if err != nil {
		return nil, err
	}

	entries := factEntries(order.CurrencyCode, cancellations, credits, replacements)
	entries = append(entries, correctionEntries(addresses[order.ID])...)

	return append(entries, deliveryChangeEntries(order.CurrencyCode, order.DeliveryChanges)...), nil
}

// deliveryChangeEntries dates each delivery change (ADR 0199).
func deliveryChangeEntries(currency string, changes []models.DeliveryChange) []TimelineEntry {
	entries := make([]TimelineEntry, 0, len(changes))
	for i := range changes {
		entries = append(entries, TimelineEntry{
			At: &changes[i].CreatedAt, Kind: KindDeliveryChanged, RefID: changes[i].ID,
			Clock: ClockDatabase, Detail: changes[i].Name,
			Amount: changes[i].Amount, Currency: currency,
		})
	}

	return entries
}

// correctionEntries dates each shipping address a correction closed (ADR 0195).
//
// The entry names the closed row and says nothing of either address: the
// timeline reaches the storefront, whose read is open to anyone holding the
// order's id, and where the parcel goes is the admin record's to say.
func correctionEntries(addresses []models.OrderAddress) []TimelineEntry {
	var entries []TimelineEntry
	for i := range addresses {
		if addresses[i].Type != models.AddressShipping || addresses[i].SupersededAt == nil {
			continue
		}
		entries = append(entries, TimelineEntry{
			At: addresses[i].SupersededAt, Kind: KindShippingAddressCorrected,
			RefID: addresses[i].ID, Clock: ClockDatabase,
		})
	}

	return entries
}

// factEntries maps the facts, split out of [Service.orderFactEntries] for the
// reason [exchangeEntries] is.
func factEntries(
	currency string,
	cancellations []models.OrderLineCancellation,
	credits []models.OrderCreditLine,
	replacements []models.Replacement,
) []TimelineEntry {
	entries := make([]TimelineEntry, 0, len(cancellations)+len(credits)+len(replacements))

	for i := range cancellations {
		entries = append(entries, TimelineEntry{
			At: &cancellations[i].CreatedAt, Kind: KindOrderLineCanceled,
			RefID: cancellations[i].ID, Clock: ClockDatabase,
			Detail: cancellations[i].OrderLineItemID, Quantity: cancellations[i].Quantity,
		})
	}
	for i := range credits {
		entries = append(entries, TimelineEntry{
			At: &credits[i].CreatedAt, Kind: KindOrderCredited, RefID: credits[i].ID,
			Clock: ClockDatabase, Amount: credits[i].Amount, Currency: currency,
		})
	}
	for i := range replacements {
		entries = append(entries, TimelineEntry{
			At: &replacements[i].CreatedAt, Kind: KindReplacementOpened,
			RefID: replacements[i].ID, Clock: ClockDatabase,
		})
		entries = appendMoment(entries, replacements[i].DispatchedAt,
			KindReplacementDispatched, replacements[i].ID)
		entries = appendMoment(entries, replacements[i].CanceledAt,
			KindReplacementCanceled, replacements[i].ID)
	}

	return entries
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

// customerVisibleKinds are the moments a SHOPPER may see on their own order.
//
// # Why the set is smaller than the support desk's
//
// Two kinds are left out and each for its own reason.
//
// The MONEY moments (payment.captured, payment.refunded) are the merchant's
// ledger view of a payment, not the customer's. A partial capture is an
// internal fact about a hold; a refund's recorded amount is what the shop moved
// on its side, and the number the customer will reconcile against is the one
// their bank shows on the day it lands. Publishing a figure that is true here
// and different there invites a dispute about the wrong number.
//
// order.archived is the merchant FILING the order away. Nothing happened to the
// goods or the money, and a customer told their order was "archived" would
// reasonably read it as something being done to them.
//
// Everything else is about the order's own lifecycle or about the GOODS — where
// they are, that they came back, that a claim was opened — which is exactly
// what the person waiting for a parcel is asking.
var customerVisibleKinds = map[string]bool{
	KindOrderPlaced:       true,
	KindOrderCompleted:    true,
	KindOrderCanceled:     true,
	KindShipmentOpened:    true,
	KindShipmentShipped:   true,
	KindShipmentDelivered: true,
	KindShipmentCanceled:  true,
	KindShipmentReturned:  true,
	KindReturnOpened:      true,
	KindReturnReceived:    true,
	KindReturnCanceled:    true,
	KindClaimOpened:       true,
	KindClaimCompleted:    true,
	KindClaimCanceled:     true,
	KindExchangeOpened:    true,
	KindExchangeCompleted: true,
	KindExchangeCanceled:  true,
	// ADR 0170: a canceled line and a replacement are about the goods. The
	// credit and the exchange's funding are money, and the erasure is the
	// shop's act on its records; those three stay on the support desk's side.
	KindOrderLineCanceled:     true,
	KindReplacementOpened:     true,
	KindReplacementDispatched: true,
	KindReplacementCanceled:   true,
	// ADR 0195: where the goods go is about the goods, and the customer who
	// rang to correct it sees that it was done. The entry carries no address.
	KindShippingAddressCorrected: true,
	// ADR 0199: which service carries the goods is about the goods. The
	// storefront's shape carries no amount, and the credit stays the desk's.
	KindDeliveryChanged: true,
}

// StorefrontTimeline is the timeline a customer may see on their own order.
//
// It is the SAME composition [Service.Timeline] performs, filtered by
// [customerVisibleKinds]. Composing it a second time would be a second place
// for a moment to be forgotten; filtering the one answer means a kind added
// tomorrow is INVISIBLE to the storefront until somebody decides it belongs
// there, which is the safer direction for a default.
//
// The amounts are not stripped here. They are absent from the surface's own
// shape instead — a type that cannot carry a figure cannot leak one — and this
// function stays the single decision about WHICH moments cross.
func (s *Service) StorefrontTimeline(
	ctx context.Context, orderID string,
) ([]TimelineEntry, error) {
	entries, err := s.Timeline(ctx, orderID)
	if err != nil {
		return nil, err
	}

	return customerVisible(entries), nil
}

// customerVisible keeps the entries a shopper may see.
//
// It is separated from [Service.StorefrontTimeline] so the RULE can be tried
// without a wired query catalog: the composition needs one and the filter needs
// nothing, and the filter is the half that decides what crosses.
func customerVisible(entries []TimelineEntry) []TimelineEntry {
	out := make([]TimelineEntry, 0, len(entries))
	for i := range entries {
		if customerVisibleKinds[entries[i].Kind] {
			out = append(out, entries[i])
		}
	}

	return out
}
