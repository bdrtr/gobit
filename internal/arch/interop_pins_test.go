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
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/adminui"
	"github.com/bdrtr/gobit/internal/modules/auth"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
	b2bsvc "github.com/bdrtr/gobit/internal/modules/b2b/service"
	cartapi "github.com/bdrtr/gobit/internal/modules/cart/api"
	cartsvc "github.com/bdrtr/gobit/internal/modules/cart/service"
	customerapi "github.com/bdrtr/gobit/internal/modules/customer/api"
	customersvc "github.com/bdrtr/gobit/internal/modules/customer/service"
	filesvc "github.com/bdrtr/gobit/internal/modules/file/service"
	fulfillsvc "github.com/bdrtr/gobit/internal/modules/fulfillment/service"
	inventorysvc "github.com/bdrtr/gobit/internal/modules/inventory/service"
	invoicesvc "github.com/bdrtr/gobit/internal/modules/invoice/service"
	notifsvc "github.com/bdrtr/gobit/internal/modules/notification/service"
	"github.com/bdrtr/gobit/internal/modules/order"
	orderapi "github.com/bdrtr/gobit/internal/modules/order/api"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	"github.com/bdrtr/gobit/internal/modules/payment"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
	pricingapi "github.com/bdrtr/gobit/internal/modules/pricing/api"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
	promotionapi "github.com/bdrtr/gobit/internal/modules/promotion/api"
	promotionsvc "github.com/bdrtr/gobit/internal/modules/promotion/service"
	regionsvc "github.com/bdrtr/gobit/internal/modules/region/service"
	"github.com/bdrtr/gobit/internal/modules/review"
	settingssvc "github.com/bdrtr/gobit/internal/modules/settings/service"
	taxsvc "github.com/bdrtr/gobit/internal/modules/tax/service"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
	fulfillingwf "github.com/bdrtr/gobit/internal/workflows/fulfilling"
	giftcardsalewf "github.com/bdrtr/gobit/internal/workflows/giftcardsale"
	invoicingwf "github.com/bdrtr/gobit/internal/workflows/invoicing"
	ordercancelwf "github.com/bdrtr/gobit/internal/workflows/ordercancel"
	returnswf "github.com/bdrtr/gobit/internal/workflows/returns"
	segmentwf "github.com/bdrtr/gobit/internal/workflows/segment"
	stockalertwf "github.com/bdrtr/gobit/internal/workflows/stockalert"
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
	_ cartapi.CartRepricing   = (*cartwf.Interop)(nil)

	_ promotionapi.PromotionTrial = (*cartwf.Interop)(nil)
	_ pricingapi.PriceListTrial   = (*cartwf.Interop)(nil)
	_ cartapi.CartOpening         = (*cartwf.Interop)(nil)
	_ cartapi.CartCompletion      = (*checkoutwf.Interop)(nil)

	_ orderapi.ReturnReceiving = (*returnswf.Interop)(nil)
	_ orderapi.Invoicing       = (*invoicingwf.Interop)(nil)
	_ orderapi.Fulfilling      = (*fulfillingwf.Interop)(nil)

	_ fulfillsvc.DispatchBound = (*fulfillingwf.Interop)(nil)

	_ customerapi.SegmentPreview = (*segmentwf.Interop)(nil)
)

// A MODULE resolving another MODULE's surface.
//
// Permitted and established (Principle 2.4): the consumer declares the narrow
// interface, the producer's concrete type satisfies it structurally, and the tie is
// a container name. Nothing checked the structural half until this file.
var (
	_ productsvc.UploadReader     = (*filesvc.Interop)(nil)
	_ productsvc.PriceSetWriter   = (*pricingsvc.Service)(nil)
	_ productsvc.StockItemWriter  = (*inventorysvc.Service)(nil)
	_ ordersvc.SpendingPolicy     = (*b2bsvc.Interop)(nil)
	_ ordersvc.CausedRefunds      = (*paymentsvc.Interop)(nil)
	_ notifsvc.OrderContactReader = (*ordersvc.Interop)(nil)
)

