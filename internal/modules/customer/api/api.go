// Package api is the customer module's HTTP surface.
//
// There are two namespaces (plan Section 8): /admin/v1 for administration,
// /store/v1 for the customer.
//
// # The storefront REQUIRES an identity this framework does not issue
//
// The store endpoints manage a customer's OWN profile and addresses, and the
// customer is named in the path. That claim is not taken on trust: every one of
// them resolves a corehttp.Identity from the container under
// corehttp.IdentityName, asks it what the request PROVES, and refuses the
// request unless the two agree ([Handler.storeCustomerID], ADR 0043).
//
// gobit still verifies nothing itself. It holds no proof about the person
// behind a storefront request — ADR 0008 drew that boundary, it STANDS, and the
// embedder is the one who satisfies the contract. What changed on 2026-09-08 is
// the failure mode of an installation that never read ADR 0008: with nothing
// bound these routes REJECT with corehttp.CodeIdentityNotBound instead of
// handing a stranger somebody's street address. Closed rather than open is
// ADR 0007's row for an unconfigured authenticator, and this is the same row.
//
// POST /store/v1/customers is the one store route that asks nothing, and the
// reason is that there is nobody to ask about yet: it MINTS a guest record, so
// the customer the request would have to prove does not exist until it answers.
//
// # One word in the old warning was wrong, and it is worth not repeating
//
// This surface was called "unauthenticated" here and in the defect ledger. It
// is not: the six address routes and the two profile routes sit under the store
// prefix of the guard stack, and a request without a publishable key never
// reaches a handler — corehttp.RequireStore answers it with a 401. What the
// address book lacked was AUTHORIZATION: nothing asked whether the caller was
// the customer the path named. Naming the gap wrongly cost a reader the search
// for a missing 401 that was never missing.
//
// # Scopes
//
// The endpoints under /admin/v1 ask for a scope SEPARATELY from identity:
//
//   - [ScopeRead] ("customer:read") — opens the GET endpoints.
//   - [ScopeWrite] ("customer:write") — opens the POST, PUT and DELETE endpoints.
//
// corehttp.ScopeAdmin ("admin") is a SUPERSCOPE and satisfies both; a fully
// privileged identity does not need to be granted them separately.
//
// The /store/v1 endpoints ask for NO scope: the storefront surface's identity
// is the publishable key, and that key by definition carries no scope.
//
// Handlers do NOT CHOOSE the status code: the service returns a typed error and
// corehttp.WriteError turns it into a status code (plan Section 2.7). This
// keeps error classification in a single place.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// maxBodyBytes is the maximum size of a single request body. An unbounded body
// is the cheapest way to exhaust memory with a single request.
const maxBodyBytes int64 = 1 << 20 // 1 MiB

// codeInvalidBody is the error code returned when a request body or parameter
// cannot be parsed.
const codeInvalidBody = "customer_invalid_body"

// The names of the path parameters.
const (
	paramID         = "id"
	paramAddressID  = "address_id"
	paramCustomerID = "customer_id"
)

