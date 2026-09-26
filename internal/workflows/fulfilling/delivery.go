package fulfilling

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
)

// The refusals of a delivery change that are this flow's (ADR 0199).
const (
	// CodeQuoteFailed reports that the fulfillment module could not price the
	// order's options.
	CodeQuoteFailed = "fulfilling_quote_failed"
	// CodeOptionUnavailable refuses an option the fulfillment module did not
	// quote for the order.
	CodeOptionUnavailable = "fulfilling_option_unavailable"
	// CodeCollectionNotTheOrders refuses a payment collection that was not
	// opened for the order, or is in another currency (ADR 0200).
	CodeCollectionNotTheOrders = "fulfilling_collection_not_the_orders"
	// CodeCollectionNotSettled refuses a payment collection that does not
	// hold exactly what it was opened for.
	CodeCollectionNotSettled = "fulfilling_collection_not_settled"
)

// deliveryFacts is the order module's DeliveryFactsJSON answer. The producing
// side documents it; it is repeated here because this package cannot import
// that module (ADR 0006).
type deliveryFacts struct {
	RegionID     string `json:"region_id"`
	CurrencyCode string `json:"currency_code"`
	CountryCode  string `json:"country_code"`
	Subtotal     int64  `json:"subtotal"`
	ItemCount    int64  `json:"item_count"`
}

// optionsRequest is the fulfillment module's option listing request, the
// fields this flow fills.
type optionsRequest struct {
	RegionID         string `json:"region_id"`
	CurrencyCode     string `json:"currency_code"`
	CountryCode      string `json:"country_code"`
	Subtotal         int64  `json:"subtotal"`
	ItemCount        int64  `json:"item_count"`
	TotalWeight      int64  `json:"total_weight"`
	IncludeAdminOnly bool   `json:"include_admin_only"`
}

// quotedOption is one option as the fulfillment module priced it.
type quotedOption struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Amount       int64  `json:"amount"`
	CurrencyCode string `json:"currency_code"`
	IsReturn     bool   `json:"is_return"`
}

// optionsResponse is the option listing's answer.
type optionsResponse struct {
	Options []quotedOption `json:"options"`
}

// deliveryChangeRequest is the order module's ChangeDeliveryJSON request.
type deliveryChangeRequest struct {
	ShippingMethodID    string `json:"shipping_method_id"`
	ShippingOptionID    string `json:"shipping_option_id"`
	Name                string `json:"name"`
	Amount              int64  `json:"amount"`
	PaymentCollectionID string `json:"payment_collection_id"`
	Paid                int64  `json:"paid"`
}

// ChangeDelivery puts one of the order's deliveries on another shipping option,
// at the price the fulfillment module quotes for the order, and returns the
// order module's answer: the change, or JSON null when it was already on that
// option (ADR 0199).
//
// A parcel already opened was handed to its carrier on the old service, so the
// change is refused while any parcel of the order is pending, shipped or
// delivered, as an address correction is (ADR 0195).
//
// The option is priced on the order's own facts and has to be among the
// options the fulfillment module lists for them; an option it does not list is
// not one this order can go on. Admin-only options are listed, because the
// operator is the one changing it, and return options are not.
//
// The quote and the write are not one transaction. A price changed between
// them is the price the operator was quoted, which is the one they asked for.
//
// # A dearer delivery is paid first (ADR 0200)
//
// A change that costs more is refused until it names a payment collection:
// one opened for this order, in its currency, and holding exactly what it was
// opened for — captured in full and nothing given back. This flow reads the
// collection and hands the order module what it holds; the order module holds
// that to the difference under its lock, and refuses a collection that paid
// for anything else. The money is taken through the payment module's own
// endpoints, as an exchange's is (ADR 0120).
func (w *Workflows) ChangeDelivery(
	ctx context.Context, orderID, shippingMethodID, shippingOptionID, collectionID string,
) (json.RawMessage, error) {
	shippingOptionID = strings.TrimSpace(shippingOptionID)
	switch {
	case strings.TrimSpace(orderID) == "":
		return nil, errors.Invalid(CodeInvalidInput, "the order id is required")
	case shippingOptionID == "":
		return nil, errors.Invalid(CodeInvalidInput, "the shipping option id is required")
	}

	if err := w.refuseWhileUnderway(ctx, orderID, "the service it was opened on", "changing the delivery"); err != nil {
		return nil, err
	}

	option, currency, err := w.quoteForOrder(ctx, orderID, shippingOptionID)
	if err != nil {
		return nil, err
	}

	var paid int64
	if collectionID = strings.TrimSpace(collectionID); collectionID != "" {
		if paid, err = w.heldForOrder(ctx, orderID, currency, collectionID); err != nil {
			return nil, err
		}
	}

	request, err := json.Marshal(deliveryChangeRequest{
		ShippingMethodID:    shippingMethodID,
		ShippingOptionID:    option.ID,
		Name:                option.Name,
		Amount:              option.Amount,
		PaymentCollectionID: collectionID,
		Paid:                paid,
	})
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeInvalidInput,
			"the delivery change could not be built")
	}

	return w.orders.ChangeDeliveryJSON(ctx, orderID, request)
}

