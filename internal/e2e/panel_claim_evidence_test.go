//go:build integration

package e2e

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
)

// TestAnOperatorAttachesEvidenceToAClaimInThePanel is ADR 0325 on the
// production wiring: a photograph sent from the order's page is stored by the
// file module, under its own allow list and bound, and bound to the claim by
// the order module; the page links it with its caption, the link serves the
// image, and removing the evidence leaves the file served.
func TestAnOperatorAttachesEvidenceToAClaimInThePanel(t *testing.T) {
	ctx := t.Context()
	orderID, _, _ := notificationOrder(ctx, t, "E2E Panel Evidence Product")
	created, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+orderID+"/claims",
		map[string]any{"type": "refund", "reason": "arrived dented"})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var claim afterSalesRecordResponse
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &claim))

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	principal := corehttp.Principal{ID: "usr_claims", Kind: "user",
		Scopes: []string{"order:read", "order:write", "file:read", "file:write"}}
	send := func(req *http.Request) *httptest.ResponseRecorder {
		t.Helper()
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), principal))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	pagePath := adminui.OrdersPath + "/" + orderID
	formPath := pagePath + "/claims/" + claim.Data.ID + "/evidence"
	page := send(httptest.NewRequest(http.MethodGet, pagePath, http.NoBody)).Body.String()
	assert.Contains(t, page, `action="`+formPath+`" enctype="multipart/form-data"`, "the claim offers the upload")

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	require.NoError(t, form.WriteField("caption", "the dented corner"))
	part, err := form.CreateFormFile("file", "dent.png")
	require.NoError(t, err)
	_, err = part.Write(pngContent(t))
	require.NoError(t, err)
	require.NoError(t, form.Close())
	req := httptest.NewRequest(http.MethodPost, formPath, &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	attached := send(req)
	require.Equal(t, http.StatusOK, attached.Code, attached.Body.String())
	assert.Contains(t, attached.Body.String(), "The file was attached to the claim.")

	link := regexp.MustCompile(`<a href="(/files/[^"]+)" rel="noopener noreferrer">the dented corner</a>`).
		FindStringSubmatch(attached.Body.String())
	require.Len(t, link, 2, "the page links the evidence with its caption")
	served := adminRequest(t, http.MethodGet, link[1], "")
	require.Equal(t, http.StatusOK, served.Code)
	assert.Equal(t, "image/png", served.Header().Get("Content-Type"), "the file module serves the image")

	detach := regexp.MustCompile(`action="(` + regexp.QuoteMeta(formPath) + `/[^"]+/detach)"`).
		FindStringSubmatch(attached.Body.String())
	require.Len(t, detach, 2)
	removed := send(httptest.NewRequest(http.MethodPost, detach[1], strings.NewReader(url.Values{}.Encode())))
	require.Equal(t, http.StatusOK, removed.Code, removed.Body.String())
	assert.NotContains(t, removed.Body.String(), "the dented corner", "the claim no longer shows it")
	assert.Equal(t, http.StatusOK, adminRequest(t, http.MethodGet, link[1], "").Code, "the file is kept")
}
