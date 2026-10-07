package service

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// The after-sales entities (ADR 0270).
//
// An order's returns, claims, exchanges and replacements are offered to the
// read layer as four entities of their own, for the reason the lines are
// ([LineItemQueryProvider]): they are a set of variable length per order, and
// a record has one join key. Each is read PER ORDER — the "order_id" filter is
// required — because that is the question the tables are indexed for and the
// one the admin panel's order page asks; a listing across orders is a report
// no reader has asked for yet.
//
// # What they leave out
//
// The metadata, for the reason the line leaves its own out: it is the
// caller's bag and has no shape this module states. A claim's evidence and a
// replacement line's parts, which have endpoints of their own.
const (
	// ReturnEntity, ClaimEntity, ExchangeEntity and ReplacementEntity are the
	// entity names; each carries its owner, as [LineItemEntity] does.
	ReturnEntity      = "order_return"
	ClaimEntity       = "order_claim"
	ExchangeEntity    = "order_exchange"
	ReplacementEntity = "order_replacement"

	// ReturnProviderName, ClaimProviderName, ExchangeProviderName and
	// ReplacementProviderName are the providers' names in the container.
	ReturnProviderName      = ReturnEntity + query.ProviderSuffix
	ClaimProviderName       = ClaimEntity + query.ProviderSuffix
	ExchangeProviderName    = ExchangeEntity + query.ProviderSuffix
	ReplacementProviderName = ReplacementEntity + query.ProviderSuffix
)

// The fields the after-sales entities offer. A name several entities share
// means the same thing on each.
const (
	// FieldAfterSalesOrderID is the order a return, claim or exchange belongs
	// to, and the filter every after-sales entity requires. A replacement
	// names its claim or exchange instead and takes the filter all the same.
	FieldAfterSalesOrderID = "order_id"
	// FieldAfterSalesStatus is the record's status.
	FieldAfterSalesStatus = "status"
	// FieldAfterSalesNote is the operator's free-form note; empty when none.
	FieldAfterSalesNote = "note"
	// FieldAfterSalesCanceledAt is when the request was withdrawn; nil while
	// it stands.
	FieldAfterSalesCanceledAt = "canceled_at"
	// FieldAfterSalesCreatedAt is when the record was opened.
	FieldAfterSalesCreatedAt = "created_at"
	// FieldAfterSalesItems are the record's lines, in the order they were
	// written, each a map; read in one query for the whole page, and only when
	// asked for.
	FieldAfterSalesItems = "items"

	// FieldReturnRefundAmount is the amount planned to be refunded (minor
	// unit), FieldReturnReason the return's rationale.
	FieldReturnRefundAmount = "refund_amount"
	FieldReturnReason       = "reason"
	// FieldReturnReceivedAt is when the goods arrived, nil until they do, and
	// FieldReturnReceivedLocationID where; empty until they do.
	FieldReturnReceivedAt         = "received_at"
	FieldReturnReceivedLocationID = "received_location_id"

	// FieldClaimType is how the claim is settled; FieldClaimCompletedAt when
	// it was, nil until it is. A claim also offers refund_amount and reason.
	FieldClaimType        = "type"
	FieldClaimCompletedAt = "completed_at"

	// FieldExchangeDifferenceDue is the exchange's difference (minor unit),
	// NEGATIVE when it is paid to the customer; FieldExchangePaymentCollectionID
	// the collection opened for it, empty until one is named.
	FieldExchangeDifferenceDue       = "difference_due"
	FieldExchangePaymentCollectionID = "payment_collection_id"
	// FieldExchangeReturnID is the return whose goods the exchange takes
	// back, empty on one that names none; with it the difference is derived
	// (ADR 0432).
	FieldExchangeReturnID = "return_id"

	// FieldReplacementClaimID and FieldReplacementExchangeID are the record
	// the replacement settles; exactly one is set.
	FieldReplacementClaimID    = "claim_id"
	FieldReplacementExchangeID = "exchange_id"
	// FieldReplacementShippingOptionID is how it is sent and
	// FieldReplacementLocationID where from.
	FieldReplacementShippingOptionID = "shipping_option_id"
	FieldReplacementLocationID       = "location_id"
	// FieldReplacementDispatchedAt is when the goods left, nil until they do,
	// and FieldReplacementFulfillmentID the parcel they left in.
	FieldReplacementDispatchedAt  = "dispatched_at"
	FieldReplacementFulfillmentID = "fulfillment_id"
)

