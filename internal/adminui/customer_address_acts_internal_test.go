package adminui

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// fakeAddressActs adds addresses as fakeAddressAdder does and moves defaults
// and removes addresses, recording each act.
type fakeAddressActs struct {
	fakeAddressAdder
	acts   []string
	actErr error
}

func (f *fakeAddressActs) MakeAddressDefault(_ context.Context, customerID, addressID, kind string) error {
	f.acts = append(f.acts, "default|"+customerID+"|"+addressID+"|"+kind)
	return f.actErr
}

func (f *fakeAddressActs) RemoveCustomerAddress(_ context.Context, customerID, addressID string) error {
	f.acts = append(f.acts, "remove|"+customerID+"|"+addressID)
	return f.actErr
}

// rowHolding is the table row of the page that holds the marker.
func rowHolding(t *testing.T, body, marker string) string {
	t.Helper()

	at := strings.Index(body, marker)
	require.GreaterOrEqual(t, at, 0, "the page holds %s", marker)
	start := strings.LastIndex(body[:at], "<tr>")
	end := strings.Index(body[at:], "</tr>")
	require.True(t, start >= 0 && end >= 0)

	return body[start : at+end]
}

// TestAnAddressIsMadeDefaultOrRemovedOnTheCustomersPage is ADR 0360: a
// writer whose surface can act is offered, on each address's row, the
// defaults it does not hold and its removal, a reader nothing; the surface
// is asked to move the default named or remove the address, and the page
// says which; a refusal comes back on the page.
func TestAnAddressIsMadeDefaultOrRemovedOnTheCustomersPage(t *testing.T) {
	t.Parallel()

	acts := &fakeAddressActs{}
	panel := membershipPanel(t, addressCatalog(), acts)
	page := CustomersPath + "/cus_1"
	writer := []string{scopeCustomerRead, scopeCustomerWrite}

	rec := campaignsRequest(panel, http.MethodGet, page, nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	shipping := rowHolding(t, body, page+"/addresses/cadr_1/remove")
	assert.NotContains(t, shipping, `value="shipping"`, "the default shipping address is not offered it again")
	assert.Contains(t, shipping, `<input type="hidden" name="kind" value="billing"><button type="submit">Make default billing</button>`)
	other := rowHolding(t, body, page+"/addresses/cadr_2/remove")
	assert.Contains(t, other, `value="shipping"><button type="submit">Make default shipping</button>`)
	assert.Contains(t, other, `value="billing"><button type="submit">Make default billing</button>`)
	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopeCustomerRead)
	assert.NotContains(t, rec.Body.String(), "/remove", "a reader acts on nothing")
	assert.NotContains(t, rec.Body.String(), "/default")

	rec = campaignsRequest(panel, http.MethodPost, page+"/addresses/cadr_2/default", url.Values{formDefaultKind: {"shipping"}},
		writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, page+"?written=default", rec.Header().Get("Location"))
	assert.Contains(t, campaignsRequest(panel, http.MethodGet, rec.Header().Get("Location"), nil, scopeCustomerRead).Body.String(),
		"The default address was moved.")
	rec = campaignsRequest(panel, http.MethodPost, page+"/addresses/cadr_2/default", url.Values{formDefaultKind: {"billing"}},
		writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	rec = campaignsRequest(panel, http.MethodPost, page+"/addresses/cadr_1/remove", nil, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, page+"?written=removed", rec.Header().Get("Location"))
	assert.Contains(t, campaignsRequest(panel, http.MethodGet, rec.Header().Get("Location"), nil, scopeCustomerRead).Body.String(),
		"The address was removed.")
	assert.Equal(t, []string{"default|cus_1|cadr_2|shipping", "default|cus_1|cadr_2|billing", "remove|cus_1|cadr_1"},
		acts.acts, "the default the form names")

	acts.actErr = errors.NotFound("customer_address_not_found", "address cadr_9 not found")
	rec = campaignsRequest(panel, http.MethodPost, page+"/addresses/cadr_9/remove", nil, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "address cadr_9 not found")
	acts.actErr = errors.Invalid("customer_invalid_input", `a default is "shipping" or "billing", not "gift"`)
	rec = campaignsRequest(panel, http.MethodPost, page+"/addresses/cadr_2/default", url.Values{formDefaultKind: {"gift"}},
		writer...)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	acts.actErr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, page+"/addresses/cadr_2/remove", nil, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	rec = campaignsRequest(panel, http.MethodPost, page+"/addresses/cadr_2/remove", nil, scopeCustomerRead)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	plain := membershipPanel(t, addressCatalog(), &fakeAddressAdder{})
	assert.NotContains(t, campaignsRequest(plain, http.MethodGet, page, nil, writer...).Body.String(), "/remove")
	assert.Equal(t, http.StatusServiceUnavailable,
		campaignsRequest(plain, http.MethodPost, page+"/addresses/cadr_2/remove", nil, writer...).Code)
}
