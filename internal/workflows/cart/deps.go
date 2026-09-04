package cart

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
)

// Service names in the container (ADR 0006). Concrete types are resolved by
// these names; none of them is known at compile time.
const (
	// ServiceCart is the cart module's service.
	ServiceCart = "cart.interop"
	// ServicePricing is the pricing module's service.
	ServicePricing = "pricing.service"
	// ServiceRegion is the region module's service.
	ServiceRegion = "region.service"
	// ServiceCustomer is the customer module's service.
	ServiceCustomer = "customer.service"
	// ServicePromotion is the promotion module's cross-module surface.
	//
	// IT IS OPTIONAL: if it is not registered the discount stays zero (see
	// [Discounts]).
	ServicePromotion = "promotion.interop"
	// ServiceTax is the tax module's cross-module surface.
	//
	// IT IS OPTIONAL: if it is not registered the tax is computed with the
	// region's rate (see [Taxes]).
	ServiceTax = "tax.interop"
	// ServiceLink is the core's Module Links service.
	ServiceLink = "core.link"
	// ServiceQuery is the core's cross-module read layer.
	ServiceQuery = "core.query"
)

// Cross-module CONTRACT constants.
//
// The values are defined in the product module as well and are REPEATED here:
// this package cannot import that module (ADR 0006) and the repetition is the
// accepted price of isolation (ADR 0001). A typo does not stay silent — if the
// link name is wrong core/link returns errors.NotFound, if the entity name is
// wrong Query returns errors.NotFound.
const (
	// LinkVariantPriceSet is the name of the link that binds a variant to its
	// price set in the pricing module; the product module declares its definition.
	LinkVariantPriceSet = "product_variant_price_set"
	// EntityVariant is the entity name of variants in the Query layer.
	EntityVariant = "variant"
	// FieldTitle is the name of the field that holds the title in a variant record.
	FieldTitle = "title"
	// FilterSalesChannelIDs is the sales channel filter key of the variant query;
	// the product module declares its definition (productsvc.FilterSalesChannelIDs).
	//
	// What it means and why it is routed through this flow are in saleschannel.go.
	// Repeating the name here is the price of isolation; if the two drift apart
	// the provider DOES NOT RECOGNIZE the filter and adding a line fails with
	// errors.Invalid — not silent, but an arch test still ties the two names
	// together.
	FilterSalesChannelIDs = "sales_channel_ids"
	// EntityRegion is the entity name of regions in the Query layer; the region
	// module declares its definition.
	EntityRegion = "region"
	// FieldCountries is the name of the field that holds the country sub-records
	// in a region record.
	FieldCountries = "countries"
	// FieldCode is the name of the field that holds the ISO 3166-1 alpha-2 code in
	// a country sub-record.
	FieldCode = "code"
)

