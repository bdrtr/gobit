// Package stockalert mails a customer once when a variant they marked on their
// wishlist is back in stock (ADR 0215), or when its price drops (ADR 0216).
//
// It is a workflow because it combines four modules (ADR 0006): the customer
// module holds the marks and the address, the product module answers whether a
// variant is in stock and shown as the storefront shows it, pricing answers
// what the customer's cart would be charged for it, through the cart
// workflow's own quote, and the notification module sends. None of them
// imports another, and this package imports none of them.
//
// # A price that drops
//
// A price mark names a region. The first pass after the mark records the unit
// price the customer's cart in that region would be charged for one, price
// lists included and promotions not (the cart workflow's quote); a later pass
// that finds that price lower, in the same currency, mails once and clears the
// mark. A variant the storefront does not show in the mark's channels is not
// priced.
//
// # Back, not merely in
//
// A mark is ARMED the first time a pass finds its variant out of stock, and a
// pass mails only an armed mark whose variant is in stock again. A mark set on
// a variant that is in stock therefore waits for it to run out and come back,
// rather than mailing "back in stock" about something that never left.
//
// # Once
//
// The mail's reference names the mark and the moment it was armed, and the
// notification module sends a (template, reference) pair once, so a pass that
// stops between the mail and the clear sends nothing the second time. The
// clear takes the mark off only while it is still armed at that moment: a
// customer who marked the variant again meanwhile keeps the new mark.
//
// # Who is mailed
//
// Only the customer who set the mark, at the address on their own record: the
// mark is set by the proven customer alone (ADR 0190), so nothing here writes
// to an address somebody else named (ADR 0051).
package stockalert

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// The container names this flow resolves, spelled by hand because it imports
// no module; internal/arch pins the surfaces behind them.
const (
	ServiceCustomers     = "customer.service"
	ServiceProducts      = "product.interop"
	ServiceNotifications = "notification.interop"
	ServiceQuery         = "core.query"
)

// The cross-module names the flow reads, spelled by hand for the same reason.
const (
	// TemplateBackInStock is the notification a stock mail is sent with, and
	// TemplatePriceDrop the notification a price mail is sent with.
	TemplateBackInStock = "wishlist.back_in_stock"
	TemplatePriceDrop   = "wishlist.price_drop"
	// ChannelEmail is the notification channel.
	ChannelEmail = "email"
	// EntityVariant and EntityProduct are the read layer's catalog entities,
	// read for the names the mail carries.
	EntityVariant = "variant"
	EntityProduct = "product"
)

// The template's data keys. Every value is a string (core/provider's rule).
const (
	DataVariantID    = "variant_id"
	DataVariantTitle = "variant_title"
	DataProductTitle = "product_title"
	DataProductID    = "product_id"
	DataHandle       = "product_handle"
	// DataCurrencyCode, DataPreviousAmount and DataAmount are a price mail's:
	// the price at the mark and the price now, in minor units.
	DataCurrencyCode   = "currency_code"
	DataPreviousAmount = "previous_amount"
	DataAmount         = "amount"
)

// page is how many marks one read takes.
const page = 100

// CodePassIncomplete reports a pass that could not handle every mark it read.
const CodePassIncomplete = "stock_alert_pass_incomplete"

// CodeNotReady reports a flow that could not be wired or a record it could not
// read.
const CodeNotReady = "stock_alert_not_ready"

// Customers is the slice of the customer module this flow calls.
type Customers interface {
	// WishlistAlertsJSON pages the marked wishlist items after the given key.
	WishlistAlertsJSON(ctx context.Context, afterCustomerID, afterVariantID string, limit int) (json.RawMessage, error)
	// ArmStockAlert records that a marked variant was seen out of stock.
	ArmStockAlert(ctx context.Context, customerID, variantID string) (bool, error)
	// ClearStockAlert takes a mark off while it is armed at the given moment.
	ClearStockAlert(ctx context.Context, customerID, variantID string, armedAt time.Time) (bool, error)
	// CustomerEmail returns the customer's address.
	CustomerEmail(ctx context.Context, customerID string) (string, error)
	// RecordPriceBaseline records the price at the mark, once.
	RecordPriceBaseline(
		ctx context.Context, customerID, variantID string, markedAt time.Time, currency string, amount int64,
	) (bool, error)
	// ClearPriceAlert takes a price mark off while it is the given mark.
	ClearPriceAlert(ctx context.Context, customerID, variantID string, markedAt time.Time) (bool, error)
}

