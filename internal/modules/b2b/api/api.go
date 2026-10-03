// Package api is the b2b module's HTTP surface.
//
// There are two namespaces (plan Section 8): /admin/v1 for administration,
// /store/v1 for the customer. All of the module's endpoints sit under the "b2b"
// segment; the next B2B concept (quotes, an approval flow) goes into the same
// tree when it is added.
//
// # Storefront: somebody else's company CANNOT be read
//
// The storefront surface has NO endpoint called with a company id. The only way
// to a company goes through the customer's OWN employee record:
//
//	GET /store/v1/b2b/customers/{customer_id}/company
//	GET /store/v1/b2b/customers/{customer_id}/employee
//
// Both resolve only that customer's membership (see
// service.Service.MembershipOfCustomer). A request to "read somebody else's
// company" is therefore not a request that is refused but one that CANNOT BE
// EXPRESSED: no endpoint has a company id parameter a client could write.
//
// # The storefront endpoints read the customer FROM THE PATH, but no longer BELIEVE it
//
// In this repository the identity of a storefront request is the publishable API
// key, and that key represents a SALES CHANNEL, not a customer (see
// corehttp.RequireStore). The endpoints take the customer id from the path; that
// value is a CLAIM, and what backs it is looked for.
//
// Until 2026-09-08 nothing looked. What that cost is worth keeping, because it
// is what the change bought: a caller holding somebody's customer id read that
// person's employer — the company's name, contact address and billing address
// — and their spending limit, its reset interval and the start of the current
// window. Nothing about the identifier is a secret; it travels in cart and
// order response bodies. Closing the company's own name as a parameter was
// never the hole. The hole was believing the customer id.
//
// Since ADR 0057 both routes resolve a corehttp.Identity from the container
// under corehttp.IdentityName, ask it what the request PROVES and refuse when
// the two DISAGREE ([Handler.storeCustomerID]). With nothing bound they answer
// as they always have: an installation that never bound a verifier keeps its
// working B2B storefront, and the oracle above stays open there until it binds
// one. That residue is stated rather than paid for by an upgrade — the address
// book could be closed outright in ADR 0043 because no anonymous caller has a
// correct use for somebody's street address, and these two routes are the same
// class of leak reached through a surface that is in service.
//
// gobit still verifies nothing itself: ADR 0008 stands and the embedder is the
// one who satisfies the contract. The comparison is corehttp.ProvenCustomer's
// and not this package's, which is what keeps this module's copy from becoming
// a second, silently diverging answer to one authorization question.
//
// # Scopes
//
// The endpoints under /admin/v1 ask for a scope SEPARATELY from identity:
//
//   - [ScopeRead] ("b2b:read") — opens the GET endpoints.
//   - [ScopeWrite] ("b2b:write") — opens the POST, PUT and DELETE endpoints.
//
// corehttp.ScopeAdmin ("admin") is a SUPERSCOPE and satisfies both.
//
// Handlers do NOT CHOOSE the status code: the service returns a typed error and
// corehttp.WriteError turns it into a status code (plan Section 2.7).
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
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
	"github.com/bdrtr/gobit/internal/modules/b2b/service"
)

// maxBodyBytes is the maximum size of a single request body. An unbounded body
// is the cheapest way to exhaust memory with a single request.
const maxBodyBytes int64 = 1 << 20 // 1 MiB

// codeInvalidBody is the error code returned when a request body or parameter
// cannot be parsed.
const codeInvalidBody = "b2b_invalid_body"

// The names of the path parameters.
const (
	paramID         = "id"
	paramCustomerID = "customer_id"
)

// The scope vocabulary: the scopes b2b's admin endpoints ask for.
//
// The names follow the same pattern in ALL modules ("<module>:read" /
// "<module>:write"). Each module inventing its own word would mean the person
// granting scopes memorizing a separate vocabulary per module; and a mistake
// made in a vocabulary nobody memorized always falls the same way — too much
// is granted.
const (
	// ScopeRead is the scope the READ endpoints of the b2b admin surface ask
	// for.
	//
	// It is enough to read companies and employee records; it opens no write
	// endpoint. Fully privileged identities do not need to be granted it
	// separately: a caller carrying corehttp.ScopeAdmin satisfies it too.
	ScopeRead = "b2b:read"

	// ScopeWrite is the scope the WRITE endpoints of the b2b admin surface ask
	// for.
	//
	// It opens the endpoints that open and close companies and that change
	// employees' SPENDING AUTHORITY. The second is the real reason it is
	// separate from the read scope in this module: raising an employee's limit
	// widens their permission to spend the company's money.
	ScopeWrite = "b2b:write"
)

