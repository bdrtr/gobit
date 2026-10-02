package adminui

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// fakeCanceler acts on after-sales records as the shared fake does and
// cancels orders, recording each cancel.
type fakeCanceler struct {
	fakeAfterSales
	canceled []string
	err      error
}

func (f *fakeCanceler) CancelOrder(_ context.Context, orderID, reason string) error {
	f.canceled = append(f.canceled, orderID+"|"+reason)
	return f.err
}

// cancelPanel is a panel over the pending order with the canceling surface.
func cancelPanel(t *testing.T, afterSales AfterSalesAdmin) *UI {
	t.Helper()

	panel := newCatalogPanel(t, linkedOrderCatalog())
	panel.afterSales = afterSales
	panel.scopes = builtInScopes()

	return panel
}

// TestAPendingOrderIsCanceledOnItsPage is ADR 0339: a writer is offered the
// cancel on a pending order, a reader is not; the order is canceled with the
// reason typed, and the page says its units were written off; the module's
// refusal, an order with money collected, is drawn on the order; a surface
// that cannot cancel offers nothing and answers 503.
func TestAPendingOrderIsCanceledOnItsPage(t *testing.T) {
	t.Parallel()

	canceler := &fakeCanceler{}
	panel := cancelPanel(t, canceler)
	page := OrdersPath + "/order_1"
	writer := []string{scopeOrderRead, scopeOrderWrite}

	rec := campaignsRequest(panel, http.MethodGet, page, nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `action="`+page+`/cancel"`, "a pending order is offered the cancel")
	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopeOrderRead)
	assert.NotContains(t, rec.Body.String(), `action="`+page+`/cancel"`, "a reader cancels nothing")

	rec = campaignsRequest(panel, http.MethodPost, page+"/cancel", url.Values{formCancelReason: {" never paid "}}, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"order_1|never paid"}, canceler.canceled, "with the reason typed, trimmed")
	assert.Contains(t, rec.Body.String(), "The order was canceled; its units were written off and their stock comes back.")

	canceler.err = errors.Conflict("order_money_collected", "order order_1 has money collected; it cannot be canceled")
	rec = campaignsRequest(panel, http.MethodPost, page+"/cancel", url.Values{}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "it cannot be canceled")
	assert.Contains(t, rec.Body.String(), "Silver ring", "the refusal is drawn on the order")
	canceler.err = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, page+"/cancel", url.Values{}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	bare := cancelPanel(t, &fakeAfterSales{})
	rec = campaignsRequest(bare, http.MethodGet, page, nil, writer...)
	assert.NotContains(t, rec.Body.String(), "/cancel\"", "a surface that cannot cancel offers nothing")
	rec = campaignsRequest(bare, http.MethodPost, page+"/cancel", url.Values{}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestOnlyAPendingOrderIsOfferedTheCancel: the cancel is offered on a pending
// order alone; a completed or a canceled one is not offered it.
func TestOnlyAPendingOrderIsOfferedTheCancel(t *testing.T) {
	t.Parallel()

	panel := cancelPanel(t, &fakeCanceler{})
	for status, offered := range map[string]bool{"pending": true, "completed": false, "canceled": false, "archived": false} {
		r := (&http.Request{}).WithContext(corehttp.WithPrincipal(context.Background(),
			corehttp.Principal{ID: "user_1", Kind: "user", Scopes: []string{scopeOrderRead, scopeOrderWrite}}))
		assert.Equal(t, offered, panel.canCancelOrder(r, status), status)
	}
}
