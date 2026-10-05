package adminui

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
)

// The telephone order's cart is priced in the channel it was opened in (ADR
// 0397).

// TestTheOpenFormNamesTheChannelTheCartIsPricedIn: the channel the operator
// picks reaches the surface trimmed, and a form without one asks for none.
func TestTheOpenFormNamesTheChannelTheCartIsPricedIn(t *testing.T) {
	t.Parallel()

	carts := &fakeCarts{}
	panel := newCatalogPanel(t, phoneCatalog(false))
	panel.carts = carts

	rec := phoneRequest(panel, http.MethodPost, CartsPath, url.Values{
		formCountryCode: {"TR"}, formEmail: {"caller@example.com"}, formSalesChannelID: {" sc_phone "},
	}, scopeCartWrite)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())

	rec = phoneRequest(panel, http.MethodPost, CartsPath, url.Values{
		formCountryCode: {"TR"}, formEmail: {"caller@example.com"},
	}, scopeCartWrite)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())

	assert.Equal(t, []string{"sc_phone", ""}, carts.openedIn)
}

// TestTheOpenFormOffersTheChannelsByName: an operator who may read the sales
// channels picks one by name or none, and one who may not types an id or
// leaves it blank, reading nothing of the channels (ADR 0260).
func TestTheOpenFormOffersTheChannelsByName(t *testing.T) {
	t.Parallel()

	withChannels := func() *fakeCatalog {
		return &fakeCatalog{byEntity: map[string][]query.Record{EntitySalesChannel: {
			{fieldID: "sc_web", fieldChannelName: "Web shop"},
			{fieldID: "sc_phone", fieldChannelName: "Call center"},
		}}}
	}
	channelReads := func(catalog *fakeCatalog) int {
		return len(slices.DeleteFunc(slices.Clone(catalog.specs), func(spec query.GraphSpec) bool {
			return spec.Entity != EntitySalesChannel
		}))
	}

	reader := withChannels()
	panel := newCatalogPanel(t, reader)
	panel.carts = &fakeCarts{}
	page := phoneRequest(panel, http.MethodGet, CartsPath, nil, scopeCartWrite, scopeAuthRead)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	body := page.Body.String()
	assert.Contains(t, body, `<select name="sales_channel_id"`)
	assert.Contains(t, body, `<option value="">no channel — channel prices do not apply</option>`)
	assert.Contains(t, body, `<option value="sc_phone">Call center</option>`)
	assert.Equal(t, 1, channelReads(reader))

	blind := withChannels()
	panel = newCatalogPanel(t, blind)
	panel.carts = &fakeCarts{}
	page = phoneRequest(panel, http.MethodGet, CartsPath, nil, scopeCartWrite)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), `<input name="sales_channel_id"`)
	assert.NotContains(t, page.Body.String(), `<select name="sales_channel_id"`)
	assert.Zero(t, channelReads(blind), "the channels are read under auth:read alone")
}

// TestTheCartPageSaysItsChannelAndChoosesIt: the page reads the cart's
// channel, says it, and the line and completion forms start on it; a cart in
// none says so and chooses nothing.
func TestTheCartPageSaysItsChannelAndChoosesIt(t *testing.T) {
	t.Parallel()

	catalog := phoneCatalog(false)
	catalog.byEntity[EntityCart][0][FieldCartSalesChannel] = "sc_phone"
	catalog.byEntity[EntitySalesChannel] = []query.Record{
		{fieldID: "sc_web", fieldChannelName: "Web shop"},
		{fieldID: "sc_phone", fieldChannelName: "Call center"},
	}
	panel := newCatalogPanel(t, catalog)
	panel.carts = &fakeCarts{}

	page := phoneRequest(panel, http.MethodGet, CartsPath+"/cart_phone", nil, scopeCartRead, scopeCartWrite, scopeAuthRead)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	body := page.Body.String()
	assert.Contains(t, body, "Priced in channel sc_phone")
	assert.Equal(t, 2, strings.Count(body, `<option value="sc_phone" selected>Call center</option>`),
		"the line form and the completion form start on the cart's channel")
	var asked bool
	for _, spec := range catalog.specs {
		if spec.Entity == EntityCart && slices.Contains(spec.Fields, FieldCartSalesChannel) {
			asked = true
		}
	}
	assert.True(t, asked, "the cart's read asks for its channel")

	blind := phoneRequest(panel, http.MethodGet, CartsPath+"/cart_phone", nil, scopeCartRead, scopeCartWrite)
	require.Equal(t, http.StatusOK, blind.Code, blind.Body.String())
	assert.Equal(t, 2, strings.Count(blind.Body.String(), `<input name="sales_channel_id" value="sc_phone"`),
		"the id box starts on the cart's channel too")

	// A refused line comes back on the channel the operator typed, not the
	// cart's: a resubmission writes under the channel they chose.
	refused := phoneRequest(panel, http.MethodPost, CartsPath+"/cart_phone/lines", url.Values{
		formSalesChannelID: {"sc_web"}, formVariantID: {"variant_shirt"}, formQuantity: {"two"},
	}, scopeCartRead, scopeCartWrite, scopeAuthRead)
	require.Equal(t, http.StatusUnprocessableEntity, refused.Code, refused.Body.String())
	assert.Contains(t, refused.Body.String(), `<option value="sc_web" selected>Web shop</option>`)
	assert.NotContains(t, refused.Body.String(), `<option value="sc_phone" selected>`,
		"the cart's channel does not overwrite the one typed")

	refused = phoneRequest(panel, http.MethodPost, CartsPath+"/cart_phone/lines", url.Values{
		formSalesChannelID: {"sc_web"}, formVariantID: {"variant_shirt"}, formQuantity: {"two"},
	}, scopeCartRead, scopeCartWrite)
	require.Equal(t, http.StatusUnprocessableEntity, refused.Code, refused.Body.String())
	assert.Contains(t, refused.Body.String(), `<input name="sales_channel_id" value="sc_web"`)
	assert.NotContains(t, refused.Body.String(), `<input name="sales_channel_id" value="sc_phone"`,
		"the id box keeps the one typed too")

	none := newCatalogPanel(t, phoneCatalog(false))
	none.carts = &fakeCarts{}
	page = phoneRequest(none, http.MethodGet, CartsPath+"/cart_phone", nil, scopeCartRead, scopeCartWrite)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), "Priced in no channel: channel prices do not apply")
	assert.Equal(t, 2, strings.Count(page.Body.String(), `<input name="sales_channel_id" value=""`))
}
