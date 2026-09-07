package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// timelinePath is the support desk's view of one order.
const timelinePath = "/admin/v1/orders/order_1/timeline"

// TestTheTimelineNamesTheClockThatStampedEveryMoment is the field that keeps a
// sorted list from being read as a causal chain.
//
// The moments on a timeline do not share one axis and it was measured: the
// order's own stamps and the after-sales records' come from the DATABASE, the
// capture and the shipment transitions come from whichever PROCESS wrote them.
// On one machine the two agree and nothing shows. Across machines they can
// disagree by more than the gap between two real events, and the list then
// prints a capture before the order it paid for. An operator who cannot see
// which clock stamped which line reads that as money taken for an order that
// did not exist yet, and opens an incident. The clock field is the whole
// defense, and it is one line in the mapping — a line that costs nothing to
// lose and nothing in the tests to notice, until this one.
func TestTheTimelineNamesTheClockThatStampedEveryMoment(t *testing.T) {
	placed := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	captured := time.Date(2026, 9, 5, 9, 59, 58, 0, time.UTC)

	svc := &fakeOrders{timeline: []service.TimelineEntry{
		{
			At: &placed, Kind: service.KindOrderPlaced, RefID: "order_1",
			Clock: service.ClockDatabase,
		},
		{
			At: &captured, Kind: service.KindPaymentCaptured, RefID: "pcol_1",
			Clock: service.ClockApplication, Amount: 6100, Currency: "TRY",
		},
	}}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodGet, timelinePath, "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var body struct {
		Data []struct {
			At       *time.Time `json:"at"`
			Kind     string     `json:"kind"`
			RefID    string     `json:"ref_id"`
			Clock    string     `json:"clock"`
			Amount   int64      `json:"amount"`
			Currency string     `json:"currency_code"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data, 2)

	assert.Equal(t, service.ClockDatabase, body.Data[0].Clock,
		"the order's own stamp comes from the database and the client has to be told so")
	assert.Equal(t, service.ClockApplication, body.Data[1].Clock,
		"the capture comes from the process that wrote it; without this the line above "+
			"it looks like an order placed after it was paid for")

	assert.Equal(t, service.KindOrderPlaced, body.Data[0].Kind,
		"the kind is what a support screen switches on to render the line")
	assert.Equal(t, "pcol_1", body.Data[1].RefID,
		"the entry has to name the record its moment belongs to, otherwise the operator "+
			"cannot open the thing the line is about")
	assert.Equal(t, int64(6100), body.Data[1].Amount,
		"a money entry that carries no amount says money moved and refuses to say how much")
	assert.Equal(t, "TRY", body.Data[1].Currency,
		"an amount with no currency is a number nobody may act on")
}

// TestATimelineMomentThatWasNeverRecordedStaysNULL refuses to invent a date.
//
// Some facts on a timeline are real and have no moment: a status the order
// carries whose stamp column was never written. The service reports those with
// a nil At and puts them LAST. If the DTO carried a value type instead of a
// pointer, every one of them would arrive as 0001-01-01 — a date the client
// renders, sorts and believes, and which would place the fact at the beginning
// of the shop's history rather than at the end of the list where "we do not
// know when" belongs.
func TestATimelineMomentThatWasNeverRecordedStaysNULL(t *testing.T) {
	placed := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)

	svc := &fakeOrders{timeline: []service.TimelineEntry{
		{At: &placed, Kind: service.KindOrderPlaced, RefID: "order_1", Clock: service.ClockDatabase},
		// No moment, and therefore no clock either.
		{Kind: service.KindOrderArchived, RefID: "order_1"},
	}}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodGet, timelinePath, "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	payload := decodeResponse(t, rec)
	data, ok := payload["data"].([]any)
	require.True(t, ok, rec.Body.String())
	require.Len(t, data, 2)

	unstamped, ok := data[1].(map[string]any)
	require.True(t, ok)
	require.Contains(t, unstamped, "at",
		"the field has to be present and null, not absent: a client that cannot find the "+
			"key cannot tell an unknown moment from an entry it failed to parse")
	assert.Nil(t, unstamped["at"],
		"a fact whose moment was never written must not arrive carrying a date")
	assert.Empty(t, unstamped["clock"],
		"there is no clock to name when nothing was stamped")

	stamped, ok := data[0].(map[string]any)
	require.True(t, ok)
	assert.NotNil(t, stamped["at"],
		"the moments that WERE recorded still have to arrive; a mapping that nulled "+
			"everything would pass the assertion above and say nothing")
}

// TestAnOrderWithNothingOnItsTimelineAnswersAnEmptyArray is the difference
// between a list and a crash.
//
// Go encodes a nil slice as null, and the timeline is the screen an operator
// opens when something has gone wrong — which is exactly when they cannot
// afford it to be the screen that breaks. An order can genuinely have an empty
// timeline while the service is perfectly healthy, so this is the ordinary
// case and not the exceptional one.
func TestAnOrderWithNothingOnItsTimelineAnswersAnEmptyArray(t *testing.T) {
	r := newRouter(&fakeOrders{})

	rec := doRequest(t, r, http.MethodGet, timelinePath, "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	payload := decodeResponse(t, rec)
	data, ok := payload["data"].([]any)
	require.True(t, ok,
		"an order with nothing on its timeline still answers with a list: %s",
		rec.Body.String())
	assert.Empty(t, data)

	for _, paging := range []string{"count", "offset", "limit"} {
		assert.NotContains(t, payload, paging,
			"a timeline is bounded by its order and there is no page to ask for; "+
				"announcing one would describe a page that does not exist")
	}
}

// TestATimelineThatCouldNotBeBuiltIsNotAnsweredAsAnEmptyOne keeps a failure
// from being read as a fact.
//
// The timeline reaches two other modules through links and this module's own
// after-sales tables. Any of those reads can fail, and the two answers look
// identical to whoever is holding the screen unless the handler distinguishes
// them: 200 with [] says "nothing has ever happened to this order", which for
// an order that was paid for and shipped is a false statement the support desk
// will act on. The error has to come back as an error.
func TestATimelineThatCouldNotBeBuiltIsNotAnsweredAsAnEmptyOne(t *testing.T) {
	svc := &fakeOrders{timelineErr: errors.Internal(
		"order_timeline_failed", "the shipment link could not be read")}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodGet, timelinePath, "")

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())

	payload := decodeResponse(t, rec)
	assert.NotContains(t, payload, "data",
		"a timeline that could not be built must not arrive as an empty one")

	failure, ok := payload["error"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.Equal(t, "order_timeline_failed", failure["code"])
}
