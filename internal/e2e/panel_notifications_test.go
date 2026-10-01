//go:build integration

package e2e

import (
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
)

// panelDeliveryFailure is the reason the test writes on a delivery it fails.
const panelDeliveryFailure = "e2e: the mail server did not answer"

// TestAnOperatorSendsAnOrdersConfirmationAgainInThePanel is ADR 0317 on the
// production wiring: an order's confirmation, written by the notification
// module when the order was placed, is found on the Notifications screen by
// the order's id through the module's registered surface; once it has failed
// it carries the reason and a button, and the button sends it again and
// returns to the order's list, which says so.
func TestAnOperatorSendsAnOrdersConfirmationAgainInThePanel(t *testing.T) {
	ctx := t.Context()
	token := jetonAl(t, adminEmail, adminPassword)
	orderID, _, _ := notificationOrder(ctx, t, "E2E Panel Notification Product")
	record := awaitNotification(t, token, orderID)

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_support", Kind: "user", Scopes: []string{"notification:read", "notification:write"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	ordersList := adminui.NotificationsPath + "?reference=" + orderID
	button := `action="` + adminui.NotificationsPath + "/" + record.ID + `/resend"`
	listed := send(http.MethodGet, ordersList, nil)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	body := listed.Body.String()
	assert.Contains(t, body, "1 for "+orderID)
	assert.Contains(t, body, "<td>order.placed</td>")
	assert.Contains(t, body, "<td>"+record.Channel+"</td>")
	assert.Contains(t, body, "<td>"+orderID+"</td>")
	assert.Contains(t, body, "<td>"+record.Status, "the status the module wrote")
	assert.NotContains(t, body, "0001-01-01", "the last change is the record's")
	assert.NotContains(t, body, button, "a sent confirmation is not offered again")

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE notification_deliveries SET status = 'failed', error = $2 WHERE id = $1`,
		record.ID, panelDeliveryFailure)
	require.NoError(t, err)
	failed := send(http.MethodGet, ordersList, nil).Body.String()
	assert.Contains(t, failed, panelDeliveryFailure, "a failed delivery carries the provider's reason")
	require.Contains(t, failed, button, "a failed confirmation is offered again")
	assert.Contains(t, failed, `<input type="hidden" name="reference" value="`+orderID+`">`,
		"the button returns to the order's list")

	resent := send(http.MethodPost, adminui.NotificationsPath+"/"+record.ID+"/resend",
		url.Values{"status": {"failed"}, "reference": {orderID}})
	require.Equal(t, http.StatusSeeOther, resent.Code, resent.Body.String())
	back := resent.Header().Get("Location")
	assert.Contains(t, back, "reference="+orderID, "it returns to the order's list")

	after := send(http.MethodGet, back, nil).Body.String()
	assert.Contains(t, after, "Delivery "+record.ID+" was sent again.")
	assert.Contains(t, after, "<td>sent</td>")
	assert.NotContains(t, after, button)
}
