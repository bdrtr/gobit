//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
	customersvc "github.com/bdrtr/gobit/internal/modules/customer/service"
)

// TestAnOperatorPutsACustomerIntoAGroupInThePanel is ADR 0322 on the
// production wiring: a customer's page offers the shop's groups through the
// customer module's group entity, the form puts the customer into one through
// the registered `customer.admin` surface, the page names it, the remove
// button takes the customer out, and a second press is refused on the page.
func TestAnOperatorPutsACustomerIntoAGroupInThePanel(t *testing.T) {
	ctx := t.Context()
	group, err := customerSvc.CreateGroup(ctx, customersvc.GroupInput{
		Name: fmt.Sprintf("E2E Panel Trade %d", fixtureCounter.Add(1)),
	})
	require.NoError(t, err)
	customerID, email := newCustomer(ctx, t)

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_support", Kind: "user", Scopes: []string{"customer:read", "customer:write"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	pagePath := adminui.CustomersPath + "/" + customerID
	page := send(http.MethodGet, pagePath, nil).Body.String()
	assert.Contains(t, page, "The customer is in no group.")
	assert.Contains(t, page, `<option value="`+group.ID+`">`+group.Name+`</option>`, "the shop's groups are offered")

	joined := send(http.MethodPost, pagePath+"/groups", url.Values{"group_id": {group.ID}})
	require.Equal(t, http.StatusSeeOther, joined.Code, joined.Body.String())
	groups, err := customerSvc.ListGroupsOf(ctx, customerID)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, group.ID, groups[0].ID)
	page = send(http.MethodGet, pagePath, nil).Body.String()
	assert.Contains(t, page, "<td>"+group.Name+"</td>", "the page names the group")
	assert.NotContains(t, page, `<option value="`+group.ID+`">`, "a group the customer is in is not offered")

	left := send(http.MethodPost, pagePath+"/groups/"+group.ID+"/remove", url.Values{})
	require.Equal(t, http.StatusSeeOther, left.Code, left.Body.String())
	groups, err = customerSvc.ListGroupsOf(ctx, customerID)
	require.NoError(t, err)
	assert.Empty(t, groups)

	again := send(http.MethodPost, pagePath+"/groups/"+group.ID+"/remove", url.Values{})
	require.Equal(t, http.StatusUnprocessableEntity, again.Code, again.Body.String())
	assert.Contains(t, again.Body.String(), email, "the refusal is drawn on the customer's page")
	assert.Contains(t, again.Body.String(), "customer "+customerID+" is not a member of group "+group.ID,
		"the module's reason, in the panel's language")
}
