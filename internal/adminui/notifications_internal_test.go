package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// fakeDeliveries lists as scripted and records each listing and resend.
type fakeDeliveries struct {
	body      string
	total     int64
	asked     []string
	pages     [][2]int32
	resent    []string
	outcome   string
	resendErr error
}

func (f *fakeDeliveries) DeliveriesJSON(_ context.Context, status, reference string, limit, offset int32) (json.RawMessage, int64, error) {
	f.asked = append(f.asked, status+"|"+reference)
	f.pages = append(f.pages, [2]int32{limit, offset})
	return json.RawMessage(f.body), f.total, nil
}

func (f *fakeDeliveries) ResendDelivery(_ context.Context, id string) (string, error) {
	f.resent = append(f.resent, id)
	return f.outcome, f.resendErr
}

// listingOnly lists and cannot send anything again: it holds the fake
// rather than embedding it, so the resend is not promoted.
type listingOnly struct{ deliveries *fakeDeliveries }

func (l listingOnly) DeliveriesJSON(ctx context.Context, status, reference string, limit, offset int32) (json.RawMessage, int64, error) {
	return l.deliveries.DeliveriesJSON(ctx, status, reference, limit, offset)
}

// twoDeliveries is a failed confirmation and a failed invitation.
const twoDeliveries = `[
	{"id":"ndel_1","template":"order.placed","channel":"email","reference":"order_1","status":"failed",
	 "error":"the mail server did not answer","updated_at":"2026-10-01T09:30:00Z","resendable":true},
	{"id":"ndel_2","template":"invitation","channel":"email","reference":"inv_1","status":"failed",
	 "error":"","updated_at":"2026-10-01T08:00:00Z","resendable":false}]`

