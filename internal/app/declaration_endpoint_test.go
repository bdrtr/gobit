package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/internal/workflows/datasubject"
)

// TestTheDeclarationSaysWhatAnErasureDoesToEveryColumn is the privacy notice's
// source (ADR 0172): every declared column is published with what a successful
// erasure does to it, so a controller can say which data goes on request and
// which stays, from the list the erasure reports from.
func TestTheDeclarationSaysWhatAnErasureDoesToEveryColumn(t *testing.T) {
	co, err := datasubject.FromContainer(container.New(nil), registeredModules(t))
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	personalDataHandler(co)(rec, httptest.NewRequest(http.MethodGet, personalDataPath, http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var body declarationsDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	byPath := map[string]string{}
	for _, declaration := range body.Data {
		for _, holding := range declaration.Holdings {
			assert.Contains(t, []string{"emptied", "kept"}, holding.OnErasure,
				"%s.%s of %s", holding.Table, holding.Column, declaration.Holder)
			byPath[holding.Table+"."+holding.Column] = holding.OnErasure
		}
	}
	require.NotEmpty(t, byPath)
	assert.Equal(t, "emptied", byPath["orders.email"], "an erasure empties the buyer's address")
	assert.Equal(t, "kept", byPath["orders.metadata"], "and never rewrites a free-form column")
}
