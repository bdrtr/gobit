//go:build integration

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// versionedRequest is an admin request with If-Match.
func versionedRequest(t *testing.T, method, path string, body any, ifMatch string) *httptest.ResponseRecorder {
	t.Helper()

	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		require.NoError(t, err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer "+secretKey)
	request.Header.Set("Content-Type", "application/json")
	if ifMatch != "" {
		request.Header.Set("If-Match", ifMatch)
	}
	recorder := httptest.NewRecorder()
	testRouter.ServeHTTP(recorder, request)
	return recorder
}

// TestTwoOperatorsSavingOneProductDoNotOverwriteEachOther is ADR 0222 on the
// production wiring: the product answers its version as an ETag, a write asked
// on it moves it on, a second write asked on the same version is refused 412
// and writes nothing, a variant write answers the product's version too, and
// * asks nothing.
func TestTwoOperatorsSavingOneProductDoNotOverwriteEachOther(t *testing.T) {
	rec := versionedRequest(t, http.MethodPost, "/admin/v1/products", map[string]any{
		"handle": fmt.Sprintf("e2e-versioned-%d", time.Now().UnixNano()), "title": "Versioned",
		"status": "draft", "variants": []map[string]any{{"title": "One size"}},
	}, "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, `"1"`, rec.Header().Get("ETag"))
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	path := "/admin/v1/products/" + created.Data.ID

	rec = versionedRequest(t, http.MethodGet, path, nil, "")
	require.Equal(t, http.StatusOK, rec.Code)
	read := rec.Header().Get("ETag")
	assert.Equal(t, `"1"`, read)

	first := versionedRequest(t, http.MethodPatch, path, map[string]any{"title": "First operator"}, read)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	assert.Equal(t, `"2"`, first.Header().Get("ETag"))

	second := versionedRequest(t, http.MethodPatch, path, map[string]any{"title": "Second operator"}, read)
	require.Equal(t, http.StatusPreconditionFailed, second.Code, second.Body.String())
	assert.Contains(t, second.Body.String(), "product_version_mismatch")
	assert.Empty(t, second.Header().Get("ETag"), "a refused write answers no version")

	rec = versionedRequest(t, http.MethodGet, path, nil, "")
	assert.Contains(t, rec.Body.String(), "First operator", "the second save wrote nothing")

	variant := versionedRequest(t, http.MethodPost, path+"/variants", map[string]any{"title": "Large"}, `"2"`)
	require.Equal(t, http.StatusCreated, variant.Code, variant.Body.String())
	assert.Equal(t, `"3"`, variant.Header().Get("ETag"), "a variant's answer carries its product's version")

	anyVersion := versionedRequest(t, http.MethodPatch, path, map[string]any{"title": "Anyone"}, "*")
	require.Equal(t, http.StatusOK, anyVersion.Code, anyVersion.Body.String())
	assert.Equal(t, `"4"`, anyVersion.Header().Get("ETag"))

	malformed := versionedRequest(t, http.MethodPatch, path, map[string]any{"title": "Weak"}, `W/"4"`)
	assert.Equal(t, http.StatusUnprocessableEntity, malformed.Code, malformed.Body.String())
}