// heldForOrder reads the collection a dearer change names and returns what it
// holds, when it was opened for the order, in its currency, and holds exactly
// what it was opened for.
//
// The amount has to be what it holds for the reason ADR 0120 gives an
// exchange: a collection opened wider could take more later, past anything the
// order agreed to.
func (w *Workflows) heldForOrder(ctx context.Context, orderID, currency, collectionID string) (int64, error) {
	reference, err := w.payments.CollectionReference(ctx, collectionID)
	if err != nil {
		return 0, errors.Wrap(err, errors.KindOf(err), CodeCollectionNotTheOrders,
			"collection %s could not be read", collectionID)
	}
	if reference != orderID {
		return 0, errors.Conflict(CodeCollectionNotTheOrders,
			"collection %s was opened for %q, not for order %s", collectionID, reference, orderID)
	}
	collectionCurrency, err := w.payments.CollectionCurrency(ctx, collectionID)
	if err != nil {
		return 0, errors.Wrap(err, errors.KindOf(err), CodeCollectionNotTheOrders,
			"the currency of collection %s could not be read", collectionID)
	}
	if collectionCurrency != currency {
		return 0, errors.Conflict(CodeCollectionNotTheOrders,
			"collection %s is in %s and order %s is in %s", collectionID, collectionCurrency, orderID, currency)
	}
	_, amount, _, captured, refunded, err := w.payments.Collection(ctx, collectionID)
	if err != nil {
		return 0, errors.Wrap(err, errors.KindOf(err), CodeCollectionNotTheOrders,
			"collection %s could not be read", collectionID)
	}
	if held := captured - refunded; held != amount {
		return 0, errors.Conflict(CodeCollectionNotSettled,
			"collection %s was opened for %d and holds %d (captured %d, refunded %d); it pays "+
				"for a change when it holds all it was opened for",
			collectionID, amount, held, captured, refunded)
	}

	return amount, nil
}

// quoteForOrder prices the order's options and returns the one asked for, and
// the order's currency.
func (w *Workflows) quoteForOrder(
	ctx context.Context, orderID, shippingOptionID string,
) (option quotedOption, currency string, err error) {
	raw, err := w.orders.DeliveryFactsJSON(ctx, orderID)
	if err != nil {
		return quotedOption{}, "", errors.Wrap(err, errors.KindOf(err), CodeOrderUnreadable,
			"order %s could not be read, so its delivery was not changed", orderID)
	}
	var facts deliveryFacts
	if err := json.Unmarshal(raw, &facts); err != nil {
		return quotedOption{}, "", errors.Wrap(err, errors.KindInternal, CodeOrderUnreadable,
			"order %s's delivery facts could not be parsed", orderID)
	}

	request, err := json.Marshal(optionsRequest{
		RegionID:     facts.RegionID,
		CurrencyCode: facts.CurrencyCode,
		CountryCode:  facts.CountryCode,
		Subtotal:     facts.Subtotal,
		ItemCount:    facts.ItemCount,
		// An order carries no weight, as the cart it came from did not; a
		// weight-banded option prices at its lowest band, as it did at checkout.
		TotalWeight:      0,
		IncludeAdminOnly: true,
	})
	if err != nil {
		return quotedOption{}, "", errors.Wrap(err, errors.KindInternal, CodeQuoteFailed,
			"the quote request for order %s could not be built", orderID)
	}

	answer, err := w.fulfillments.ListOptionsJSON(ctx, request)
	if err != nil {
		return quotedOption{}, "", errors.Wrap(err, errors.KindOf(err), CodeQuoteFailed,
			"the shipping options could not be quoted for order %s", orderID)
	}
	var quoted optionsResponse
	if err := json.Unmarshal(answer, &quoted); err != nil {
		return quotedOption{}, "", errors.Wrap(err, errors.KindInternal, CodeQuoteFailed,
			"the shipping quote for order %s could not be parsed", orderID)
	}

	for i := range quoted.Options {
		candidate := quoted.Options[i]
		if candidate.ID != shippingOptionID || candidate.IsReturn {
			continue
		}
		// A quote in another currency would be subtracted from the order's
		// amount as a bare integer.
		if candidate.CurrencyCode != facts.CurrencyCode {
			break
		}

		return candidate, facts.CurrencyCode, nil
	}

	return quotedOption{}, "", errors.Conflict(CodeOptionUnavailable,
		"the shipping option %q is not available for order %s", shippingOptionID, orderID)
}
