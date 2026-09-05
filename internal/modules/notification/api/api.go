// Package api is the HTTP surface of the notification module.
//
// # Why READ ONLY and /admin/v1 only
//
// The module's single write path is an EVENT SUBSCRIBER: the notification is
// triggered when an order is placed. Opening a "send a notification" endpoint
// would make the same job doable over two paths, and the second path would
// make the idempotency key selectable from outside — that is, the only
// protection that prevents a duplicate notification would be left to the
// caller's attention.
//
// There is no endpoint opened to the customer either: the delivery log is the
// store's internal record, and there is nothing the customer could learn from
// it that they could not already learn from the order itself.
//
// The handlers DO NOT CHOOSE the status code: the service returns a typed
// error and corehttp.WriteError translates it into a status code (plan Section
// 2.7).
//
// # Authorization
//
// The single endpoint requires [ScopeRead]. A write scope IS NOT DEFINED:
// there is no endpoint it could be given to, and defining it already would
// mean putting a scope nobody can see the counterpart of into the scope
// dictionary.
//
// The scope check comes AFTER THE IDENTITY: with no identity it returns 401,
// with an identity whose scope is not enough it returns 403.
// corehttp.RequireAdmin, which establishes the identity, is not mounted in this
// module but on the side that builds the router (corehttp.APIGuards).
package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// pathAdminDeliveries is the path of the delivery log listing.
//
// The route is registered with its FULL PATH; a prefix such as "/admin/v1" IS
// NOT MOUNTED, because the first module that mounts it owns that whole subtree
// and would collide with the other modules that use the same prefix.
const pathAdminDeliveries = "/admin/v1/notifications"

// codeInvalidQuery is the error code returned when a query parameter could not
// be parsed.
const codeInvalidQuery = "notification_invalid_query"

// Query parameters.
const (
	queryReference = "reference"
	queryStatus    = "status"
	queryLimit     = "limit"
	queryOffset    = "offset"
)

// The scope dictionary consists of a SINGLE ENTRY; there is no write scope (see
// the package documentation).

// ScopeRead is the scope for reading the delivery log.
//
// corehttp.ScopeAdmin is a SUPERIOR SCOPE and satisfies this one too; it does
// not need to be listed as well, corehttp.Principal.HasScope already does that.
const ScopeRead = "notification:read"

// Deliveries is the NARROW surface the handler asks of the service.
//
// A single-method interface is used instead of the concrete *service.Service:
// the HTTP layer binds not to the whole of the service but only to the call
// listed here, and the handler behavior (the envelope, the status mapping, the
// parameter parsing) can be tested with a fake implementation, without a real
// database.
type Deliveries interface {
	ListDeliveries(ctx context.Context, in service.ListDeliveriesInput) ([]models.Delivery, int64, error)
}

// Handler holds notification's HTTP handlers.
type Handler struct {
	svc Deliveries
}

// New produces a handler that works over the given service.
func New(svc Deliveries) *Handler { return &Handler{svc: svc} }

// Routes mounts notification's admin routes on the router.
//
// There are two layers of protection and both of them are needed: the IDENTITY
// (corehttp.RequireAdmin, on the side that builds the router) and the SCOPE
// (here, [ScopeRead]). Without the second one an administration user whose
// scopes had been emptied could read the delivery log too; the log carries no
// personal data but it shows which order a notification went out for and when —
// that is, it is the timeline of the order flow.
func (h *Handler) Routes(r chi.Router) {
	r.With(corehttp.RequireScope(ScopeRead)).Get(pathAdminDeliveries, h.listDeliveries)
}

// listDeliveries is the GET /admin/v1/notifications handler.
//
// Filters: "reference" (the order id) and "status". Both are optional; when
// they are not given the whole log is paged, newest to oldest.
func (h *Handler) listDeliveries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	in, err := listInput(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	records, total, err := h.svc.ListDeliveries(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	writePage(w, r, records, total, in.Page)
}

// listInput builds the list input out of the query string.
func listInput(r *http.Request) (service.ListDeliveriesInput, error) {
	limit, err := intParam(r, queryLimit)
	if err != nil {
		return service.ListDeliveriesInput{}, err
	}
	offset, err := intParam(r, queryOffset)
	if err != nil {
		return service.ListDeliveriesInput{}, err
	}

	return service.ListDeliveriesInput{
		Reference: optionalParam(r, queryReference),
		Status:    optionalParam(r, queryStatus),
		Page:      service.Page{Limit: limit, Offset: offset},
	}, nil
}

// optionalParam returns a filter that was not given as nil, and one that was
// given as a pointer.
//
// The distinction is carried all the way to the service: nil means "do not
// filter", while a value pointing at the empty string means "bring the records
// whose reference is empty". Carrying the two in a value type would have meant
// silently returning the WHOLE log to a client that wrote "?reference=".
func optionalParam(r *http.Request, name string) *string {
	values := r.URL.Query()
	if !values.Has(name) {
		return nil
	}
	value := values.Get(name)
	return &value
}

// intParam reads a single numeric query parameter; when it is absent it returns
// zero.
//
// A value that CANNOT BE CONVERTED TO A NUMBER returns an error; falling back
// to zero silently would have led the client to get the first page instead of
// the page it asked for.
func intParam(r *http.Request, name string) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, coreerrors.Invalid(codeInvalidQuery,
			"the %q parameter has to be an integer, %q was given", name, raw)
	}
	return value, nil
}

// listEnvelope is the envelope of list responses (plan Section 8).
type listEnvelope struct {
	// Data is the records on the current page.
	Data any `json:"data"`
	// Count is the TOTAL number of records matching the filter.
	Count int64 `json:"count"`
	// Offset is the applied number of skipped records.
	Offset int64 `json:"offset"`
	// Limit is the applied page size.
	Limit int64 `json:"limit"`
}

// writePage writes the records inside the list envelope.
//
// The Limit in the envelope is NOT the request's raw value but the value the
// service applied: when no limit is given the service applies its default, and
// the envelope reporting it is what the client needs in order to compute the
// next page correctly.
func writePage(w http.ResponseWriter, r *http.Request, records []models.Delivery, total int64, page service.Page) {
	limit := page.Limit
	if limit == 0 {
		limit = service.DefaultLimit
	}

	items := make([]deliveryDTO, 0, len(records))
	for i := range records {
		items = append(items, toDeliveryDTO(records[i]))
	}

	corehttp.WriteJSON(r.Context(), w, http.StatusOK, listEnvelope{
		Data:   items,
		Count:  total,
		Offset: page.Offset,
		Limit:  limit,
	})
}
