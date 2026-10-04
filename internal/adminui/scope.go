package adminui

import (
	"fmt"
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// The privileges the panel's screens require.
//
// # Why the values are SPELLED here
//
// The panel imports no module (Principle 2.4) and core knows none either
// (Principle 2.1), so the same string is written in two trees: the module's api
// package names it for its endpoints, this file names it for the screen that
// reads the same data. That duplication is audited rather than tolerated —
// internal/arch reads both sides from source and refuses a value no module
// declares, because a scope the panel spells differently is a screen NOBODY can
// open and one it spells too widely is a screen everybody can.
//
// # Why they match the API's
//
// An operator's privilege has to mean the same thing on both admin surfaces. A
// panel that invented "panel:customers" would let an installation grant customer
// data through one door after refusing it at the other, and the operator holding
// the grant would have no way to know which of the two answers is the policy.
const (
	scopeProductRead    = "product:read"
	scopeProductWrite   = "product:write"
	scopePricingWrite   = "pricing:write"
	scopeOrderRead      = "order:read"
	scopeOrderWrite     = "order:write"
	scopeCustomerRead   = "customer:read"
	scopeInventoryRead  = "inventory:read"
	scopeInventoryWrite = "inventory:write"
	scopeReviewRead     = "review:read"

	// The order page reads an order's payment and parcels only for an operator
	// holding these as well (ADR 0251): the order's privilege opens the page,
	// and each module's own opens its data on it.
	scopePaymentRead = "payment:read"
	// Recording an offline method's money as received is a payment write
	// (ADR 0287).
	scopePaymentWrite = "payment:write"
	// A telephone order's cart is read under the cart's privilege and built
	// under its write (ADR 0290).
	scopeCartRead        = "cart:read"
	scopeCartWrite       = "cart:write"
	scopeFulfillmentRead = "fulfillment:read"
	// The product and variant pages read a variant's prices under this one and
	// its stock under [scopeInventoryRead] (ADR 0260).
	scopePricingRead = "pricing:read"
	// The sales channels are the auth module's, read under its privilege for
	// the telephone order's channel list (ADR 0305).
	scopeAuthRead = "auth:read"
	// The regions are the region module's, read under its privilege on the
	// Regions screen (ADR 0354).
	scopeRegionRead = "region:read"
	// The tax regions are the tax module's (ADR 0355).
	scopeTaxRead = "tax:read"
)

// routeKey is how the scope table names a route: its method and its path.
func routeKey(method, path string) string { return method + " " + path }

// builtInScopes is the privilege each route the panel SHIPS requires.
//
// # One table, read twice
//
// The route registration wraps every handler with the scope found here, and the
// menu drops an entry the operator cannot open. Two tables would let a menu
// promise a screen the route refuses — the panel has already paid for the mirror
// image of that split (see [UI.pageRoutes]).
//
// # It is keyed by METHOD and path (ADR 0255)
//
// It used to be keyed by path, and a POST added later to a read path would
// have inherited the read privilege. Now a route is its method and its path,
// and binding one the table does not list stops the panel from being built
// ([UI.needs]), so a new verb on an old path arrives with a decision or not at
// all. The paths that take both verbs list both, with the same privilege: an
// edit form an operator cannot submit is a screen that wastes their time.
//
// # Open routes are LISTED, with no privilege
//
// The login page, its submission, the sign-out, the stylesheet and the entry
// point. The first two establish identity and cannot require a privilege
// carried by an identity that does not exist yet; signing out must work for
// anyone who can sign in; the stylesheet is install-identical bytes the login
// page itself needs; and the entry point holds no data and redirects to the
// first screen the operator can open, answering 403 only when none is open
// (see [UI.home]). They are written down rather than left out, so an absence
// can only ever mean a route nobody decided about.
func builtInScopes() map[string]string {
	get, post := http.MethodGet, http.MethodPost

	return map[string]string{
		routeKey(get, StylesheetPath): "",
		routeKey(get, LoginPath):      "",
		routeKey(post, LoginPath):     "",
		routeKey(post, LogoutPath):    "",
		routeKey(get, URLPrefix):      "",
		// A person's own second factor asks for a session and no privilege
		// (ADR 0264): a person who can sign in may protect their account, and
		// one the installation requires a factor of holds none until they do
		// (ADR 0265).
		routeKey(get, SecondFactorPath):         "",
		routeKey(post, SecondFactorEnrollPath):  "",
		routeKey(post, SecondFactorConfirmPath): "",
		routeKey(post, SecondFactorRemovePath):  "",
		// So are the person's own sessions (ADR 0268).
		routeKey(get, SessionsPath):              "",
		routeKey(post, SessionsRevokePath):       "",
		routeKey(post, SessionsRevokeOthersPath): "",

		routeKey(get, ProductsPath):     scopeProductRead,
		routeKey(get, ProductPath):      scopeProductRead,
		routeKey(get, VariantPath):      scopeProductRead,
		routeKey(get, ProductEditPath):  scopeProductWrite,
		routeKey(post, ProductEditPath): scopeProductWrite,
		// A product's history and the restore of a revision (ADR 0316).
		routeKey(get, ProductRevisionsPath):        scopeProductRead,
		routeKey(post, ProductRevisionRestorePath): scopeProductWrite,
		// Creating a product and adding a variant are product writes (ADR
		// 0307).
		routeKey(get, ProductNewPath):       scopeProductWrite,
		routeKey(post, ProductNewPath):      scopeProductWrite,
		routeKey(post, ProductVariantsPath): scopeProductWrite,
		// Editing the related products is a product write like any other.
		routeKey(get, ProductRelationsPath):  scopeProductWrite,
		routeKey(post, ProductRelationsPath): scopeProductWrite,
		// And so is editing the add-ons (ADR 0232).
		routeKey(get, ProductAddOnsPath):  scopeProductWrite,
		routeKey(post, ProductAddOnsPath): scopeProductWrite,
		routeKey(post, VariantPricePath):  scopePricingWrite,
		// Adding a price goes through the product module's surface, which
		// links the price set; the handler asks for the price's own write as
		// well (ADR 0309).
		routeKey(post, VariantPricesPath): scopeProductWrite,
		// And so is keeping a variant's stock, whose item is inventory's (ADR
		// 0310).
		routeKey(post, VariantStockItemPath): scopeProductWrite,
		routeKey(post, VariantStockPath):     scopeInventoryWrite,
		// A variant's bundle is a revision of its product (ADR 0236).
		routeKey(get, VariantBundlePath):  scopeProductWrite,
		routeKey(post, VariantBundlePath): scopeProductWrite,
		routeKey(get, OrdersPath):         scopeOrderRead,
		routeKey(get, OrderPath):          scopeOrderRead,
		// An act on an after-sales record is an order write (ADR 0271).
		routeKey(post, OrderAfterSalePath): scopeOrderWrite,
		// And so is opening one (ADR 0272).
		routeKey(post, OrderAfterSaleOpenPath): scopeOrderWrite,
		// Recording an offline payment is the payment module's write
		// (ADR 0287).
		routeKey(post, OrderPaymentReceivedPath): scopePaymentWrite,
		// The telephone order (ADR 0290). The form that opens a cart is the
		// write's own screen, so it asks for the write; the cart's page asks
		// for the read, and offers its form only to a writer.
		routeKey(get, CartsPath):      scopeCartWrite,
		routeKey(post, CartsPath):     scopeCartWrite,
		routeKey(get, CartPath):       scopeCartRead,
		routeKey(post, CartLinesPath): scopeCartWrite,
		// The rest of it is the cart's write too (ADR 0291).
		routeKey(post, CartAddressPath):  scopeCartWrite,
		routeKey(post, CartBillingPath):  scopeCartWrite,
		routeKey(post, CartShippingPath): scopeCartWrite,
		routeKey(post, CartCompletePath): scopeCartWrite,
		// The operator's corrections (ADR 0300).
		routeKey(post, CartLineRemovePath): scopeCartWrite,
		routeKey(post, CartDiscardPath):    scopeCartWrite,
		// The sales report is made of order lines and shows what they sold for.
		// It names no scope of its own because it holds no data of its own: an
		// operator who may read the orders may read their total.
		routeKey(get, SalesPath):     scopeOrderRead,
		routeKey(get, CustomersPath): scopeCustomerRead,
		routeKey(get, CustomerPath):  scopeCustomerRead,
		// An order's parcel is opened by the order module and moved by the
		// fulfillment module (ADR 0324).
		routeKey(post, OrderParcelsPath): scopeOrderWrite,
		// An order's invoice is issued by the order module, through the
		// invoicing flow, under its write (ADR 0335).
		routeKey(post, OrderInvoicePath): scopeOrderWrite,
		// And completes and archives it (ADR 0340).
		routeKey(post, OrderCompletePath): scopeOrderWrite,
		routeKey(post, OrderArchivePath):  scopeOrderWrite,
		// And writes off units of a line (ADR 0341).
		routeKey(post, OrderLineCancellationsPath): scopeOrderWrite,
		// And credits it, puts a delivery on another option and corrects
		// where it ships (ADR 0388).
		routeKey(post, OrderCreditLinesPath):     scopeOrderWrite,
		routeKey(post, OrderDeliveryPath):        scopeOrderWrite,
		routeKey(post, OrderShippingAddressPath): scopeOrderWrite,
		// And cancels the order (ADR 0339).
		routeKey(post, OrderCancelPath):    scopeOrderWrite,
		routeKey(post, OrderParcelActPath): scopeFulfillmentWrite,
		// The store profile is the settings module's (ADR 0336).
		routeKey(get, StoreProfilePath):  scopeSettingsRead,
		routeKey(post, StoreProfilePath): scopeSettingsWrite,
		// The invoices are the invoice module's (ADR 0343), and so are one's
		// page and moving its status (ADR 0344).
		routeKey(get, InvoicesPath):       scopeInvoiceRead,
		routeKey(get, InvoicePath):        scopeInvoiceRead,
		routeKey(post, InvoiceStatusPath): scopeInvoiceWrite,
		// The users are the auth module's (ADR 0345), and so is one's page;
		// their privileges are written under admin, as the API writes them
		// (ADR 0347).
		routeKey(get, UsersPath):       scopeAuthRead,
		routeKey(get, UserPath):        scopeAuthRead,
		routeKey(post, UserScopesPath): scopeAdmin,
		// Inviting a user is an administrator's too (ADR 0348).
		routeKey(post, UserInvitationsPath): scopeAdmin,
		routeKey(post, UserInvitationPath):  scopeAdmin,
		// And so is removing one (ADR 0349).
		routeKey(post, UserRemovePath): scopeAdmin,
		// The API keys are read under the same privilege as the users, and
		// revoked under admin, as the API does (ADR 0350).
		routeKey(get, APIKeysPath):       scopeAuthRead,
		routeKey(post, APIKeyRevokePath): scopeAdmin,
		// And so is making one (ADR 0351).
		routeKey(post, APIKeysPath): scopeAdmin,
		// The sales channels are read and corrected as the keys are (ADR
		// 0352).
		routeKey(get, SalesChannelsPath): scopeAuthRead,
		routeKey(post, SalesChannelPath): scopeAdmin,
		// And so is making one (ADR 0353).
		routeKey(post, SalesChannelsPath): scopeAdmin,
		// The regions are the region module's (ADR 0354).
		routeKey(get, RegionsPath): scopeRegionRead,
		// And correcting one is too (ADR 0362).
		routeKey(post, RegionPath):  scopeRegionWrite,
		routeKey(post, TaxRatePath): scopeTaxWrite,
		// The tax regions are the tax module's (ADR 0355).
		routeKey(get, TaxesPath): scopeTaxRead,
		// The parcels are the fulfillment module's (ADR 0356).
		routeKey(get, ParcelsPath): scopeFulfillmentRead,
		// The payments are the payment module's (ADR 0357).
		routeKey(get, PaymentsPath): scopePaymentRead,
		// The shipping options are the fulfillment module's, and so are
		// revising one (ADR 0333) and writing one (ADR 0334).
		routeKey(get, ShippingOptionsPath):       scopeFulfillmentRead,
		routeKey(post, ShippingOptionsPath):      scopeFulfillmentWrite,
		routeKey(post, ShippingOptionRevisePath): scopeFulfillmentWrite,
		// A claim's evidence is bound by the order module, the file stored by
		// the file module under its own write, asked in the handler (ADR
		// 0325).
		routeKey(post, OrderClaimEvidencePath):       scopeOrderWrite,
		routeKey(post, OrderClaimEvidenceDetachPath): scopeOrderWrite,
		// A customer's groups are the customer module's (ADR 0322), and so
		// are the groups, the form that writes one (ADR 0323) and the one
		// that revises one (ADR 0329).
		routeKey(get, CustomerGroupListPath):    scopeCustomerRead,
		routeKey(post, CustomerGroupListPath):   scopeCustomerWrite,
		routeKey(post, CustomerGroupRevisePath): scopeCustomerWrite,
		routeKey(post, CustomerGroupsPath):      scopeCustomerWrite,
		// And correcting one of their addresses (ADR 0342), and adding one
		// (ADR 0359).
		routeKey(post, CustomerAddressPath):   scopeCustomerWrite,
		routeKey(post, CustomerAddressesPath): scopeCustomerWrite,
		// And moving a default or removing an address (ADR 0360).
		routeKey(post, CustomerAddressDefaultPath): scopeCustomerWrite,
		routeKey(post, CustomerAddressRemovePath):  scopeCustomerWrite,
		// And correcting a customer's name and phone (ADR 0337).
		routeKey(post, CustomerContactPath):     scopeCustomerWrite,
		routeKey(post, CustomerGroupRemovePath): scopeCustomerWrite,
		routeKey(get, InventoryPath):            scopeInventoryRead,
		routeKey(get, ReviewsPath):              scopeReviewRead,
		// The promotions are the promotion module's (ADR 0311), and so is
		// switching one's status (ADR 0312).
		routeKey(get, PromotionsPath):       scopePromotionRead,
		routeKey(post, PromotionStatusPath): scopePromotionWrite,
		// And changing how much its discount gives (ADR 0338).
		routeKey(post, PromotionDiscountPath): scopePromotionWrite,
		routeKey(get, PromotionPath):          scopePromotionRead,
		routeKey(post, PromotionsPath):        scopePromotionWrite,
		// A variant's prices on price lists are the pricing module's (ADR
		// 0327).
		routeKey(post, VariantListPricesPath):      scopePricingWrite,
		routeKey(post, VariantListPriceRemovePath): scopePricingWrite,
		// A price list's status switch is the pricing module's (ADR 0328),
		// and so is revising its terms (ADR 0330).
		routeKey(post, PriceListStatusPath): scopePricingWrite,
		routeKey(post, PriceListRevisePath): scopePricingWrite,
		// The price lists and the form that writes one are the pricing
		// module's (ADR 0326).
		routeKey(get, PriceListsPath):  scopePricingRead,
		routeKey(post, PriceListsPath): scopePricingWrite,
		// The campaigns, the form that writes one (ADR 0319) and the one that
		// revises one (ADR 0331) are the promotion module's.
		routeKey(get, CampaignsPath):       scopePromotionRead,
		routeKey(post, CampaignsPath):      scopePromotionWrite,
		routeKey(post, CampaignRevisePath): scopePromotionWrite,
		// And putting a promotion into one (ADR 0320).
		routeKey(post, PromotionCampaignPath): scopePromotionWrite,
		// And limiting one to customer groups (ADR 0321).
		routeKey(post, PromotionGroupRulesPath): scopePromotionWrite,
		// The delivery log and a resend are the notification module's (ADR
		// 0317).
		routeKey(get, NotificationsPath):       scopeNotificationRead,
		routeKey(post, NotificationResendPath): scopeNotificationWrite,
		// A promotion's rules (ADR 0315).
		routeKey(post, PromotionRulesPath):      scopePromotionWrite,
		routeKey(post, PromotionRuleRemovePath): scopePromotionWrite,
		routeKey(get, ReviewsScriptPath):        scopeReviewRead,
	}
}

// screenScopes is every panel path's privilege: the built-ins plus the screens
// plugins registered.
//
// The registered screens are merged in HERE, in one place, rather than checked
// on a second path at request time: the route wrapper and the menu filter then
// read one map and cannot disagree about a plugin's screen either.
func screenScopes(screens []pageScreen) map[string]string {
	scopes := builtInScopes()
	for i := range screens {
		scopes[routeKey(http.MethodGet, screens[i].page.Path)] = screens[i].page.Scope
		scopes[routeKey(http.MethodGet, screens[i].scriptPath())] = screens[i].page.Scope
	}

	return scopes
}

// needs wraps a handler with the privilege its route is listed under.
//
// It takes the path rather than the scope so that a route's binding and its
// privilege are one lookup instead of two facts a reader keeps in step: the line
// says which screen this is, and the table says what that screen costs.
//
// # The answer is the panel's page, not the API's envelope
//
// 403 with HTML, for the reason [UI.unexpectedFailure] gives: the client is a
// browser that navigated here, and a JSON envelope would be unreadable to the
// one person who could ask for the missing grant. The page keeps the menu, so
// the operator lands somewhere they can leave.
//
// # A route listed with no privilege is open, and an unlisted one is refused
//
// The handler is returned unwrapped for a route the table lists with no
// privilege; that is how the entry point, the login and the stylesheet stay
// open. A route the table does not list at all PANICS, while the panel is being
// built: an absence is a route nobody decided about, and binding it either
// open or under a neighbour's privilege is the silent choice ADR 0255 removed.
func (u *UI) needs(method, path string, next http.HandlerFunc) http.HandlerFunc {
	scope, listed := u.scopes[routeKey(method, path)]
	if !listed {
		panic(fmt.Sprintf("adminui: %s %s is bound with no entry in the scope table; "+
			"list it in builtInScopes with the privilege it needs, or with none", method, path))
	}
	if scope == "" {
		return next
	}

	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := corehttp.PrincipalFromContext(r.Context())
		if !ok || !principal.HasScope(scope) {
			u.errorPage(w, r, http.StatusForbidden, "Not permitted",
				"This screen needs the "+scope+" privilege, which this account does not "+
					"carry. An administrator can grant it.")

			return
		}

		next(w, r)
	}
}

// allowedItems keeps the menu entries the operator can actually open.
//
// # Why the menu is filtered at all
//
// An entry leading to a 403 is the defect the panel already has a test against
// in the other direction (a screen missing from the menu): it teaches the
// operator that the panel is broken rather than that the grant is missing. The
// refusal page says which privilege is absent; the menu says nothing, so an
// entry that can only refuse is worse than no entry.
//
// # It is NOT a security boundary
//
// Hiding the link protects nothing — the route refuses the request, and that is
// the check. This function is about what the operator is told.
func allowedItems(items []navItem, scopes map[string]string, principal corehttp.Principal) []navItem {
	out := make([]navItem, 0, len(items))
	for i := range items {
		scope := scopes[routeKey(http.MethodGet, items[i].Path)]
		if scope == "" || principal.HasScope(scope) {
			out = append(out, items[i])
		}
	}

	return out
}
