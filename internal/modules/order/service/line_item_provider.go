package service

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// LineItemEntity is the entity name the LINE is offered under.
//
// It is deliberately not "line_item": the entity namespace of the Query layer
// is flat and shared by every module, and a bare "line_item" would be the first
// name a cart or a quote line would also want. The name carries its owner.
const LineItemEntity = "order_line_item"

// LineItemProviderName is the line provider's name in the container (ADR 0004).
const LineItemProviderName = LineItemEntity + query.ProviderSuffix

// The fields the LINE offers the Query layer.
//
// They are the ones a demand question is made of: WHAT was sold (the variant
// and the title it was sold under), HOW MANY, and AT WHAT AMOUNT. The
// identifiers are separate Go constants from the order's even where the string
// is the same ("subtotal", "total", "created_at"): they belong to a different
// entity's contract, and the two sets can move apart — the order has a
// shipping_total and the line never will, because shipping does not exist at
// line level.
const (
	// FieldLineItemOrderID is the order the line belongs to.
	//
	// It is the JOIN KEY upwards: a consumer that needs the order's own facts —
	// the customer, the region, the currency, the moment it was placed — reads
	// them from the "order" entity with this identifier. They are deliberately
	// NOT copied onto the line record; two entities answering the same question
	// is how the two start disagreeing.
	FieldLineItemOrderID = "order_id"
	// FieldLineItemVariantID is the product variant the line sold. It is
	// another module's identifier and is not validated here (Principle 2.2).
	FieldLineItemVariantID = "variant_id"
	// FieldLineItemTitle is the name the line was sold under.
	//
	// It is the COPY taken at the moment of sale, not the variant's current
	// name: a catalog rename must not rewrite what an invoice says. A consumer
	// wanting today's name reads the product entity through the variant id.
	FieldLineItemTitle = "title"
	// FieldLineItemQuantity is how many units the line sold.
	FieldLineItemQuantity = "quantity"
	// FieldLineItemUnitPrice is the unit price at the moment of sale (minor
	// unit).
	FieldLineItemUnitPrice = "unit_price"
	// FieldLineItemSubtotal is the line's subtotal (minor unit).
	FieldLineItemSubtotal = "subtotal"
	// FieldLineItemDiscountTotal is the discount falling on the line, stored
	// POSITIVE (minor unit).
	FieldLineItemDiscountTotal = "discount_total"
	// FieldLineItemTaxTotal is the tax falling on the line (minor unit).
	FieldLineItemTaxTotal = "tax_total"
	// FieldLineItemTaxRateBps is the rate that tax was computed at, in basis
	// points (2000 = 20%).
	//
	// It is offered because it CANNOT BE DERIVED from the amounts: the tax is
	// rounded down per line, so 20% and 19.99% produce the same figure (see
	// migration 000004). A consumer recomputing the rate would be inventing it.
	// On an order placed before the column existed it is 0, and 0 is also a
	// legitimate rate — the two cannot be told apart, which is the price paid
	// in that migration and is repeated here so a report does not read a
	// historical zero as a tax exemption.
	FieldLineItemTaxRateBps = "tax_rate_bps"
	// FieldLineItemTotal is the line's total: subtotal - discount + tax (minor
	// unit).
	FieldLineItemTotal = "total"
	// FieldLineItemCreatedAt is when the line ROW was written.
	//
	// It is NOT the moment of the sale and must not be used as one: a line
	// added to an existing order (an exchange) carries the day of the exchange.
	// The sale's moment is the order's placed_at, which is why the date FILTER
	// of this provider ([FilterPlacedFrom]) is bound to the order and not to
	// this column.
	FieldLineItemCreatedAt = "created_at"
)

// The filter names this provider accepts that are NOT fields.
//
// A filter usually names a field, and these two do not: the date a line was
// sold is a fact of the ORDER (placed_at), reached through a join. It is not
// offered as a field, because a field of the line entity has to be a column of
// the line, and putting the order's stamp into the record would make the same
// fact answerable from two entities — which is how two answers start to
// disagree. A consumer that wants to SEE the date reads it from the "order"
// entity with [FieldLineItemOrderID].
const (
	// FilterPlacedFrom is the INCLUSIVE lower bound of the order's placed_at.
	FilterPlacedFrom = "placed_from"
	// FilterPlacedTo is the EXCLUSIVE upper bound of the order's placed_at.
	//
	// Half-open [from, to) so that two consecutive periods asked for back to
	// back cannot both count an order placed exactly on the boundary.
	FilterPlacedTo = "placed_to"
)