// Customer is the surface the handlers need from the service.
//
// Keeping it narrow keeps the tests simple: the HTTP behavior can be verified
// with a fake a few lines long, without a real database.
type Customer interface {
	// CreateCustomer creates a registered customer account.
	CreateCustomer(ctx context.Context, in service.CustomerInput) (models.Customer, error)
	// RegisterGuest creates a guest customer record.
	RegisterGuest(ctx context.Context, in service.CustomerInput) (models.Customer, error)
	// GetCustomer returns the customer by its identifier.
	GetCustomer(ctx context.Context, id string) (models.Customer, error)
	// ListCustomers filters and pages the customers.
	ListCustomers(ctx context.Context, in service.ListCustomersInput) (service.Page[models.Customer], error)
	// UpdateCustomer updates the given fields of the customer.
	UpdateCustomer(ctx context.Context, id string, in service.UpdateCustomerInput) (models.Customer, error)
	// DeleteCustomer soft-deletes the customer.
	DeleteCustomer(ctx context.Context, id string) error
	// ConvertGuestToAccount turns a guest into a registered account.
	ConvertGuestToAccount(ctx context.Context, customerID string) error

	// CreateGroup creates a new customer group.
	CreateGroup(ctx context.Context, in service.GroupInput) (models.CustomerGroup, error)
	// GetGroup returns the group by its identifier.
	GetGroup(ctx context.Context, id string) (models.CustomerGroup, error)
	// ListGroups pages the groups.
	ListGroups(ctx context.Context, limit, offset int64) (service.Page[models.CustomerGroup], error)
	// UpdateGroup updates the given fields of the group.
	UpdateGroup(ctx context.Context, id string, in service.UpdateGroupInput) (models.CustomerGroup, error)
	// DeleteGroup soft-deletes the group.
	DeleteGroup(ctx context.Context, id string) error
	// AddToGroup adds the customer to the group.
	AddToGroup(ctx context.Context, customerID, groupID string) error
	// RemoveFromGroup removes the customer from the group.
	RemoveFromGroup(ctx context.Context, customerID, groupID string) error
	// ListGroupsOf returns the customer's groups.
	ListGroupsOf(ctx context.Context, customerID string) ([]models.CustomerGroup, error)
	// SetGroupSegment and ClearGroupSegment give a group a rule that decides
	// its members, and take it away (ADR 0217).
	SetGroupSegment(ctx context.Context, groupID string, rule models.SegmentRule) (models.CustomerGroup, error)
	ClearGroupSegment(ctx context.Context, groupID string) (models.CustomerGroup, error)

	// CreateAddress adds a new address for the customer.
	CreateAddress(ctx context.Context, customerID string, in service.AddressInput) (models.CustomerAddress, error)
	// ListAddresses returns the customer's addresses.
	ListAddresses(ctx context.Context, customerID string) ([]models.CustomerAddress, error)
	// UpdateAddress updates the given fields of the address.
	UpdateAddress(ctx context.Context, customerID, addressID string, in service.UpdateAddressInput) (models.CustomerAddress, error)
	// DeleteAddress soft-deletes the address.
	DeleteAddress(ctx context.Context, customerID, addressID string) error
	// SetDefaultShippingAddress makes the address the default shipping address.
	SetDefaultShippingAddress(ctx context.Context, customerID, addressID string) (models.CustomerAddress, error)
	// SetDefaultBillingAddress makes the address the default billing address.
	SetDefaultBillingAddress(ctx context.Context, customerID, addressID string) (models.CustomerAddress, error)

	// SaveToWishlist puts a variant on the customer's wishlist (ADR 0190).
	SaveToWishlist(ctx context.Context, customerID, variantID string) (models.WishlistItem, error)
	// ListWishlist returns the customer's wishlist, newest first.
	ListWishlist(ctx context.Context, customerID string) ([]models.WishlistItem, error)
	// RemoveFromWishlist takes a variant off the customer's wishlist.
	RemoveFromWishlist(ctx context.Context, customerID, variantID string) error
	// MarkStockAlert and UnmarkStockAlert set and clear an item's stock alert
	// (ADR 0215).
	MarkStockAlert(ctx context.Context, customerID, variantID string, channels []string) (models.WishlistItem, error)
	UnmarkStockAlert(ctx context.Context, customerID, variantID string) error
	// MarkPriceAlert and UnmarkPriceAlert set and clear an item's price alert
	// (ADR 0216).
	MarkPriceAlert(
		ctx context.Context, customerID, variantID, regionID string, channels []string,
	) (models.WishlistItem, error)
	UnmarkPriceAlert(ctx context.Context, customerID, variantID string) error
}

// Handler is the customer module's set of HTTP handlers.
type Handler struct {
	svc Customer
	// preview is the segment flow the preview endpoint runs on; see
	// [Handler.WithPreview].
	preview SegmentPreview
	// identity proves which customer a storefront request belongs to. It is
	// NIL when the installation bound none, and that state is CLOSED rather
	// than open; see [Handler.storeCustomerID].
	identity corehttp.Identity
}

// New builds the set of handlers that runs on the given service.
//
// identity may be nil and the zero value is the SAFE one: with no identity the
// store routes that name a customer refuse every request. A constructor that
// defaulted a missing identity to "the path is the truth" would restore the
// hole ADR 0043 closed, and it would do it silently — which is this
// repository's most expensive class of fault.
//
// The module hands in a wrapper that resolves the embedder's implementation
// from the container ON FIRST USE (see the customer module's Register), so a
// nil arriving here means the caller built the handler by hand: a test, or an
// embedder driving the package directly.
func New(svc Customer, identity corehttp.Identity) *Handler {
	return &Handler{svc: svc, identity: identity}
}

