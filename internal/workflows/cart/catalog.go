package cart

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// attrRegionID is the name of the attribute that carries the region in pricing's
// rule context.
//
// The name MUST be EXACTLY the same as the name that appears in the pricing
// module's rule records: if the field the rule looks at is not in the context, the
// rule does not match and the region-specific price would be silently eliminated
// and the base price chosen instead (see pricing matchRule).
const attrRegionID = "region_id"

// AttrCustomerGroupID is the name of the attribute that carries the customer's
// segment in the rule context, under which [Workflows.priceContext] writes the
// customer's groups twice.
//
// The attribute map holds a SINGLE value: a rule context is one value per
// attribute, and ADR 0049 chose which one, the head of the customer's ordered
// groups, so an `eq vip` rule means "the group we picked is vip". The list map
// holds the whole SET under the same name, where an `any_in` rule reads it, so
// "any of my groups" is expressible there (ADR 0144). It is exported because
// the panel writes promotion rules naming it and spells it by hand, pinned in
// internal/arch (ADR 0321).
const AttrCustomerGroupID = "customer_group_id"

// AttrCustomerID is the name of the attribute that carries the customer's own id
// in the rule context (ADR 0185).
//
// A price ruled on it is that customer's contract price, and the pricing ladder
// ranks a price that names the buyer above one that does not. That makes the
// spelling a contract with pricing, which this package cannot import and which
// cannot import it, so the names are exported on both sides and internal/arch
// binds them.
const AttrCustomerID = "customer_id"

// AttrCompanyID is the name of the attribute that carries the company the
// customer buys for, when the customer is a company's employee (ADR 0185).
//
// A price ruled on it is the company's contract price, for every employee who
// buys for it. The spelling is bound to pricing's the way [AttrCustomerID] is.
const AttrCompanyID = "company_id"

// AttrSalesChannelID is the name of the attribute that carries the sales
// channel the cart was opened in (ADR 0397).
//
// A price or a promotion ruled on it is that channel's: a storefront's own
// price, asked by every cart opened through its key. The spelling is bound to
// pricing's the way [AttrCustomerID] is, since a drift would leave every
// channel price matching no cart.
const AttrSalesChannelID = "sales_channel_id"

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
// a filter (see saleschannel.go). This is the door through which a line is ADDED to
// the cart, and a raise asks it again (ADR 0281), so the scope rule is applied on the
// write path here: for a variant out of scope the catalog returns no record at all.
// A merge moves lines already in a cart and does not come through it.
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

// productIDsFor maps the given variant ids to the products they belong to, in a
// SINGLE catalog read.
//
// # Why the cart needs this at all
//
// A cart line knows its variant. Every tax rule is written about a PRODUCT. So
// without this hop the tax request carries no product at all, and the tax
// module — which has no way to object — falls every line through to the
// region's DEFAULT rate. A basket mixing a 1% book, an 8% food item and a 20%
// electronic is then charged 20% throughout, and nothing in the response says
// so.
//
// # A variant missing from the answer is NOT an error
//
// The map simply has no entry for it, and the caller sends an empty product for
// that line — which is the behavior every line had before this existed. The
// alternative would be to fail a whole checkout because one variant was
// invisible in the current sales channel, and that trades a wrong tax rate for
// no sale at all.
//
// A read FAILURE is a different matter and is returned, for the reason
// [Workflows.priceSetsFor] gives: a fault is not the same fact as an absence.
func (w *Workflows) productIDsFor(ctx context.Context, variantIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(variantIDs))
	if len(variantIDs) == 0 {
		return out, nil
	}

	filters := map[string]any{FilterIDs: variantIDs}
	if channels, apply := salesChannelFilter(ctx); apply {
		filters[FilterSalesChannelIDs] = channels
	}

	records, err := w.catalog.Graph(ctx, query.GraphSpec{
		Entity:  EntityVariant,
		Fields:  []string{query.IDField, FieldProductID},
		Filters: filters,
		Limit:   len(variantIDs),
	})
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeCatalogReadFailed,
			"could not read the products of %d variants from the catalog", len(variantIDs))
	}

	for i := range records {
		variantID, ok := records[i][query.IDField].(string)
		if !ok || variantID == "" {
			continue
		}
		productID, ok := records[i][FieldProductID].(string)
		if !ok || productID == "" {
			continue
		}
		out[variantID] = productID
	}

	return out, nil
}

// priceSubject is what a cart's PRICE is chosen for: its region, the sales
// channel it was opened in and its customer.
//
// It is a type of its own, and not the [Snapshot], so that the cart's metadata
// has no field to arrive in: whoever holds the storefront's publishable key
// writes that bag, so a price ruled on it would be a price the caller chooses
// (ADR 0403). Its fields are named because three strings in a row are three
// places to swap the buyer for the channel with nothing failing to compile.
type priceSubject struct {
	RegionID       string
	SalesChannelID string
	CustomerID     string
}

