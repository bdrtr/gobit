package adminui

import (
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
	scopeCustomerRead   = "customer:read"
	scopeInventoryRead  = "inventory:read"
	scopeInventoryWrite = "inventory:write"
	scopeReviewRead     = "review:read"
)

// builtInScopes is the privilege each path the panel SHIPS requires.
//
// # One table, read twice
//
// The route registration wraps every handler with the scope found here, and the
// menu drops an entry the operator cannot open. Two tables would let a menu
// promise a screen the route refuses — the panel has already paid for the mirror
// image of that split (see [UI.pageRoutes]).
//
// # It is keyed by PATH, not by method and path
//
// The two paths that take both verbs need the same privilege for each: an edit
// form an operator cannot submit is a screen that wastes their time and teaches
// them the panel is broken. The cost is written down in docs/known-limits.md —
// a POST added later to a read path would inherit the read privilege.
//
// # What is deliberately ABSENT
//
// The login page, its submission, the sign-out and the stylesheet. The first two
// establish identity and cannot require a privilege carried by an identity that
// does not exist yet; signing out must work for anyone who can sign in, or an
// operator granted nothing could not clear their own session; and the stylesheet
// is install-identical bytes the login page itself needs. Everything else is
// covered, and internal/app asserts it by WALKING the router rather than by
// reading this map — a path that fell out of here would otherwise become a
// screen with no check at all.
func builtInScopes() map[string]string {
	return map[string]string{
		ProductsPath:     scopeProductRead,
		ProductPath:      scopeProductRead,
		VariantPath:      scopeProductRead,
		ProductEditPath:  scopeProductWrite,
		VariantPricePath: scopePricingWrite,
		VariantStockPath: scopeInventoryWrite,
		OrdersPath:       scopeOrderRead,
		OrderPath:        scopeOrderRead,
		// The sales report is made of order lines and shows what they sold for.
		// It names no scope of its own because it holds no data of its own: an
		// operator who may read the orders may read their total.
		SalesPath:         scopeOrderRead,
		CustomersPath:     scopeCustomerRead,
		CustomerPath:      scopeCustomerRead,
		InventoryPath:     scopeInventoryRead,
		ReviewsPath:       scopeReviewRead,
		ReviewsScriptPath: scopeReviewRead,
		// The panel's entry point holds no data and redirects to the first
		// screen the operator can open, so a privilege here would refuse them
		// the door rather than the room. It answers 403 only when NO room is
		// open — see [UI.home].
		URLPrefix: "",
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
		scopes[screens[i].page.Path] = screens[i].page.Scope
		scopes[screens[i].scriptPath()] = screens[i].page.Scope
	}

	return scopes
}

// needs wraps a handler with the privilege the given PATH is listed under.
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
// # A path with no entry carries no privilege
//
// The handler is returned unwrapped. That is how the entry point, the login and
// the stylesheet stay open, and it is the ONE way a route can end up unchecked —
// which is why the router walk in internal/app requires a refusal from every
// path that is not on that short list.
func (u *UI) needs(path string, next http.HandlerFunc) http.HandlerFunc {
	scope := u.scopes[path]
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
		scope := scopes[items[i].Path]
		if scope == "" || principal.HasScope(scope) {
			out = append(out, items[i])
		}
	}

	return out
}