// Error codes. Clients may branch on them; the messages can change, the codes
// cannot.
const (
	// CodeInvalidInput reports that the input did not pass validation.
	CodeInvalidInput = "cart_workflow_invalid_input"
	// CodeNotReady reports that the workflows were wired with a missing
	// dependency.
	CodeNotReady = "cart_workflow_not_ready"
	// CodeDependencyMissing reports that a service could not be resolved in the
	// container.
	CodeDependencyMissing = "cart_workflow_dependency_missing"
	// CodeVariantUnknown reports that a variant that is not in the catalog was
	// referenced.
	CodeVariantUnknown = "cart_workflow_variant_unknown"
	// CodeVariantNotPriced reports that the variant is bound to no price set at
	// all.
	CodeVariantNotPriced = "cart_workflow_variant_not_priced"
	// CodeLinkReadFailed reports that the link layer COULD NOT BE READ.
	//
	// It is separate from the business codes: "it has no price" and "we could not
	// learn whether it has a price" are different situations and the client
	// behaves differently towards the two (one is permanent, the other
	// retryable).
	CodeLinkReadFailed = "cart_workflow_link_read_failed"
	// CodeCatalogReadFailed reports that the catalog read failed; it DOES NOT
	// mean that the variant does not exist.
	CodeCatalogReadFailed = "cart_workflow_catalog_read_failed"
	// CodeVariantPriceSetAmbiguous reports that the variant appears to be bound to
	// more than one price set.
	CodeVariantPriceSetAmbiguous = "cart_workflow_variant_price_set_ambiguous"
	// CodePriceUnavailable reports that the variant has no price in the cart's
	// currency.
	CodePriceUnavailable = "cart_workflow_price_unavailable"
	// CodePriceResponseInvalid says that the pricing module reported a bulk price
	// response outside the contract.
	//
	// It is SEPARATE from "no price": this code says that the response could not
	// be read or did not line up with the request, and no line's amount is
	// trustworthy.
	CodePriceResponseInvalid = "cart_workflow_price_response_invalid"
	// CodeCartLineLimit reports that the cart reached its line count ceiling.
	CodeCartLineLimit = "cart_workflow_line_limit_reached"
	// CodeCartCompleted reports that a computation was requested on a completed
	// cart.
	CodeCartCompleted = "cart_workflow_cart_completed"
	// CodeSnapshotInvalid reports that the cart snapshot could not be read.
	CodeSnapshotInvalid = "cart_workflow_snapshot_invalid"
	// CodeTaxRateInvalid reports that the region reported a tax rate outside the
	// contract.
	CodeTaxRateInvalid = "cart_workflow_tax_rate_invalid"
	// CodeAmountOverflow reports that an amount exceeded the permitted range.
	CodeAmountOverflow = "cart_workflow_amount_overflow"
	// CodeTotalsConflict reports that the cart changed too often for the totals to
	// be written.
	CodeTotalsConflict = "cart_workflow_totals_conflict"
	// CodeTotalsAfterChange reports that the line WAS WRITTEN but the totals could
	// not be computed.
	CodeTotalsAfterChange = "cart_workflow_totals_after_change_failed"
	// CodeDiscountFailed reports that the discount computation FAILED.
	//
	// It is separate from "no discount": a discount of zero is a normal outcome,
	// while this code says that the computation could not be performed at all.
	CodeDiscountFailed = "cart_workflow_discount_failed"
	// CodeDiscountInvalid says that the promotion module reported a discount
	// result outside the contract.
	CodeDiscountInvalid = "cart_workflow_discount_invalid"
	// CodeTaxFailed reports that the tax computation FAILED.
	CodeTaxFailed = "cart_workflow_tax_failed"
	// CodeTaxInvalid says that the tax module reported a computation result
	// outside the contract.
	CodeTaxInvalid = "cart_workflow_tax_invalid"
	// CodeRegionReadFailed reports that the region record COULD NOT BE READ from
	// the Query layer; it DOES NOT mean that the region does not exist.
	CodeRegionReadFailed = "cart_workflow_region_read_failed"
)

// codeServiceNotFound is the container's "this name is not registered" error
// code.
//
// The code IS REPEATED HERE because its counterpart in core/container is
// unexported and an error code is the only bond that travels across packages
// (see core/errors: "the code is part of the contract"). If its value changes
// [FromContainer] silently becomes LESS forgiving — the workflows cannot be
// wired at all while promotion/tax are unregistered, so the failure is seen at
// startup and loudly. The opposite direction (silently becoming more
// permissive) would have been unacceptable.
const codeServiceNotFound = "container_service_not_found"

