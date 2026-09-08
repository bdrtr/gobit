package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/invoice/api"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// testNow is the fixed clock of these tests.
//
// The YEAR is part of every number the module prints, so a test reading a
// number back has to decide which year it is issuing in; taken from the real
// clock, every assertion on a number would change meaning on the first of
// January.
var testNow = time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)

// adminPrincipal is the default caller: an operator holding every privilege.
//
// The admin endpoints are guarded by corehttp.RequireScope, which answers 401
// when the context carries no identity at all. These tests mount the module's
// routes directly, so the middleware that would normally put the identity there
// is not in the chain and the test puts it there itself. The identity is the
// ONLY thing added — nothing about the status codes, the envelopes or the error
// codes being asserted below comes from it.
var adminPrincipal = corehttp.Principal{
	ID:     "usr_admin",
	Kind:   "user",
	Scopes: []string{corehttp.ScopeAdmin},
}

// readerPrincipal is a narrow caller holding [api.ScopeRead] and nothing else.
var readerPrincipal = corehttp.Principal{
	ID:     "usr_reader",
	Kind:   "user",
	Scopes: []string{api.ScopeRead},
}

// newTestRouter mounts the module's real routes over the real service.
func newTestRouter(t *testing.T) (chi.Router, *memRepo) {
	t.Helper()

	repo := newMemRepo()
	svc := service.New(repo, service.Options{Now: func() time.Time { return testNow }})

	r := chi.NewRouter()
	api.New(svc).Routes(r)

	return r, repo
}

// do runs a request as the fully privileged operator.
func do(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	return doAs(t, r, adminPrincipal, method, path, body)
}

// doAs runs a request as the given caller.
func doAs(
	t *testing.T, r chi.Router, principal corehttp.Principal, method, path, body string,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), principal))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// decodeItem reads the single-record envelope.
func decodeItem(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var envelope struct {
		Data map[string]any `json:"data"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "body: %s", rec.Body.String())

	return envelope.Data
}

// decodeItems reads a LIST that travels inside the single-record envelope.
//
// The series endpoint answers with {"data": [...]} and no paging fields, which
// is what it describes in the OpenAPI document as well: the series of a shop
// are a handful of rows that are never paged, and an envelope carrying a count
// and a limit would promise a paging this endpoint does not do.
func decodeItems(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()

	var envelope struct {
		Data []map[string]any `json:"data"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "body: %s", rec.Body.String())

	return envelope.Data
}

// listEnvelope is the paginated envelope as a client reads it back.
type listEnvelope struct {
	Data       []map[string]any `json:"data"`
	Count      int64            `json:"count"`
	Offset     int64            `json:"offset"`
	Limit      int64            `json:"limit"`
	NextCursor string           `json:"next_cursor"`
}

// decodeList reads the paginated envelope.
func decodeList(t *testing.T, rec *httptest.ResponseRecorder) listEnvelope {
	t.Helper()

	var envelope listEnvelope

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "body: %s", rec.Body.String())

	return envelope
}

// errorCode returns the code inside the error envelope.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "body: %s", rec.Body.String())

	return envelope.Error.Code
}

// issueBody is a request body that satisfies every rule.
func issueBody(prefix string) string { return issueBodyWith(prefix, documentTotal, "") }

// documentTotal is what the one line of [issueBody] adds up to.
const documentTotal = 2400

// issueBodyWith builds the request with the document total of the caller's
// choosing and, optionally, one extra top-level field written out in full
// (leading comma included).
//
// The two knobs are the two ways an issue request goes wrong in the world: a
// total that disagrees with the rows because an assembler lost one, and a field
// name the caller believes in that the server has never heard of. They are
// parameters rather than a search-and-replace over the JSON text, because
// "total" appears on the line as well as on the document and a replacement
// would silently edit the wrong one.
func issueBodyWith(prefix string, total int64, extra string) string {
	return fmt.Sprintf(`{
		"series_prefix": %q,
		"kind": "sale",
		"currency_code": "TRY",
		"seller": {"name": "Gobit Shop", "tax_number": "1234567890", "country_code": "TR"},
		"buyer": {"name": "A Customer", "email": "ada@example.com", "country_code": "TR"},
		"lines": [{
			"description": "Red T-Shirt",
			"quantity": 2,
			"unit_price": 1000,
			"subtotal": 2000,
			"tax_rate_bps": 2000,
			"tax_total": 400,
			"total": 2400
		}],
		"subtotal": 2000,
		"tax_total": 400,
		"total": %d%s
	}`, prefix, total, extra)
}

