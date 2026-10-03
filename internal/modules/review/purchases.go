package review

import (
	"context"
	"log/slog"
	"sync"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// The names this module reads across a boundary. They belong to the core and
// to the order and product modules, which this module does not import.
const (
	// svcQuery is the core's cross-module read layer.
	svcQuery = "core.query"
	// orderInteropName is the order module's primitive surface.
	orderInteropName = "order.interop"
	// entityVariant and filterProductID are the catalog's variants, read by
	// the product they belong to.
	entityVariant   = "variant"
	filterProductID = "product_id"
)

// codePurchaseReadFailed answers a review whose writer's purchase could not be
// read.
const codePurchaseReadFailed = "review_purchase_read_failed"

// OrderPurchases is the order module's answer this module reads (ADR 0372).
type OrderPurchases interface {
	CustomerBoughtAnyOf(ctx context.Context, customerID string, variantIDs []string) (bool, error)
}

// purchases answers whether a proven customer bought the product a review is
// about (ADR 0372): the product's variants from the catalog through the read
// layer, then the customer's orders through the order module's surface, which
// carries no product on its lines.
//
// Both are resolved on first use: the order module may be registered after
// this one. A composition without either writes reviews unverified rather
// than refusing them, and says so once.
type purchases struct {
	c   *container.Container
	log *slog.Logger

	once    sync.Once
	catalog query.Query
	orders  OrderPurchases
	err     error
}

// Bought reports whether the customer has an order that was not canceled with
// a line of one of the product's variants.
func (p *purchases) Bought(ctx context.Context, customerID, productID string) (bool, error) {
	p.once.Do(func() { p.resolve(ctx) })
	if p.err != nil || p.catalog == nil || p.orders == nil {
		return false, p.err
	}

	records, err := p.catalog.Graph(ctx, query.GraphSpec{
		Entity:  entityVariant,
		Fields:  []string{query.IDField},
		Filters: map[string]any{filterProductID: productID},
	})
	if err != nil {
		return false, errors.Wrap(err, errors.KindOf(err), codePurchaseReadFailed,
			"the variants of product %s could not be read", productID)
	}
	variantIDs := make([]string, 0, len(records))
	for _, record := range records {
		if id, _ := record[query.IDField].(string); id != "" {
			variantIDs = append(variantIDs, id)
		}
	}
	if len(variantIDs) == 0 {
		return false, nil
	}

	bought, err := p.orders.CustomerBoughtAnyOf(ctx, customerID, variantIDs)
	if err != nil {
		return false, errors.Wrap(err, errors.KindOf(err), codePurchaseReadFailed,
			"whether the customer bought product %s could not be read", productID)
	}

	return bought, nil
}

// resolve looks the read layer and the order module's surface up and
// remembers the outcome: both found; one of them not installed, which writes
// every review unverified and warns once; or one registered under the wrong
// type, a wiring fault answered as Internal.
func (p *purchases) resolve(ctx context.Context) {
	catalog, err := container.Resolve[query.Query](p.c, svcQuery)
	if err != nil {
		p.absent(ctx, err, svcQuery)

		return
	}
	orders, err := container.Resolve[OrderPurchases](p.c, orderInteropName)
	if err != nil {
		p.absent(ctx, err, orderInteropName)

		return
	}
	p.catalog, p.orders = catalog, orders
}

// absent records why a name could not be used.
func (p *purchases) absent(ctx context.Context, err error, name string) {
	if errors.IsNotFound(err) {
		p.log.WarnContext(ctx, "a review cannot be marked a verified purchase here; every review is written unverified",
			slog.String("missing", name))

		return
	}
	p.err = errors.Wrap(err, errors.KindInternal, codeSetupFailed,
		"the %s module could not resolve %q", ModuleName, name)
}