// lineItemFieldGetters maps a field name to the value it reads off the model.
//
// The map IS the contract: a field that is not in it is refused rather than
// answered with a zero value, because a consumer cannot tell a zero it asked
// for from a zero that means "this field does not exist" (ADR 0004).
//
// # Why metadata is not here
//
// The line's metadata is the CALLER's free-form bag: this module writes what it
// is handed and never reads it, so it has no shape, no meaning the module can
// state, and no guarantee of being present on any two lines. Offering it would
// publish a field whose contract is "whatever some workflow happened to put
// there" — and once a report reads a key out of it, that key becomes a contract
// nobody agreed to and nothing protects. The shipment provider leaves out the
// carrier's data bag for the same reason. A consumer that genuinely needs one
// of those keys should have it promoted to a column, where it can be validated.
var lineItemFieldGetters = map[string]func(models.OrderLineItem) any{
	FieldID:                    func(l models.OrderLineItem) any { return l.ID },
	FieldLineItemOrderID:       func(l models.OrderLineItem) any { return l.OrderID },
	FieldLineItemVariantID:     func(l models.OrderLineItem) any { return l.VariantID },
	FieldLineItemTitle:         func(l models.OrderLineItem) any { return l.Title },
	FieldLineItemQuantity:      func(l models.OrderLineItem) any { return l.Quantity },
	FieldLineItemUnitPrice:     func(l models.OrderLineItem) any { return l.UnitPrice },
	FieldLineItemSubtotal:      func(l models.OrderLineItem) any { return l.Subtotal },
	FieldLineItemDiscountTotal: func(l models.OrderLineItem) any { return l.DiscountTotal },
	FieldLineItemTaxTotal:      func(l models.OrderLineItem) any { return l.TaxTotal },
	FieldLineItemTaxRateBps:    func(l models.OrderLineItem) any { return l.TaxRateBps },
	FieldLineItemTotal:         func(l models.OrderLineItem) any { return l.Total },
	FieldLineItemCreatedAt:     func(l models.OrderLineItem) any { return l.CreatedAt },
}

// LineItemQueryProvider offers the ORDER LINE to the cross-module read layer.
//
// # Why a second provider in one module
//
// The module already offers "order", and [QueryProvider] states why that one
// does not carry the lines: they are an unpaginated set of variable length per
// order and embedding them in a Record would break Query's join key contract
// (one "id" field per record). That reasoning is still right, and it left a
// hole: nothing could ask which VARIANTS sold, in what quantity, in a period.
// The order's filters are id/customer_id/region_id/status — no date, and no way
// down to the lines — so demand analytics and forecasting had no read at all.
//
// The answer is not to change the order entity but to give the line its own,
// exactly as the fulfillment module offers the shipment next to the shipping
// option: a module may offer several entities and the Query layer looks each up
// by its own name.
//
// # What it is not
//
// It is not a reporting engine. It returns LINES, not sums: no grouping, no
// aggregation, no ranking. Aggregation over a filtered set is the consumer's
// job, and pushing a GROUP BY behind this interface would mean a provider whose
// records are not records of an entity — the one thing Query's contract cannot
// express.
type LineItemQueryProvider struct {
	svc *Service
}

// That the provider satisfies the core contract is verified at compile time; a
// signature drift does not survive until run time.
var _ query.Provider = (*LineItemQueryProvider)(nil)

// NewLineItemQueryProvider produces a provider running on the given service.
func NewLineItemQueryProvider(svc *Service) *LineItemQueryProvider {
	return &LineItemQueryProvider{svc: svc}
}

// Entity returns the entity name the provider offers.
func (p *LineItemQueryProvider) Entity() string { return LineItemEntity }