// issue posts a document and returns its identifier.
func issue(t *testing.T, r chi.Router, prefix string) string {
	t.Helper()

	rec := do(t, r, http.MethodPost, "/admin/v1/invoices", issueBody(prefix))
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	id, ok := decodeItem(t, rec)["id"].(string)
	require.True(t, ok, "an issued document has to come back with its identifier")

	return id
}

// TestAnIssuedDocumentComesBackWithItsNumber is the endpoint's whole purpose.
//
// A shop that posts a document and gets back a 200 with no number has no way to
// tell "it was filed" from "it was accepted for later": the number is the only
// evidence the document exists, and 201 is the only answer that says a new
// record was created at this moment. The number's shape is legal rather than
// cosmetic — three letters, four year digits, nine sequence digits — and a
// document whose number came back in another shape would be refused by the
// regime after its number was already spent.
func TestAnIssuedDocumentComesBackWithItsNumber(t *testing.T) {
	t.Parallel()

	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodPost, "/admin/v1/invoices", issueBody("GBT"))
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	created := decodeItem(t, rec)
	assert.Equal(t, "GBT2026000000001", created["number"],
		"the first document of the year takes number one of its series")
	assert.Equal(t, "issued", created["status"],
		"a document is born issued; there is no draft for it to be born into")
	assert.NotEmpty(t, created["series_id"], "the document has to name the series it was numbered from")

	lines, ok := created["lines"].([]any)
	require.True(t, ok, "the issue response carries the rows it filed")
	require.Len(t, lines, 1)

	line, ok := lines[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(1), line["position"],
		"the printed order starts at row 1, not at row 0")
}

// TestAFieldTheServerDoesNotKnowIsRefusedRatherThanIgnored is why the decoder
// disallows unknown fields.
//
// The body below carries "discount": 500 — a field that does not exist; the
// document's discount is "discount_total". Ignored, the request would be
// accepted and a document filed for the FULL amount, with the shop believing it
// had granted a discount and the customer holding an invoice that does not show
// one. On a document that cannot be edited afterwards, the only way to correct
// that is a cancellation and a new number.
func TestAFieldTheServerDoesNotKnowIsRefusedRatherThanIgnored(t *testing.T) {
	t.Parallel()

	r, _ := newTestRouter(t)

	body := issueBodyWith("GBT", documentTotal, `,
		"discount": 500`)

	rec := do(t, r, http.MethodPost, "/admin/v1/invoices", body)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
	assert.NotEmpty(t, errorCode(t, rec), "a refusal has to carry a code the client can act on")

	list := do(t, r, http.MethodGet, "/admin/v1/invoices", "")
	require.Equal(t, http.StatusOK, list.Code)
	assert.Empty(t, decodeList(t, list).Data,
		"a body that was refused must not have filed a document")
}

