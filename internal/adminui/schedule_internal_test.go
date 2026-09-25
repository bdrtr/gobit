package adminui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
)

// scheduledCatalog is a read layer holding one scheduled draft.
func scheduledCatalog(at time.Time) *fakeCatalog {
	return &fakeCatalog{byEntity: map[string][]query.Record{
		EntityProduct: {{
			"id": "prod_1", "title": "Coffee", "handle": "coffee", "status": "draft",
			fieldPublishAt: at,
		}},
	}}
}

// TestAScheduledDraftShowsItsMoment verifies the product page and the form show
// the stored moment in UTC (ADR 0178).
func TestAScheduledDraftShowsItsMoment(t *testing.T) {
	t.Parallel()

	at := time.Date(2099, 1, 2, 9, 30, 0, 0, time.FixedZone("TRT", 3*3600))
	panel := newEditPanel(t, scheduledCatalog(at), &fakeProductWriter{})

	page := httptest.NewRecorder()
	editRouter(panel).ServeHTTP(page, httptest.NewRequest(http.MethodGet, ProductsPath+"/prod_1", http.NoBody))
	require.Equal(t, http.StatusOK, page.Code)
	assert.Contains(t, page.Body.String(), "2099-01-02 06:30 UTC",
		"the page prints the moment in the zone the form reads it in")

	form := httptest.NewRecorder()
	editRouter(panel).ServeHTTP(form, httptest.NewRequest(http.MethodGet, ProductsPath+"/prod_1/edit", http.NoBody))
	require.Equal(t, http.StatusOK, form.Code)
	assert.Contains(t, form.Body.String(), `name="publish_at" value="2099-01-02T06:30"`)
}

// TestTheFormSchedulesAndUnschedulesADraft verifies the two outcomes of a saved
// draft: a moment schedules it, an empty field takes a schedule off.
func TestTheFormSchedulesAndUnschedulesADraft(t *testing.T) {
	t.Parallel()

	writer := &fakeProductWriter{}
	panel := newEditPanel(t, editCatalog(), writer)

	rec := postEdit(panel, "prod_1", url.Values{
		"title": {"Coffee"}, "handle": {"coffee"}, "status": {"draft"},
		"publish_at": {"2099-01-02T09:30"},
	})
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	require.Len(t, writer.scheduled, 1)
	assert.True(t, writer.scheduled[0].Equal(time.Date(2099, 1, 2, 9, 30, 0, 0, time.UTC)),
		"the form's moment is read as UTC, as its label says")

	rec = postEdit(panel, "prod_1", url.Values{
		"title": {"Coffee"}, "handle": {"coffee"}, "status": {"draft"}, "publish_at": {""},
	})
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, 1, writer.unscheduled, "an empty moment takes the schedule off")
}

// TestAScheduleThatCannotBeKeptWritesNothing verifies the checks made before
// any write: an edit refused for its moment does not leave the basics saved.
func TestAScheduleThatCannotBeKeptWritesNothing(t *testing.T) {
	t.Parallel()

	for name, form := range map[string]url.Values{
		"a malformed moment":   {"status": {"draft"}, "publish_at": {"next tuesday"}},
		"a moment in the past": {"status": {"draft"}, "publish_at": {"2001-01-01T00:00"}},
		"a moment on a product that will be published": {
			"status": {"published"}, "publish_at": {"2099-01-02T09:30"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			writer := &fakeProductWriter{}
			panel := newEditPanel(t, editCatalog(), writer)
			form.Set("title", "Coffee")
			form.Set("handle", "coffee")

			rec := postEdit(panel, "prod_1", form)

			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
			assert.Zero(t, writer.calls, "the basics were saved although the edit was refused")
			assert.Empty(t, writer.scheduled)
			assert.Contains(t, rec.Body.String(), `value="`+form.Get("publish_at")+`"`,
				"the typed moment comes back so the mistake is visible")
		})
	}
}

// TestPublishingByHandTouchesNoSchedule verifies that a product saved as
// published or archived is not asked about a schedule: the status change took
// it off in the module's own statement.
func TestPublishingByHandTouchesNoSchedule(t *testing.T) {
	t.Parallel()

	writer := &fakeProductWriter{}
	panel := newEditPanel(t, editCatalog(), writer)

	rec := postEdit(panel, "prod_1", url.Values{
		"title": {"Coffee"}, "handle": {"coffee"}, "status": {"published"}, "publish_at": {""},
	})

	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, 1, writer.calls)
	assert.Empty(t, writer.scheduled)
	assert.Zero(t, writer.unscheduled)
}
