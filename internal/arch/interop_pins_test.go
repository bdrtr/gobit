package arch_test

// This file hands the COMPILER the comparison nothing else in this repository
// makes.
//
// # What was wrong
//
// A consumer declares the narrow interface it needs in its OWN package and
// resolves the concrete value from the container by NAME (Principle 2.1/2.4, ADR
// 0001). The producer never imports the consumer and the consumer never imports
// the producer, so a signature drifting apart compiles on both sides. The producer
// of one such surface says so in its own godoc — and says something false with it:
// "the compiler never sees them together".
//
// It does, from here. Neither module may import the other, but a THIRD package in
// the same Go module may import both, and an assignment then costs the compiler one
// check and nobody a test run. That sentence being false is why nobody wrote this
// file, and why `POST /store/v1/carts/{id}/promotions` shipped resolving an
// interface the registered value did not implement (gap D73).
//
// # Why it lives in internal/arch rather than in internal/e2e
//
// internal/e2e already imports the whole tree, and it is Docker-gated: a pin there
// would be checked only in the integration lane. These are compile-time facts and
// they belong in the lane that compiles, which is every lane.
//
// # What a failure here means
//
// `go build` fails with "does not implement". That is the whole gate: the assignment
// is the assertion, there is no test body to run, and a drift cannot reach a lane
// that runs tests because it cannot reach a lane that compiles them.

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	corehttp "github.com/bdrtr/gobit/core/http"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
	b2bsvc "github.com/bdrtr/gobit/internal/modules/b2b/service"
	cartapi "github.com/bdrtr/gobit/internal/modules/cart/api"
	cartsvc "github.com/bdrtr/gobit/internal/modules/cart/service"
	customersvc "github.com/bdrtr/gobit/internal/modules/customer/service"
	filesvc "github.com/bdrtr/gobit/internal/modules/file/service"
	fulfillsvc "github.com/bdrtr/gobit/internal/modules/fulfillment/service"
	inventorysvc "github.com/bdrtr/gobit/internal/modules/inventory/service"
	invoicesvc "github.com/bdrtr/gobit/internal/modules/invoice/service"
	notifsvc "github.com/bdrtr/gobit/internal/modules/notification/service"
	orderapi "github.com/bdrtr/gobit/internal/modules/order/api"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
	promotionsvc "github.com/bdrtr/gobit/internal/modules/promotion/service"
	regionsvc "github.com/bdrtr/gobit/internal/modules/region/service"
	settingssvc "github.com/bdrtr/gobit/internal/modules/settings/service"
	taxsvc "github.com/bdrtr/gobit/internal/modules/tax/service"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
	fulfillingwf "github.com/bdrtr/gobit/internal/workflows/fulfilling"
	invoicingwf "github.com/bdrtr/gobit/internal/workflows/invoicing"
	ordercancelwf "github.com/bdrtr/gobit/internal/workflows/ordercancel"
	returnswf "github.com/bdrtr/gobit/internal/workflows/returns"
	"github.com/bdrtr/gobit/plugins/searchpg"
)

// A MODULE resolving a FLOW's surface.
//
// The endpoint stays on the module and the cross-module decision lives above it;
// the module resolves the flow by name at request time, which is what defers the
// failure to the first request and cached it there.
var (
	_ cartapi.LinePricing     = (*cartwf.Interop)(nil)
	_ cartapi.CartPromotions  = (*cartwf.Interop)(nil)
	_ cartapi.ShippingPricing = (*cartwf.Interop)(nil)
	_ cartapi.CartOpening     = (*cartwf.Interop)(nil)
	_ cartapi.CartCompletion  = (*checkoutwf.Interop)(nil)

	_ orderapi.ReturnReceiving = (*returnswf.Interop)(nil)
	_ orderapi.Invoicing       = (*invoicingwf.Interop)(nil)
	_ orderapi.Fulfilling      = (*fulfillingwf.Interop)(nil)

	_ fulfillsvc.DispatchBound = (*fulfillingwf.Interop)(nil)
)

// A MODULE resolving another MODULE's surface.
//
// Permitted and established (Principle 2.4): the consumer declares the narrow
// interface, the producer's concrete type satisfies it structurally, and the tie is
// a container name. Nothing checked the structural half until this file.
var (
	_ productsvc.UploadReader     = (*filesvc.Interop)(nil)
	_ ordersvc.SpendingPolicy     = (*b2bsvc.Interop)(nil)
	_ notifsvc.OrderContactReader = (*ordersvc.Interop)(nil)
)