// TestTheStatusCodeIsTheServiceErrorsKindAndNotTheHandlersChoice is the claim
// the whole HTTP layer rests on.
//
// A handler that mapped errors itself would be a second place where the meaning
// of a refusal is decided, and the two would drift: the same rule broken at the
// service would reach one client as 422 and another as 500, and a 500 is what a
// monitoring system pages someone for at three in the morning. Every case below
// is a DIFFERENT kind, because a handler that answered 422 to everything would
// pass a table that only carried invalid input.
func TestTheStatusCodeIsTheServiceErrorsKindAndNotTheHandlersChoice(t *testing.T) {
	t.Parallel()

	r, _ := newTestRouter(t)
	id := issue(t, r, "GBT")

	tests := map[string]struct {
		method string
		path   string
		body   string
		status int
	}{
		"a document whose totals disagree with its lines": {
			http.MethodPost, "/admin/v1/invoices",
			issueBodyWith("GBT", documentTotal+1, ""),
			http.StatusUnprocessableEntity,
		},
		"a series prefix the number format cannot carry": {
			http.MethodPost, "/admin/v1/invoices", issueBody("GB"),
			http.StatusUnprocessableEntity,
		},
		"an empty body": {
			http.MethodPost, "/admin/v1/invoices", "",
			http.StatusUnprocessableEntity,
		},
		"a move the document may not make": {
			http.MethodPost, "/admin/v1/invoices/" + id + "/status",
			`{"status":"accepted"}`, http.StatusConflict,
		},
		"a body that is not JSON at all": {
			http.MethodPost, "/admin/v1/invoices/" + id + "/status",
			`{"status":`, http.StatusUnprocessableEntity,
		},
		"a starting position that is not a number": {
			http.MethodGet, "/admin/v1/invoices?offset=here", "",
			http.StatusUnprocessableEntity,
		},
		"a status this module does not know": {
			http.MethodPost, "/admin/v1/invoices/" + id + "/status",
			`{"status":"archived"}`, http.StatusUnprocessableEntity,
		},
		"a document that does not exist": {
			http.MethodGet, "/admin/v1/invoices/inv_missing", "",
			http.StatusNotFound,
		},
		"a page size that is not a number": {
			http.MethodGet, "/admin/v1/invoices?limit=many", "",
			http.StatusUnprocessableEntity,
		},
		"a page size past the ceiling": {
			http.MethodGet, "/admin/v1/invoices?limit=101", "",
			http.StatusUnprocessableEntity,
		},
		"a negative offset": {
			http.MethodGet, "/admin/v1/invoices?offset=-1", "",
			http.StatusUnprocessableEntity,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := do(t, r, tt.method, tt.path, tt.body)
			assert.Equal(t, tt.status, rec.Code, "body: %s", rec.Body.String())
			assert.NotEmpty(t, errorCode(t, rec), "every refusal carries a code")
		})
	}
}

// TestTheListingReportsHowManyDocumentsMatchAndNotHowManyFitOnThePage is what
// makes the envelope usable.
//
// Count is what an operator's screen turns into "3 documents"; the page size is
// what it turns into a row of page buttons. Reporting the page's own length as
// the count would tell a shop with two thousand invoices that it has twenty,
// and the mistake is invisible on any page that happens to be full.
//
// The next position is offered only while there IS one. A cursor that keeps
// coming back on the last page makes a client walk forever, asking for a page
// it has already been given.
func TestTheListingReportsHowManyDocumentsMatchAndNotHowManyFitOnThePage(t *testing.T) {
	t.Parallel()

	r, _ := newTestRouter(t)

	for range 3 {
		issue(t, r, "GBT")
	}

	first := do(t, r, http.MethodGet, "/admin/v1/invoices?limit=2", "")
	require.Equal(t, http.StatusOK, first.Code, "body: %s", first.Body.String())

	page := decodeList(t, first)
	assert.Len(t, page.Data, 2, "the page has to hold what was asked for")
	assert.Equal(t, int64(3), page.Count,
		"the count is every document matching the filter, not the two on this page")
	assert.Equal(t, int64(2), page.Limit, "the applied page size is reported back")
	assert.Equal(t, int64(0), page.Offset)
	assert.NotEmpty(t, page.NextCursor,
		"a third document is waiting, so the envelope has to offer the position it starts below")

	last := do(t, r, http.MethodGet, "/admin/v1/invoices?limit=2&offset=2", "")
	require.Equal(t, http.StatusOK, last.Code)

	tail := decodeList(t, last)
	assert.Len(t, tail.Data, 1)
	assert.Equal(t, int64(3), tail.Count, "the count does not shrink as the pages are walked")
	assert.Empty(t, tail.NextCursor,
		"the listing is exhausted; a cursor here would send the client round again")
}

// TestTheDefaultPageSizeIsAppliedAndReported keeps the unasked-for bound
// visible.
//
// A client that sends no limit is still given one, because a listing that
// returned every invoice a shop ever issued would be a body nobody can hold.
// What matters is that the applied bound is REPORTED: an operator who receives
// twenty rows and no limit cannot tell a shop with twenty invoices from a shop
// whose page was cut off.
func TestTheDefaultPageSizeIsAppliedAndReported(t *testing.T) {
	t.Parallel()

	r, _ := newTestRouter(t)
	issue(t, r, "GBT")

	rec := do(t, r, http.MethodGet, "/admin/v1/invoices", "")
	require.Equal(t, http.StatusOK, rec.Code)

	page := decodeList(t, rec)
	assert.Equal(t, int64(service.DefaultLimit), page.Limit,
		"the page size the service applied has to be the one the envelope announces")
	assert.Len(t, page.Data, 1)
}

