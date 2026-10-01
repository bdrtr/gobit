package adminui

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// fakeMemberships records each group write.
type fakeMemberships struct {
	added   []string
	removed []string
	err     error
}

func (f *fakeMemberships) AddCustomerToGroup(_ context.Context, customerID, groupID string) error {
	f.added = append(f.added, customerID+"|"+groupID)
	return f.err
}

func (f *fakeMemberships) RemoveCustomerFromGroup(_ context.Context, customerID, groupID string) error {
	f.removed = append(f.removed, customerID+"|"+groupID)
	return f.err
}

// membershipCatalog holds a customer in two groups, one of which no longer
// exists, and a shop with three groups.
func membershipCatalog() *fakeCatalog {
	return &fakeCatalog{byEntity: map[string][]query.Record{
		EntityCustomer: {{fieldID: "cus_1", "email": "ada@example.test", fieldCustomerGroupIDs: []string{"custgrp_vip", "custgrp_gone"}}},
		EntityCustomerGroup: {
			{fieldID: "custgrp_vip", fieldName: "VIP"},
			{fieldID: "custgrp_trade", fieldName: "Trade"},
			{fieldID: "custgrp_staff", fieldName: "Staff"},
		},
	}}
}

// membershipPanel is a panel over the catalog with the customer surface.
func membershipPanel(t *testing.T, catalog Catalog, memberships GroupMembership) *UI {
	t.Helper()

	panel := newCatalogPanel(t, catalog)
	panel.memberships = memberships
	panel.scopes = builtInScopes()

	return panel
}

// TestTheCustomerPageNamesTheirGroups is ADR 0322: the customer's groups in
// their rank order, by name or by id when the group is gone; a writer may
// take the customer out of each and is offered the groups they are not in; a
// reader is offered nothing and not read the shop's groups.
func TestTheCustomerPageNamesTheirGroups(t *testing.T) {
	t.Parallel()

	catalog := membershipCatalog()
	panel := membershipPanel(t, catalog, &fakeMemberships{})
	page := CustomersPath + "/cus_1"

	rec := campaignsRequest(panel, http.MethodGet, page, nil, scopeCustomerRead, scopeCustomerWrite)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	vip, gone := strings.Index(body, "<td>VIP</td>"), strings.Index(body, "<td>custgrp_gone</td>")
	assert.True(t, vip >= 0 && gone > vip, "the groups in the record's order, a gone one by its id")
	assert.Contains(t, body, `action="`+page+`/groups/custgrp_vip/remove"`)
	assert.Contains(t, body, `<option value="custgrp_trade">Trade</option>`)
	assert.Contains(t, body, `<form method="post" action="`+page+`/groups">`, "the form puts the customer into a group")
	assert.NotContains(t, body, `<option value="custgrp_vip">`, "a group the customer is in is not offered")
	var asked bool
	for _, spec := range catalog.specs {
		if spec.Entity == EntityCustomer {
			asked = asked || slices.Contains(spec.Fields, fieldCustomerGroupIDs)
		}
	}
	assert.True(t, asked, "the customer is read with their groups; the fake hands back fields nobody asked for")

	before := len(catalog.specs)
	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopeCustomerRead)
	body = rec.Body.String()
	assert.Contains(t, body, "<td>VIP</td>")
	assert.NotContains(t, body, "/remove\"", "a reader takes nobody out")
	assert.NotContains(t, body, "Put into the group")
	listed := 0
	for _, spec := range catalog.specs[before:] {
		if spec.Entity == EntityCustomerGroup && spec.Filters == nil {
			listed++
		}
	}
	assert.Zero(t, listed, "the shop's groups are not listed for a reader")

	rec = campaignsRequest(membershipPanel(t, membershipCatalog(), nil), http.MethodGet, page, nil,
		scopeCustomerRead, scopeCustomerWrite)
	assert.NotContains(t, rec.Body.String(), "Put into the group", "no surface, no form")

	failing := membershipCatalog()
	failing.errByEntity = map[string]error{EntityCustomerGroup: errors.Unavailable("db_down", "no")}
	rec = campaignsRequest(membershipPanel(t, failing, &fakeMemberships{}), http.MethodGet, page, nil,
		scopeCustomerRead, scopeCustomerWrite)
	require.Equal(t, http.StatusOK, rec.Code, "the customer stands when the groups cannot be read")
	assert.Contains(t, rec.Body.String(), "The groups' names could not be read; they are shown by id.")
	assert.Contains(t, rec.Body.String(), "<td>custgrp_vip</td>")
	assert.Contains(t, rec.Body.String(), "The customer groups could not be read, so none is offered.")
}

// TestACustomerIsPutIntoAndTakenOutOfGroups: the surface is asked for the
// customer and the group in the request, the page comes back; a refusal is
// drawn on it, to a writer who cannot read as the reason alone, and a
// failure is not a refusal.
func TestACustomerIsPutIntoAndTakenOutOfGroups(t *testing.T) {
	t.Parallel()

	memberships := &fakeMemberships{}
	panel := membershipPanel(t, membershipCatalog(), memberships)
	page := CustomersPath + "/cus_1"
	writer := []string{scopeCustomerRead, scopeCustomerWrite}

	rec := campaignsRequest(panel, http.MethodPost, page+"/groups", url.Values{formCustomerGroup: {" custgrp_trade "}}, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, page, rec.Header().Get("Location"))
	assert.Equal(t, []string{"cus_1|custgrp_trade"}, memberships.added)

	rec = campaignsRequest(panel, http.MethodPost, page+"/groups/custgrp_vip/remove", url.Values{}, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"cus_1|custgrp_vip"}, memberships.removed)

	rec = campaignsRequest(panel, http.MethodPost, page+"/groups", url.Values{formCustomerGroup: {" "}}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "Choose a customer group.")
	assert.Len(t, memberships.added, 1, "nothing is written without a group")

	memberships.err = errors.NotFound("customer_group_membership_not_found", "customer cus_1 is not in group custgrp_staff")
	rec = campaignsRequest(panel, http.MethodPost, page+"/groups/custgrp_staff/remove", url.Values{}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "customer cus_1 is not in group custgrp_staff")
	assert.Contains(t, rec.Body.String(), "<td>VIP</td>", "the refusal is drawn on the page")
	rec = campaignsRequest(panel, http.MethodPost, page+"/groups/custgrp_staff/remove", url.Values{}, scopeCustomerWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.NotContains(t, rec.Body.String(), "ada@example.test", "a writer who cannot read is shown none of the customer")

	memberships.err = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, page+"/groups", url.Values{formCustomerGroup: {"custgrp_trade"}}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	rec = campaignsRequest(membershipPanel(t, membershipCatalog(), nil), http.MethodPost, page+"/groups",
		url.Values{formCustomerGroup: {"custgrp_trade"}}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "no surface, no write")
}
