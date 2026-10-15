package http

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAnExtraHeaderJoinsTheCoresListOnce holds the browser allow-list a
// plugin's proof headers extend (ADR 0444): the core's own names first and
// unchanged, each extra one after them in its canonical form, and a name the
// list already carries, in any case, not repeated.
func TestAnExtraHeaderJoinsTheCoresListOnce(t *testing.T) {
	got := allowedHeaders([]string{"x-service-request-key", " ", "x-order-token", "X-Service-Request-Key"})

	assert.Equal(t, append(append([]string{}, corsAllowedHeaders...), "X-Service-Request-Key"), got)
	assert.Equal(t, corsAllowedHeaders, allowedHeaders(nil))
	assert.Equal(t, corsAllowedHeaders, allowedHeaders([]string{"X-A, Authorization", "Traceparent"}),
		"a refused name reached the preflight answer")
}

// TestAStoreProofHeaderIsANameTheCoreDoesNotRead holds ADR 0444's refusals:
// not a field name, or one the core already reads.
func TestAStoreProofHeaderIsANameTheCoreDoesNotRead(t *testing.T) {
	assert.NoError(t, CheckStoreProofHeader("x-service-request-key"))
	for _, own := range corsAllowedHeaders {
		assert.Error(t, CheckStoreProofHeader(strings.ToLower(own)), own)
	}
	for _, bad := range []string{"", "X A", "X-A,B", "X/A", "X(A)", "X;A", "X=A", `X"A`, "X@A", "X?A", "X[A]", "X{A}", "X\\A", "Authorization", "cookie", "Tracestate"} {
		assert.Error(t, CheckStoreProofHeader(bad), bad)
	}
}
