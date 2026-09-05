package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// Cross-module read constants.
//
// The values are declared in the payment module and REPEATED here: this module
// cannot import that one (Principle 2.1/2.4), and the repetition is the
// accepted price of isolation (ADR 0001). A typo does not stay silent — the
// link service returns NotFound for an undeclared name, and the Query layer
// does the same for an unknown entity.
const (
	// LinkOrderPayment binds an order to the payment collection opened for it;
	// the payment module declares it.
	LinkOrderPayment = "order_payment"
	// EntityPaymentCollection is the payment module's entity in the Query layer.
	EntityPaymentCollection = "payment_collection"
)

// Payment collection fields read through the Query layer.
const (
	fieldPaymentStatus     = "status"
	fieldPaymentAmount     = "amount"
	fieldPaymentAuthorized = "authorized_amount"
	fieldPaymentCaptured   = "captured_amount"
	fieldPaymentRefunded   = "refunded_amount"
	fieldPaymentCurrency   = "currency_code"
)

// OrderPayment is the payment module's LIVE view of an order's collection.
//
// # Why this exists next to the order's own summary
//
// [models.OrderSummary] carries what the order RECORDED was paid on it, written
// by the checkout saga (ADR 0022). This is what the payment module says right
// now. They are meant to agree, and the reason to have both is that a
// difference between them is then VISIBLE rather than merely possible — the
// same argument ADR 0020 makes about a session and its provider.
//
// An operator reading an order with both in front of them can tell a recorded
// payment from a real one; before this read existed there was no way from an
// order to its money at all, because the collection's Reference carries the
// CART id.
type OrderPayment struct {
	// CollectionID is the payment collection bound to the order.
	CollectionID string
	// Status is the collection's derived status.
	Status string
	// Amount is what has to be collected (minor unit).
	Amount int64
	// AuthorizedAmount, CapturedAmount and RefundedAmount are the collection's
	// own figures.
	AuthorizedAmount int64
	CapturedAmount   int64
	RefundedAmount   int64
	// CurrencyCode is the collection's currency.
	CurrencyCode string
}

// Catalog is the Query-layer surface this module reads through (ADR 0004).
//
// It is the ONLY way this module can see another module's data: the payment
// service speaks in payment's own types and is closed to cross-module calls,
// while Query exists for exactly that gap.
type Catalog interface {
	// Graph fetches the root records according to the spec and applies the
	// expansions.
	Graph(ctx context.Context, spec query.GraphSpec) ([]query.Record, error)
}

// PaymentOf returns the live payment collection bound to the order.
//
// # A missing collection is NOT an error
//
// The second value reports whether one was found. An order can genuinely have
// none: the saga binds the collection AFTER the order is written, so an order
// whose checkout died in between has one moment where it exists without a
// payment — and that is a fact worth showing an operator, not a failure worth
// hiding the whole order behind.
//
// # A missing Query layer IS an error
//
// If the catalog was not wired this call fails rather than answering "no
// payment". The two must not look the same: "this order was never paid" and
// "nobody could ask" are the distinction ADR 0020 was built around, one level
// down.
func (s *Service) PaymentOf(ctx context.Context, orderID string) (OrderPayment, bool, error) {
	if err := requireID("order_id", orderID); err != nil {
		return OrderPayment{}, false, err
	}
	if s.catalog == nil {
		return OrderPayment{}, false, errors.Internal(CodeNotReady,
			"the query layer is not wired, so an order's payment cannot be read")
	}

	records, err := s.catalog.Graph(ctx, query.GraphSpec{
		Entity:  EntityName,
		Fields:  []string{query.IDField},
		Filters: map[string]any{query.IDField: orderID},
		Limit:   1,
		Expand: []query.Expansion{{
			Link: LinkOrderPayment,
			As:   EntityPaymentCollection,
			Fields: []string{
				query.IDField,
				fieldPaymentStatus, fieldPaymentAmount, fieldPaymentCurrency,
				fieldPaymentAuthorized, fieldPaymentCaptured, fieldPaymentRefunded,
			},
		}},
	})
	if err != nil {
		return OrderPayment{}, false, errors.Wrap(err, errors.KindOf(err), CodeCatalogReadFailed,
			"the payment of order %s could not be read", orderID)
	}
	if len(records) == 0 {
		return OrderPayment{}, false, errors.NotFound(CodeOrderNotFound,
			"order not found: %s", orderID)
	}

	collection, ok := firstExpanded(records[0][EntityPaymentCollection])
	if !ok {
		return OrderPayment{}, false, nil
	}

	return OrderPayment{
		CollectionID:     recordText(collection, query.IDField),
		Status:           recordText(collection, fieldPaymentStatus),
		Amount:           recordInt(collection, fieldPaymentAmount),
		AuthorizedAmount: recordInt(collection, fieldPaymentAuthorized),
		CapturedAmount:   recordInt(collection, fieldPaymentCaptured),
		RefundedAmount:   recordInt(collection, fieldPaymentRefunded),
		CurrencyCode:     recordText(collection, fieldPaymentCurrency),
	}, true, nil
}

// firstExpanded takes the first record out of an expansion result.
//
// The expansion is declared OneToOne on the payment side, so at most one record
// comes back; taking the first rather than asserting the length keeps a
// cardinality change from turning into a panic here.
func firstExpanded(raw any) (query.Record, bool) {
	switch value := raw.(type) {
	case query.Record:
		return value, true
	case []query.Record:
		if len(value) == 0 {
			return nil, false
		}

		return value[0], true
	default:
		return nil, false
	}
}

// recordText reads a string field; a missing or differently typed field is the
// empty string.
func recordText(rec query.Record, field string) string {
	text, _ := rec[field].(string)

	return text
}

// recordInt reads an integer field.
//
// Both int64 and int are accepted because a provider may produce either, and a
// money value silently becoming zero because of a type assertion is the class
// of fault this repository spends most of its comments on.
func recordInt(rec query.Record, field string) int64 {
	switch value := rec[field].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case int32:
		return int64(value)
	default:
		return 0
	}
}
