package cart

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
)

// attrRegionID is the name of the attribute that carries the region in pricing's
// rule context.
//
// The name MUST be EXACTLY the same as the name that appears in the pricing
// module's rule records: if the field the rule looks at is not in the context, the
// rule does not match and the region-specific price would be silently eliminated
// and the base price chosen instead (see pricing matchRule).
const attrRegionID = "region_id"

// priceSetsFor resolves the price sets of the given variants with a SINGLE link
// query.
//
// The query is done in bulk: a separate call per line means ten round trips on a
// ten-line cart, and N+1 is the road plan Section 5.3 explicitly closes.
//
// # A variant with no price set is REFUSED
//
// The decision is errors.Invalid. A variant with no price set has a price in no
// currency; letting it into the cart means opening a line whose unit price is ZERO,
// and a zero-amount line silently makes the cart cheaper — this silent loss of money
// is exactly what the cart module's totals contract (the coverage requirement) tries
// to close. The error is not NotFound because the variant EXISTS; what is missing is
// that it is sellable, and the caller can fix the request (pick another variant).
//
// # More than one set
//
// The "product_variant_price_set" definition is OneToOne and the database index
// makes a second binding impossible. If more than one set is nevertheless seen,
// which one is to be priced is undefined; silently picking the first would tie the
// price to an ordering accident. This is why the situation is reported with
// errors.Internal: the data has gone bad behind the constraint.
func (w *Workflows) priceSetsFor(ctx context.Context, variantIDs []string) (map[string]string, error) {
	if len(variantIDs) == 0 {
		return map[string]string{}, nil
	}

	linked, err := w.links.ListMany(ctx, LinkVariantPriceSet, variantIDs)
	if err != nil {
		// An infrastructure failure is not reported like a BUSINESS state:
		// CodeVariantNotPriced means "this product has no price" and the client
		// branches on it. A transient database outage reaching the storefront
		// as a permanent "product without a price" message would be
		// indistinguishable from a genuinely unpriced variant. The underlying
		// error's kind is PRESERVED, its code is not rewritten.
		return nil, errors.Wrap(err, errors.KindOf(err), CodeLinkReadFailed,
			"could not read the %q link (%d variants)", LinkVariantPriceSet, len(variantIDs))
	}

	out := make(map[string]string, len(variantIDs))
	for _, variantID := range variantIDs {
		sets := linked[variantID]
		switch len(sets) {
		case 0:
			return nil, errors.Invalid(CodeVariantNotPriced,
				"variant %s has no price; a product without a price cannot enter the cart", variantID)
		case 1:
			out[variantID] = sets[0]
		default:
			return nil, errors.Internal(CodeVariantPriceSetAmbiguous,
				"variant %s appears to be bound to %d price sets; the %q definition must be singular",
				variantID, len(sets), LinkVariantPriceSet)
		}
	}
	return out, nil
}

// variantTitle reads the variant's title in the catalog from the Query layer.
//
// # Why the title is read from the catalog
//
// The cart line's title is COPIED from the variant (see LineItem in the cart
// module): even if the catalog changes later, the name seen in the cart does not.
// The only party that can copy it is this flow — the cart module does not know
// product.
//
// Taking the title from the caller would be cheaper but would cost two things: the
// free text the storefront sends would enter the cart as is, and the variant's REAL
// existence would be verified nowhere. The second is not theoretical: the product
// module cleans up a deleted variant's price/stock links on a BEST EFFORT basis and
// when it cannot clean them it only logs a warning, which means a deleted variant
// could enter the cart through an orphaned price link.
//
// The read goes through Query because the product service's read signatures speak in
// its own model types and are closed to cross-module calls; Query exists for exactly
// this gap (ADR 0004).
//
// # The read is scoped to the request's SALES CHANNELS
//
// The query carries the channels coming from the request's authenticated identity as
// a filter (see saleschannel.go). This is the ONE door through which a line enters
// the cart, and the scope rule is applied on the write path here: for a variant out
// of scope the catalog returns no record at all.
//
// # An out-of-scope variant returns "NOT FOUND"
//
// The error kind and its CODE are EXACTLY the same as those of a variant that is not
// in the catalog at all; even the message is the same. A distinguishable error (say
// "forbidden") would pierce the concealment itself: a competitor arriving with any
// publishable key they happen to hold could learn which variant IDs are sold on
// ANOTHER channel by trying them one by one. The read surface makes the same
// decision and writes the same rationale (see productsvc.Service.GetStoreProduct);
// the two surfaces giving the SAME answer is what says the scope is a single rule.
func (w *Workflows) variantTitle(ctx context.Context, variantID string) (string, error) {
	filters := map[string]any{query.IDField: variantID}
	if channels, apply := salesChannelFilter(ctx); apply {
		filters[FilterSalesChannelIDs] = channels
	}

	records, err := w.catalog.Graph(ctx, query.GraphSpec{
		Entity:  EntityVariant,
		Fields:  []string{query.IDField, FieldTitle},
		Filters: filters,
		Limit:   1,
	})
	if err != nil {
		// Same rationale: a read failure IS NOT "the variant is not in the
		// catalog" (see priceSetsFor).
		return "", errors.Wrap(err, errors.KindOf(err), CodeCatalogReadFailed,
			"could not read variant %s from the catalog", variantID)
	}
	if len(records) == 0 {
		return "", errors.NotFound(CodeVariantUnknown,
			"variant %s is not in the catalog", variantID)
	}

	title, ok := records[0][FieldTitle].(string)
	if !ok || title == "" {
		return "", errors.Internal(CodeVariantUnknown,
			"could not read the title of variant %s (field %q: %v)",
			variantID, FieldTitle, records[0][FieldTitle])
	}
	return title, nil
}