// Scope vocabulary: the scopes customer's admin endpoints ask for.
//
// The names follow the same pattern in ALL modules ("<module>:read" /
// "<module>:write"). Every module coining its own word would mean the person
// granting scopes memorizes a separate vocabulary per module; and a mistake made
// in a vocabulary nobody memorized always falls the same way — too much is
// granted.
const (
	// ScopeRead is the scope the READ endpoints of customer's admin surface ask
	// for.
	//
	// It is enough to read customer records, their addresses and their groups;
	// it opens no write endpoint. Fully privileged identities do not need it
	// granted separately: a caller carrying corehttp.ScopeAdmin satisfies this
	// one too (see corehttp.Principal.HasScope).
	ScopeRead = "customer:read"

	// ScopeWrite is the scope the WRITE endpoints of customer's admin surface
	// ask for.
	//
	// It opens the endpoints that create, update and delete a customer, convert
	// a guest into an account, write addresses and change group membership.
	// Some of these endpoints change personal data PERMANENTLY, which is why it
	// matters that this scope is not mixed up with the read scope.
	ScopeWrite = "customer:write"
)

// Routes binds customer's admin and store routes to the router.
//
// The routes are registered with full paths, NOT with chi's Route/Mount
// helpers: several modules share the /admin/v1 prefix, and mounting the same
// prefix twice would make chi panic. Registering full paths writes them side by
// side into the same tree.
//
// # PROTECTION
//
// The admin endpoints have two layers, and both are needed:
//
//  1. IDENTITY — corehttp.RequireAdmin. It is mounted NOT in this module but on
//     the side that builds the router (see corehttp.APIGuards).
//  2. SCOPE — HERE, endpoint by endpoint, with corehttp.RequireScope: read
//     endpoints ask for [ScopeRead], write endpoints for [ScopeWrite].
//
// Without the second layer authentication would stand in for authorization:
// an admin user whose scopes were deliberately emptied is still a valid
// identity and could delete customer records with
// DELETE /admin/v1/customers/{id}.
//
// The store endpoints get NO scope: the storefront surface's identity is the
// publishable key, and that key by definition carries NO scope. The
// authorization the store routes DO carry is a different question with a
// different answer: the customer named in the path has to be the customer the
// request proves, and that check is inside the handler rather than in front of
// it — see [Handler.storeCustomerID]. It cannot be middleware here for the same
// reason the core's is not: the parameter it compares against belongs to the
// route.
func (h *Handler) Routes(r chi.Router) {
	read := r.With(corehttp.RequireScope(ScopeRead))
	write := r.With(corehttp.RequireScope(ScopeWrite))

	// --- admin ---
	write.Post("/admin/v1/customers", h.adminCreateCustomer)
	read.Get("/admin/v1/customers", h.adminListCustomers)
	read.Get("/admin/v1/customers/{id}", h.adminGetCustomer)
	write.Put("/admin/v1/customers/{id}", h.adminUpdateCustomer)
	write.Delete("/admin/v1/customers/{id}", h.adminDeleteCustomer)
	write.Post("/admin/v1/customers/{id}/convert-to-account", h.adminConvertGuest)

	read.Get("/admin/v1/customers/{id}/groups", h.adminListGroupsOfCustomer)
	read.Get("/admin/v1/customers/{id}/addresses", h.adminListAddresses)
	read.Get("/admin/v1/customers/{id}/wishlist", h.adminListWishlist)
	write.Post("/admin/v1/customers/{id}/addresses", h.adminCreateAddress)
	write.Put("/admin/v1/customers/{id}/addresses/{address_id}", h.adminUpdateAddress)
	write.Delete("/admin/v1/customers/{id}/addresses/{address_id}", h.adminDeleteAddress)
	write.Post("/admin/v1/customers/{id}/addresses/{address_id}/default-shipping", h.adminSetDefaultShipping)
	write.Post("/admin/v1/customers/{id}/addresses/{address_id}/default-billing", h.adminSetDefaultBilling)

	write.Post("/admin/v1/customer-groups", h.adminCreateGroup)
	read.Get("/admin/v1/customer-groups", h.adminListGroups)
	read.Get("/admin/v1/customer-groups/{id}", h.adminGetGroup)
	write.Put("/admin/v1/customer-groups/{id}", h.adminUpdateGroup)
	write.Delete("/admin/v1/customer-groups/{id}", h.adminDeleteGroup)
	write.Post("/admin/v1/customer-groups/{id}/customers", h.adminAddToGroup)
	write.Delete("/admin/v1/customer-groups/{id}/customers/{customer_id}", h.adminRemoveFromGroup)
	write.Put("/admin/v1/customer-groups/{id}/segment", h.adminSetGroupSegment)
	write.Delete("/admin/v1/customer-groups/{id}/segment", h.adminClearGroupSegment)
	write.Post("/admin/v1/customer-segments/preview", h.adminPreviewSegment)

	// --- storefront ---
	//
	// The eleven routes carrying {id} require the claim to be BACKED; the guest
	// registration below is the one that cannot, because it is what creates the
	// customer (see the package doc).
	r.Post("/store/v1/customers", h.storeRegisterGuest)
	r.Get("/store/v1/customers/{id}", h.storeGetCustomer)
	r.Put("/store/v1/customers/{id}", h.storeUpdateCustomer)
	r.Get("/store/v1/customers/{id}/addresses", h.storeListAddresses)
	r.Post("/store/v1/customers/{id}/addresses", h.storeCreateAddress)
	r.Put("/store/v1/customers/{id}/addresses/{address_id}", h.storeUpdateAddress)
	r.Delete("/store/v1/customers/{id}/addresses/{address_id}", h.storeDeleteAddress)
	r.Post("/store/v1/customers/{id}/addresses/{address_id}/default-shipping", h.storeSetDefaultShipping)
	r.Post("/store/v1/customers/{id}/addresses/{address_id}/default-billing", h.storeSetDefaultBilling)
	r.Get("/store/v1/customers/{id}/wishlist", h.storeListWishlist)
	r.Put("/store/v1/customers/{id}/wishlist/{variant_id}", h.storeSaveToWishlist)
	r.Delete("/store/v1/customers/{id}/wishlist/{variant_id}", h.storeRemoveFromWishlist)
	r.Put("/store/v1/customers/{id}/wishlist/{variant_id}/stock-alert", h.storeMarkStockAlert)
	r.Delete("/store/v1/customers/{id}/wishlist/{variant_id}/stock-alert", h.storeUnmarkStockAlert)
	r.Put("/store/v1/customers/{id}/wishlist/{variant_id}/price-alert", h.storeMarkPriceAlert)
	r.Delete("/store/v1/customers/{id}/wishlist/{variant_id}/price-alert", h.storeUnmarkPriceAlert)
}

