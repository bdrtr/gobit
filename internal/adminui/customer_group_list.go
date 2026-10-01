package adminui

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// The customer groups screen (ADR 0323): the shop's groups with their rank,
// read through the customer module's group entity, and the form that writes
// one through the module's panel surface.

// CustomerGroupListPath lists the customer groups and takes the form that
// writes one.
const CustomerGroupListPath = URLPrefix + "/customer-groups"

// customerGroupsLabel is what the section is called on screen.
const customerGroupsLabel = "Customer groups"

// customerGroupsPerPage is the list's page size, the other lists'.
const customerGroupsPerPage = 25

// fieldRank is a customer group's rank, the smaller ranking first (ADR 0049).
const fieldRank = "rank"

// The group form's fields.
const (
	formGroupName = "name"
	formGroupRank = "rank"
)

// GroupCreator is the narrow surface a customer group is written through.
type GroupCreator interface {
	// CreateGroup writes a customer group and returns its id.
	CreateGroup(ctx context.Context, name string, rank int32) (string, error)
}

// customerGroupRow is one group as the list prints it.
type customerGroupRow struct {
	ID        string
	Name      string
	Rank      int64
	CreatedAt time.Time
}

// canCreateGroups reports whether the operator may write a group here.
func (u *UI) canCreateGroups(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.memberships.(GroupCreator)

	return ok && principal.HasScope(scopeCustomerWrite)
}

// listCustomerGroups renders the groups.
func (u *UI) listCustomerGroups(w http.ResponseWriter, r *http.Request) {
	u.renderCustomerGroups(w, r, http.StatusOK, "", url.Values{})
}

// createCustomerGroup writes the group the form describes and returns to the
// list, which names it; a refusal comes back on the list with what was typed
// (ADR 0323).
func (u *UI) createCustomerGroup(w http.ResponseWriter, r *http.Request) {
	creator, ok := u.memberships.(GroupCreator)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Customers unavailable",
			"The customer module's panel surface cannot write a group in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	name := strings.TrimSpace(r.PostFormValue(formGroupName))
	rank, err := parseRank(r.PostFormValue(formGroupRank))
	if err != nil {
		err = errors.Invalid(CodeAmountInvalid, "A rank is a whole number; the smaller ranks first.")
	} else {
		_, err = creator.CreateGroup(r.Context(), name, rank)
	}
	switch {
	case err == nil:
		created := url.Values{paramCreated: {name}}
		corehttp.WriteRedirect(r.Context(), w, CustomerGroupListPath+"?"+created.Encode())
	case errors.IsInvalid(err) || errors.IsConflict(err):
		u.renderCustomerGroups(w, r, http.StatusUnprocessableEntity, messageFor(err), r.PostForm)
	default:
		u.unexpectedFailure(w, r, err, "The customer group could not be written")
	}
}

// parseRank reads a rank, zero when it is left empty.
func parseRank(text string) (int32, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, nil
	}
	rank, err := strconv.ParseInt(text, 10, 32)
	if err != nil {
		return 0, err
	}

	return int32(rank), nil
}

// renderCustomerGroups lists the groups, newest first, with a refused write's
// reason and what was typed. An operator who may write and not read the
// customers is told the reason alone (ADR 0260).
func (u *UI) renderCustomerGroups(w http.ResponseWriter, r *http.Request, code int, refused string, typed url.Values) {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if refused != "" && !principal.HasScope(scopeCustomerRead) {
		u.errorPage(w, r, code, "Not done", refused)
		return
	}

	page := pageNumber(r.URL.Query().Get("page"))
	offset := (page - 1) * customerGroupsPerPage
	if offset > math.MaxInt32 {
		offset = math.MaxInt32
	}
	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity: EntityCustomerGroup,
		Fields: []string{fieldID, fieldName, fieldRank, fieldCreatedAt},
		// One more than the page, so whether there is a next one comes out of
		// this read.
		Limit:  customerGroupsPerPage + 1,
		Offset: offset,
	})
	if err != nil {
		u.catalogFailure(w, r, err, "The customer groups could not be read.")
		return
	}
	more := len(records) > customerGroupsPerPage
	if more {
		records = records[:customerGroupsPerPage]
	}
	rows := make([]customerGroupRow, 0, len(records))
	for _, rec := range records {
		rows = append(rows, customerGroupRow{
			ID: recordString(rec, fieldID), Name: recordString(rec, fieldName),
			Rank: recordInt(rec, fieldRank), CreatedAt: recordTime(rec, fieldCreatedAt),
		})
	}

	data := map[string]any{
		titleKey:     customerGroupsLabel,
		"Groups":     rows,
		"Created":    r.URL.Query().Get(paramCreated),
		canCreateKey: u.canCreateGroups(r),
		refusedKey:   refused,
		typedKey:     typed,
	}
	addPaging(data, page, more, CustomerGroupListPath)

	u.templates.render(w, r, code, "customer_groups.gohtml", data)
}
