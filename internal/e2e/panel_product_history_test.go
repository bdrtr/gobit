//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestAnOperatorRestoresAProductsRevisionInThePanel is ADR 0316 on the
// production wiring: after a rename the history lists both revisions, the
// first restores at the version the page was read at and the title is back,
// and the same form sent again is refused because the product moved on.
func TestAnOperatorRestoresAProductsRevisionInThePanel(t *testing.T) {
	ctx := t.Context()
	seq := fixtureCounter.Add(1)
	product, err := productSvc.CreateProduct(ctx, productsvc.CreateProductInput{
		Handle: fmt.Sprintf("e2e-history-%d", seq), Title: "E2E Original", Status: productmodels.StatusDraft,
	})
	require.NoError(t, err)
	renamed := "E2E Renamed"
	_, err = productSvc.UpdateProduct(ctx, product.ID, productsvc.UpdateProductInput{Title: &renamed})
	require.NoError(t, err)

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_catalog", Kind: "user", Scopes: []string{"product:read", "product:write"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	historyPath := adminui.ProductsPath + "/" + product.ID + "/revisions"
	history := send(http.MethodGet, historyPath, nil)
	require.Equal(t, http.StatusOK, history.Code, history.Body.String())
	assert.Contains(t, history.Body.String(), "History of E2E Renamed")
	assert.Contains(t, history.Body.String(), `action="`+historyPath+`/1/restore"`)
	assert.Contains(t, history.Body.String(), `name="version" value="2"`)

	form := url.Values{"version": {"2"}}
	restored := send(http.MethodPost, historyPath+"/1/restore", form)
	require.Equal(t, http.StatusSeeOther, restored.Code, restored.Body.String())
	again, err := productSvc.GetProduct(ctx, product.ID)
	require.NoError(t, err)
	assert.Equal(t, "E2E Original", again.Title)

	stale := send(http.MethodPost, historyPath+"/1/restore", form)
	require.Equal(t, http.StatusUnprocessableEntity, stale.Code, stale.Body.String())
	assert.Contains(t, stale.Body.String(), "History of E2E Original", "the refusal is drawn on the history")
}
