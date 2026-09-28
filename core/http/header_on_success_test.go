package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// TestAHeaderOnSuccessGoesOutOnlyWithASuccess is ADR 0222: a value known after
// the handler goes out on a written or an implied 2xx, not on a 500 after the
// work was done, and not when there is no value.
func TestAHeaderOnSuccessGoesOutOnlyWithASuccess(t *testing.T) {
	t.Parallel()

	known := func() (string, bool) { return `"5"`, true }

	failed := httptest.NewRecorder()
	corehttp.HeaderOnSuccess(failed, "ETag", known).WriteHeader(http.StatusInternalServerError)
	assert.Empty(t, failed.Header().Get("ETag"))

	created := httptest.NewRecorder()
	corehttp.HeaderOnSuccess(created, "ETag", known).WriteHeader(http.StatusCreated)
	assert.Equal(t, `"5"`, created.Header().Get("ETag"))

	implied := httptest.NewRecorder()
	_, err := corehttp.HeaderOnSuccess(implied, "ETag", known).Write([]byte("{}"))
	assert.NoError(t, err)
	assert.Equal(t, `"5"`, implied.Header().Get("ETag"))

	unknown := httptest.NewRecorder()
	corehttp.HeaderOnSuccess(unknown, "ETag", func() (string, bool) { return `"0"`, false }).WriteHeader(http.StatusOK)
	assert.Empty(t, unknown.Header().Get("ETag"), "no value, no header")

	underneath := httptest.NewRecorder()
	unwrapper, ok := corehttp.HeaderOnSuccess(underneath, "ETag", known).(interface{ Unwrap() http.ResponseWriter })
	assert.True(t, ok)
	if ok {
		assert.Same(t, underneath, unwrapper.Unwrap(), "http.ResponseController reaches the writer underneath")
	}
}