// TestTheFilterReachesTheServiceInsteadOfBeingDroppedOnTheWay holds the query
// string to its meaning.
//
// A filter silently ignored is worse than one that errors: the operator asked
// for the canceled documents, received the issued ones, and has no way to tell
// from the response that the question was not the one answered.
func TestTheFilterReachesTheServiceInsteadOfBeingDroppedOnTheWay(t *testing.T) {
	t.Parallel()

	r, repo := newTestRouter(t)

	issued := issue(t, r, "GBT")
	issue(t, r, "GBT")

	moved := do(t, r, http.MethodPost, "/admin/v1/invoices/"+issued+"/status",
		`{"status":"sent"}`)
	require.Equal(t, http.StatusOK, moved.Code, "body: %s", moved.Body.String())

	rec := do(t, r, http.MethodGet, "/admin/v1/invoices?status=sent&kind=sale", "")
	require.Equal(t, http.StatusOK, rec.Code)

	page := decodeList(t, rec)
	require.Len(t, page.Data, 1, "only the document that was sent matches")
	assert.Equal(t, issued, page.Data[0]["id"])
	assert.Equal(t, int64(1), page.Count, "the count is filtered too, or paging over it lies")

	require.NotNil(t, repo.listFilter.Status, "the status filter has to arrive at the storage layer")
	assert.Equal(t, "sent", *repo.listFilter.Status)
	require.NotNil(t, repo.listFilter.Kind)
	assert.Equal(t, "sale", *repo.listFilter.Kind)
}

// TestACursorAndAnOffsetTogetherAreRefused is a refusal in the client's
// interest.
//
// Each of the two names a position. Honoring both would serve the page N rows
// PAST the cursor — a page neither of them asked for — and the rows in between
// would be missing from a walk that looked complete. Refusing is the only
// answer that cannot silently lose documents.
func TestACursorAndAnOffsetTogetherAreRefused(t *testing.T) {
	t.Parallel()

	r, _ := newTestRouter(t)
	issue(t, r, "GBT")
	issue(t, r, "GBT")

	first := do(t, r, http.MethodGet, "/admin/v1/invoices?limit=1", "")
	require.Equal(t, http.StatusOK, first.Code)

	cursor := decodeList(t, first).NextCursor
	require.NotEmpty(t, cursor)

	alone := do(t, r, http.MethodGet, "/admin/v1/invoices?limit=1&after="+cursor, "")
	assert.Equal(t, http.StatusOK, alone.Code,
		"a cursor on its own is the ordinary way to ask for the next page")

	together := do(t, r, http.MethodGet,
		"/admin/v1/invoices?limit=1&offset=1&after="+cursor, "")
	assert.Equal(t, http.StatusUnprocessableEntity, together.Code,
		"body: %s", together.Body.String())
	assert.NotEmpty(t, errorCode(t, together))
}

// TestACursorMintedForAnotherListingIsRefused is why a cursor carries the name
// of the listing it belongs to.
//
// A position from the order listing decodes perfectly here: it is a valid time
// and a valid identifier, and applied to this table it would silently select
// the wrong window of invoices. The fault would read as missing documents
// rather than as an error, which is the worst way for a shop to meet it.
func TestACursorMintedForAnotherListingIsRefused(t *testing.T) {
	t.Parallel()

	r, _ := newTestRouter(t)
	issue(t, r, "GBT")

	foreign := corepage.Encode("orders", corepage.Cursor{Time: testNow, ID: "ord_1"})

	rec := do(t, r, http.MethodGet, "/admin/v1/invoices?after="+foreign, "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, corepage.CodeInvalidCursor, errorCode(t, rec),
		"the client has to learn that the cursor names no position HERE")
}

