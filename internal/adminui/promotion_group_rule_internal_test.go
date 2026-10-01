package adminui

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// groupPromotion is an order discount limited to one customer group, one of
// whose ids names no group any longer.
const groupPromotion = `{"id":"promo_1","code":"VIP10","type":"standard","status":"active",
	"campaign":null,"latest_uses":[],
	"application_method":{"type":"percentage","target_type":"order","allocation":"across","value":1000},
	"rules":[{"id":"prule_1","type":"context","attribute":"customer_group_id","operator":"any_in","values":["custgrp_vip","custgrp_gone"]}]}`

// groupsPanel is a panel whose read layer knows the customer groups.
func groupsPanel(t *testing.T, editor PromotionLister, groups ...query.Record) *UI {
	t.Helper()

	panel := newCatalogPanel(t, &fakeCatalog{byEntity: map[string][]query.Record{EntityCustomerGroup: groups}})
	panel.promotions = editor
	panel.scopes = builtInScopes()

	return panel
}

// TestAPromotionIsLimitedToCustomerGroups is ADR 0321: a writer who may read
// the customers sees a group rule's groups by name and is offered the groups;
// the form writes one context rule over the chosen, trimmed; a writer who may
// not read the customers sees the ids and no form, and is not read the groups.
func TestAPromotionIsLimitedToCustomerGroups(t *testing.T) {
	t.Parallel()

	editor := &fakeRuleEditor{fakePromotionReader: fakePromotionReader{page: groupPromotion}}
	catalog := &fakeCatalog{byEntity: map[string][]query.Record{EntityCustomerGroup: {
		{fieldID: "custgrp_vip", fieldName: "VIP"},
		{fieldID: "custgrp_trade", fieldName: "Trade"},
	}}}
	panel := newCatalogPanel(t, catalog)
	panel.promotions = editor
	panel.scopes = builtInScopes()
	page := PromotionsPath + "/promo_1"

	rec := campaignsRequest(panel, http.MethodGet, page, nil, scopePromotionRead, scopePromotionWrite, scopeCustomerRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "<td>VIP, custgrp_gone</td>", "a group named, one that is gone by its id")
	assert.Contains(t, body, `action="`+page+`/rules/customer-groups"`)
	assert.Contains(t, body, `<option value="custgrp_trade">Trade</option>`)
	assert.NotContains(t, body, "Only the newest")
	var read bool
	for _, spec := range catalog.specs {
		if spec.Entity == EntityCustomerGroup {
			read = true
			assert.Equal(t, groupsOffered+1, spec.Limit, "one more than offered, to know there are more")
		}
	}
	assert.True(t, read)

	rec = campaignsRequest(panel, http.MethodPost, page+"/rules/customer-groups",
		url.Values{formGroup: {" custgrp_vip ", "", "custgrp_trade"}}, scopePromotionRead, scopePromotionWrite)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, page, rec.Header().Get("Location"))
	assert.Equal(t, []string{"promo_1|context|customer_group_id|any_in|custgrp_vip,custgrp_trade"}, editor.added)

	rec = campaignsRequest(panel, http.MethodPost, page+"/rules/customer-groups",
		url.Values{formGroup: {" "}}, scopePromotionRead, scopePromotionWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "Choose at least one customer group.")
	assert.Len(t, editor.added, 1, "nothing is written without a group")

	before := len(catalog.specs)
	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopePromotionRead, scopePromotionWrite)
	body = rec.Body.String()
	assert.Contains(t, body, "<td>custgrp_vip, custgrp_gone</td>", "the ids, to an operator who may not read the customers")
	assert.NotContains(t, body, "/rules/customer-groups\"")
	for _, spec := range catalog.specs[before:] {
		assert.NotEqual(t, EntityCustomerGroup, spec.Entity, "the groups are not read for that operator")
	}

	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopePromotionRead, scopeCustomerRead)
	assert.NotContains(t, rec.Body.String(), "/rules/customer-groups\"", "a reader writes no rule")
	assert.Contains(t, rec.Body.String(), "<td>VIP, custgrp_gone</td>")
}

// TestTheGroupFormSaysWhatItCannotOffer: a failed read, more groups than it
// offers, and no group at all are each said.
func TestTheGroupFormSaysWhatItCannotOffer(t *testing.T) {
	t.Parallel()

	writer := []string{scopePromotionRead, scopePromotionWrite, scopeCustomerRead}
	page := PromotionsPath + "/promo_1"
	editor := &fakeRuleEditor{fakePromotionReader: fakePromotionReader{page: groupPromotion}}

	failing := groupsPanel(t, editor)
	failing.catalog = &fakeCatalog{errByEntity: map[string]error{EntityCustomerGroup: errors.Unavailable("db_down", "no")}}
	rec := campaignsRequest(failing, http.MethodGet, page, nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, "the page stands when the groups cannot be read")
	assert.Contains(t, rec.Body.String(), "The customer groups could not be read.")

	many := make([]query.Record, 0, groupsOffered+1)
	for i := range groupsOffered + 1 {
		many = append(many, query.Record{fieldID: fmt.Sprintf("custgrp_%03d", i), fieldName: fmt.Sprintf("Group %d", i)})
	}
	rec = campaignsRequest(groupsPanel(t, editor, many...), http.MethodGet, page, nil, writer...)
	assert.Contains(t, rec.Body.String(), fmt.Sprintf("Only the newest %d groups are offered.", groupsOffered))
	assert.NotContains(t, rec.Body.String(), fmt.Sprintf(`value="custgrp_%03d"`, groupsOffered),
		"the one past the offer is left out")

	rec = campaignsRequest(groupsPanel(t, editor), http.MethodGet, page, nil, writer...)
	assert.Contains(t, rec.Body.String(), "No customer group exists.")
}
