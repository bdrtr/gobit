package adminui

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// fakeKeeper acts as the shared after-sales fake and keeps a claim's
// evidence as scripted, recording each attach and detach.
type fakeKeeper struct {
	fakeAfterSales
	evidence  string
	listErr   error
	attached  []string
	detached  []string
	attachErr error
}

func (f *fakeKeeper) ClaimEvidenceJSON(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(f.evidence), f.listErr
}

func (f *fakeKeeper) AttachClaimEvidence(_ context.Context, claimID, uploadID, caption string) (string, error) {
	f.attached = append(f.attached, claimID+"|"+uploadID+"|"+caption)
	return "cev_new", f.attachErr
}

func (f *fakeKeeper) DetachClaimEvidence(_ context.Context, evidenceID string) error {
	f.detached = append(f.detached, evidenceID)
	return f.attachErr
}

// fakeFiles stores files in memory, recording the type each was detected as.
type fakeFiles struct {
	bound    int64
	stored   []string
	deleted  []string
	storeErr error
}

func (f *fakeFiles) MaxUploadBytes() int64 { return f.bound }

func (f *fakeFiles) UploadFile(_ context.Context, contentType, name, by string, body io.Reader) (id, address string, err error) {
	content, err := io.ReadAll(body)
	if err != nil {
		return "", "", err
	}
	f.stored = append(f.stored, contentType+"|"+name+"|"+by+"|"+string(content))
	return "upl_new", "/files/new", f.storeErr
}

func (f *fakeFiles) UploadURL(_ context.Context, id string) (string, error) {
	return "/files/" + id, nil
}

func (f *fakeFiles) DeleteUpload(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}

// oneClaim is an order with one claim.
func oneClaim() *fakeCatalog {
	return afterSalesCatalog(map[string][]query.Record{
		EntityOrderClaim: {{"id": "claim_1", "status": "requested", "type": "refund", "created_at": at(3)}},
	})
}

// twoPhotos is a claim's evidence, one captioned and one not.
const twoPhotos = `[
	{"id":"cev_1","upload_id":"upl_dent","caption":"the dent","created_at":"2026-09-30T10:00:00Z"},
	{"id":"cev_2","upload_id":"upl_box","caption":"","created_at":"2026-09-30T11:00:00Z"}]`

// evidencePanel is a panel over the order with one claim.
func evidencePanel(t *testing.T, keeper AfterSalesAdmin, files FileUploader) *UI {
	t.Helper()

	panel := newCatalogPanel(t, oneClaim())
	panel.afterSales = keeper
	panel.files = files
	panel.scopes = builtInScopes()

	return panel
}

// evidenceForm is a multipart form with the caption and the file, in the
// form's order.
func evidenceForm(t *testing.T, caption, name, content string) (form io.Reader, contentType string) {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if caption != "" {
		require.NoError(t, writer.WriteField(formEvidenceCaption, caption))
	}
	if name != "" {
		part, err := writer.CreateFormFile(formEvidenceFile, name)
		require.NoError(t, err)
		_, err = part.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())

	return &body, writer.FormDataContentType()
}

