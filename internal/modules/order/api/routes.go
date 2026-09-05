package api

import (
	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// Scope vocabulary: the scopes the admin endpoints of order ask for.
//
// The distinction is over READING/WRITING, not over the resource. Per-resource
// scopes such as "order_returns:write" grow the list but produce no new
// decision that could be made today: there is no need yet for an identity that
// can open a return but cannot cancel the order, and a scope name defined for a
// need that does not exist is a name whose purpose is unknown on the day it is
// first granted.
const (
	// ScopeRead is the scope the READ endpoints of the order admin surface ask
	// for.
	//
	// It suffices for reading orders and return/exchange/claim records; it
	// opens no status transition. It does not have to be granted separately to
	// fully privileged identities: a caller carrying corehttp.ScopeAdmin
	// satisfies this one as well (see corehttp.Principal.HasScope).
	ScopeRead = "order:read"

	// ScopeWrite is the scope the WRITE endpoints of the order admin surface
	// ask for.
	//
	// The order's status transitions (cancel, complete, archive), opening an
	// after-sales record and EVERY after-sales transition — receiving,
	// refunding, settling, withdrawing — ask for it. Most of them are
	// IRREVERSIBLE, a canceled order is not reopened, which is why separating
	// it from the read scope is not a formality: it is the limit of the damage.
	// The withdrawals are on this side of the line for a reason of their own —
	// withdrawing a return hands a line's returnable quantity back, and
	// withdrawing a claim takes a record off the list of things the shop still
	// owes.
	ScopeWrite = "order:write"
)

// Routes binds the module's store and admin endpoints to the router.
//
// The endpoints are registered with their FULL PATH; no sub-router
// (chi.Route/Mount) IS OPENED for "/store/v1" or "/admin/v1". The reason is
// concrete — the registry calls the Routes of every module on the SAME router,
// and chi refuses being mounted a second time on the same pattern with a panic.
// Had the first module mounted "/store/v1", the second module would have
// brought the server down at start-up.
//
// The order CREATION endpoint is deliberately absent; for the rationale see the
// package documentation.
//
// # PROTECTION
//
// The admin endpoints are protected by two layers and both of
// them are necessary:
//
//  1. IDENTITY — corehttp.RequireAdmin is attached on the side that builds the
//     router (see corehttp.APIGuards); it is not this module's job.
//  2. SCOPE — the endpoints are marked HERE, endpoint by endpoint, with
//     corehttp.RequireScope.
//
// Without the second layer, authentication would stand in for authorization: an
// admin user whose scopes had been left EMPTY could log in and cancel an order
// whose payment had been captured. Identity answers the question "who", not the
// question "what can they do".
func (h *Handler) Routes(r chi.Router) {
	read := r.With(corehttp.RequireScope(ScopeRead))
	write := r.With(corehttp.RequireScope(ScopeWrite))

	// --- Store API (customer, READ ONLY) ---
	//
	// No scope IS ADDED to the storefront surface: the identity there is the
	// publishable key and that key by definition carries no scope. Verifying
	// that the order BELONGS TO THE CUSTOMER is separate work and is still not
	// done; this is a deliberate gap, not a hidden assumption.
	r.Get("/store/v1/orders/{id}", h.storeGetOrder)
	r.Post("/store/v1/orders/{id}/returns", h.storeRequestReturn)

	// --- Admin API (administration) ---
	read.Get("/admin/v1/orders", h.adminListOrders)
	read.Get("/admin/v1/orders/{id}", h.adminGetOrder)
	read.Get("/admin/v1/orders/{id}/payment", h.adminGetOrderPayment)

	// Status transitions are POSTs and have no bodies (except cancel): a
	// transition is not a field update, it is an ACTION, and it must not look
	// like a PATCH applied to the resource itself.
	write.Post("/admin/v1/orders/{id}/cancel", h.adminCancelOrder)
	write.Post("/admin/v1/orders/{id}/complete", h.adminCompleteOrder)
	write.Post("/admin/v1/orders/{id}/archive", h.adminArchiveOrder)

	// After-sales records. The comment that stood here said transitions were
	// "the next phase's work"; every transition the three record types have is
	// bound now, and the list is complete rather than merely long: a return can
	// be received, refunded and WITHDRAWN, a claim settled and WITHDRAWN, an
	// exchange withdrawn.
	//
	// The receive and refund and settle routes go through a flow rather than
	// the service, because each of the three moves stock or money in another
	// module. The three CANCELS do not, and go straight to the service; the
	// argument is on each handler and it is the same one every time — a request
	// that is taken back tells nobody, because nothing was done yet.
	//
	// The two cancels below closed D17 on 2026-09-06. Binding them was not a
	// formality: an UPDATE that no reachable route calls leaves the column
	// audit green — it reads the .sql files, not the call graph — while no
	// operator can produce the write. Service.CancelReturn and
	// Service.CancelClaim had queries, repository methods and transition tables
	// and NO caller anywhere in production, exactly as the exchange's cancel
	// had the day before.
	read.Get("/admin/v1/orders/{id}/returns", h.adminListReturns)
	write.Post("/admin/v1/orders/{id}/returns", h.adminCreateReturn)
	read.Get("/admin/v1/orders/{id}/returns/{returnId}", h.adminGetReturn)
	write.Post("/admin/v1/orders/{id}/returns/{returnId}/receive", h.adminReceiveReturn)
	write.Post("/admin/v1/orders/{id}/returns/{returnId}/refund", h.adminRefundReturn)
	// The return's release valve. Without it a request — which the STOREFRONT
	// can open knowing nothing but the order id — held the line's returnable
	// quantity for good; see Handler.adminCancelReturn.
	write.Post("/admin/v1/orders/{id}/returns/{returnId}/cancel", h.adminCancelReturn)
	read.Get("/admin/v1/orders/{id}/exchanges", h.adminListExchanges)
	write.Post("/admin/v1/orders/{id}/exchanges", h.adminCreateExchange)
	read.Get("/admin/v1/orders/{id}/exchanges/{exchangeId}", h.adminGetExchange)
	// The exchange's ONLY transition.
	write.Post("/admin/v1/orders/{id}/exchanges/{exchangeId}/cancel", h.adminCancelExchange)
	read.Get("/admin/v1/orders/{id}/claims", h.adminListClaims)
	write.Post("/admin/v1/orders/{id}/claims", h.adminCreateClaim)
	read.Get("/admin/v1/orders/{id}/claims/{claimId}", h.adminGetClaim)
	write.Post("/admin/v1/orders/{id}/claims/{claimId}/settle", h.adminSettleClaim)
	// The claim's other exit. Settling refuses everything but a "requested"
	// claim of type "refund", so without this route a claim opened in error had
	// no exit at all; see Handler.adminCancelClaim.
	write.Post("/admin/v1/orders/{id}/claims/{claimId}/cancel", h.adminCancelClaim)

	// Invoicing. The endpoints are on the ORDER because "invoice this order" is
	// a question asked about an order and the client asking it holds an order
	// id; the invoice module's own endpoint takes a finished document and knows
	// nothing about orders.
	write.Post("/admin/v1/orders/{id}/invoice", h.adminIssueInvoice)
	read.Get("/admin/v1/orders/{id}/invoice", h.adminGetOrderInvoice)

	// Shipments. On the ORDER for the same reason invoicing is: "ship this
	// order" is asked about an order, and until the binding existed nothing
	// could answer which order a parcel belonged to.
	write.Post("/admin/v1/orders/{id}/fulfillments", h.adminOpenShipment)
	read.Get("/admin/v1/orders/{id}/fulfillments", h.adminListShipments)

	// The timeline. It is the support desk's view and it composes what the
	// other endpoints answer one at a time.
	read.Get("/admin/v1/orders/{id}/timeline", h.adminGetOrderTimeline)
}