// The keys of one of a record's lines.
const (
	itemKeyLineItemID   = "line_item_id"
	itemKeyVariantID    = "variant_id"
	itemKeyQuantity     = "quantity"
	itemKeyRefundAmount = "refund_amount"
)

// moment turns a nullable stamp into the read layer's value: nil or the time.
func moment(at *time.Time) any {
	if at == nil {
		return nil
	}

	return *at
}

// afterSalesProvider is one after-sales entity: the fields it offers, how it
// reads a page of one order's records and a batch by identifier, and how it
// reads the page's lines when it has any.
type afterSalesProvider[T any] struct {
	entity  string
	getters map[string]func(T) any
	idOf    func(T) string
	page    func(ctx context.Context, filter models.ChildFilter) ([]T, error)
	byIDs   func(ctx context.Context, ids []string) ([]T, error)
	// items reads the lines of the given records by record id; nil for an
	// entity without lines.
	items func(ctx context.Context, ids []string) (map[string][]map[string]any, error)
}

var _ query.Provider = (*afterSalesProvider[models.Return])(nil)

// Entity returns the entity name the provider offers.
func (p *afterSalesProvider[T]) Entity() string { return p.entity }

// List returns one order's records, newest first.
//
// Supported filters: "order_id" (text), which is REQUIRED, and "id" (text or
// a list of text), which selects by identity and stands alone. Any other
// filter, or a field the entity does not offer, is refused with
// errors.Invalid (ADR 0004). The limit is clamped to [MaxLimit].
func (p *afterSalesProvider[T]) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	if err := p.validateFields(opts.Fields); err != nil {
		return nil, err
	}

	if raw, ok := opts.Filters[FieldID]; ok {
		if len(opts.Filters) > 1 {
			return nil, errors.Invalid(CodeInvalidInput,
				"the %q filter selects records by identity and cannot be combined with another "+
					"filter", FieldID)
		}
		ids, err := idFilter(raw)
		if err != nil {
			return nil, err
		}

		return p.FetchByIDs(ctx, ids, opts.Fields)
	}

	for _, name := range slices.Sorted(maps.Keys(opts.Filters)) {
		if name != FieldAfterSalesOrderID {
			return nil, errors.Invalid(CodeInvalidInput,
				"entity %q does not support the %q filter", p.entity, name)
		}
	}
	raw, ok := opts.Filters[FieldAfterSalesOrderID]
	if !ok {
		return nil, errors.Invalid(CodeInvalidInput,
			"entity %q is read per order: the %q filter is required", p.entity, FieldAfterSalesOrderID)
	}
	orderID, err := textFilter(FieldAfterSalesOrderID, raw)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(orderID) == "" {
		return nil, errors.Invalid(CodeInvalidInput,
			"entity %q is read per order: the %q filter cannot be empty", p.entity, FieldAfterSalesOrderID)
	}

	records, err := p.page(ctx, models.ChildFilter{
		OrderID: orderID, Limit: providerLimit(opts.Limit), Offset: int64(opts.Offset),
	})
	if err != nil {
		return nil, err
	}

	return p.records(ctx, records, opts.Fields)
}

// FetchByIDs returns the records of the given identifiers as a batch, newest
// first; an identifier with no record is left out.
func (p *afterSalesProvider[T]) FetchByIDs(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	if err := p.validateFields(fields); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []query.Record{}, nil
	}

	records, err := p.byIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	return p.records(ctx, records, fields)
}

