package service

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// CodeAsOfInvalid is returned for a moment an order cannot be read at: one
// that has not happened, or one before the order existed.
const CodeAsOfInvalid = "order_as_of_invalid"

// The statuses a parcel can be in, as the fulfillment module names them.
const (
	shipmentPending   = "pending"
	shipmentShipped   = "shipped"
	shipmentDelivered = "delivered"
	shipmentCanceled  = "canceled"
	shipmentReturned  = "returned"
)

// OrderAsOf reads the order as it stood at a past moment (ADR 0171).
//
// # Derived, not stored
//
// Every answer comes from a row that carries its own moment, which is what
// ADR 0170 put on the timeline: the order's stamps, its line cancellations and
// credits, its after-sales stamps, the payment collection's movements and the
// parcels' transitions. A status at the moment is the latest stamp at or before
// it; money at the moment is the movements up to it. The order's recorded
// summary is NOT read: it keeps only its latest value.
//
// # The clocks
//
// A capture and a parcel's transitions are stamped by the process that wrote
// them, everything else by the database (ADR 0053). A moment between two events
// stamped by different clocks is answered as exactly as those clocks agree.
//
// # What it refuses
//
// A moment that has not happened: the answer would present today's state as a
// fact about a time nothing has been recorded for. And a moment before the
// order was placed: there was no order to read.
func (s *Service) OrderAsOf(ctx context.Context, orderID string, at time.Time) (models.OrderAsOf, error) {
	if at.IsZero() {
		return models.OrderAsOf{}, errors.Invalid(CodeAsOfInvalid, "a moment to read the order at is required")
	}
	if now := s.now(); at.After(now) {
		return models.OrderAsOf{}, errors.Invalid(CodeAsOfInvalid,
			"%s has not happened yet; an order can be read as it stood, not as it will stand",
			at.UTC().Format(time.RFC3339))
	}

	order, err := s.GetOrder(ctx, orderID)
	if err != nil {
		return models.OrderAsOf{}, err
	}
	if at.Before(order.PlacedAt) {
		return models.OrderAsOf{}, errors.Invalid(CodeAsOfInvalid,
			"order %s was placed at %s and did not exist at %s", orderID,
			order.PlacedAt.UTC().Format(time.RFC3339), at.UTC().Format(time.RFC3339))
	}

	in := asOfInputs{order: order}
	collection, hasCollection, parcels, err := s.orderGraph(ctx, orderID)
	if err != nil {
		return models.OrderAsOf{}, err
	}
	if hasCollection {
		if in.movements, err = readMovements(collection); err != nil {
			return models.OrderAsOf{}, err
		}
	}
	in.shipments, _ = parcels.([]query.Record)

	if in.cancellations, err = s.ListLineCancellations(ctx, orderID); err != nil {
		return models.OrderAsOf{}, err
	}
	if in.credits, err = s.ListCreditLines(ctx, orderID); err != nil {
		return models.OrderAsOf{}, err
	}
	page := Page{Limit: timelinePageLimit}
	if in.returns, _, err = s.ListReturns(ctx, orderID, page); err != nil {
		return models.OrderAsOf{}, err
	}
	if in.claims, _, err = s.ListClaims(ctx, orderID, page); err != nil {
		return models.OrderAsOf{}, err
	}
	if in.exchanges, _, err = s.ListExchanges(ctx, orderID, page); err != nil {
		return models.OrderAsOf{}, err
	}
	if in.replacements, err = s.store.ListReplacementsByOrder(ctx, orderID, timelinePageLimit); err != nil {
		return models.OrderAsOf{}, err
	}

	return orderAsOf(at, &in), nil
}

// asOfInputs are the rows a reading at a moment is derived from.
type asOfInputs struct {
	order         models.OrderDetail
	movements     []paymentMovement
	shipments     []query.Record
	cancellations []models.OrderLineCancellation
	credits       []models.OrderCreditLine
	returns       []models.Return
	claims        []models.Claim
	exchanges     []models.Exchange
	replacements  []models.Replacement
}

// orderAsOf derives the reading. It reads nothing, so every rule it applies can
// be tried without a database.
func orderAsOf(at time.Time, in *asOfInputs) models.OrderAsOf {
	out := models.OrderAsOf{OrderID: in.order.ID, At: at}
	out.Status = orderStatusAt(&in.order.Order, at)
	out.Money = moneyAt(at, in)
	out.Lines = linesAt(at, in.order.Items, in.cancellations)
	out.Contact = contactAt(&in.order.Order, at)

	for i := range in.returns {
		out.Returns = appendRecord(out.Returns, at, in.returns[i].ID, in.returns[i].CreatedAt,
			stamp{string(models.ReturnReceived), in.returns[i].ReceivedAt},
			stamp{string(models.ReturnCanceled), in.returns[i].CanceledAt})
	}
	for i := range in.claims {
		out.Claims = appendRecord(out.Claims, at, in.claims[i].ID, in.claims[i].CreatedAt,
			stamp{string(models.ClaimCompleted), in.claims[i].CompletedAt},
			stamp{string(models.ClaimCanceled), in.claims[i].CanceledAt})
	}
	for i := range in.exchanges {
		out.Exchanges = appendRecord(out.Exchanges, at, in.exchanges[i].ID, in.exchanges[i].CreatedAt,
			stamp{string(models.ExchangeFunded), in.exchanges[i].FundedAt},
			stamp{string(models.ExchangeCompleted), in.exchanges[i].CompletedAt},
			stamp{string(models.ExchangeCanceled), in.exchanges[i].CanceledAt})
	}
	for i := range in.replacements {
		out.Replacements = appendRecord(out.Replacements, at, in.replacements[i].ID,
			in.replacements[i].CreatedAt,
			stamp{string(models.ReplacementDispatched), in.replacements[i].DispatchedAt},
			stamp{string(models.ReplacementCanceled), in.replacements[i].CanceledAt})
	}
	for _, parcel := range in.shipments {
		created := recordTime(parcel, fieldShipmentCreated)
		if created == nil {
			continue
		}
		out.Shipments = appendRecordAs(out.Shipments, at, recordText(parcel, query.IDField),
			*created, shipmentPending,
			stamp{shipmentShipped, recordTime(parcel, fieldShipmentShipped)},
			stamp{shipmentDelivered, recordTime(parcel, fieldShipmentDelivered)},
			stamp{shipmentCanceled, recordTime(parcel, fieldShipmentCanceled)},
			stamp{shipmentReturned, recordTime(parcel, fieldShipmentReturned)})
	}

	return out
}