// TestADocumentIsSentAndThenCanceledAndTheReasonIsKept walks the status
// endpoint the way an operator does.
//
// The document is immutable, so this endpoint is the ONLY thing about it that
// can change, and each move is a fact someone later has to account for: a
// cancellation carries the sentence that explains it, and a document already
// sent cannot be sent again — a second transmission of a legal document is a
// duplicate at the receiving side, not a retry.
func TestADocumentIsSentAndThenCanceledAndTheReasonIsKept(t *testing.T) {
	t.Parallel()

	r, _ := newTestRouter(t)
	id := issue(t, r, "GBT")

	sent := do(t, r, http.MethodPost, "/admin/v1/invoices/"+id+"/status",
		`{"status":"sent","provider_id":"prv_1","external_id":"ETTN-9"}`)
	require.Equal(t, http.StatusOK, sent.Code, "body: %s", sent.Body.String())

	moved := decodeItem(t, sent)
	assert.Equal(t, "sent", moved["status"])
	assert.Equal(t, "prv_1", moved["provider_id"],
		"the provider that took the document has to be recorded with the move")
	assert.Equal(t, "ETTN-9", moved["external_id"])

	again := do(t, r, http.MethodPost, "/admin/v1/invoices/"+id+"/status", `{"status":"sent"}`)
	assert.Equal(t, http.StatusConflict, again.Code,
		"a document already sent may not be sent a second time — body: %s", again.Body.String())

	canceled := do(t, r, http.MethodPost, "/admin/v1/invoices/"+id+"/status",
		`{"status":"canceled","reason":"the customer withdrew the order"}`)
	require.Equal(t, http.StatusOK, canceled.Code, "body: %s", canceled.Body.String())

	withdrawn := decodeItem(t, canceled)
	assert.Equal(t, "canceled", withdrawn["status"])
	assert.Equal(t, "the customer withdrew the order", withdrawn["status_reason"],
		"the reason is the whole content of the account a person later has to give")

	read := do(t, r, http.MethodGet, "/admin/v1/invoices/"+id, "")
	require.Equal(t, http.StatusOK, read.Code)
	assert.Equal(t, "canceled", decodeItem(t, read)["status"],
		"the move is stored, not just echoed back to the caller that made it")
}