// postEvidence sends the form through the panel's routes.
func postEvidence(panel *UI, path string, body io.Reader, contentType string, scopes ...string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	panel.Routes(r)
	req := httptest.NewRequest(http.MethodPost, path, body)
	req.Header.Set("Content-Type", contentType)
	req = req.WithContext(corehttp.WithPrincipal(req.Context(),
		corehttp.Principal{ID: "usr_claims", Kind: "user", Scopes: scopes}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// TestAClaimShowsItsEvidence is ADR 0325: a claim's files, oldest first with
// what the operator said they show, linked to the file for an operator who
// may read the files and by upload id otherwise; a failed read leaves the
// claim; the forms are offered to an order writer, the upload only to one who
// may also write files.
func TestAClaimShowsItsEvidence(t *testing.T) {
	t.Parallel()

	keeper := &fakeKeeper{evidence: twoPhotos}
	panel := evidencePanel(t, keeper, &fakeFiles{bound: 1 << 20})
	page := OrdersPath + "/order_1"
	writer := []string{scopeOrderRead, scopeOrderWrite, scopeFileRead, scopeFileWrite}

	rec := campaignsRequest(panel, http.MethodGet, page, nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, `<a href="/files/upl_dent" rel="noopener noreferrer">the dent</a>`)
	assert.Contains(t, body, `<a href="/files/upl_box" rel="noopener noreferrer">the file</a>`)
	assert.Contains(t, body, `action="`+page+`/claims/claim_1/evidence/cev_1/detach"`)
	assert.Contains(t, body, `action="`+page+`/claims/claim_1/evidence" enctype="multipart/form-data"`)

	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopeOrderRead, scopeOrderWrite)
	body = rec.Body.String()
	assert.Contains(t, body, "the dent <span class=\"muted\">upl_dent</span>", "by upload id, to an operator who may not read the files")
	assert.NotContains(t, body, `href="/files/`)
	assert.NotContains(t, body, `enctype="multipart/form-data"`, "storing a file needs the files' write")
	assert.Contains(t, body, "/evidence/cev_1/detach", "removing evidence is the order's write")

	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopeOrderRead)
	assert.NotContains(t, rec.Body.String(), "/detach\"", "a reader removes nothing")
	assert.Contains(t, rec.Body.String(), "the dent")

	keeper.listErr = errors.Unavailable("db_down", "no")
	rec = campaignsRequest(panel, http.MethodGet, page, nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, "the order stands when the evidence cannot be read")
	assert.Contains(t, rec.Body.String(), "The evidence could not be read.")
}

// TestEvidenceIsStoredThenBound: the caption and the file are read from the
// form, the file's type detected from its first bytes, the file stored as
// the operator and bound to the claim; a form without a file, a second file,
// a field the form does not have and a file over the bound are refused; a
// file the claim refuses is removed again; storing needs the files' write.
func TestEvidenceIsStoredThenBound(t *testing.T) {
	t.Parallel()

	keeper := &fakeKeeper{evidence: `[]`}
	files := &fakeFiles{bound: 1 << 10}
	panel := evidencePanel(t, keeper, files)
	path := OrdersPath + "/order_1/claims/claim_1/evidence"
	writer := []string{scopeOrderRead, scopeOrderWrite, scopeFileWrite}
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("x", 16)

	body, kind := evidenceForm(t, " the dent ", "dent.png", png)
	rec := postEvidence(panel, path, body, kind, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "The file was attached to the claim.")
	assert.Equal(t, []string{"image/png|dent.png|usr_claims|" + png}, files.stored, "detected from the bytes, stored whole")
	assert.Equal(t, []string{"claim_1|upl_new|the dent"}, keeper.attached)

	body, kind = evidenceForm(t, "nothing", "", "")
	rec = postEvidence(panel, path, body, kind, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "Choose a file to attach.")

	var two bytes.Buffer
	multi := multipart.NewWriter(&two)
	for _, name := range []string{"a.txt", "b.txt"} {
		part, err := multi.CreateFormFile(formEvidenceFile, name)
		require.NoError(t, err)
		_, _ = part.Write([]byte("text"))
	}
	require.NoError(t, multi.Close())
	stored := len(files.stored)
	rec = postEvidence(panel, path, &two, multi.FormDataContentType(), writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "Attach one file at a time.")
	assert.Len(t, files.stored, stored+1, "the first file was stored")
	assert.Equal(t, []string{"upl_new"}, files.deleted, "and removed again")

	// The module holds a file to its bound; the panel's own cut is the bound
	// with the form's envelope, the backstop for a body past both.
	body, kind = evidenceForm(t, "", "huge.txt", strings.Repeat("y", evidenceEnvelope+2<<10))
	rec = postEvidence(panel, path, body, kind, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "The file is larger than the shop accepts; the bound is 1024 bytes.")

	keeper.attachErr = errors.NotFound("order_claim_not_found", "claim claim_1 was not found")
	files.deleted = nil
	body, kind = evidenceForm(t, "", "dent.png", png)
	rec = postEvidence(panel, path, body, kind, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "claim claim_1 was not found")
	assert.Equal(t, []string{"upl_new"}, files.deleted, "a file the claim refused is removed again")

	body, kind = evidenceForm(t, "", "dent.png", png)
	rec = postEvidence(panel, path, body, kind, scopeOrderRead, scopeOrderWrite)
	assert.Equal(t, http.StatusForbidden, rec.Code, "storing a file needs the files' write")

	var stray bytes.Buffer
	multi = multipart.NewWriter(&stray)
	require.NoError(t, multi.WriteField("upload_id", "upl_other"))
	require.NoError(t, multi.Close())
	keeper.attachErr = nil
	rec = postEvidence(panel, path, &stray, multi.FormDataContentType(), writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "The form carried a field it does not have.")

	rec = postEvidence(evidencePanel(t, keeper, nil), path, strings.NewReader(""), kind, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "no file surface, no evidence")
}

// TestEvidenceIsRemovedFromItsClaim: the surface is asked for the evidence in
// the path and the order is drawn again; a refusal is drawn on it.
func TestEvidenceIsRemovedFromItsClaim(t *testing.T) {
	t.Parallel()

	keeper := &fakeKeeper{evidence: twoPhotos}
	panel := evidencePanel(t, keeper, &fakeFiles{bound: 1 << 10})
	path := OrdersPath + "/order_1/claims/claim_1/evidence/cev_2/detach"

	rec := campaignsRequest(panel, http.MethodPost, path, url.Values{}, scopeOrderRead, scopeOrderWrite)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "The evidence was removed from the claim; the file is kept.")
	assert.Equal(t, []string{"cev_2"}, keeper.detached)

	keeper.attachErr = errors.NotFound("order_claim_evidence_not_found", "evidence cev_2 was not found")
	rec = campaignsRequest(panel, http.MethodPost, path, url.Values{}, scopeOrderRead, scopeOrderWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "evidence cev_2 was not found")
}