// itemEnvelope is the envelope of single-record responses (plan Section 8).
type itemEnvelope struct {
	// Data is the body of the single record.
	Data any `json:"data"`
}

// listEnvelope is the envelope of list responses (plan Section 8).
type listEnvelope struct {
	// Data are the records on the current page.
	Data any `json:"data"`
	// Count is the TOTAL number of records matching the filter.
	Count int64 `json:"count"`
	// Offset is the number of skipped records that was applied.
	Offset int64 `json:"offset"`
	// Limit is the page size that was applied.
	Limit int64 `json:"limit"`
	// NextCursor is the opaque position to send back as "after" for the next
	// page; it is ABSENT when this page is the last one.
	//
	// Its absence is the end-of-listing signal, which is what a client walking
	// forward needs and what offset alone cannot give without a count.
	NextCursor string `json:"next_cursor,omitempty"`
}

// writeItem writes a single-record response with its envelope.
func writeItem(w http.ResponseWriter, r *http.Request, status int, data any) {
	corehttp.WriteJSON(r.Context(), w, status, itemEnvelope{Data: data})
}

// writeItems writes an unpaged list with its envelope.
//
// On the endpoints that are not paged (a customer's addresses, groups) the
// envelope's numeric fields are filled with the record count: the envelope
// shape a client sees does not change from endpoint to endpoint.
//
// Limit EQUALS the number of records returned and is NOT CLIPPED to
// [service.MaxLimit]. Were it clipped, the response for a customer with 250
// addresses would say "count=250, limit=100"; the client would take the page
// size to be 100, enter a paging loop and read the same records again. There
// are no pages here — the single page is all the records.
func writeItems[T any](w http.ResponseWriter, r *http.Request, items []T) {
	if items == nil {
		items = []T{}
	}
	count := int64(len(items))
	corehttp.WriteJSON(r.Context(), w, http.StatusOK, listEnvelope{
		Data:   items,
		Count:  count,
		Offset: 0,
		Limit:  count,
	})
}

// writePage writes the service page with the list envelope.
func writePage[S any, T any](w http.ResponseWriter, r *http.Request, page service.Page[S], convert func(S) T) {
	items := make([]T, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, convert(item))
	}
	corehttp.WriteJSON(r.Context(), w, http.StatusOK, listEnvelope{
		Data:       items,
		Count:      page.Count,
		Offset:     page.Offset,
		Limit:      page.Limit,
		NextCursor: page.NextCursor,
	})
}