// The AUTH module resolving the NOTIFICATION module's surface.
//
// The consumer interface is unexported (`auth.messenger`), so this pin names the
// producer against the shape the consumer needs rather than against the consumer's
// own type — which is the one thing this file cannot do for an unexported
// interface, and is written here rather than left to be noticed.
var _ interface {
	Send(ctx context.Context, template, channel, reference, to string, data map[string]string) error
} = (*notifsvc.Interop)(nil)

// A FLOW resolving a MODULE's surface.
//
// These are resolved in the flow's FromContainer, so a drift fails at STARTUP
// rather than on a request — which is louder, and still only in a lane that boots
// the application. The compiler is earlier than both.
var (
	_ cartwf.Carts     = (*cartsvc.Interop)(nil)
	_ cartwf.Prices    = (*pricingsvc.Service)(nil)
	_ cartwf.Regions   = (*regionsvc.Service)(nil)
	_ cartwf.Customers = (*customersvc.Service)(nil)
	_ cartwf.Discounts = (*promotionsvc.Interop)(nil)
	_ cartwf.Taxes     = (*taxsvc.Interop)(nil)
	_ cartwf.Shipping  = (*fulfillsvc.Interop)(nil)

	_ checkoutwf.Carts       = (*cartsvc.Interop)(nil)
	_ checkoutwf.Inventory   = (*inventorysvc.Interop)(nil)
	_ checkoutwf.Fulfillment = (*fulfillsvc.Interop)(nil)
	_ checkoutwf.Orders      = (*ordersvc.Interop)(nil)
	_ checkoutwf.Payments    = (*paymentsvc.Interop)(nil)
	_ checkoutwf.Promotions  = (*promotionsvc.Interop)(nil)

	_ fulfillingwf.Orders       = (*ordersvc.Interop)(nil)
	_ fulfillingwf.Fulfillments = (*fulfillsvc.Interop)(nil)

	_ invoicingwf.Orders       = (*ordersvc.Interop)(nil)
	_ invoicingwf.Invoices     = (*invoicesvc.Interop)(nil)
	_ invoicingwf.StoreProfile = (*settingssvc.Interop)(nil)

	_ returnswf.Orders    = (*ordersvc.Interop)(nil)
	_ returnswf.Inventory = (*inventorysvc.Interop)(nil)
	_ returnswf.Payments  = (*paymentsvc.Interop)(nil)

	_ ordercancelwf.Inventory   = (*inventorysvc.Interop)(nil)
	_ ordercancelwf.Fulfillment = (*fulfillsvc.Interop)(nil)
	_ ordercancelwf.Orders      = (*ordersvc.Interop)(nil)
)

// A PLUGIN resolving a MODULE's surface, and the CORE resolving one.
//
// The plugin case is the same shape one layer further out: plugins may not import
// modules at all (TestPluginsDoNotImportModules), so its narrow interface is
// declared in the plugin's own package and nothing but this file can see both.
//
// The auth pin is the one pair the producer could already make itself — auth may
// import core, so internal/modules/auth/service/interop.go carries it in
// PRODUCTION code. It is repeated here so the ledger below has no hole a reader has
// to know about.
var (
	_ searchpg.StoreProductReader = (*productsvc.Interop)(nil)
	_ corehttp.Authenticator      = (*authsvc.Interop)(nil)
)

// A FLOW resolving another FLOW's surface.
//
// One pair today: the returns flow opens a shipment through the fulfilling flow
// rather than through the fulfillment module, because opening one is a decision
// that spans both.
var _ returnswf.Shipping = (*fulfillingwf.Interop)(nil)