// B2B is the surface the handlers need from the service.
//
// Keeping it narrow simplifies the tests: the HTTP behavior can be verified
// with a fake of a few lines, without a real database.
type B2B interface {
	// CreateCompany creates a new company.
	CreateCompany(ctx context.Context, in service.CompanyInput) (models.Company, error)
	// GetCompany returns a company by its id.
	GetCompany(ctx context.Context, id string) (models.Company, error)
	// ListCompanies filters and pages companies.
	ListCompanies(ctx context.Context, in service.ListCompaniesInput) (service.Page[models.Company], error)
	// UpdateCompany updates the given fields of a company.
	UpdateCompany(ctx context.Context, id string, in service.UpdateCompanyInput) (models.Company, error)
	// DeleteCompany soft-deletes a company and its employees.
	DeleteCompany(ctx context.Context, id string) error

	// CreateEmployee adds a new employee to a company.
	CreateEmployee(ctx context.Context, in service.EmployeeInput) (models.CompanyEmployee, error)
	// GetEmployee returns an employee by its id.
	GetEmployee(ctx context.Context, id string) (models.CompanyEmployee, error)
	// ListEmployees filters and pages employees.
	ListEmployees(ctx context.Context, in service.ListEmployeesInput) (service.Page[models.CompanyEmployee], error)
	// UpdateEmployee updates the given fields of an employee.
	UpdateEmployee(ctx context.Context, id string, in service.UpdateEmployeeInput) (models.CompanyEmployee, error)
	// DeleteEmployee soft-deletes an employee and removes the customer bond.
	DeleteEmployee(ctx context.Context, id string) error

	// MembershipOfCustomer returns the customer's OWN membership.
	MembershipOfCustomer(ctx context.Context, customerID string) (service.Membership, error)
}

// IdentityLookup hands back the customer identity the installation bound, a NIL
// identity when it bound none, or an error when the binding itself is broken.
//
// It is a lookup rather than the corehttp.Identity itself because "this
// installation bound no verifier" is an answer these two routes act on, and
// that contract cannot carry it: CustomerID returns an identifier or an error,
// and an absent binding is neither.
//
// What these routes DO with that answer changed under this type. ADR 0057 served
// the path's claim rather than turning every b2b storefront read in an unprepared
// installation into a 401; ADR 0125 makes it exactly that 401 and gives such an
// installation one setting to keep the old answer. The distinction this lookup
// carries is what let either record be written.
type IdentityLookup func(ctx context.Context) (corehttp.Identity, error)

// Handler is the b2b module's set of HTTP handlers.
type Handler struct {
	svc B2B
	// identity finds the verifier that proves which customer a storefront
	// request belongs to. Only the DISAGREEMENT it can then see is refused;
	// see [Handler.storeCustomerID].
	identity IdentityLookup
	// trustUnverified says whether the claim may be served when NO verifier is
	// bound. It is false by default and by zero value (ADR 0125), and it is the
	// SAME installation-wide choice the cart module takes — one field in the
	// composition root feeds both, because two answers to one question is the
	// divergence ADR 0057 built a shared comparison to prevent.
	trustUnverified bool
}

// New builds the set of handlers that works over the given service.
//
// identity may be nil, and a nil one means what a lookup finding nothing means:
// this handler holds no verifier, does not pretend to, and answers the path
// claim as this module answered it before ADR 0057. The module hands in a
// wrapper that resolves the embedder's implementation from the container ON
// FIRST USE, so a nil arriving here means the caller built the handler by hand:
// a test, or an embedder driving the package directly.
func New(svc B2B, identity IdentityLookup, trustUnverified bool) *Handler {
	return &Handler{svc: svc, identity: identity, trustUnverified: trustUnverified}
}