// Carts is the surface of the cart module ("cart.interop") that this package
// uses.
//
// # Why primitive signatures and JSON
//
// This package CANNOT import the cart module (ADR 0006), so it cannot NAME a
// type such as models.Cart in its signatures; the moment it names one, that
// type becomes this package's own and the concrete service does not satisfy it
// structurally. That is why the signatures use only primitive and stdlib types
// — the same pattern as the interop surfaces in the region, pricing and
// customer modules.
//
// Two methods carry structural data (the instantaneous shape of the cart and
// the computed totals) and pass the boundary as json.RawMessage. The
// alternatives were worse: a separate method per field makes it impossible to
// read the cart at a single consistent instant (the lines are read in one call,
// the revision in another one and the cart changes in between); parallel slices
// (ids, amounts, taxes …) can silently slip out of alignment at the call site.
// JSON is the ONLY structural form both sides can name, and if the module later
// moves out into a separate service the contract stays exactly as it is. The
// schema is defined in one place, in the [Snapshot] and [Totals] types.
//
// # Name collision warning
//
// The method names are deliberately DIFFERENT from their counterparts in the
// cart service (OpenCart, AddCartLineItem …): had they carried the same names
// the module could never have published this surface, because a single type
// cannot carry two different signatures at the same time. The only exception is
// RemoveLineItem; cart's existing signature already matches it exactly.
type Carts interface {
	// OpenCart opens a new cart and returns ITS ID.
	//
	// If customerID is empty the cart belongs to a GUEST; email may be left
	// empty. Its counterpart in the cart service is CreateCart.
	//
	// metadata is the free-form data (a JSON object) the caller attaches to the
	// CART and may be left empty. The rationale is the same as the metadata
	// paragraph of [Carts.AddCartLineItem]: the field is the storefront's intent,
	// this package DOES NOT READ it and it enters no computation — but since this
	// flow is the only way to open a cart, carrying it is mandatory; if it were
	// not carried the field the client sent would silently be dropped.
	OpenCart(
		ctx context.Context,
		regionID, currencyCode, customerID, email string,
		metadata json.RawMessage,
	) (cartID string, err error)

	// CartSnapshotJSON returns the shape of the cart that enters the computation
	// in a SINGLE read.
	//
	// The body is in the [Snapshot] schema. If the cart does not exist,
	// errors.NotFound. Its counterpart in the cart service is GetCart.
	CartSnapshotJSON(ctx context.Context, cartID string) (json.RawMessage, error)

	// AddCartLineItem adds a line to the cart and returns THE LINE'S ID.
	//
	// If the same variant is already in the cart no new line is opened, the
	// quantity of the existing line is increased and that line's id is returned.
	// Its counterpart in the cart service is AddLineItem.
	//
	// metadata is the FREE-FORM data (a JSON object) the caller attaches to the
	// line and may be left empty. This package DOES NOT READ it, it only carries
	// it: the field is the storefront's intent (a gift note, personalization) and
	// enters no step of the computation. Carrying it is mandatory nevertheless,
	// because this flow is the only way to open a line; if it were not carried the
	// field the client sent would silently be dropped, and a "setting believed to
	// have been sent but never applied" is exactly the reason why this API rejects
	// the fields it does not recognize.
	AddCartLineItem(
		ctx context.Context,
		cartID, variantID, title string,
		quantity, unitPrice int64,
		metadata json.RawMessage,
	) (lineItemID string, err error)

	// SetCartLineItemQuantity writes the line's quantity as an ABSOLUTE value; the
	// quantity must be positive. Its counterpart in the cart service is
	// UpdateLineItemQuantity.
	SetCartLineItemQuantity(ctx context.Context, cartID, lineItemID string, quantity int64) error

	// RemoveLineItem removes the line from the cart. If the line does not exist,
	// errors.NotFound.
	RemoveLineItem(ctx context.Context, cartID, lineItemID string) error

	// SetCartTotalsJSON writes the computed totals to the cart.
	//
	// The body is in the [Totals] schema and must cover ALL the lines of the cart.
	// If the declared revision does not match the cart's current shape it returns
	// errors.Conflict. Its counterpart in the cart service is SetTotals.
	SetCartTotalsJSON(ctx context.Context, cartID string, totals json.RawMessage) error
}