// pinnedNames is the container name behind every assignment above.
//
// The assignments give the COMPILER the comparison; this map is what lets a TEST
// say the list is complete. Without it the file would be the thing this repository
// keeps finding wrong — a hand-kept list that decides what gets verified and is
// checked against nothing, so a surface added tomorrow is simply absent and silent
// (the language detector's roots, the documentation scan's trees, the separate
// module list, the personal-data audit's trees: four of them, all closed the same
// way).
var pinnedNames = map[string]string{
	"workflows.cart.interop":       "cart module's four storefront endpoints",
	"workflows.checkout.interop":   "the cart module's completion endpoint",
	"workflows.returns.interop":    "the order module's receive endpoint",
	"workflows.invoicing.interop":  "the order module's invoice endpoint",
	"workflows.fulfilling.interop": "the order module's shipment reads and the fulfillment module's dispatch bound",
	"file.interop":                 "the product module reading an upload back",
	"b2b.interop":                  "the order module asking the spending policy",
	"order.interop":                "the notification module's contact read, and four flows",
	"cart.interop":                 "the cart and checkout flows",
	"inventory.interop":            "the checkout, returns and cancellation flows",
	"fulfillment.interop":          "the cart, checkout, fulfilling and cancellation flows",
	"payment.interop":              "the checkout and returns flows",
	"promotion.interop":            "the cart and checkout flows",
	"tax.interop":                  "the cart flow",
	"invoice.interop":              "the invoicing flow",
	"settings.interop":             "the invoicing flow's store profile",
	"product.interop":              "the searchpg plugin's catalog read",
	"auth.interop":                 "the composition root and the admin panel's authenticator",
	"notification.interop":         "the auth module carrying an invitation",
}

// interopPinExemptions are the consumed interop names this file does NOT pin, and
// why each one cannot be.
//
// A reason is required. An exemption with no sentence is the shape that turns a
// gate into a list somebody edits when it goes red.
var interopPinExemptions = map[string]string{
	"core.identity": "the slot is filled by the EMBEDDING application and by nothing in " +
		"this repository (ADR 0008/0043), so there is no producer here to assign. What " +
		"holds the shape instead is core/identitytest.Contract (ADR 0126).",
	"pricing.service": "the producer is a *service.Service rather than an Interop, and it " +
		"IS pinned above — the name is listed here because it does not end in .interop " +
		"and the derivation below prices the interop family.",
}

// TestEveryConsumedInteropNameIsPinned checks the list against the world.
//
// # The population is DERIVED
//
// It is the interop names this repository both PROVIDES and RESOLVES from outside
// the module that owns them — the same derivation
// [TestTheInteropSurfacesHaveAConsumer] makes one file over, reused rather than
// re-written so the two cannot come to disagree about what a consumed interop is.
//
// # What it cannot see
//
// It prices NAMES, not method sets. A name whose pin above went stale — pinned
// against the wrong interface, say — passes here and fails at `go build`, which is
// the division of labor: the compiler checks the shapes and this checks that every
// shape is presented to it.
func TestEveryConsumedInteropNameIsPinned(t *testing.T) {
	t.Parallel()

	tree := scanProductionSource(t)
	provided := tree.providedNames(t)

	consuming := map[string][]string{}
	for name, sites := range tree.calls {
		if !strings.Contains(strings.ToLower(name), resolveCallFragment) {
			continue
		}
		for _, site := range sites {
			for _, arg := range site.call.Args {
				for _, value := range tree.stringValues(site.file, site.fn, arg, 0) {
					consuming[value] = append(consuming[value], site.file.path)
				}
			}
		}
	}

	checked := 0
	for _, name := range slices.Sorted(maps.Keys(provided)) {
		if !strings.HasSuffix(name, interopFamily) {
			continue
		}

		ownerPrefix := owningModulePrefix(provided[name])
		outside := false
		for _, path := range consuming[name] {
			if ownerPrefix != "" && strings.HasPrefix(path, ownerPrefix) {
				continue
			}
			outside = true
		}
		if !outside {
			continue
		}
		checked++

		if _, exempt := interopPinExemptions[name]; exempt {
			continue
		}

		assert.Containsf(t, pinnedNames, name,
			"%q is resolved from outside the module that provides it and this file pins "+
				"nothing for it.\nA consumer declares the interface in its OWN package and "+
				"the producer never imports it, so a drift compiles on both sides and fails "+
				"at RESOLUTION — on a request path, cached by a sync.Once, with startup "+
				"green (gap D73).\nAdd `var _ <consumer interface> = (*<producer>)(nil)` "+
				"above and a line in pinnedNames, or an entry in interopPinExemptions "+
				"saying why the compiler cannot be handed this one.", name)
	}

	assert.GreaterOrEqual(t, checked, 8,
		"only %d consumed interop names were priced; the derivation has gone blind and "+
			"this gate would pass whatever the tree held", checked)
}