// orderStatusAt is the order's status at a moment.
//
// The transitions are one-way (pending -> completed -> archived, pending ->
// canceled) and each is stamped by a column nothing clears. One case has no
// answer: an order archived before migration 000007 dated the archiving, read
// after its completion, was completed or archived and the records do not say
// which.
func orderStatusAt(order *models.Order, at time.Time) *models.OrderStatus {
	status := models.OrderPending
	switch {
	case reached(order.CanceledAt, at):
		status = models.OrderCanceled
	case reached(order.ArchivedAt, at):
		status = models.OrderArchived
	case reached(order.CompletedAt, at):
		if order.Status == models.OrderArchived && order.ArchivedAt == nil {
			return nil
		}
		status = models.OrderCompleted
	}

	return &status
}

// moneyAt sums what had moved by the moment.
func moneyAt(at time.Time, in *asOfInputs) models.MoneyAsOf {
	money := models.MoneyAsOf{Currency: in.order.CurrencyCode, Total: in.order.Total}
	for i := range in.credits {
		if !in.credits[i].CreatedAt.After(at) {
			money.Credited += in.credits[i].Amount
		}
	}
	for i := range in.movements {
		if in.movements[i].At.After(at) {
			continue
		}
		if in.movements[i].Kind == movementCapture {
			money.Captured += in.movements[i].Amount
		} else {
			money.Refunded += in.movements[i].Amount
		}
	}
	// The live order's formula, over the sums at the moment: two formulas
	// would be two answers to "what was owed".
	money.Outstanding = models.OrderSummary{
		PaidTotal: money.Captured, RefundedTotal: money.Refunded,
	}.Outstanding(money.Total, money.Credited)

	return money
}

// linesAt are the lines with what had been canceled of each by the moment.
func linesAt(
	at time.Time, items []models.OrderLineItem, cancellations []models.OrderLineCancellation,
) []models.LineAsOf {
	canceled := map[string]int64{}
	for i := range cancellations {
		if !cancellations[i].CreatedAt.After(at) {
			canceled[cancellations[i].OrderLineItemID] += cancellations[i].Quantity
		}
	}

	out := make([]models.LineAsOf, 0, len(items))
	for i := range items {
		out = append(out, models.LineAsOf{
			LineItemID: items[i].ID, VariantID: items[i].VariantID, Title: items[i].Title,
			Quantity: items[i].Quantity, UnitPrice: items[i].UnitPrice,
			Canceled: canceled[items[i].ID],
		})
	}

	return out
}

// contactAt says whether the contact held now is the one held at the moment.
func contactAt(order *models.Order, at time.Time) models.ContactAsOf {
	switch {
	case order.PersonalDataErasedAt == nil:
		return models.ContactHeld
	case order.PersonalDataErasedAt.After(at):
		return models.ContactErasedSince
	default:
		return models.ContactErased
	}
}

// stamp is a status a record enters and the moment it did; the moment is nil
// while it has not.
type stamp struct {
	status string
	at     *time.Time
}

// appendRecord adds an after-sales record that existed at the moment, with its
// status then; every one starts as "requested".
func appendRecord(
	out []models.RecordAsOf, at time.Time, id string, created time.Time, stamps ...stamp,
) []models.RecordAsOf {
	return appendRecordAs(out, at, id, created, "requested", stamps...)
}

// appendRecordAs adds a record that existed at the moment, in the status of
// the latest stamp at or before it; a record created after the moment is left
// out, because it did not exist then.
func appendRecordAs(
	out []models.RecordAsOf, at time.Time, id string, created time.Time,
	initial string, stamps ...stamp,
) []models.RecordAsOf {
	if created.After(at) {
		return out
	}

	// The latest reached stamp decides, compared among the stamps and not
	// against the creation: a parcel is created on the database's clock and
	// shipped on the application's, and a skew between them must not hide a
	// transition behind the creation it followed.
	current := models.RecordAsOf{ID: id, Status: initial, Since: created}
	var latest *stamp
	for i := range stamps {
		if reached(stamps[i].at, at) && (latest == nil || !stamps[i].at.Before(*latest.at)) {
			latest = &stamps[i]
		}
	}
	if latest != nil {
		current.Status, current.Since = latest.status, *latest.at
	}

	return append(out, current)
}

// reached reports whether a stamp exists and is at or before the moment.
func reached(stamp *time.Time, at time.Time) bool {
	return stamp != nil && !stamp.After(at)
}