// Prices is the surface of the pricing module ("pricing.service") that this
// package uses.
//
// The two methods run the SAME selection rule and their separation is a cost
// argument: the singular one asks for a SINGLE price (while a line is being
// opened), the bulk one asks for ALL the lines of a cart in a single round (in
// the totals round). The measurement and why the two paths pick the same amount
// are in the [Workflows.unitPrices] godoc.
type Prices interface {
	// CalculateAmount returns the UNIT amount of a price set in the given context
	// as a minor unit. If there is no applicable price, errors.NotFound.
	CalculateAmount(
		ctx context.Context,
		priceSetID, currencyCode string,
		quantity int32,
		attributes map[string]string,
	) (int64, error)

	// CalculateAmountsJSON returns the unit amounts of several containers in a
	// SINGLE round.
	//
	// The schema of the request and response bodies is defined in one place, in
	// the [priceRequest] and [priceResponse] types. The response must arrive in
	// the SAME ORDER and at the SAME LENGTH as the request; an item with no price
	// is not an error but a result reported with a flag.
	CalculateAmountsJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
}

// Regions is the surface of the region module ("region.service") that this
// package uses.
type Regions interface {
	// RegionIDForCountry returns the region id for a country code; if there is no
	// region, errors.NotFound.
	RegionIDForCountry(ctx context.Context, countryCode string) (string, error)
	// RegionCurrency returns the region's currency code and its number of decimal
	// digits.
	RegionCurrency(ctx context.Context, regionID string) (code string, decimalDigits int32, err error)
	// RegionTax returns the region's basis point tax rate and whether the tax is
	// applied automatically.
	RegionTax(ctx context.Context, regionID string) (rateBps int32, automatic bool, err error)
}

// Customers is the surface of the customer module ("customer.service") that
// this package uses.
type Customers interface {
	// CustomerEmail returns the customer's email address; if the customer does not
	// exist, errors.NotFound.
	CustomerEmail(ctx context.Context, customerID string) (string, error)
}

// Discounts is the surface of the promotion module ("promotion.interop") that
// this package uses.
//
// The surface has a SINGLE method. promotion's interop publishes three methods
// (ComputeDiscountsJSON, RedeemPromotion, ReleasePromotion) but the cart
// computation uses only the first one: the computation IS SIDE-EFFECT FREE and
// actually spending a coupon is the order's job (see the promotion package
// comment, "computation and redemption are SEPARATE"). Writing the two unused
// methods here would have meant this package owning a contract it does not need
// and its fakes growing needlessly.
//
// The schema of the request and response bodies is defined in one place, in the
// [discountRequest] and [discountResponse] types.
type Discounts interface {
	// ComputeDiscountsJSON computes the discounts for the cart context; IT WRITES
	// NOTHING and consumes no coupon counter.
	ComputeDiscountsJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
}

// Taxes is the surface of the tax module ("tax.interop") that this package
// uses.
//
// The surface has a SINGLE method. tax's interop publishes RateForCountry as
// well and that one is the exact counterpart of region's temporary RegionTax
// method — but the cart computation asks for tax PER ITEM (an invoice must be
// explainable line by line and different rates per line may arrive depending on
// the product class), and a single flat rate cannot give that. That is why the
// computation is always done with [Taxes.CalculateTaxJSON].
//
// The schema of the request and response bodies is defined in one place, in the
// [taxRequest] and [taxResponse] types.
type Taxes interface {
	// CalculateTaxJSON computes the tax for the given country and items.
	CalculateTaxJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
}

// Links is the surface of the core's Module Links service ("core.link") that
// this package uses.
//
// There is only a BATCH read: the same path is used for a single row too, and
// so the number of queries does not change as the number of lines grows (there
// is no N+1).
type Links interface {
	// ListMany returns the links of the given source ids in a SINGLE query.
	ListMany(ctx context.Context, name string, fromIDs []string) (map[string][]string, error)
}