// Prices is the cart workflow's quote: the unit price a one-unit line would be
// charged in the customer's cart in the region, before promotions.
type Prices interface {
	QuoteUnitPrices(ctx context.Context, regionID, customerID string, variantIDs []string) (
		currency string, prices map[string]int64, err error)
}

// Catalog is the slice of the product module this flow calls.
type Catalog interface {
	// VariantsInStock answers the storefront's in-stock badge per variant in
	// the given sales channels.
	VariantsInStock(ctx context.Context, variantIDs, salesChannelIDs []string) (map[string]bool, error)
}

// Notifier is the slice of the notification module this flow calls.
type Notifier interface {
	// Send delivers one message; (template, reference) is its idempotency key.
	Send(ctx context.Context, template, channel, reference, to string, data map[string]string) error
}

// Reader is the read layer.
type Reader interface {
	// Graph reads records of an entity.
	Graph(ctx context.Context, spec query.GraphSpec) ([]query.Record, error)
}

// Workflow is the wired flow.
type Workflow struct {
	customers Customers
	catalog   Catalog
	prices    Prices
	notifier  Notifier
	reader    Reader
	log       *slog.Logger
}

// New builds the flow from resolved dependencies.
func New(
	customers Customers, catalog Catalog, prices Prices, notifier Notifier, reader Reader, log *slog.Logger,
) *Workflow {
	if log == nil {
		log = slog.Default()
	}

	return &Workflow{customers: customers, catalog: catalog, prices: prices, notifier: notifier, reader: reader, log: log}
}

// alert is one marked item as the customer module pages it.
type alert struct {
	CustomerID      string     `json:"customer_id"`
	VariantID       string     `json:"variant_id"`
	StockAlert      bool       `json:"stock_alert"`
	SalesChannelIDs []string   `json:"sales_channel_ids"`
	ArmedAt         *time.Time `json:"armed_at"`

	PriceAlert           bool       `json:"price_alert"`
	PriceMarkedAt        *time.Time `json:"price_marked_at"`
	PriceRegionID        string     `json:"price_region_id"`
	PriceSalesChannelIDs []string   `json:"price_sales_channel_ids"`
	PriceCurrencyCode    string     `json:"price_currency_code"`
	PriceAmount          *int64     `json:"price_amount"`
}

// Pass walks every mark once. It arms the stock marks whose variant is out of
// stock and mails and clears the armed ones whose variant is back; it records
// the price at the mark of the price marks that have none and mails and clears
// the ones whose price is lower now. It reports the stock marks armed, the
// prices recorded and the mails of either kind. A mark that fails is reported
// and does not stop the others.
func (w *Workflow) Pass(ctx context.Context) (armed, recorded, mailed int, err error) {
	var first error
	failed := 0
	afterCustomer, afterVariant := "", ""
	for {
		raw, err := w.customers.WishlistAlertsJSON(ctx, afterCustomer, afterVariant, page)
		if err != nil {
			return armed, recorded, mailed, err
		}
		var alerts []alert
		if err := json.Unmarshal(raw, &alerts); err != nil {
			return armed, recorded, mailed, errors.Wrap(err, errors.KindInternal, CodeNotReady,
				"the wishlist alerts could not be read")
		}
		a, m, pageErrs := w.handle(ctx, alerts)
		r, pm, priceErrs := w.handlePrices(ctx, alerts)
		armed, recorded, mailed = armed+a, recorded+r, mailed+m+pm
		for _, pageErr := range append(pageErrs, priceErrs...) {
			failed++
			if first == nil {
				first = pageErr
			}
		}
		if len(alerts) < page {
			break
		}
		afterCustomer, afterVariant = alerts[len(alerts)-1].CustomerID, alerts[len(alerts)-1].VariantID
	}
	if first != nil {
		return armed, recorded, mailed, errors.Wrap(first, errors.KindOf(first), CodePassIncomplete,
			"%d wishlist alerts could not be handled", failed)
	}

	return armed, recorded, mailed, nil
}