// A module resolving another module's surface from its root package.
//
// The two consumer interfaces were unexported until D226, and an unexported one
// can only be pinned by copying its shape, which goes on matching the producer
// after the consumer's own type has moved. They are exported so the pin names
// the consumer's type itself.
var (
	_ auth.Messenger        = (*notifsvc.Interop)(nil)
	_ review.OrderPurchases = (*ordersvc.Interop)(nil)
)

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
	_ cartwf.Companies = (*b2bsvc.Interop)(nil)
	_ cartwf.Orders    = (*ordersvc.Interop)(nil)

	_ checkoutwf.Carts       = (*cartsvc.Interop)(nil)
	_ checkoutwf.Inventory   = (*inventorysvc.Interop)(nil)
	_ checkoutwf.Fulfillment = (*fulfillsvc.Interop)(nil)
	_ checkoutwf.Orders      = (*ordersvc.Interop)(nil)
	_ checkoutwf.Payments    = (*paymentsvc.Interop)(nil)
	_ checkoutwf.Promotions  = (*promotionsvc.Interop)(nil)

	_ fulfillingwf.Orders       = (*ordersvc.Interop)(nil)
	_ fulfillingwf.Fulfillments = (*fulfillsvc.Interop)(nil)
	_ fulfillingwf.Payments     = (*paymentsvc.Interop)(nil)

	_ invoicingwf.Orders       = (*ordersvc.Interop)(nil)
	_ invoicingwf.Invoices     = (*invoicesvc.Interop)(nil)
	_ invoicingwf.StoreProfile = (*settingssvc.Interop)(nil)

	_ returnswf.Orders    = (*ordersvc.Interop)(nil)
	_ returnswf.Inventory = (*inventorysvc.Interop)(nil)
	_ returnswf.Payments  = (*paymentsvc.Interop)(nil)

	_ ordercancelwf.Inventory   = (*inventorysvc.Interop)(nil)
	_ ordercancelwf.Fulfillment = (*fulfillsvc.Interop)(nil)
	_ ordercancelwf.Orders      = (*ordersvc.Interop)(nil)

	// The gift card sale flow (ADR 0210).
	_ giftcardsalewf.Payments = (*paymentsvc.Interop)(nil)
	_ giftcardsalewf.Orders   = (*ordersvc.Interop)(nil)
	_ giftcardsalewf.Notifier = (*notifsvc.Interop)(nil)
	// Its sweep reads orders' collections through the link's forward direction
	// (ADR 0212), which the capture's path never did.
	_ giftcardsalewf.Links  = link.LinkService(nil)
	_ giftcardsalewf.Reader = query.Query(nil)

	// The stock alert flow (ADR 0215).
	_ stockalertwf.Customers = (*customersvc.Service)(nil)
	_ stockalertwf.Catalog   = (*productsvc.Interop)(nil)
	_ stockalertwf.Notifier  = (*notifsvc.Interop)(nil)
	_ stockalertwf.Reader    = query.Query(nil)
	// Its price marks are judged by the cart workflow's quote (ADR 0216).
	_ stockalertwf.Prices = (*cartwf.Workflows)(nil)

	// The segment flow (ADR 0217).
	_ segmentwf.Customers = (*customersvc.Service)(nil)
	_ segmentwf.Orders    = (*ordersvc.Interop)(nil)
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