// Catalog is the surface of the core's Query layer ("core.query") that this
// package uses (ADR 0004).
//
// The catalog data (the variant's title) is read from here: the product
// module's service speaks in rich types and is therefore closed to cross-module
// calls, while Query exists for exactly that gap.
type Catalog interface {
	// Graph pulls the root records according to the spec and applies the
	// expansions.
	Graph(ctx context.Context, spec query.GraphSpec) ([]query.Record, error)
}

// Deps holds the dependencies of the workflows.
type Deps struct {
	// Carts is the cart surface; it is mandatory.
	Carts Carts
	// Prices is the pricing surface; it is mandatory.
	Prices Prices
	// Regions is the region surface; it is mandatory.
	Regions Regions
	// Customers is the customer surface; it is mandatory.
	//
	// It is called only for a REGISTERED customer's cart; the guest flow never
	// touches this surface. It is mandatory nevertheless: its absence is a wiring
	// error that blows up on the first request of a registered customer's cart,
	// and that error must be seen at startup.
	Customers Customers
	// Discounts is the promotion surface; IT IS OPTIONAL.
	//
	// If it is nil the discount stays zero and the storefront keeps working; the
	// rationale is in the [Workflows.applyDiscounts] godoc.
	Discounts Discounts
	// Taxes is the tax surface; IT IS OPTIONAL.
	//
	// If it is nil the tax is computed with the region's rate (the Phase 5 path)
	// and the source used IS VISIBLE in the [Totals.TaxSource] field; the
	// rationale is in the [Workflows.applyTaxes] godoc.
	Taxes Taxes
	// Links is the Module Links surface; it is mandatory.
	Links Links
	// Catalog is the Query surface; it is mandatory.
	Catalog Catalog
	// Logger: if nil is given the logs are discarded.
	Logger *slog.Logger
}

// Workflows is the type that runs the cart workflows. It is safe for concurrent
// use.
type Workflows struct {
	carts     Carts
	prices    Prices
	regions   Regions
	customers Customers
	discounts Discounts
	taxes     Taxes
	links     Links
	catalog   Catalog
	log       *slog.Logger
}

// New wires the workflows with the given dependencies.
//
// A missing MANDATORY dependency returns an error at WIRING time; no nil check
// is done at run time. Leaving the absence to the first call would have meant a
// wrongly wired setup blowing up only in a customer's cart.
//
// [Deps.Discounts] and [Deps.Taxes] are OUTSIDE this rule and may be left nil:
// each of them carries a DEGRADATION path along which the workflow keeps
// working when its own module is not present in the setup, and that path is
// selected by a nil check. Had they been counted mandatory, in a deployment
// that does not install the promotion or the tax module the cart workflows
// could not have been wired at all — modularity itself would have been lost.
func New(deps Deps) (*Workflows, error) {
	for _, dep := range []struct {
		name    string
		missing bool
	}{
		{ServiceCart, deps.Carts == nil},
		{ServicePricing, deps.Prices == nil},
		{ServiceRegion, deps.Regions == nil},
		{ServiceCustomer, deps.Customers == nil},
		{ServiceLink, deps.Links == nil},
		{ServiceQuery, deps.Catalog == nil},
	} {
		if dep.missing {
			return nil, errors.Internal(CodeNotReady,
				"the cart workflows cannot be wired without the %q surface", dep.name)
		}
	}

	log := deps.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Workflows{
		carts:     deps.Carts,
		prices:    deps.Prices,
		regions:   deps.Regions,
		customers: deps.Customers,
		discounts: deps.Discounts,
		taxes:     deps.Taxes,
		links:     deps.Links,
		catalog:   deps.Catalog,
		log:       log,
	}, nil
}