// records turns the page into records with the requested fields, reading the
// page's lines in one query when they are asked for.
func (p *afterSalesProvider[T]) records(ctx context.Context, page []T, fields []string) ([]query.Record, error) {
	selected := fields
	if len(selected) == 0 {
		selected = slices.Sorted(maps.Keys(p.getters))
		if p.items != nil {
			selected = append(selected, FieldAfterSalesItems)
		}
	}

	var items map[string][]map[string]any
	if p.items != nil && slices.Contains(selected, FieldAfterSalesItems) {
		ids := make([]string, 0, len(page))
		for i := range page {
			ids = append(ids, p.idOf(page[i]))
		}
		read, err := p.items(ctx, ids)
		if err != nil {
			return nil, err
		}
		items = read
	}

	out := make([]query.Record, 0, len(page))
	for i := range page {
		record := make(query.Record, len(selected))
		for _, name := range selected {
			if name == FieldAfterSalesItems {
				lines := items[p.idOf(page[i])]
				if lines == nil {
					lines = []map[string]any{}
				}
				record[name] = lines

				continue
			}
			record[name] = p.getters[name](page[i])
		}
		out = append(out, record)
	}

	return out, nil
}

// validateFields refuses a field the entity does not offer.
func (p *afterSalesProvider[T]) validateFields(fields []string) error {
	for _, name := range fields {
		if _, ok := p.getters[name]; ok {
			continue
		}
		if name == FieldAfterSalesItems && p.items != nil {
			continue
		}

		return errors.Invalid(CodeInvalidInput, "entity %q does not offer the field %q", p.entity, name)
	}

	return nil
}

// NewReturnQueryProvider offers an order's returns to the read layer.
func NewReturnQueryProvider(svc *Service) query.Provider {
	return &afterSalesProvider[models.Return]{
		entity: ReturnEntity,
		getters: map[string]func(models.Return) any{
			FieldID:                       func(r models.Return) any { return r.ID },
			FieldAfterSalesOrderID:        func(r models.Return) any { return r.OrderID },
			FieldAfterSalesStatus:         func(r models.Return) any { return r.Status.String() },
			FieldAfterSalesNote:           func(r models.Return) any { return r.Note },
			FieldAfterSalesCanceledAt:     func(r models.Return) any { return moment(r.CanceledAt) },
			FieldAfterSalesCreatedAt:      func(r models.Return) any { return r.CreatedAt },
			FieldReturnRefundAmount:       func(r models.Return) any { return r.RefundAmount },
			FieldReturnReason:             func(r models.Return) any { return r.Reason },
			FieldReturnReceivedAt:         func(r models.Return) any { return moment(r.ReceivedAt) },
			FieldReturnReceivedLocationID: func(r models.Return) any { return r.ReceivedLocationID },
		},
		idOf: func(r models.Return) string { return r.ID },
		page: func(ctx context.Context, filter models.ChildFilter) ([]models.Return, error) {
			records, _, err := svc.store.ListReturns(ctx, filter)
			return records, err
		},
		byIDs: svc.store.ReturnsByIDs,
		items: func(ctx context.Context, ids []string) (map[string][]map[string]any, error) {
			read, err := svc.store.ReturnItemsOf(ctx, ids)
			if err != nil {
				return nil, err
			}
			out := make(map[string][]map[string]any, len(read))
			for id, lines := range read {
				for i := range lines {
					out[id] = append(out[id], map[string]any{
						itemKeyLineItemID:   lines[i].OrderLineItemID,
						itemKeyQuantity:     lines[i].Quantity,
						itemKeyRefundAmount: lines[i].RefundAmount,
					})
				}
			}

			return out, nil
		},
	}
}

// NewClaimQueryProvider offers an order's claims to the read layer.
func NewClaimQueryProvider(svc *Service) query.Provider {
	return &afterSalesProvider[models.Claim]{
		entity: ClaimEntity,
		getters: map[string]func(models.Claim) any{
			FieldID:                   func(c models.Claim) any { return c.ID },
			FieldAfterSalesOrderID:    func(c models.Claim) any { return c.OrderID },
			FieldAfterSalesStatus:     func(c models.Claim) any { return c.Status.String() },
			FieldAfterSalesNote:       func(c models.Claim) any { return c.Note },
			FieldAfterSalesCanceledAt: func(c models.Claim) any { return moment(c.CanceledAt) },
			FieldAfterSalesCreatedAt:  func(c models.Claim) any { return c.CreatedAt },
			FieldClaimType:            func(c models.Claim) any { return c.Type.String() },
			FieldReturnRefundAmount:   func(c models.Claim) any { return c.RefundAmount },
			FieldReturnReason:         func(c models.Claim) any { return c.Reason },
			FieldClaimCompletedAt:     func(c models.Claim) any { return moment(c.CompletedAt) },
		},
		idOf: func(c models.Claim) string { return c.ID },
		page: func(ctx context.Context, filter models.ChildFilter) ([]models.Claim, error) {
			records, _, err := svc.store.ListClaims(ctx, filter)
			return records, err
		},
		byIDs: svc.store.ClaimsByIDs,
	}
}