// Routes binds b2b's admin and store routes to the router.
//
// The routes are registered with full paths, NOT with chi's Route/Mount
// helpers: several modules share the /admin/v1 prefix, and mounting the same
// prefix twice would panic in chi. Registering full paths writes them side by
// side into the same tree.
//
// # PROTECTION
//
// The admin endpoints have two layers, and both are needed:
//
//  1. IDENTITY — corehttp.RequireAdmin. It is attached NOT in this module but
//     by the side that builds the router (see corehttp.APIGuards).
//  2. SCOPE — HERE, endpoint by endpoint, with corehttp.RequireScope: the read
//     endpoints ask for [ScopeRead], the write endpoints for [ScopeWrite].
//
// Without the second layer authentication would stand in for authorization: an
// admin user whose scopes were deliberately emptied is still a valid identity,
// and could raise their own spending limit with
// PUT /admin/v1/b2b/employees/{id}.
//
// NO scope is added to the store endpoints: the store surface's identity is
// the publishable key, and that key by definition carries NO scope. For the
// sense in which the storefront endpoints are protected (and the sense in which
// they are not), see the package documentation.
func (h *Handler) Routes(r chi.Router) {
	read := r.With(corehttp.RequireScope(ScopeRead))
	write := r.With(corehttp.RequireScope(ScopeWrite))

	// --- admin: companies ---
	write.Post("/admin/v1/b2b/companies", h.adminCreateCompany)
	read.Get("/admin/v1/b2b/companies", h.adminListCompanies)
	read.Get("/admin/v1/b2b/companies/{id}", h.adminGetCompany)
	write.Put("/admin/v1/b2b/companies/{id}", h.adminUpdateCompany)
	write.Delete("/admin/v1/b2b/companies/{id}", h.adminDeleteCompany)

	// --- admin: employees ---
	write.Post("/admin/v1/b2b/employees", h.adminCreateEmployee)
	read.Get("/admin/v1/b2b/employees", h.adminListEmployees)
	read.Get("/admin/v1/b2b/employees/{id}", h.adminGetEmployee)
	write.Put("/admin/v1/b2b/employees/{id}", h.adminUpdateEmployee)
	write.Delete("/admin/v1/b2b/employees/{id}", h.adminDeleteEmployee)

	// --- storefront ---
	//
	// The path starts with the CUSTOMER, not the company: the resource's key is
	// the customer's own id and the company is DERIVED from it. A
	// "/store/v1/b2b/companies/{id}" endpoint is deliberately absent from this
	// module.
	r.Get("/store/v1/b2b/customers/{customer_id}/company", h.storeGetCompany)
	r.Get("/store/v1/b2b/customers/{customer_id}/employee", h.storeGetEmployee)
}

// itemEnvelope is the envelope of single-item responses (plan Section 8).
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
	// Offset is the applied skip count.
	Offset int64 `json:"offset"`
	// Limit is the applied page size.
	Limit int64 `json:"limit"`
}

// writeItem writes a single-item response in its envelope.
func writeItem(w http.ResponseWriter, r *http.Request, status int, data any) {
	corehttp.WriteJSON(r.Context(), w, status, itemEnvelope{Data: data})
}

// writePage writes a service page in the list envelope.
func writePage[S any, T any](w http.ResponseWriter, r *http.Request, page service.Page[S], convert func(S) T) {
	items := make([]T, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, convert(item))
	}
	corehttp.WriteJSON(r.Context(), w, http.StatusOK, listEnvelope{
		Data:   items,
		Count:  page.Count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// decodeBody decodes the request body into the target.
//
// Unknown fields are REJECTED: a silently ignored field means a value the
// client believes it sent is never written. In this module that value could be
// a spending limit. The body size is bounded too; if the bound is exceeded it
// comes back as a parse error.
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

// storeCustomerID returns the customer a storefront request may act on, or an
// error that ends the request.
//
// The path names a customer; that is the CLAIM. The bound identity says which
// customer the request PROVES. corehttp.ProvenCustomer compares them and every
// disagreement is a refusal — a mismatch is a 403, an implementation that
// proves nothing is a 500, and the embedder's own error passes through with the
// status its kind picks. All of them are written out on that function.
//
// # Why an installation with no verifier is still served
//
// Because nothing here can contradict the claim, and refusing an unchecked
// claim would withdraw a working surface from an embedder who did nothing
// wrong. Until ADR 0125 the claim was handed back as it arrived, which is what
// this module did before ADR 0057; that record named the residue instead of
// charging an upgrade for it. It is REFUSED now, and the old answer is one
// setting away (STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM) — so an operator
// who wants it still has it, and nobody gets it without deciding.
//
// # Why the comparison is not written here
//
// It is corehttp.ProvenCustomer's, shared with the address book (ADR 0057).
// This package doc used to say the work was "wait for a session to exist"; when
// ADR 0043 published the contract it became "bind it", and it warned in the
// same paragraph that doing it in passing would put a second, silently
// diverging copy of an authorization rule in the tree. What stays here is the
// CLAIM: this module spells it "customer_id" in the path and the address book
// spells it "id", which is the one thing a shared comparison cannot know.
func (h *Handler) storeCustomerID(r *http.Request) (string, error) {
	claimed := pathParam(r, paramCustomerID)

	var identity corehttp.Identity
	if h.identity != nil {
		var err error
		if identity, err = h.identity(r.Context()); err != nil {
			return "", err
		}
	}
	if identity == nil && h.trustUnverified {
		return claimed, nil
	}

	return corehttp.ProvenCustomer(identity, r, claimed)
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
// The difference between nil and false is meaningful here:
// "is_company_admin=false" filters for employees who are not admins, whereas
// not giving the parameter at all filters nothing.
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
