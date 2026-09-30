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
	scopePaymentRead     = "payment:read"
	scopeFulfillmentRead = "fulfillment:read"
	// The product and variant pages read a variant's prices under this one and
	// its stock under [scopeInventoryRead] (ADR 0260).
	scopePricingRead = "pricing:read"
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
		// Editing the related products is a product write like any other.
		routeKey(get, ProductRelationsPath):  scopeProductWrite,
		routeKey(post, ProductRelationsPath): scopeProductWrite,
		// And so is editing the add-ons (ADR 0232).
		routeKey(get, ProductAddOnsPath):  scopeProductWrite,
		routeKey(post, ProductAddOnsPath): scopeProductWrite,
		routeKey(post, VariantPricePath):  scopePricingWrite,
		routeKey(post, VariantStockPath):  scopeInventoryWrite,
		// A variant's bundle is a revision of its product (ADR 0236).
		routeKey(get, VariantBundlePath):  scopeProductWrite,
		routeKey(post, VariantBundlePath): scopeProductWrite,
		routeKey(get, OrdersPath):         scopeOrderRead,
		routeKey(get, OrderPath):          scopeOrderRead,
		// An act on an after-sales record is an order write (ADR 0271).
		routeKey(post, OrderAfterSalePath): scopeOrderWrite,
		// And so is opening one (ADR 0272).
		routeKey(post, OrderAfterSaleOpenPath): scopeOrderWrite,
		// The sales report is made of order lines and shows what they sold for.
		// It names no scope of its own because it holds no data of its own: an
		// operator who may read the orders may read their total.
		routeKey(get, SalesPath):         scopeOrderRead,
		routeKey(get, CustomersPath):     scopeCustomerRead,
		routeKey(get, CustomerPath):      scopeCustomerRead,
		routeKey(get, InventoryPath):     scopeInventoryRead,
		routeKey(get, ReviewsPath):       scopeReviewRead,
		routeKey(get, ReviewsScriptPath): scopeReviewRead,
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