// handle answers one page's stock marks, a channel set at a time.
func (w *Workflow) handle(ctx context.Context, alerts []alert) (armed, mailed int, failures []error) {
	groups := map[string][]*alert{}
	var order []string
	for i := range alerts {
		a := &alerts[i]
		if !a.StockAlert {
			continue
		}
		key := channelKey(a.SalesChannelIDs)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], a)
	}
	for _, key := range order {
		group := groups[key]
		variantIDs := make([]string, 0, len(group))
		for _, a := range group {
			variantIDs = append(variantIDs, a.VariantID)
		}
		inStock, err := w.catalog.VariantsInStock(ctx, variantIDs, group[0].SalesChannelIDs)
		if err != nil {
			failures = append(failures, err)

			continue
		}
		for _, a := range group {
			switch {
			case a.ArmedAt == nil && !inStock[a.VariantID]:
				done, err := w.customers.ArmStockAlert(ctx, a.CustomerID, a.VariantID)
				if err != nil {
					failures = append(failures, err)
				} else if done {
					armed++
				}
			case a.ArmedAt != nil && inStock[a.VariantID]:
				if err := w.mail(ctx, a); err != nil {
					w.log.ErrorContext(ctx, "a stock alert could not be mailed",
						"customer_id", a.CustomerID, "variant_id", a.VariantID, "error", err)
					failures = append(failures, err)
				} else {
					mailed++
				}
			}
		}
	}

	return armed, mailed, failures
}

// mail sends one armed mark's mail and clears it.
func (w *Workflow) mail(ctx context.Context, a *alert) error {
	to, err := w.customers.CustomerEmail(ctx, a.CustomerID)
	if err != nil {
		return err
	}
	data, err := w.names(ctx, a.VariantID)
	if err != nil {
		return err
	}
	reference := fmt.Sprintf("%s:%s:%d", a.CustomerID, a.VariantID, a.ArmedAt.UnixNano())
	if err := w.notifier.Send(ctx, TemplateBackInStock, ChannelEmail, reference, to, data); err != nil {
		return err
	}
	_, err = w.customers.ClearStockAlert(ctx, a.CustomerID, a.VariantID, *a.ArmedAt)

	return err
}

// handlePrices answers one page's price marks, a customer, region and channel
// set at a time, since the price is the customer's in the region.
func (w *Workflow) handlePrices(ctx context.Context, alerts []alert) (recorded, mailed int, failures []error) {
	groups := map[string][]*alert{}
	var order []string
	for i := range alerts {
		a := &alerts[i]
		if !a.PriceAlert || a.PriceMarkedAt == nil {
			continue
		}
		key := a.CustomerID + "\x00" + a.PriceRegionID + "\x00" + channelKey(a.PriceSalesChannelIDs)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], a)
	}
	for _, key := range order {
		group := groups[key]
		variantIDs := make([]string, 0, len(group))
		for _, a := range group {
			variantIDs = append(variantIDs, a.VariantID)
		}
		// The storefront's own answer decides which variants are shown in the
		// mark's channels; only their price is asked.
		shown, err := w.catalog.VariantsInStock(ctx, variantIDs, group[0].PriceSalesChannelIDs)
		if err != nil {
			failures = append(failures, err)

			continue
		}
		currency, quotes, err := w.prices.QuoteUnitPrices(ctx, group[0].PriceRegionID, group[0].CustomerID, variantIDs)
		if errors.CodeOf(err) == cartwf.CodeQuoteRegionUnknown {
			// The request named a region that does not exist, or one that was
			// deleted since: the mark waits for nothing, and a pass is not the
			// worse for it.
			w.log.DebugContext(ctx, "a price mark names a region that does not exist",
				"customer_id", group[0].CustomerID, "region_id", group[0].PriceRegionID)

			continue
		}
		if err != nil {
			failures = append(failures, err)

			continue
		}
		for _, a := range group {
			price, priced := quotes[a.VariantID]
			if _, visible := shown[a.VariantID]; !visible || !priced {
				continue
			}
			switch {
			case a.PriceAmount == nil:
				done, err := w.customers.RecordPriceBaseline(ctx, a.CustomerID, a.VariantID,
					*a.PriceMarkedAt, currency, price)
				if err != nil {
					failures = append(failures, err)
				} else if done {
					recorded++
				}
			case currency == a.PriceCurrencyCode && price < *a.PriceAmount:
				if err := w.mailPrice(ctx, a, currency, price); err != nil {
					w.log.ErrorContext(ctx, "a price alert could not be mailed",
						"customer_id", a.CustomerID, "variant_id", a.VariantID, "error", err)
					failures = append(failures, err)
				} else {
					mailed++
				}
			}
		}
	}

	return recorded, mailed, failures
}