// priceSubjectOf is the subject a cart's own rounds price for: the line's
// opening price, the totals and the discount all build it here, so none of
// them can name a different buyer or channel.
func priceSubjectOf(snap Snapshot) priceSubject {
	return priceSubject{RegionID: snap.RegionID, SalesChannelID: snap.SalesChannelID, CustomerID: snap.CustomerID}
}

// priceContext builds the attribute map a cart's PRICE is chosen with: the
// region, the sales channel the cart was opened in, the customer, their company
// and their head group.
//
// Every round calls this: the line, the totals, the discount, the wishlist
// quote and both trials. A cart's metadata reaches none of them (ADR 0403,
// ADR 0407).
//
// # Why the group is resolved HERE and not at each call site
//
// Every price and discount round builds this context, and ADR 0049's decision
// is that they all send the SAME single group. A copy of "take the head of the
// slice" per caller is a place for each to drift apart, and the drift would be
// invisible: each engine would simply price a different segment, with no error
// anywhere.
//
// # Why a guest OMITS the attribute rather than sending an empty one
//
// A cart with no customer has no segment, and the elimination rule already knows
// what to do with a missing attribute: a rule naming an attribute the context
// does not carry does not match, so a segment price is ELIMINATED. Sending an
// empty string would instead ask every rule whether it lists "", which is a
// value a merchant could accidentally configure. The default direction is that a
// segment price stays CLOSED rather than opening to everybody.
//
// # Why the customer and the company go in by id
//
// A contract price is written for one buyer: a customer, or a company whose
// employees all buy at it (ADR 0185). The customer's id is on the cart, and the
// company comes from the b2b module, which answers "" for somebody who is no
// company's employee — and then the attribute is omitted, for the reason a guest
// omits the group below. Without the b2b surface the company is never there, and
// a company price never matches.
//
// # Why the sales channel is the CART's and not the request's
//
// The subject's channel is the one the cart recorded when it was opened
// (ADR 0397), and never the principal of the request computing the round.
// Every write reprices the whole cart under whichever principal made it — a
// storefront key, an operator's asserted channel, an operator's address write
// carrying none — so a channel read off the request would price one cart at
// two prices depending on who touched it last, and the completion's
// `expected_total` would compare against a figure a different write produced.
// A cart that names none omits the attribute, for the guest's reason above:
// a channel price stays closed rather than matching "". The quote and the list
// trial name none, since neither has a cart.
//
// # Why a failure to read the groups or the company is NOT fatal
//
// A cart total that cannot be computed because the customer or b2b module is
// briefly unavailable is worse than a cart total computed without what could not
// be read: the first stops the shop, the second charges the ordinary price. The
// error is returned so the caller can log it, and the context comes back with
// everything that WAS read.
func (w *Workflows) priceContext(
	ctx context.Context, subject priceSubject,
) (attributes map[string]string, lists map[string][]string, err error) {
	attributes = map[string]string{attrRegionID: subject.RegionID}
	if subject.SalesChannelID != "" {
		attributes[AttrSalesChannelID] = subject.SalesChannelID
	}

	customerID := subject.CustomerID
	if customerID == "" {
		return attributes, nil, nil
	}
	attributes[AttrCustomerID] = customerID

	var companyErr error
	if w.companies != nil {
		var company string
		company, companyErr = w.companies.CompanyOfCustomer(ctx, customerID)
		if companyErr == nil && company != "" {
			attributes[AttrCompanyID] = company
		}
	}
	if w.customers == nil {
		return attributes, nil, companyErr
	}

	groups, groupErr := w.customers.CustomerGroupIDs(ctx, customerID)
	if groupErr != nil {
		return attributes, nil, errors.Join(companyErr, groupErr)
	}
	if len(groups) == 0 {
		return attributes, nil, companyErr
	}

	// The groups go out TWICE, and the two answer different questions.
	//
	// The HEAD goes into the single-valued context, because the surface promises
	// rank order (ADR 0049) and because every rule shipped before this read it
	// there: an `eq vip` rule has to keep meaning "the group we picked is vip".
	//
	// ALL of them go into the list, where only the operator that reads a list looks.
	// Without it a customer in {retail, vip} whose head is retail did not match a
	// rule written for vip — a segment discount silently not applying to somebody
	// who IS in the segment (ADR 0144).
	attributes[AttrCustomerGroupID] = groups[0]
	lists = map[string][]string{AttrCustomerGroupID: groups}

	return attributes, lists, companyErr
}

// CartAttributePrefix is the attribute space ADR 0111 filled from a cart's
// metadata. No context carries it: the bag reaches no price (ADR 0403) and no
// promotion (ADR 0407), since whoever holds the storefront's publishable key
// writes it. Pricing and promotion refuse a new rule naming it, and one written
// before matches no cart, under spellings internal/arch binds to this one. It
// stays reserved so that no later name wakes those rules.
const CartAttributePrefix = "cart."
