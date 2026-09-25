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

// scheduledCatalog is a read layer holding one draft with both moments.
func scheduledCatalog(publishAt, archiveAt time.Time) *fakeCatalog {
	return &fakeCatalog{byEntity: map[string][]query.Record{
		EntityProduct: {{
			"id": "prod_1", "title": "Coffee", "handle": "coffee", "status": "draft",
			fieldPublishAt: publishAt, fieldArchiveAt: archiveAt,
		}},
	}}
}

// scheduleForm is the edit form for prod_1 with the given status and moments.
func scheduleForm(status, publishAt, archiveAt string) url.Values {
	return url.Values{
		"title": {"Coffee"}, "handle": {"coffee"}, "status": {status},
		"publish_at": {publishAt}, "archive_at": {archiveAt},
	}
}

// TestAScheduledDraftShowsItsMoments verifies the product page and the form
// show the stored moments in UTC (ADR 0178, ADR 0179).
func TestAScheduledDraftShowsItsMoments(t *testing.T) {
	t.Parallel()

	trt := time.FixedZone("TRT", 3*3600)
	arrive := time.Date(2099, 1, 2, 9, 30, 0, 0, trt)
	leave := time.Date(2099, 2, 2, 9, 30, 0, 0, trt)
	panel := newEditPanel(t, scheduledCatalog(arrive, leave), &fakeProductWriter{})

	page := httptest.NewRecorder()
	editRouter(panel).ServeHTTP(page, httptest.NewRequest(http.MethodGet, ProductsPath+"/prod_1", http.NoBody))
	require.Equal(t, http.StatusOK, page.Code)
	assert.Contains(t, page.Body.String(), "2099-01-02 06:30 UTC",
		"the page prints the moment in the zone the form reads it in")
	assert.Contains(t, page.Body.String(), "2099-02-02 06:30 UTC")

	form := httptest.NewRecorder()
	editRouter(panel).ServeHTTP(form, httptest.NewRequest(http.MethodGet, ProductsPath+"/prod_1/edit", http.NoBody))
	require.Equal(t, http.StatusOK, form.Code)
	assert.Contains(t, form.Body.String(), `name="publish_at" value="2099-01-02T06:30"`)
	assert.Contains(t, form.Body.String(), `name="archive_at" value="2099-02-02T06:30"`)
}

// TestTheFormSchedulesAndUnschedules verifies what a saved form asks for: the
// moments typed, read as UTC, or no schedule when both are empty.
func TestTheFormSchedulesAndUnschedules(t *testing.T) {
	t.Parallel()

	writer := &fakeProductWriter{}
	panel := newEditPanel(t, editCatalog(), writer)

	rec := postEdit(panel, "prod_1", scheduleForm("draft", "2099-01-02T09:30", "2099-02-02T09:30"))
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	require.Len(t, writer.scheduled, 1)
	call := writer.scheduled[0]
	require.NotNil(t, call.publishAt)
	require.NotNil(t, call.archiveAt)
	assert.True(t, call.publishAt.Equal(time.Date(2099, 1, 2, 9, 30, 0, 0, time.UTC)),
		"the form's moment is read as UTC, as its label says")
	assert.True(t, call.archiveAt.Equal(time.Date(2099, 2, 2, 9, 30, 0, 0, time.UTC)))

	rec = postEdit(panel, "prod_1", scheduleForm("published", "", "2099-02-02T09:30"))
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	require.Len(t, writer.scheduled, 2)
	assert.Nil(t, writer.scheduled[1].publishAt, "a published product is scheduled to leave only")
	assert.NotNil(t, writer.scheduled[1].archiveAt)

	rec = postEdit(panel, "prod_1", scheduleForm("draft", "", ""))
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, 1, writer.unscheduled, "two empty moments take the schedule off")
}

// TestAScheduleThatCannotBeKeptWritesNothing verifies the checks made before
// any write: an edit refused for its moments does not leave the basics saved.
func TestAScheduleThatCannotBeKeptWritesNothing(t *testing.T) {
	t.Parallel()

	for name, form := range map[string]url.Values{
		"a malformed moment":                       scheduleForm("draft", "next tuesday", ""),
		"a moment in the past":                     scheduleForm("draft", "", "2001-01-01T00:00"),
		"a publication on a live product":          scheduleForm("published", "2099-01-02T09:30", ""),
		"a moment to leave on an archived product": scheduleForm("archived", "", "2099-01-02T09:30"),
		"leaving before arriving":                  scheduleForm("draft", "2099-02-02T09:30", "2099-01-02T09:30"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			writer := &fakeProductWriter{}
			panel := newEditPanel(t, editCatalog(), writer)

			rec := postEdit(panel, "prod_1", form)

			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
			assert.Zero(t, writer.calls, "the basics were saved although the edit was refused")
			assert.Empty(t, writer.scheduled)
			for _, field := range []string{"publish_at", "archive_at"} {
				assert.Contains(t, rec.Body.String(), `name="`+field+`" value="`+form.Get(field)+`"`,
					"the typed moments come back so the mistake is visible")
			}
		})
	}
}

// TestArchivingByHandTouchesNoSchedule verifies that a product saved as
// archived is not asked about a schedule: the status change took it off in the
// module's own statement.
func TestArchivingByHandTouchesNoSchedule(t *testing.T) {
	t.Parallel()

	writer := &fakeProductWriter{}
	panel := newEditPanel(t, editCatalog(), writer)

	rec := postEdit(panel, "prod_1", scheduleForm("archived", "", ""))

	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, 1, writer.calls)
	assert.Empty(t, writer.scheduled)
	assert.Zero(t, writer.unscheduled)
}
