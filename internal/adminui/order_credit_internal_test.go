package adminui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// fakeCreditor acts on after-sales records as the shared fake does, lists
// the scripted credits and records each credit written.
type fakeCreditor struct {
	fakeAfterSales
	credits  string
	readErr  error
	written  []string
	writeErr error
}

func (f *fakeCreditor) CreditLinesJSON(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(f.credits), f.readErr
}

func (f *fakeCreditor) CreditOrder(
	_ context.Context, orderID string, readCredited, amount int64, reason, note string,
) error {
	f.written = append(f.written, fmt.Sprintf("%s|%d|%d|%s|%s", orderID, readCredited, amount, reason, note))
	return f.writeErr
}

// twoCredits are two credits whose sum is neither their count nor the last
// one's amount.
const twoCredits = `{"credited_total":1500,"lines":[
	{"id":"ocl_1","amount":1000,"reason":"goodwill","note":"late","created_at":"2026-10-01T09:00:00Z"},
	{"id":"ocl_2","amount":500,"reason":"delivery_change","note":"odchg_1","created_at":"2026-10-02T09:00:00Z"}]}`

// creditForm is the credit form on the page, empty when there is none.
func creditForm(body string) string {
	_, form, found := strings.Cut(body, `/credit-lines">`)
	if !found {
		return ""
	}
	form, _, _ = strings.Cut(form, "</form>")

	return form
}

// TestAnOrderIsCreditedOnItsPage is ADR 0388: anyone who opens the order
// reads its credits and their total; a writer is offered the form carrying
// the total in minor units; the credit is written with the amount in the
// order's currency, the reason and the note; what the panel cannot read is
// not sent, and the module's refusal, the total having moved, is drawn on the
// order.
func TestAnOrderIsCreditedOnItsPage(t *testing.T) {
	t.Parallel()

	creditor := &fakeCreditor{credits: twoCredits}
	panel := cancelPanel(t, creditor)
	page := OrdersPath + "/order_1"
	writer := []string{scopeOrderRead, scopeOrderWrite}

	rec := campaignsRequest(panel, http.MethodGet, page, nil, scopeOrderRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	for _, want := range []string{
		"<h2>Credits</h2>", "10.00 TRY", "goodwill", "late", "5.00 TRY", "delivery_change", "15.00 TRY",
		"2026-10-01 09:00", "2026-10-02 09:00",
	} {
		assert.Contains(t, body, want)
	}
	assert.Empty(t, creditForm(body), "a reader credits nothing")

	rec = campaignsRequest(panel, http.MethodGet, page, nil, writer...)
	form := creditForm(rec.Body.String())
	require.NotEmpty(t, form, "a writer is offered the credit")
	assert.Contains(t, form, `name="read_credited" value="1500"`, "the sum, in minor units")
	assert.Contains(t, form, `name="currency" value="TRY"`)

	rec = campaignsRequest(panel, http.MethodPost, page+"/credit-lines", url.Values{
		formReadCredited: {"1500"}, "amount": {" 12.50 "}, "currency": {"TRY"},
		formCreditReason: {" goodwill "}, formCreditNote: {" agreed on the phone "},
	}, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"order_1|1500|1250|goodwill|agreed on the phone"}, creditor.written)
	assert.Contains(t, rec.Body.String(), "12.50 TRY was credited")

	for reason, typed := range map[string]url.Values{
		"The credits the page was drawn with could not be read": {
			formReadCredited: {"many"}, "amount": {"1"}, "currency": {"TRY"}, formCreditReason: {"r"},
		},
		"more than one decimal point": {
			formReadCredited: {"0"}, "amount": {"1.2.3"}, "currency": {"TRY"}, formCreditReason: {"r"},
		},
	} {
		rec = campaignsRequest(panel, http.MethodPost, page+"/credit-lines", typed, writer...)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, reason)
		assert.Contains(t, rec.Body.String(), reason)
	}
	assert.Len(t, creditor.written, 1, "what the panel cannot read is not sent")

	creditor.writeErr = errors.Conflict("order_credit_moved",
		"order order_1 has 2500 credited now, not 1500; draw the page again")
	rec = campaignsRequest(panel, http.MethodPost, page+"/credit-lines", url.Values{
		formReadCredited: {"1500"}, "amount": {"1"}, "currency": {"TRY"}, formCreditReason: {"again"},
	}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "draw the page again")
	creditor.writeErr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, page+"/credit-lines", url.Values{
		formReadCredited: {"1500"}, "amount": {"1"}, "currency": {"TRY"}, formCreditReason: {"again"},
	}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	bare := cancelPanel(t, &fakeAfterSales{})
	rec = campaignsRequest(bare, http.MethodGet, page, nil, writer...)
	assert.NotContains(t, rec.Body.String(), "<h2>Credits</h2>", "a surface that lists no credits draws none")
	rec = campaignsRequest(bare, http.MethodPost, page+"/credit-lines", url.Values{}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestCreditsThatCannotBeReadDrawNoForm: the form carries the total, so a
// page that could not read the credits says so and offers none.
func TestCreditsThatCannotBeReadDrawNoForm(t *testing.T) {
	t.Parallel()

	panel := cancelPanel(t, &fakeCreditor{readErr: errors.Unavailable("db_down", "no")})
	rec := campaignsRequest(panel, http.MethodGet, OrdersPath+"/order_1", nil, scopeOrderRead, scopeOrderWrite)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "The credits could not be read.")
	assert.Empty(t, creditForm(rec.Body.String()))
}

// TestEveryOrderButACanceledOneIsCredited: the credit is offered on a
// pending, completed or archived order, and not on a canceled one, which was
// canceled with nothing collected.
func TestEveryOrderButACanceledOneIsCredited(t *testing.T) {
	t.Parallel()

	panel := cancelPanel(t, &fakeCreditor{})
	r := (&http.Request{}).WithContext(corehttp.WithPrincipal(context.Background(),
		corehttp.Principal{ID: "user_1", Kind: "user", Scopes: []string{scopeOrderRead, scopeOrderWrite}}))
	for status, offered := range map[string]bool{"pending": true, "completed": true, "archived": true, "canceled": false} {
		detail := orderDetail{orderRow: orderRow{Status: status}, CreditsShown: true}
		assert.Equal(t, offered, panel.canCredit(r, &detail), status)
	}
	unread := orderDetail{orderRow: orderRow{Status: "pending"}, CreditsShown: true, CreditsUnread: true}
	assert.False(t, panel.canCredit(r, &unread), "the credits unread, the form has no total to carry")
}
