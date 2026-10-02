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

// fakeCloser acts on after-sales records as the shared fake does and
// completes and archives orders, recording each move.
type fakeCloser struct {
	fakeAfterSales
	moves []string
	err   error
}

func (f *fakeCloser) CompleteOrder(_ context.Context, orderID string) error {
	f.moves = append(f.moves, "complete|"+orderID)
	return f.err
}

func (f *fakeCloser) ArchiveOrder(_ context.Context, orderID string) error {
	f.moves = append(f.moves, "archive|"+orderID)
	return f.err
}

// TestAnOrderIsCompletedAndArchivedOnItsPage is ADR 0340: a writer is
// offered the completion on a pending order, a reader is not; the order is
// completed and archived through the surface, the page saying each; the
// module's refusal is drawn on the order; a surface that cannot close an
// order offers nothing and answers 503.
func TestAnOrderIsCompletedAndArchivedOnItsPage(t *testing.T) {
	t.Parallel()

	closer := &fakeCloser{}
	panel := cancelPanel(t, closer)
	page := OrdersPath + "/order_1"
	writer := []string{scopeOrderRead, scopeOrderWrite}

	rec := campaignsRequest(panel, http.MethodGet, page, nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `action="`+page+`/complete"`, "a pending order is offered the completion")
	assert.NotContains(t, rec.Body.String(), `action="`+page+`/archive"`, "and not the archive")
	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopeOrderRead)
	assert.NotContains(t, rec.Body.String(), `action="`+page+`/complete"`, "a reader moves nothing")

	rec = campaignsRequest(panel, http.MethodPost, page+"/complete", url.Values{}, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "The order was marked completed.")
	rec = campaignsRequest(panel, http.MethodPost, page+"/archive", url.Values{}, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "The order was archived; it leaves the daily lists.")
	assert.Equal(t, []string{"complete|order_1", "archive|order_1"}, closer.moves)

	closer.err = errors.Conflict("order_invalid_transition", "order order_1 is completed; it cannot be completed again")
	rec = campaignsRequest(panel, http.MethodPost, page+"/complete", url.Values{}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "it cannot be completed again")
	assert.Contains(t, rec.Body.String(), "Silver ring", "the refusal is drawn on the order")
	closer.err = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, page+"/archive", url.Values{}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	bare := cancelPanel(t, &fakeAfterSales{})
	rec = campaignsRequest(bare, http.MethodGet, page, nil, writer...)
	assert.NotContains(t, rec.Body.String(), "/complete\"", "a surface that cannot close an order offers nothing")
	rec = campaignsRequest(bare, http.MethodPost, page+"/complete", url.Values{}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestEachStatusIsOfferedItsOneMove: a pending order is completed, a
// completed one archived, and a canceled or archived one is offered neither.
func TestEachStatusIsOfferedItsOneMove(t *testing.T) {
	t.Parallel()

	panel := cancelPanel(t, &fakeCloser{})
	r := (&http.Request{}).WithContext(corehttp.WithPrincipal(context.Background(),
		corehttp.Principal{ID: "user_1", Kind: "user", Scopes: []string{scopeOrderRead, scopeOrderWrite}}))
	for status, move := range map[string]string{"pending": "complete", "completed": "archive", "canceled": "", "archived": ""} {
		assert.Equal(t, move, panel.orderCloseMove(r, status), status)
	}
}