// TestACancellationWithoutAReasonIsRefused holds the one field an operator
// would rather not fill in.
//
// A cancellation and a rejection are the two states someone has to account for
// afterwards — to an auditor, to a customer holding the document, or to a tax
// authority reading a number that was spent on nothing. "Canceled, reason
// blank" is the answer that cannot be given, so the endpoint refuses to record
// it.
func TestACancellationWithoutAReasonIsRefused(t *testing.T) {
	t.Parallel()

	r, _ := newTestRouter(t)
	id := issue(t, r, "GBT")

	rec := do(t, r, http.MethodPost, "/admin/v1/invoices/"+id+"/status",
		`{"status":"canceled"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())

	read := do(t, r, http.MethodGet, "/admin/v1/invoices/"+id, "")
	require.Equal(t, http.StatusOK, read.Code)
	assert.Equal(t, "issued", decodeItem(t, read)["status"],
		"a refused move must not have happened anyway")
}

// TestTheSeriesListingShowsHowFarEachSeriesHasGone is the only place a
// numbering mistake is visible.
//
// The prefix comes from the installation's configuration, and a typo in it
// opens a SECOND series that numbers from one — the documents keep being
// issued, nothing fails, and the shop is quietly filing two number ranges. This
// endpoint is where an operator sees the extra series sitting at 1 next to the
// real one, so it has to report each series' reach and not merely its
// existence.
func TestTheSeriesListingShowsHowFarEachSeriesHasGone(t *testing.T) {
	t.Parallel()

	r, _ := newTestRouter(t)

	issue(t, r, "GBT")
	issue(t, r, "GBT")
	issue(t, r, "TYP")

	rec := do(t, r, http.MethodGet, "/admin/v1/invoice-series", "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	reach := map[string]float64{}

	for _, row := range decodeItems(t, rec) {
		prefix, ok := row["prefix"].(string)
		require.True(t, ok, "a series has to name its prefix")

		last, ok := row["last_number"].(float64)
		require.True(t, ok, "a series has to say how far it has gone")

		assert.Equal(t, float64(2026), row["year"], "a series belongs to a year")
		assert.NotEmpty(t, row["id"])

		reach[prefix] = last
	}

	assert.Equal(t, map[string]float64{"GBT": 2, "TYP": 1}, reach,
		"the series the typo opened has to be visible next to the real one, at 1")
}

// TestAReadOnlyOperatorCannotIssueOrMoveADocument is the privilege half of the
// surface.
//
// Being an admin is not the same as being allowed to write a legal document.
// Without the scope check, any operator with a read-only console could issue an
// invoice under the shop's own number series or cancel one that a customer is
// holding. The answer is 403 and not 401: the caller is known, and what is
// missing is the privilege.
func TestAReadOnlyOperatorCannotIssueOrMoveADocument(t *testing.T) {
	t.Parallel()

	r, _ := newTestRouter(t)
	id := issue(t, r, "GBT")

	writes := map[string]struct {
		path string
		body string
	}{
		"issuing a document": {"/admin/v1/invoices", issueBody("GBT")},
		"moving a document":  {"/admin/v1/invoices/" + id + "/status", `{"status":"sent"}`},
	}

	for name, w := range writes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := doAs(t, r, readerPrincipal, http.MethodPost, w.path, w.body)
			assert.Equal(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())
			assert.Equal(t, corehttp.CodeForbidden, errorCode(t, rec))
		})
	}
}

// TestAReadOnlyOperatorStillReadsTheDocuments is the pair of the test above.
//
// If the privilege map simply closed everything, the 403s next door would prove
// that it is strict rather than that it is right, and an accounting screen that
// only reads would be locked out of the documents it exists to show.
func TestAReadOnlyOperatorStillReadsTheDocuments(t *testing.T) {
	t.Parallel()

	r, _ := newTestRouter(t)
	id := issue(t, r, "GBT")

	for _, path := range []string{
		"/admin/v1/invoices",
		"/admin/v1/invoices/" + id,
		"/admin/v1/invoice-series",
	} {
		rec := doAs(t, r, readerPrincipal, http.MethodGet, path, "")
		assert.Equal(t, http.StatusOK, rec.Code, "path: %s — body: %s", path, rec.Body.String())
	}
}

// TestNoIdentityIsRefusedBeforeTheDocumentIsRead keeps an unauthenticated
// request away from the module entirely.
//
// 401 rather than 403: nobody has been identified yet, so there is no privilege
// to be missing, and a client that receives 403 stops retrying while a client
// that receives 401 goes and authenticates.
func TestNoIdentityIsRefusedBeforeTheDocumentIsRead(t *testing.T) {
	t.Parallel()

	r, _ := newTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/admin/v1/invoices",
		strings.NewReader(issueBody("GBT")))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code, "body: %s", rec.Body.String())
}

// TestThereIsNoStorefrontSurface proves a DECISION rather than an absence.
//
// A document is a record between the shop and the tax authority; what a
// customer receives is a copy the shop sends them. That is the whole of the
// decision — the second reason once given beside it, that the storefront had no
// identity with which to keep one customer from another's document, stopped
// being true with ADR 0043 and is recorded as corrected in the package doc. A
// route added here by habit — every other module has a /store/v1 — is caught by
// this test rather than by the person who finds their neighbour's invoice.
func TestThereIsNoStorefrontSurface(t *testing.T) {
	t.Parallel()

	r, _ := newTestRouter(t)

	for _, path := range []string{
		"/store/v1/invoices",
		"/store/v1/invoices/inv_1",
		"/store/v1/invoice-series",
	} {
		rec := do(t, r, http.MethodGet, path, "")
		assert.Equal(t, http.StatusNotFound, rec.Code, "path: %s", path)
	}
}

// TestABodyLargerThanTheLimitIsRefusedRatherThanRead bounds what one request
// can make the server allocate.
//
// A document with a few hundred rows is large; anything past the limit is a
// mistake or an attempt to have the server hold a megabyte per connection. The
// body is cut off rather than read to the end, so what reaches the decoder is
// truncated JSON and the request is refused — which is the point: the refusal
// costs the limit, not the size of what was sent.
func TestABodyLargerThanTheLimitIsRefusedRatherThanRead(t *testing.T) {
	t.Parallel()

	r, _ := newTestRouter(t)

	oversized := strings.Replace(issueBody("GBT"),
		`"description": "Red T-Shirt"`,
		`"description": "`+strings.Repeat("a", 2<<20)+`"`, 1)

	rec := do(t, r, http.MethodPost, "/admin/v1/invoices", oversized)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.NotEmpty(t, errorCode(t, rec))

	list := do(t, r, http.MethodGet, "/admin/v1/invoices", "")
	require.Equal(t, http.StatusOK, list.Code)
	assert.Empty(t, decodeList(t, list).Data, "nothing was filed from a body that was cut off")
}