// List returns the root records.
//
// Supported filters: "id" (text or a list of text), "order_id" (text),
// "variant_id" (text), "placed_from" and "placed_to" (RFC 3339 text or a
// time.Time). Any other filter, or an unrecognized field, is rejected with
// errors.Invalid (ADR 0004).
//
// The two date filters are the reason this entity exists; they select on the
// ORDER's placed_at and the interval is half-open [from, to).
//
// The limit is CLAMPED to [MaxLimit], silently; see [providerLimit]. That makes
// this a PAGED read of a table that is the module's largest — a report covering
// a month has to walk the pages, and the ordering (newest sale first, ties
// broken by the line id) is stable enough for that to terminate.
func (p *LineItemQueryProvider) List(
	ctx context.Context, opts query.ListOptions,
) ([]query.Record, error) {
	if err := validateLineItemFields(opts.Fields); err != nil {
		return nil, err
	}

	// An id filter short-circuits to the batch read, for the reason
	// [QueryProvider.List] gives: it is the same set of records and one query
	// rather than a filtered scan.
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

	in := ListLineItemsInput{
		Page: Page{Limit: providerLimit(opts.Limit), Offset: int64(opts.Offset)},
	}
	// The filters are walked IN NAME ORDER: map order is random, and if more
	// than one filter were invalid at once, which error is returned would be
	// random too — a test of the error message would then be flaky rather than
	// wrong.
	for _, name := range slices.Sorted(maps.Keys(opts.Filters)) {
		value := opts.Filters[name]
		switch name {
		case FieldLineItemOrderID:
			id, err := textFilter(name, value)
			if err != nil {
				return nil, err
			}
			in.OrderID = &id
		case FieldLineItemVariantID:
			id, err := textFilter(name, value)
			if err != nil {
				return nil, err
			}
			in.VariantID = &id
		case FilterPlacedFrom:
			at, err := timeFilter(name, value)
			if err != nil {
				return nil, err
			}
			in.PlacedFrom = &at
		case FilterPlacedTo:
			at, err := timeFilter(name, value)
			if err != nil {
				return nil, err
			}
			in.PlacedTo = &at
		default:
			return nil, errors.Invalid(CodeInvalidInput,
				"entity %q does not support the filter %q", LineItemEntity, name)
		}
	}

	lines, err := p.svc.ListLineItems(ctx, in)
	if err != nil {
		return nil, err
	}

	return lineItemRecords(lines, opts.Fields), nil
}

// FetchByIDs returns the records of the given identifiers as a BATCH.
//
// No record is returned for an identifier that is not found; that is not an
// error. This is the call an expansion makes, and making it one query per id
// would put back the N+1 the read layer exists to prevent.
func (p *LineItemQueryProvider) FetchByIDs(
	ctx context.Context, ids, fields []string,
) ([]query.Record, error) {
	if err := validateLineItemFields(fields); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []query.Record{}, nil
	}

	lines, err := p.svc.ListLineItemsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	return lineItemRecords(lines, fields), nil
}

// lineItemRecords turns the lines into records with the requested fields.
// If fields is empty, ALL offered fields are returned.
func lineItemRecords(lines []models.OrderLineItem, fields []string) []query.Record {
	selected := fields
	if len(selected) == 0 {
		selected = slices.Sorted(maps.Keys(lineItemFieldGetters))
	}

	out := make([]query.Record, 0, len(lines))
	// The slice is walked BY INDEX: walking by value would copy the whole
	// struct on every iteration and the cost would grow with the record count.
	for i := range lines {
		record := make(query.Record, len(selected))
		for _, name := range selected {
			record[name] = lineItemFieldGetters[name](lines[i])
		}
		out = append(out, record)
	}

	return out
}

// validateLineItemFields verifies that all the requested fields are offered.
func validateLineItemFields(fields []string) error {
	for _, name := range fields {
		if _, ok := lineItemFieldGetters[name]; !ok {
			return errors.Invalid(CodeInvalidInput,
				"entity %q does not offer the field %q", LineItemEntity, name)
		}
	}

	return nil
}

// textFilter reads a filter value that has to be text.
func textFilter(name string, value any) (string, error) {
	text, ok := value.(string)
	if !ok {
		return "", errors.Invalid(CodeInvalidInput,
			"filter %q has to be text, %T given", name, value)
	}

	return text, nil
}

// timeFilter reads a filter value that has to be an instant.
//
// BOTH a time.Time and RFC 3339 text are accepted, because the two arrive by
// two different doors: a query definition that came in as JSON can only carry
// the date as text, while an in-process caller already holds a time.Time.
// Forcing the second one to format its value just so this function can parse it
// back is a round trip that can only lose information.
//
// The format is fixed to RFC 3339 and a value without a zone is REFUSED by the
// parser. A date range is a question about a period of trading, and reading
// "2026-09-01" as UTC when the shop keeps its books in Istanbul would silently
// move the boundary by three hours — into the previous day's sales.
func timeFilter(name string, value any) (time.Time, error) {
	switch typed := value.(type) {
	case time.Time:
		return typed, nil
	case string:
		at, err := time.Parse(time.RFC3339, typed)
		if err != nil {
			return time.Time{}, errors.Wrap(err, errors.KindInvalid, CodeInvalidInput,
				"filter %q has to be an RFC 3339 date with a time zone (2006-01-02T15:04:05+03:00), "+
					"%q given", name, typed)
		}

		return at, nil
	default:
		return time.Time{}, errors.Invalid(CodeInvalidInput,
			"filter %q has to be a date, %T given", name, value)
	}
}
