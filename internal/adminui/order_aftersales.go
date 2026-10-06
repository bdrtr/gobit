package adminui

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/query"
)

// The after-sales entities the order page reads, and the fields it reads from
// them beyond the ones named elsewhere (ADR 0270). They are the order module's
// names, repeated for the reason [EntityOrder] is.
const (
	EntityOrderReturn      = "order_return"
	EntityOrderClaim       = "order_claim"
	EntityOrderExchange    = "order_exchange"
	EntityOrderReplacement = "order_replacement"

	fieldReason              = "reason"
	fieldRefundAmount        = "refund_amount"
	fieldReceivedAt          = "received_at"
	fieldReceivedLocationID  = "received_location_id"
	fieldClaimType           = "type"
	fieldCompletedAt         = "completed_at"
	fieldDifferenceDue       = "difference_due"
	fieldPaymentCollectionID = "payment_collection_id"
	fieldClaimID             = "claim_id"
	fieldExchangeID          = "exchange_id"
	fieldLocationID          = "location_id"
	fieldDispatchedAt        = "dispatched_at"
	fieldFulfillmentID       = "fulfillment_id"
)

// afterSalesPerKind is how many records of each kind the order page reads,
// newest first. An order with more has been through more than a screen should
// have to show, and the page says so.
const afterSalesPerKind = 25

// momentLayout is how the page prints a moment.
const momentLayout = "2006-01-02 15:04"

// orderAfterSale is one of the order's returns, claims, exchanges or
// replacements as the page prints it.
type orderAfterSale struct {
	// Kind is "return", "claim", "exchange" or "replacement".
	Kind   string
	ID     string
	Status string
	// Detail is what the kind adds: a return's or a claim's reason, how a
	// claim is settled, what a replacement settles and where it is sent from,
	// the collection an exchange's difference is paid through.
	Detail []string
	// Goods are the record's lines as "title × quantity".
	Goods []string
	// Money is the refund or the difference as printed, with its currency and
	// who pays; empty when the record moves none.
	Money    string
	OpenedAt time.Time
	// Since are what happened to it after it was opened, each printed with its
	// moment.
	Since []string
	// Forms are the acts it offers in its status (ADR 0271).
	Forms []afterSaleForm
	// ClaimType is how a claim is settled; empty for the other kinds.
	ClaimType string
	// Evidence are a claim's files, oldest first (ADR 0325);
	// EvidenceUnread says they could not be read.
	Evidence       []evidenceRow
	EvidenceUnread bool
	// Parcels are a return's parcels, the ones bringing it back, oldest first
	// (ADR 0413); ParcelsUnread says they could not be read. ReturnParcel is
	// the form that opens one, nil when none is offered.
	Parcels       []orderParcel
	ParcelsUnread bool
	ReturnParcel  *returnParcelForm
	// units are the order lines a return names and how many of each.
	units []saleUnit
}

// saleUnit is one line of a return: the order line and its units.
type saleUnit struct {
	lineID   string
	quantity int64
}

// saleUnits reads a record's lines as the order lines and units they name, in
// the order the record lists them.
func saleUnits(value any) []saleUnit {
	items, _ := value.([]map[string]any)
	out := make([]saleUnit, 0, len(items))
	for _, item := range items {
		quantity, _ := intValue(item[itemQuantity])
		out = append(out, saleUnit{lineID: stringValue(item[itemLineItemID]), quantity: int64(quantity)})
	}

	return out
}

// afterSaleSource is a record a replacement can be opened for (ADR 0272).
type afterSaleSource struct {
	// Value is "claim:<id>" or "exchange:<id>", what the form posts.
	Value string
	Label string
}

// replacementSources are the records a replacement can be opened for: a
// requested claim settled by goods, and an exchange that is requested or
// funded. The module decides; this keeps the page from offering the others.
func replacementSources(sales []orderAfterSale) []afterSaleSource {
	var out []afterSaleSource
	for i := range sales {
		sale := sales[i]
		switch {
		case sale.Kind == kindClaim && sale.Status == recordRequested && sale.ClaimType == "replace",
			sale.Kind == kindExchange && (sale.Status == recordRequested || sale.Status == "funded"):
			out = append(out, afterSaleSource{Value: sale.Kind + ":" + sale.ID, Label: sale.Kind + " " + sale.ID})
		}
	}

	return out
}

