package adminui

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// fakeLineCanceler acts on after-sales records as the shared fake does and
// writes off lines, recording each write-off.
type fakeLineCanceler struct {
	fakeAfterSales
	written []string
	err     error
}

func (f *fakeLineCanceler) CancelOrderLine(
	_ context.Context, orderID, lineID string, readSpokenFor, quantity int64, reason, note string,
) error {
	f.written = append(f.written, fmt.Sprintf("%s|%s|%d|%d|%s|%s", orderID, lineID, readSpokenFor, quantity, reason, note))
	return f.err
}

// writeOffCatalog is the order with a ring of which one of three units was
// asked back and one written off, a ring fully written off, and a gift card.
func writeOffCatalog() *fakeCatalog {
	catalog := linkedOrderCatalog()
	catalog.byEntity[EntityOrderLineItem] = []query.Record{
		{"id": "oli_ring", "title": "Silver ring", "quantity": int64(3), fieldAskedBack: int64(1), fieldCanceledQuantity: int64(1)},
		{"id": "oli_gone", "title": "Gold ring", "quantity": int64(1), fieldCanceledQuantity: int64(1)},
		{"id": "oli_card", "title": "Gift card", "quantity": int64(1), fieldIsGiftcard: true},
	}

	return catalog
}

// writeOffForm is the line's write-off form, empty when the line has none.
func writeOffForm(body, lineID string) string {
	_, form, found := strings.Cut(body, `name="line" value="`+lineID+`"`)
	if !found {
		return ""
	}
	form, _, _ = strings.Cut(form, "</form>")

	return form
}

// TestALinesUnitsAreWrittenOffOnItsOrdersPage is ADR 0341: a writer is
// offered the write-off on each line of a pending order with units left,
// carrying how many were spoken for when the page was drawn, and not on a
// line with none left or a gift card line; the units are written off with
// the reason and the note, and the page says so; what the panel cannot read
// is not sent, and the module's refusal, a line that moved, is drawn on the
// order.
func TestALinesUnitsAreWrittenOffOnItsOrdersPage(t *testing.T) {
	t.Parallel()

	canceler := &fakeLineCanceler{}
	panel := newCatalogPanel(t, writeOffCatalog())
	panel.afterSales = canceler
	panel.scopes = builtInScopes()
	page := OrdersPath + "/order_1"
	writer := []string{scopeOrderRead, scopeOrderWrite}

	rec := campaignsRequest(panel, http.MethodGet, page, nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	form := writeOffForm(body, "oli_ring")
	require.NotEmpty(t, form, "a line with units left is offered the write-off")
	assert.Contains(t, form, `name="read_spoken_for" value="2"`, "one asked back and one written off")
	assert.Contains(t, body, `action="`+page+`/line-cancellations"`)
	assert.Empty(t, writeOffForm(body, "oli_gone"), "a line with none left is offered nothing")
	assert.Empty(t, writeOffForm(body, "oli_card"), "a gift card line is closed in the payment module")
	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopeOrderRead)
	assert.NotContains(t, rec.Body.String(), "/line-cancellations", "a reader writes off nothing")

	rec = campaignsRequest(panel, http.MethodPost, page+"/line-cancellations", url.Values{
		formWriteOffLine: {"oli_ring"}, formReadSpokenFor: {"2"}, formWriteOffQuantity: {" 1 "},
		formWriteOffReason: {" out of stock "}, formWriteOffNote: {" supplier late "},
	}, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"order_1|oli_ring|2|1|out of stock|supplier late"}, canceler.written)
	assert.Contains(t, rec.Body.String(), "1 units were written off; what no parcel holds comes back to the shelf.")

	for reason, typed := range map[string]url.Values{
		"The line the page was drawn with could not be read": {formWriteOffLine: {"oli_ring"}, formReadSpokenFor: {"two"}, formWriteOffQuantity: {"1"}},
		"The units written off are a whole number.":          {formWriteOffLine: {"oli_ring"}, formReadSpokenFor: {"2"}, formWriteOffQuantity: {"1.5"}},
	} {
		rec = campaignsRequest(panel, http.MethodPost, page+"/line-cancellations", typed, writer...)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, reason)
		assert.Contains(t, rec.Body.String(), reason)
	}
	assert.Len(t, canceler.written, 1, "what the panel cannot read is not sent")

	canceler.err = errors.Conflict("order_line_moved",
		"order line oli_ring has 3 units asked back or written off now, not 2; draw the page again")
	rec = campaignsRequest(panel, http.MethodPost, page+"/line-cancellations", url.Values{
		formWriteOffLine: {"oli_ring"}, formReadSpokenFor: {"2"}, formWriteOffQuantity: {"1"}, formWriteOffReason: {"again"},
	}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "draw the page again")
	canceler.err = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, page+"/line-cancellations", url.Values{
		formWriteOffLine: {"oli_ring"}, formReadSpokenFor: {"2"}, formWriteOffQuantity: {"1"},
	}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	bare := cancelPanel(t, &fakeAfterSales{})
	rec = campaignsRequest(bare, http.MethodPost, page+"/line-cancellations", url.Values{}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestOnlyAPendingOrdersLinesAreWrittenOff: the write-off is offered on a
// pending order's lines alone; a completed, canceled or archived order's
// lines are past changing.
func TestOnlyAPendingOrdersLinesAreWrittenOff(t *testing.T) {
	t.Parallel()

	panel := cancelPanel(t, &fakeLineCanceler{})
	r := (&http.Request{}).WithContext(corehttp.WithPrincipal(context.Background(),
		corehttp.Principal{ID: "user_1", Kind: "user", Scopes: []string{scopeOrderRead, scopeOrderWrite}}))
	for status, offered := range map[string]bool{"pending": true, "completed": false, "canceled": false, "archived": false} {
		assert.Equal(t, offered, panel.canWriteOff(r, status), status)
	}
}