// mailPrice sends one price mark's mail and clears it.
func (w *Workflow) mailPrice(ctx context.Context, a *alert, currency string, price int64) error {
	to, err := w.customers.CustomerEmail(ctx, a.CustomerID)
	if err != nil {
		return err
	}
	data, err := w.names(ctx, a.VariantID)
	if err != nil {
		return err
	}
	data[DataCurrencyCode] = currency
	data[DataPreviousAmount] = fmt.Sprint(*a.PriceAmount)
	data[DataAmount] = fmt.Sprint(price)
	reference := fmt.Sprintf("%s:%s:price:%d", a.CustomerID, a.VariantID, a.PriceMarkedAt.UnixNano())
	if err := w.notifier.Send(ctx, TemplatePriceDrop, ChannelEmail, reference, to, data); err != nil {
		return err
	}
	_, err = w.customers.ClearPriceAlert(ctx, a.CustomerID, a.VariantID, *a.PriceMarkedAt)

	return err
}

// names reads what the mail says about the variant: its title, its product's
// title and handle.
func (w *Workflow) names(ctx context.Context, variantID string) (map[string]string, error) {
	variants, err := w.reader.Graph(ctx, query.GraphSpec{
		Entity: EntityVariant, Fields: []string{query.IDField, "title", "product_id"},
		Filters: map[string]any{"ids": []string{variantID}}, Limit: 1,
	})
	if err != nil {
		return nil, err
	}
	if len(variants) == 0 {
		return nil, errors.NotFound(CodeNotReady, "variant %s could not be read", variantID)
	}
	variantTitle, _ := variants[0]["title"].(string)
	productID, _ := variants[0]["product_id"].(string)
	products, err := w.reader.Graph(ctx, query.GraphSpec{
		Entity: EntityProduct, Fields: []string{query.IDField, "title", "handle"},
		Filters: map[string]any{"ids": []string{productID}}, Limit: 1,
	})
	if err != nil {
		return nil, err
	}
	if len(products) == 0 {
		return nil, errors.NotFound(CodeNotReady, "product %s could not be read", productID)
	}
	productTitle, _ := products[0]["title"].(string)
	handle, _ := products[0]["handle"].(string)

	return map[string]string{
		DataVariantID: variantID, DataVariantTitle: variantTitle,
		DataProductID: productID, DataProductTitle: productTitle, DataHandle: handle,
	}, nil
}

// channelKey tells channel sets apart, the absence of a set included: a mark
// set with no channel reads the whole catalog, and one set with none reads
// nothing.
func channelKey(channels []string) string {
	if channels == nil {
		return "\x00none"
	}
	sorted := slices.Clone(channels)
	slices.Sort(sorted)

	return strings.Join(sorted, ",")
}

// FromContainer resolves the flow's dependencies; the stock alert job is built
// from it.
func FromContainer(c *container.Container, log *slog.Logger) (*Workflow, error) {
	if c == nil {
		return nil, errors.Internal(CodeNotReady, "the stock alert flow cannot be wired without a container")
	}
	customers, err := resolve[Customers](c, ServiceCustomers)
	if err != nil {
		return nil, err
	}
	catalog, err := resolve[Catalog](c, ServiceProducts)
	if err != nil {
		return nil, err
	}
	notifier, err := resolve[Notifier](c, ServiceNotifications)
	if err != nil {
		return nil, err
	}
	reader, err := resolve[Reader](c, ServiceQuery)
	if err != nil {
		return nil, err
	}
	// The quote is the cart workflow's, built from the same container, so the
	// price a mark is judged by is the one the customer's cart would charge.
	prices, err := cartwf.FromContainer(c)
	if err != nil {
		return nil, err
	}

	return New(customers, catalog, prices, notifier, reader, log), nil
}

// resolve reads one service and says which name failed.
func resolve[T any](c *container.Container, name string) (T, error) {
	value, err := container.Resolve[T](c, name)
	if err != nil {
		var zero T

		return zero, errors.Wrap(err, errors.KindOf(err), CodeNotReady,
			"the stock alert flow needs %q and it could not be resolved", name)
	}

	return value, nil
}