// afterSalesOf reads the order's returns, claims, exchanges and replacements,
// newest first; the second value reports that more records exist than were
// read and the third a read that failed.
//
// One read per kind: each entity is read per order (ADR 0270). One failing
// costs the section and not the page, as the parcels' read does.
func (u *UI) afterSalesOf(
	r *http.Request, orderID, currency string, scales map[string]int, lines []orderLine,
) ([]orderAfterSale, bool, bool) {
	titles := make(map[string]string, len(lines))
	for i := range lines {
		titles[lines[i].ID] = lines[i].Title
	}

	var out []orderAfterSale
	more := false
	for _, kind := range []struct {
		entity string
		fields []string
		view   func(query.Record) orderAfterSale
	}{
		{
			entity: EntityOrderReturn,
			fields: []string{
				fieldID, fieldStatus, fieldCreatedAt, fieldCanceledAt, fieldReason,
				fieldRefundAmount, fieldReceivedAt, fieldReceivedLocationID, fieldItems,
			},
			view: func(rec query.Record) orderAfterSale {
				sale := afterSaleOf(kindReturn, rec)
				sale.Detail = nonEmpty(recordString(rec, fieldReason))
				sale.Goods = afterSaleGoods(rec[fieldItems], titles)
				sale.units = saleUnits(rec[fieldItems])
				sale.Money = refundOf(rec, currency, scales)
				sale.Since = moments(
					momentAt("received", recordAt(rec, fieldReceivedAt), recordString(rec, fieldReceivedLocationID)),
					momentAt("canceled", recordAt(rec, fieldCanceledAt), ""))

				return sale
			},
		},
		{
			entity: EntityOrderClaim,
			fields: []string{
				fieldID, fieldStatus, fieldCreatedAt, fieldCanceledAt, fieldReason,
				fieldClaimType, fieldRefundAmount, fieldCompletedAt,
			},
			view: func(rec query.Record) orderAfterSale {
				sale := afterSaleOf(kindClaim, rec)
				sale.Detail = nonEmpty("settled by "+recordString(rec, fieldClaimType), recordString(rec, fieldReason))
				sale.Money = refundOf(rec, currency, scales)
				sale.Since = moments(
					momentAt("completed", recordAt(rec, fieldCompletedAt), ""),
					momentAt("canceled", recordAt(rec, fieldCanceledAt), ""))

				return sale
			},
		},
		{
			entity: EntityOrderExchange,
			fields: []string{
				fieldID, fieldStatus, fieldCreatedAt, fieldCanceledAt,
				fieldDifferenceDue, fieldPaymentCollectionID,
			},
			view: func(rec query.Record) orderAfterSale {
				sale := afterSaleOf(kindExchange, rec)
				if collection := recordString(rec, fieldPaymentCollectionID); collection != "" {
					sale.Detail = []string{"paid through " + collection}
				}
				sale.Money = differenceOf(rec, currency, scales)
				sale.Since = moments(momentAt("canceled", recordAt(rec, fieldCanceledAt), ""))

				return sale
			},
		},
		{
			entity: EntityOrderReplacement,
			fields: []string{
				fieldID, fieldStatus, fieldCreatedAt, fieldCanceledAt, fieldClaimID, fieldExchangeID,
				fieldLocationID, fieldDispatchedAt, fieldFulfillmentID, fieldItems,
			},
			view: func(rec query.Record) orderAfterSale {
				sale := afterSaleOf(kindReplacement, rec)
				source := "for claim " + recordString(rec, fieldClaimID)
				if exchange := recordString(rec, fieldExchangeID); exchange != "" {
					source = "for exchange " + exchange
				}
				sale.Detail = nonEmpty(source, prefixed("from ", recordString(rec, fieldLocationID)))
				sale.Goods = afterSaleGoods(rec[fieldItems], titles)
				sale.Since = moments(
					momentAt("dispatched", recordAt(rec, fieldDispatchedAt), recordString(rec, fieldFulfillmentID)),
					momentAt("canceled", recordAt(rec, fieldCanceledAt), ""))

				return sale
			},
		},
	} {
		records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
			Entity:  kind.entity,
			Fields:  kind.fields,
			Filters: map[string]any{fieldOrderID: orderID},
			Limit:   afterSalesPerKind,
		})
		if err != nil {
			return nil, false, true
		}
		more = more || len(records) >= afterSalesPerKind
		for _, rec := range records {
			sale := kind.view(rec)
			if sale.Kind == kindClaim {
				sale.ClaimType = recordString(rec, fieldClaimType)
			}
			sale.Forms = afterSaleForms(sale.Kind, sale.Status, sale.ClaimType)
			out = append(out, sale)
		}
	}

	// Newest first across the kinds, the id breaking a tie.
	slices.SortStableFunc(out, func(a, b orderAfterSale) int {
		if c := b.OpenedAt.Compare(a.OpenedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})

	return out, more, false
}