// NewExchangeQueryProvider offers an order's exchanges to the read layer.
func NewExchangeQueryProvider(svc *Service) query.Provider {
	return &afterSalesProvider[models.Exchange]{
		entity: ExchangeEntity,
		getters: map[string]func(models.Exchange) any{
			FieldID:                          func(e models.Exchange) any { return e.ID },
			FieldAfterSalesOrderID:           func(e models.Exchange) any { return e.OrderID },
			FieldAfterSalesStatus:            func(e models.Exchange) any { return e.Status.String() },
			FieldAfterSalesNote:              func(e models.Exchange) any { return e.Note },
			FieldAfterSalesCanceledAt:        func(e models.Exchange) any { return moment(e.CanceledAt) },
			FieldAfterSalesCreatedAt:         func(e models.Exchange) any { return e.CreatedAt },
			FieldExchangeDifferenceDue:       func(e models.Exchange) any { return e.DifferenceDue },
			FieldExchangePaymentCollectionID: func(e models.Exchange) any { return e.PaymentCollectionID },
			FieldExchangeReturnID:            func(e models.Exchange) any { return e.ReturnID },
		},
		idOf: func(e models.Exchange) string { return e.ID },
		page: func(ctx context.Context, filter models.ChildFilter) ([]models.Exchange, error) {
			records, _, err := svc.store.ListExchanges(ctx, filter)
			return records, err
		},
		byIDs: svc.store.ExchangesByIDs,
	}
}

// NewReplacementQueryProvider offers the replacements an order's claims and
// exchanges promised to the read layer.
func NewReplacementQueryProvider(svc *Service) query.Provider {
	return &afterSalesProvider[models.Replacement]{
		entity: ReplacementEntity,
		getters: map[string]func(models.Replacement) any{
			FieldID:                          func(r models.Replacement) any { return r.ID },
			FieldAfterSalesStatus:            func(r models.Replacement) any { return r.Status.String() },
			FieldAfterSalesNote:              func(r models.Replacement) any { return r.Note },
			FieldAfterSalesCanceledAt:        func(r models.Replacement) any { return moment(r.CanceledAt) },
			FieldAfterSalesCreatedAt:         func(r models.Replacement) any { return r.CreatedAt },
			FieldReplacementClaimID:          func(r models.Replacement) any { return r.ClaimID },
			FieldReplacementExchangeID:       func(r models.Replacement) any { return r.ExchangeID },
			FieldReplacementShippingOptionID: func(r models.Replacement) any { return r.ShippingOptionID },
			FieldReplacementLocationID:       func(r models.Replacement) any { return r.LocationID },
			FieldReplacementDispatchedAt:     func(r models.Replacement) any { return moment(r.DispatchedAt) },
			FieldReplacementFulfillmentID:    func(r models.Replacement) any { return r.FulfillmentID },
		},
		idOf:  func(r models.Replacement) string { return r.ID },
		page:  svc.store.PageReplacementsOfOrder,
		byIDs: svc.store.ReplacementsByIDs,
		items: func(ctx context.Context, ids []string) (map[string][]map[string]any, error) {
			read, err := svc.store.ReplacementItemsOf(ctx, ids)
			if err != nil {
				return nil, err
			}
			out := make(map[string][]map[string]any, len(read))
			for id, lines := range read {
				for i := range lines {
					out[id] = append(out[id], map[string]any{
						itemKeyLineItemID: lines[i].OrderLineItemID,
						itemKeyVariantID:  lines[i].VariantID,
						itemKeyQuantity:   lines[i].Quantity,
					})
				}
			}

			return out, nil
		},
	}
}
