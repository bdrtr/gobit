//go:build integration

package e2e

import (
	"net/http"
	"net/url"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/adminui"
	fulfillmentsvc "github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// quotedChoice finds the option's choice in the delivery form: the value it
// carries and the label beside it.
func quotedChoice(t *testing.T, page, optionID string) (value, label string) {
	t.Helper()

	match := regexp.MustCompile(`<option value="(` + regexp.QuoteMeta(optionID) + `\|\d+)">([^<]*)</option>`).
		FindStringSubmatch(page)
	require.Len(t, match, 3, "the delivery form offers %s", optionID)

	return match[1], match[2]
}

// TestAnOperatorChangesADeliveryInThePanel is ADR 0388 on the production
// wiring: the order's page lists an admin-only cheaper option at its price
// through the order module's surface and the fulfilling flow; the change goes
// through and its credit is listed with the order's credits; the same form
// again changes nothing; a dearer option with no collection is refused; and a
// price that moved after the page was drawn refuses the form.
func TestAnOperatorChangesADeliveryInThePanel(t *testing.T) {
	sold := spyOptionPriced(t, soldDeliveryFee, false)
	orderID, methodID := deliveryOrder(t, sold)
	pickup := spyOptionPriced(t, 1_000, true)
	sameDay := spyOptionPriced(t, soldDeliveryFee+500, false)
	send := orderWriter(t)
	pagePath := adminui.OrdersPath + "/" + orderID

	page := send(http.MethodGet, pagePath, nil).Body.String()
	assert.Contains(t, page, "<h2>Deliveries</h2>")
	assert.Contains(t, page, `action="`+pagePath+`/deliveries/`+methodID+`"`)
	value, label := quotedChoice(t, page, pickup)
	assert.Equal(t, pickup+"|1000", value, "the admin-only option at its price")
	assert.Contains(t, label, "(credits 20.00 TRY)")
	assert.NotContains(t, page, `value="`+sold+`|`, "the option the delivery stands on is not offered")
	before := readCredited.FindStringSubmatch(page)
	require.Len(t, before, 2)

	form := url.Values{"option": {value}, "currency": {taxedCurrency}}
	changed := send(http.MethodPost, pagePath+"/deliveries/"+methodID, form)
	require.Equal(t, http.StatusOK, changed.Code, changed.Body.String())
	assert.Contains(t, changed.Body.String(), "20.00 TRY was credited against shipping")
	assert.Contains(t, changed.Body.String(), "delivery_change", "the change's credit is listed")
	after := readCredited.FindStringSubmatch(changed.Body.String())
	require.Len(t, after, 2)
	assert.Equal(t, "2000", after[1], "and the credit form carries it")

	again := send(http.MethodPost, pagePath+"/deliveries/"+methodID, form)
	require.Equal(t, http.StatusOK, again.Code, again.Body.String())
	assert.Contains(t, again.Body.String(), "nothing changed")

	dearer, _ := quotedChoice(t, changed.Body.String(), sameDay)
	refused := send(http.MethodPost, pagePath+"/deliveries/"+methodID, url.Values{"option": {dearer}})
	require.Equal(t, http.StatusUnprocessableEntity, refused.Code, refused.Body.String())
	assert.Contains(t, refused.Body.String(), "collect the difference")

	cheaper := spyOptionPriced(t, 500, true)
	drawn, _ := quotedChoice(t, send(http.MethodGet, pagePath, nil).Body.String(), cheaper)
	moved := int64(700)
	_, err := shippingSvc.UpdateShippingOption(t.Context(), cheaper, fulfillmentsvc.UpdateOptionInput{Amount: &moved})
	require.NoError(t, err)
	stale := send(http.MethodPost, pagePath+"/deliveries/"+methodID, url.Values{"option": {drawn}})
	require.Equal(t, http.StatusUnprocessableEntity, stale.Code, stale.Body.String())
	assert.Contains(t, stale.Body.String(), "draw the page again", "a quote that moved refuses the form")
	method := onlyShippingMethod(t, orderID)
	changes, ok := method["changes"].([]any)
	require.True(t, ok)
	assert.Len(t, changes, 1, "only the first change was written")
}
