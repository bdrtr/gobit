package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// profileStatus asks the profiling handler for one path.
func profileStatus(path string) int {
	w := httptest.NewRecorder()
	corehttp.ProfilingHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, http.NoBody))

	return w.Code
}

// TestTheProfilesAreServed covers what the repository had none of: a way to see
// what the Go side is doing.
//
// Every measurement in this codebase was database-side — EXPLAIN read inside a
// test, 52,000-row fixtures, millisecond figures in godocs — and nothing at all
// looked at allocations or goroutines.
//
// The long profiles are left out on purpose: /debug/pprof/profile samples for
// thirty seconds by default, which is a thirty-second test.
func TestTheProfilesAreServed(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"/debug/pprof/",
		"/debug/pprof/heap?debug=1",
		"/debug/pprof/goroutine?debug=1",
		"/debug/pprof/allocs?debug=1",
		"/debug/pprof/cmdline",
	} {
		assert.Equal(t, http.StatusOK, profileStatus(path), path)
	}
}

// TestTheProfilingHandlerServesNothingElse keeps the listener from becoming a
// general-purpose surface.
//
// It is unauthenticated by design — that is what makes a separate listener
// cheap — so what it answers has to stay exactly the profiles and nothing that
// wanders in from elsewhere in the process.
func TestTheProfilingHandlerServesNothingElse(t *testing.T) {
	t.Parallel()

	assert.Equal(t, http.StatusNotFound, profileStatus("/"))
	assert.Equal(t, http.StatusNotFound, profileStatus("/admin/v1/orders"))
	assert.Equal(t, http.StatusNotFound, profileStatus("/debug/pprof/not-a-profile"))
}