// notificationsRequest sends one request through the panel's routes, so each
// is asked the privilege the route table lists it under.
func notificationsRequest(panel *UI, method, path string, form url.Values, scopes ...string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	panel.Routes(r)

	request := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = request.WithContext(corehttp.WithPrincipal(request.Context(),
		corehttp.Principal{ID: "user_1", Kind: "user", Scopes: scopes}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, request)

	return rec
}

// notificationsPanel is a panel over the deliveries.
func notificationsPanel(t *testing.T, deliveries NotificationLister) *UI {
	t.Helper()

	panel := newCatalogPanel(t, &fakeCatalog{})
	panel.notifications = deliveries
	panel.scopes = builtInScopes()

	return panel
}

// TestTheNotificationsScreenListsTheFailedFirst is ADR 0317: the failed
// deliveries are listed when no status is chosen, each with its reason, an
// order's are found by its id in every status, and the resend button is on a
// resendable row for a writer only.
func TestTheNotificationsScreenListsTheFailedFirst(t *testing.T) {
	t.Parallel()

	deliveries := &fakeDeliveries{body: twoDeliveries, total: 26}
	panel := notificationsPanel(t, deliveries)

	rec := notificationsRequest(panel, http.MethodGet, NotificationsPath, nil, scopeNotificationRead, scopeNotificationWrite)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Equal(t, []string{"failed|"}, deliveries.asked, "the failed ones when none is chosen")
	assert.Contains(t, body, "the mail server did not answer")
	assert.Contains(t, body, `action="`+NotificationsPath+`/ndel_1/resend"`)
	assert.NotContains(t, body, "/ndel_2/resend", "another module's message has no button")
	assert.Contains(t, body, `name="status" value="failed"`)
	assert.Contains(t, body, "2026-10-01 09:30")
	assert.Contains(t, body, "?status=failed&amp;reference=&amp;page=2", "the next page keeps the tab")
	onePage := notificationsPanel(t, &fakeDeliveries{body: twoDeliveries, total: deliveriesPerPage})
	rec = notificationsRequest(onePage, http.MethodGet, NotificationsPath, nil, scopeNotificationRead)
	assert.NotContains(t, rec.Body.String(), "page=2", "one page has no next")

	notificationsRequest(panel, http.MethodGet, NotificationsPath+"?status=sent&page=2", nil, scopeNotificationRead)
	assert.Equal(t, "sent|", deliveries.asked[1])
	assert.Equal(t, [2]int32{deliveriesPerPage, deliveriesPerPage}, deliveries.pages[1])
	notificationsRequest(panel, http.MethodGet, NotificationsPath+"?status=lost", nil, scopeNotificationRead)
	assert.Equal(t, "failed|", deliveries.asked[2], "an unknown status lists the failed ones")

	rec = notificationsRequest(panel, http.MethodGet, NotificationsPath+"?status=sent&reference=+order_1+", nil, scopeNotificationRead)
	assert.Equal(t, "|order_1", deliveries.asked[3], "an order's deliveries in every status")
	assert.Contains(t, rec.Body.String(), "26 for order_1")
	assert.NotContains(t, rec.Body.String(), "/resend\"", "a reader sends nothing again")

	listing := notificationsPanel(t, listingOnly{&fakeDeliveries{body: twoDeliveries}})
	rec = notificationsRequest(listing, http.MethodGet, NotificationsPath, nil, scopeNotificationRead, scopeNotificationWrite)
	assert.NotContains(t, rec.Body.String(), "/resend\"", "a surface that cannot resend offers no button")
	rec = notificationsRequest(listing, http.MethodPost, NotificationsPath+"/ndel_1/resend", url.Values{}, scopeNotificationWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	absent := newCatalogPanel(t, &fakeCatalog{})
	absent.scopes = builtInScopes()
	rec = notificationsRequest(absent, http.MethodGet, NotificationsPath, nil, scopeNotificationRead)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestAResendReturnsToItsTabSayingHowItWent: the surface is asked for the
// delivery in the path, and the list it was pressed on says whether it went;
// a refusal is drawn on that list, to a writer who cannot read as the reason
// alone, and a failure is not a refusal.
func TestAResendReturnsToItsTabSayingHowItWent(t *testing.T) {
	t.Parallel()

	deliveries := &fakeDeliveries{body: twoDeliveries, total: 2, outcome: "sent"}
	panel := notificationsPanel(t, deliveries)

	rec := notificationsRequest(panel, http.MethodPost, NotificationsPath+"/ndel_1/resend",
		url.Values{"status": {"failed"}}, scopeNotificationRead, scopeNotificationWrite)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"ndel_1"}, deliveries.resent)
	location := rec.Header().Get("Location")
	assert.Equal(t, NotificationsPath+"?outcome=sent&resent=ndel_1&status=failed", location)

	after := notificationsRequest(panel, http.MethodGet, location, nil, scopeNotificationRead)
	assert.Contains(t, after.Body.String(), "Delivery ndel_1 was sent again.")
	deliveries.outcome = "failed"
	rec = notificationsRequest(panel, http.MethodPost, NotificationsPath+"/ndel_1/resend",
		url.Values{"status": {"failed"}}, scopeNotificationRead, scopeNotificationWrite)
	after = notificationsRequest(panel, http.MethodGet, rec.Header().Get("Location"), nil, scopeNotificationRead)
	assert.Contains(t, after.Body.String(), "Delivery ndel_1 was tried again and is failed; its row says why.")

	page := notificationsRequest(panel, http.MethodGet, NotificationsPath+"?reference=order_1", nil,
		scopeNotificationRead, scopeNotificationWrite)
	assert.Contains(t, page.Body.String(), `<input type="hidden" name="reference" value="order_1">`,
		"an order's list sends its id with the button")
	rec = notificationsRequest(panel, http.MethodPost, NotificationsPath+"/ndel_1/resend",
		url.Values{"status": {"failed"}, "reference": {" order_1 "}}, scopeNotificationRead, scopeNotificationWrite)
	assert.Equal(t, NotificationsPath+"?outcome=failed&reference=order_1&resent=ndel_1&status=failed",
		rec.Header().Get("Location"), "pressed on an order's list, it returns to that order")

	deliveries.resendErr = errors.Conflict("notification_not_resendable", "delivery ndel_1 is sent; only a failed one is sent again")
	rec = notificationsRequest(panel, http.MethodPost, NotificationsPath+"/ndel_1/resend",
		url.Values{"status": {"pending"}}, scopeNotificationRead, scopeNotificationWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "only a failed one is sent again")
	assert.Equal(t, "pending|", deliveries.asked[len(deliveries.asked)-1], "the refusal is drawn on the tab it was pressed on")
	notificationsRequest(panel, http.MethodPost, NotificationsPath+"/ndel_1/resend",
		url.Values{"status": {"failed"}, "reference": {"order_1"}}, scopeNotificationRead, scopeNotificationWrite)
	assert.Equal(t, "|order_1", deliveries.asked[len(deliveries.asked)-1], "or on the order's list it was pressed on")

	rec = notificationsRequest(panel, http.MethodPost, NotificationsPath+"/ndel_1/resend",
		url.Values{"status": {"failed"}}, scopeNotificationWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.NotContains(t, rec.Body.String(), "order.placed", "a writer who cannot read is shown none of the log")

	deliveries.resendErr = errors.Unavailable("db_down", "no answer")
	rec = notificationsRequest(panel, http.MethodPost, NotificationsPath+"/ndel_1/resend",
		url.Values{"status": {"failed"}}, scopeNotificationRead, scopeNotificationWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
