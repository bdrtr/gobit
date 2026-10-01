package adminui

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// EntityCustomerGroup is the customer module's group entity in the read layer
// (ADR 0321), spelled by hand and pinned against the module's constant in
// internal/arch.
const EntityCustomerGroup = "customer_group"

// groupsOffered is how many customer groups a form offers: one under the
// customer module's page ceiling of a hundred, which refuses a larger page
// rather than trimming it, so that the one more read to learn there are more
// still fits in one page.
const groupsOffered = 99

// groupOption is one customer group a form offers.
type groupOption struct {
	ID   string
	Name string
}

// groupList is the customer groups a form offers: Unavailable when they could
// not be read, Truncated when there are more than it offers.
type groupList struct {
	Options     []groupOption
	Unavailable bool
	Truncated   bool
	Offered     int
}

// groupList reads the customer groups a form offers, newest first; one more
// than it offers is read, so whether there are more comes out of this read.
func (u *UI) groupList(ctx context.Context) groupList {
	records, err := u.catalog.Graph(ctx, query.GraphSpec{
		Entity: EntityCustomerGroup,
		Fields: []string{fieldID, fieldName},
		Limit:  groupsOffered + 1,
	})
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx,
			"the panel could not read the customer groups; the group form will be absent", "error", err)

		return groupList{Unavailable: true, Offered: groupsOffered}
	}

	list := groupList{Truncated: len(records) > groupsOffered, Offered: groupsOffered}
	if list.Truncated {
		records = records[:groupsOffered]
	}
	for _, rec := range records {
		if id := recordString(rec, fieldID); id != "" {
			list.Options = append(list.Options, groupOption{ID: id, Name: recordString(rec, fieldName)})
		}
	}

	return list
}

// ServiceCustomerAdmin is the customer module's panel surface, spelled by hand
// and pinned against the module's constant in internal/arch (ADR 0322).
const ServiceCustomerAdmin = "customer.admin"

// CustomerGroupsPath puts the customer into a group, and
// CustomerGroupRemovePath takes them out of one (ADR 0322).
const (
	CustomerGroupsPath      = CustomerPath + "/groups"
	CustomerGroupRemovePath = CustomerGroupsPath + "/{groupID}/remove"
)

// scopeCustomerWrite is the customer module's write privilege, as its admin
// API names it.
const scopeCustomerWrite = "customer:write"

// fieldCustomerGroupIDs is the customer record's groups, in the order their
// rank gives them (ADR 0049).
const fieldCustomerGroupIDs = "group_ids"

// formCustomerGroup is the membership form's field, the group chosen.
const formCustomerGroup = "group_id"

// GroupMembership is the narrow surface a customer's groups are written
// through (ADR 0322).
type GroupMembership interface {
	// AddCustomerToGroup puts the customer into the group.
	AddCustomerToGroup(ctx context.Context, customerID, groupID string) error
	// RemoveCustomerFromGroup takes the customer out of the group.
	RemoveCustomerFromGroup(ctx context.Context, customerID, groupID string) error
}

// customerGroupsOf names the customer's groups in the order the record gives
// them; a group whose name could not be read keeps its id, and unread says
// the read failed.
func (u *UI) customerGroupsOf(ctx context.Context, ids []string) (groups []groupOption, unread bool) {
	groups = make([]groupOption, 0, len(ids))
	for _, id := range ids {
		groups = append(groups, groupOption{ID: id, Name: id})
	}
	if len(ids) == 0 {
		return groups, false
	}

	records, err := u.catalog.Graph(ctx, query.GraphSpec{
		Entity:  EntityCustomerGroup,
		Fields:  []string{fieldID, fieldName},
		Filters: map[string]any{filterID: ids},
		Limit:   len(ids),
	})
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx,
			"the panel could not name the customer's groups", "error", err)

		return groups, true
	}
	names := make(map[string]string, len(records))
	for _, rec := range records {
		names[recordString(rec, fieldID)] = recordString(rec, fieldName)
	}
	for i := range groups {
		if name := names[groups[i].ID]; name != "" {
			groups[i].Name = name
		}
	}

	return groups, false
}

// canEditMemberships reports whether the operator may write a customer's
// groups here.
func (u *UI) canEditMemberships(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())

	return u.memberships != nil && principal.HasScope(scopeCustomerWrite)
}

// addToGroup puts the customer in the path into the chosen group and returns
// to their page; a refusal is drawn on it (ADR 0322).
func (u *UI) addToGroup(w http.ResponseWriter, r *http.Request) {
	u.membershipWrite(w, r, func(ctx context.Context, customerID string) error {
		group := strings.TrimSpace(r.PostFormValue(formCustomerGroup))
		if group == "" {
			return errors.Invalid(codeNoGroup, "Choose a customer group.")
		}

		return u.memberships.AddCustomerToGroup(ctx, customerID, group)
	})
}

// removeFromGroup takes the customer in the path out of the group in the
// path and returns to their page (ADR 0322).
func (u *UI) removeFromGroup(w http.ResponseWriter, r *http.Request) {
	u.membershipWrite(w, r, func(ctx context.Context, customerID string) error {
		return u.memberships.RemoveCustomerFromGroup(ctx, customerID, chi.URLParam(r, "groupID"))
	})
}

// membershipWrite runs one of the customer page's group writes and returns to
// the page; a refusal is drawn on it.
func (u *UI) membershipWrite(
	w http.ResponseWriter, r *http.Request, write func(ctx context.Context, customerID string) error,
) {
	if u.memberships == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Customers unavailable",
			"The customer module's panel surface is not registered in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	id := chi.URLParam(r, "id")
	switch err := write(r.Context(), id); {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w, CustomersPath+"/"+id)
	case errors.IsInvalid(err) || errors.IsNotFound(err) || errors.IsConflict(err):
		u.renderCustomer(w, r, http.StatusUnprocessableEntity, id, messageFor(err))
	default:
		u.unexpectedFailure(w, r, err, "The customer's groups could not be written")
	}
}