// convertAll turns a slice into a slice of DTOs; a nil slice comes back empty.
func convertAll[S any, T any](items []S, convert func(S) T) []T {
	out := make([]T, 0, len(items))
	for _, item := range items {
		out = append(out, convert(item))
	}
	return out
}

// decodeBody decodes the request body into the destination.
//
// Unknown fields are REJECTED: a silently ignored field means a value the
// client believes it sent is never written. The body size is bounded too; if
// the bound is exceeded it comes back as a parse error.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	reader := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(reader)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return coreerrors.Invalid(codeInvalidBody, "request body cannot be empty")
		}
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidBody,
			"request body could not be parsed")
	}

	// A single JSON document is expected; were a second document following it
	// silently ignored, the client would believe what it sent had been
	// processed.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return coreerrors.Invalid(codeInvalidBody, "request body has to be a single JSON document")
	}
	return nil
}

// pathParam reads a path parameter.
func pathParam(r *http.Request, name string) string {
	return chi.URLParam(r, name)
}

// storeCustomerID returns the customer a storefront request is allowed to act
// on, or an error that ends the request.
//
// # What it checks
//
// The path names a customer; that is the CLAIM. The bound identity says which
// customer the request PROVES. The two must be the same string, and every other
// outcome is a refusal: nothing bound is a 401 naming the missing binding, the
// identity's own error passes through unwrapped so the embedder picks the
// status, an identity that proves nothing is a 500, and a disagreement is a 403
// rather than a 404 — the caller supplied the identifier, so there is no
// existence to hide.
//
// # Where the comparison lives
//
// In corehttp.ProvenCustomer, and it was written out here until ADR 0057. It
// moved when the second and third surfaces needed it — the b2b storefront and
// the cart — and the b2b package doc had already refused to take the contract
// in passing, because a second copy of an authorization rule keeps answering
// after it drifts. The four refusals above are summarized here and DECIDED
// there: why each is the kind it is, and why the proven identifier rather than
// the claimed one comes back, are on that function.
//
// What stays here is the CLAIM. Which part of this module's requests names a
// customer is this package's knowledge, and the path parameter is the one thing
// a shared comparison could not know.
func (h *Handler) storeCustomerID(r *http.Request) (string, error) {
	return corehttp.ProvenCustomer(h.identity, r, pathParam(r, paramID))
}

// afterParam reads the cursor of the page being asked for.
//
// An offset alongside it is REFUSED: a cursor and an offset each name a
// position, and honoring both would serve the page N rows past the cursor,
// which neither of them asked for.
func afterParam(r *http.Request, listing string, offset int64) (corepage.Cursor, error) {
	raw := r.URL.Query().Get("after")
	if raw == "" {
		return corepage.Cursor{}, nil
	}
	if offset != 0 {
		return corepage.Cursor{}, coreerrors.Invalid(codeInvalidBody,
			`"after" and "offset" name two different positions; send one of them`)
	}

	return corepage.Decode(listing, raw)
}

// pageParams reads the paging parameters from the query string.
//
// A missing parameter returns zero and the service applies its default; a value
// that CANNOT BE CONVERTED to a number returns an error instead — silently
// falling back to zero would have made the client get the first page rather
// than the page it asked for.
func pageParams(r *http.Request) (limit, offset int64, err error) {
	limit, err = intParam(r, "limit")
	if err != nil {
		return 0, 0, err
	}
	offset, err = intParam(r, "offset")
	if err != nil {
		return 0, 0, err
	}
	return limit, offset, nil
}

// intParam reads a single numeric query parameter; returns zero if it is
// absent.
func intParam(r *http.Request, name string) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, coreerrors.Invalid(codeInvalidBody,
			"the %q parameter has to be an integer, %q given", name, raw)
	}
	return value, nil
}

// boolParam reads a boolean query parameter; returns nil if it is absent.
//
// The difference between nil and false is meaningful here: "has_account=false"
// filters for guests, whereas not giving the parameter at all filters nothing.
func boolParam(r *http.Request, name string) (*bool, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, coreerrors.Invalid(codeInvalidBody,
			"the %q parameter has to be a boolean (true/false), %q given", name, raw)
	}
	return &value, nil
}

// stringParam reads a text query parameter; returns nil if it is absent.
func stringParam(r *http.Request, name string) *string {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil
	}
	return &raw
}