// afterSaleOf reads what every kind has: its id, status and opening.
func afterSaleOf(kind string, rec query.Record) orderAfterSale {
	return orderAfterSale{
		Kind:     kind,
		ID:       recordString(rec, fieldID),
		Status:   recordString(rec, fieldStatus),
		OpenedAt: recordTime(rec, fieldCreatedAt),
	}
}

// afterSaleGoods reads a record's lines as "title × quantity"; a line the page
// did not read is named by its id, and a variant sent in its place by the
// variant's.
func afterSaleGoods(value any, titles map[string]string) []string {
	items, _ := value.([]map[string]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		name := stringValue(item[itemLineItemID])
		if title := titles[name]; title != "" {
			name = title
		}
		if name == "" {
			name = "variant " + stringValue(item[fieldVariantID])
		}
		quantity, _ := intValue(item[itemQuantity])
		out = append(out, name+" × "+strconv.Itoa(quantity))
	}

	return out
}

// refundOf prints a refund the record plans; empty when it plans none.
func refundOf(rec query.Record, currency string, scales map[string]int) string {
	if recordInt(rec, fieldRefundAmount) == 0 {
		return ""
	}

	return "refund " + moneyText(rec, fieldRefundAmount, currency, scales)
}

// differenceOf prints an exchange's difference and who pays it.
func differenceOf(rec query.Record, currency string, scales map[string]int) string {
	due := recordInt(rec, fieldDifferenceDue)
	switch {
	case due > 0:
		return moneyText(rec, fieldDifferenceDue, currency, scales) + " due from the customer"
	case due < 0:
		amount, known := formatAmount(-due, currency, scales)
		return withCurrency(amount, currency, known) + " due to the customer"
	default:
		return "no difference"
	}
}

// moneyText prints one money field with its currency.
func moneyText(rec query.Record, field, currency string, scales map[string]int) string {
	amount, known := amountField(rec, field, currency, scales)

	return withCurrency(amount, currency, known)
}

// withCurrency adds the currency to a printed amount, and says so when the
// amount is in minor units because the currency's scale was unknown.
func withCurrency(amount, currency string, known bool) string {
	text := amount + " " + currency
	if !known {
		text += " (minor units)"
	}

	return text
}

// momentAt prints one moment, with where it happened when that is known;
// empty when it has not happened.
func momentAt(label string, at *time.Time, where string) string {
	if at == nil {
		return ""
	}

	return label + " " + at.UTC().Format(momentLayout) + prefixed(" · ", where)
}

// moments keeps the moments that happened.
func moments(all ...string) []string { return nonEmpty(all...) }

// nonEmpty keeps the texts that say something.
func nonEmpty(texts ...string) []string {
	out := make([]string, 0, len(texts))
	for _, text := range texts {
		if strings.TrimSpace(text) != "" {
			out = append(out, text)
		}
	}

	return out
}

// prefixed puts a prefix before a text; empty when the text is.
func prefixed(prefix, text string) string {
	if text == "" {
		return ""
	}

	return prefix + text
}