// The ADMIN PANEL resolving a module's surface.
//
// The panel is the fourth tree and may import no module (ADR 0004), so it declares
// five narrow interfaces of its own and resolves five names by hand. None of them
// was pinned until ADR 0147, and the gap was not theoretical: widening
// [authsvc.Service.Login] with the second factor's code broke `adminui.Session` and every
// lane that compiles stayed green — the panel would have failed at BOOT, in the
// smoke lane, with "does not implement".
//
// Two of the five are OPTIONAL at resolution (a panel runs without the product
// module), which makes the drift quieter still: the surface a missing module leaves
// nil is indistinguishable from the surface a renamed method leaves unsatisfied.
var (
	_ adminui.Session       = (*authsvc.Service)(nil)
	_ adminui.Catalog       = query.Query(nil)
	_ adminui.ProductWriter = (*productsvc.AdminSurface)(nil)
	_ adminui.PriceWriter   = (*pricingsvc.AdminSurface)(nil)
	_ adminui.StockAdmin    = (*inventorysvc.AdminSurface)(nil)
	// The person's own second factor (ADR 0266), optional at resolution too.
	_ adminui.SecondFactorAdmin = (*authsvc.AccountSurface)(nil)
	// And their own sessions (ADR 0268), from the same surface.
	_ adminui.SessionAdmin = (*authsvc.AccountSurface)(nil)
	// The acts on an order's after-sales records (ADR 0271).
	_ adminui.AfterSalesAdmin = (*order.AfterSalesSurface)(nil)
	_ adminui.PaymentReceiver = (*payment.ReceivingSurface)(nil)
	// The telephone order's cart (ADR 0290).
	_ adminui.TelephoneCarts = (*cartapi.TelephoneSurface)(nil)
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
// (the documentation scan's trees, the separate module list, the personal-data
// audit's trees, all closed the same way; the language detector's roots were a
// fourth until D227 replaced them with git's file list).
var pinnedNames = map[string]string{
	"workflows.cart.interop":       "the cart module's storefront endpoints and the promotion and price list trials",
	"workflows.checkout.interop":   "the cart module's completion endpoint",
	"workflows.returns.interop":    "the order module's receive endpoint",
	"workflows.segment.interop":    "the customer module's segment preview",
	"workflows.invoicing.interop":  "the order module's invoice endpoint",
	"workflows.fulfilling.interop": "the order module's shipment reads and the fulfillment module's dispatch and return bounds",
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
	"auth.service":                 "the admin panel's sign-in and sign-out",
	"core.query":                   "the admin panel's catalog read",
	"product.admin":                "the admin panel's product form",
	"pricing.admin":                "the admin panel's price form",
	"inventory.admin":              "the admin panel's stock form",
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
		"IS pinned above, for the cart flow and for the product module's import — the " +
		"name is listed here because it does not end in .interop and the derivation " +
		"below prices the interop family.",
}

// The names this file pins that the derivation does NOT price, and the measured
// size of what is still unpriced.
//
// The scan below walks the `.interop` family. Measured on 2026-09-12 the tree
// resolves TWENTY-THREE provided names from outside the module that owns them, of
// which nineteen are that family; the other four families are the five panel names
// above plus the core's own services (`core.db`, `core.eventbus`, `core.link`,
// `core.query`, `core.workflow`, `core.workflow.store`), the provider registries
// and four module services resolved by other modules.
//
// Widening the derivation is a decision rather than an edit: a core service has one
// consumer interface per module that resolves it, so "the" pin for `core.db` is a
// choice this file cannot make on its own. What is NOT deferred is the cost of
// being wrong, which is written above the panel's block — it already happened once.

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

// TestEveryInteropConsumerIsPinned prices what [TestEveryConsumedInteropNameIsPinned]
// cannot: the CONSUMER. A name pinned for one flow passes that gate while a
// second flow resolving it under an interface of its own goes unchecked, and the
// fulfilling flow's Payments was such a consumer of payment.interop until D226.
//
// The population is derived as the name gate's is: every call whose name carries
// "resolve" and whose name argument is an interop name another module provides,
// with the interface its type argument names. A generic helper's own body
// resolves T and is skipped; its CALLERS name the interface.
func TestEveryInteropConsumerIsPinned(t *testing.T) {
	t.Parallel()

	tree := scanProductionSource(t)
	provided := tree.providedNames(t)
	pinned := pinnedConsumers(t)

	checked := 0
	for name, sites := range tree.calls {
		if !strings.Contains(strings.ToLower(name), resolveCallFragment) {
			continue
		}
		for _, site := range sites {
			consumer := typeArgument(site.file, site.call.Fun)
			if consumer == "" {
				continue
			}
			for _, arg := range site.call.Args {
				for _, value := range tree.stringValues(site.file, site.fn, arg, 0) {
					producer, ok := provided[value]
					if !ok || !strings.HasSuffix(value, interopFamily) {
						continue
					}
					if prefix := owningModulePrefix(producer); prefix != "" &&
						strings.HasPrefix(site.file.path, prefix) {
						continue
					}
					if _, exempt := interopPinExemptions[value]; exempt {
						continue
					}
					checked++
					assert.Truef(t, pinned[consumer],
						"%s resolves %q as %s, and this file pins nothing for that "+
							"interface.\nThe name gate passes because the name is pinned for "+
							"another consumer; this one can drift from the producer and fail "+
							"at RESOLUTION (gap D73, D226). Add `var _ <consumer interface> = "+
							"(*<producer>)(nil)` above.",
						tree.location(site.file, site.call.Pos()), value, consumer)
				}
			}
		}
	}

	assert.GreaterOrEqual(t, checked, 20,
		"only %d interop consumers were priced; the derivation has gone blind", checked)
}

// typeArgument answers the import path and name of the interface a generic
// resolve call names, as "path.Name", or "" for a call that names none or names
// a type parameter.
func typeArgument(file *sourceFile, fun ast.Expr) string {
	var arg ast.Expr
	switch x := fun.(type) {
	case *ast.IndexExpr:
		arg = x.Index
	case *ast.IndexListExpr:
		if len(x.Indices) == 1 {
			arg = x.Indices[0]
		}
	}
	switch x := arg.(type) {
	case *ast.Ident:
		if x.Name == "T" {
			return ""
		}

		return file.importPath + "." + x.Name
	case *ast.SelectorExpr:
		pkg, ok := x.X.(*ast.Ident)
		if !ok || file.imports[pkg.Name] == "" {
			return ""
		}

		return file.imports[pkg.Name] + "." + x.Sel.Name
	}

	return ""
}

// pinnedConsumers reads this file's own pins: every `_ pkg.Interface = ...`
// declaration, as "path.Interface".
func pinnedConsumers(t *testing.T) map[string]bool {
	t.Helper()

	parsed, err := parser.ParseFile(token.NewFileSet(),
		filepath.Join(repoRoot, "internal", "arch", "interop_pins_test.go"), nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("this file could not be parsed: %v", err)
	}
	imports := map[string]string{}
	for _, imp := range parsed.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		local := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			local = imp.Name.Name
		}
		imports[local] = path
	}

	pinned := map[string]bool{}
	for _, decl := range parsed.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != "_" {
				continue
			}
			selector, ok := value.Type.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			if pkg, ok := selector.X.(*ast.Ident); ok && imports[pkg.Name] != "" {
				pinned[imports[pkg.Name]+"."+selector.Sel.Name] = true
			}
		}
	}

	return pinned
}