// FromContainer resolves the dependencies from the container BY NAME and wires
// the workflows (ADR 0006).
//
// The resolution order IS FIXED by registration name: if more than one service
// is missing or mismatched, the same error is returned on every run and the
// diagnosis becomes reproducible. The mismatch error writes both the registered
// concrete type and the expected interface, and for an interface the missing
// methods (see container.Resolve).
//
// # Two names ARE OPTIONAL
//
// If [ServicePromotion] and [ServiceTax] ARE NOT REGISTERED the workflows are
// still wired and the relevant surface stays nil (see [resolveOptional]). If
// one is registered but DOES NOT SATISFY the surface the wiring fails
// nevertheless: that is a wiring error, and degrading silently would have kept
// a wrongly registered module invisible forever.
func FromContainer(c *container.Container) (*Workflows, error) {
	if c == nil {
		return nil, errors.Internal(CodeNotReady, "the cart workflows cannot be wired without a container")
	}

	carts, err := resolve[Carts](c, ServiceCart)
	if err != nil {
		return nil, err
	}
	prices, err := resolve[Prices](c, ServicePricing)
	if err != nil {
		return nil, err
	}
	regions, err := resolve[Regions](c, ServiceRegion)
	if err != nil {
		return nil, err
	}
	customers, err := resolve[Customers](c, ServiceCustomer)
	if err != nil {
		return nil, err
	}
	discounts, err := resolveOptional[Discounts](c, ServicePromotion)
	if err != nil {
		return nil, err
	}
	taxes, err := resolveOptional[Taxes](c, ServiceTax)
	if err != nil {
		return nil, err
	}
	links, err := resolve[Links](c, ServiceLink)
	if err != nil {
		return nil, err
	}
	catalog, err := resolve[Catalog](c, ServiceQuery)
	if err != nil {
		return nil, err
	}

	// The application wires the logger with slog.SetDefault at startup; the
	// workflows do not look for a separate logger registration.
	log := slog.Default().With("workflow", "cart")
	// A missing optional surface is announced ONCE AT WIRING time, not on every
	// totals round: a warning per round produces millions of lines a day, and a
	// warning lost in the noise is as good as one that never happened.
	if discounts == nil {
		log.Warn("the promotion surface is not registered; the cart discount will be computed as ZERO",
			slog.String("servis", ServicePromotion))
	}
	if taxes == nil {
		log.Warn("the tax surface is not registered; the tax will be computed with the region rate",
			slog.String("servis", ServiceTax), slog.String("tax_source", TaxSourceRegion))
	}

	return New(Deps{
		Carts:     carts,
		Prices:    prices,
		Regions:   regions,
		Customers: customers,
		Discounts: discounts,
		Taxes:     taxes,
		Links:     links,
		Catalog:   catalog,
		Logger:    log,
	})
}

// resolveOptional resolves an optional service; if it IS NOT REGISTERED it
// returns the zero value and a nil error.
//
// The degradation is narrowed by the error CODE and not by its KIND, and the
// distinction is deliberate (the same pattern: the product module's storefront
// listing). Looking at the kind (KindNotFound) was too wide: a NotFound that a
// registered lazy constructor produces INSIDE ITSELF passes through that gate
// too, and a real wiring fault would turn into a cart silently running without
// discounts or taxes. A registered but mismatched type DOES NOT return from
// here either; that error passes to the caller.
func resolveOptional[T any](c *container.Container, name string) (T, error) {
	value, err := container.Resolve[T](c, name)
	if err == nil {
		return value, nil
	}

	var zero T
	if errors.CodeOf(err) == codeServiceNotFound {
		return zero, nil
	}
	return zero, errors.Wrap(err, errors.KindOf(err), CodeDependencyMissing,
		"the cart workflows could not resolve the %q service", name)
}

// resolve resolves a single service and wraps its error PRESERVING ITS KIND.
//
// Preserving the kind is a must: an unregistered name must stay NotFound (404)
// and a mismatched type Invalid (422). Turning them all into Internal would
// have made a fixable wiring error look like a server fault.
func resolve[T any](c *container.Container, name string) (T, error) {
	value, err := container.Resolve[T](c, name)
	if err != nil {
		var zero T
		return zero, errors.Wrap(err, errors.KindOf(err), CodeDependencyMissing,
			"the cart workflows could not resolve the %q service", name)
	}
	return value, nil
}
