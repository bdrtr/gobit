package graph_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// measurementCatalog builds a realistic catalog for the BYTE measurements of
// the calibration.
//
// The complexity calibration (limits_test.go) COUNTS FIELDS and needs no data
// for that; the byte calibration can measure nothing without content. The
// weight of the product was taken from a real catalog: a 4 KiB description,
// three variants (with their price and inventory records), options, images,
// tags and categories.
//
// The measurement is meaningful ONLY with this fixture, and if it is changed
// [TestResponseByteCalibration] starts measuring something else; its numbers
// must be updated together with the table in the README.
func measurementCatalog(count int) service.ListResult[service.StoreProduct] {
	description := strings.Repeat("Soft cotton crew-neck t-shirts. ", 137)
	short := "Summer collection"
	moment := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)

	products := make([]service.StoreProduct, 0, count)

	for i := range count {
		identity := "prod_" + strconv.Itoa(i)

		variants := make([]service.StoreVariant, 0, 3)
		for v := range 3 {
			variants = append(variants, service.StoreVariant{
				Variant: models.Variant{
					ID: identity + "_var_" + strconv.Itoa(v), ProductID: identity,
					Title: "S", SKU: &short, Rank: int32(v), CreatedAt: moment, UpdatedAt: moment,
					OptionValues: []models.OptionValue{
						{ID: "optval_1", OptionID: "opt_1", Value: "S"},
						{ID: "optval_2", OptionID: "opt_2", Value: "Red"},
					},
				},
				PriceSet:      query.Record{"id": "pset_1", "amount": 19990, "currency": "TRY"},
				InventoryItem: query.Record{"id": "iitem_1", "stocked_quantity": 42},
			})
		}

		products = append(products, service.StoreProduct{
			Product: models.Product{
				ID: identity, Handle: "t-shirt-" + strconv.Itoa(i), Title: "Crew Neck T-Shirt",
				Subtitle: &short, Description: &description, Thumbnail: &short,
				Metadata: map[string]any{"color": "red"}, CreatedAt: moment, UpdatedAt: moment,
				Options: []models.Option{{ID: "opt_1", ProductID: identity, Title: "Size",
					Values: []models.OptionValue{{ID: "optval_1", OptionID: "opt_1", Value: "S"}}}},
				Images: []models.Image{{ID: "img_1", ProductID: identity,
					URL: "https://cdn.example.com/1.jpg"}},
				Tags:       []models.Tag{{ID: "tag_1", Value: "new"}},
				Categories: []models.Category{{ID: "pcat_1", Name: "T-Shirt", Handle: "t-shirt"}},
			},
			Variants: variants,
		})
	}

	return service.ListResult[service.StoreProduct]{
		Items: products, Count: ptr(5000), Offset: 0, Limit: count,
	}
}

// logCapture takes the log lines the server writes into memory.
//
// The masking claim has TWO halves and the first one alone is incomplete: the
// error not going to the client must not mean the error DISAPPEARING. If the
// operator does not see the real text somewhere, masking hides not a leak but a
// fault.
//
// The lock is a real need: gqlgen resolves root fields concurrently, that is,
// more than one goroutine may write a log in a single request.
type logCapture struct {
	mu     sync.Mutex
	record bytes.Buffer
}

// Write takes the log line into memory.
func (l *logCapture) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.record.Write(p)
}

// text returns the log lines written up to that moment.
func (l *logCapture) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.record.String()
}

// identityWithLog builds a store identity context whose logs are captured.
//
// The logger is PUT into the context because that is where the core reads it
// from (corehttp.WriteError -> LoggerFromContext); changing the global logger
// would make the tests impossible to run in parallel.
func identityWithLog(channels []string) (context.Context, *logCapture) {
	capture := &logCapture{}
	ctx := corehttp.WithLogger(identityWith(channels),
		slog.New(slog.NewTextHandler(capture, nil)))

	return ctx, capture
}

// restEnvelope returns the body the same error produces on the REST surface.
func restEnvelope(t *testing.T, err error) corehttp.ErrorResponse {
	t.Helper()

	rec := httptest.NewRecorder()
	corehttp.WriteError(context.Background(), rec, err)

	var envelope corehttp.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))

	return envelope
}

// TestInternalErrorDoesNotLeakToTheClient verifies that the second read surface
// does not open up an internal server detail the first one hides.
//
// This test exercises the reason this module exists: the rule "which error may
// be handed to the client as it is" is defined in ONE place in the core, and a
// second surface applying it on its own means a leak the day they diverge.
func TestInternalErrorDoesNotLeakToTheClient(t *testing.T) {
	t.Parallel()

	secret := coreerrors.Internal("product_db_down",
		"connection dropped: postgres://gobit:password@10.0.0.7:5432/gobit")

	svc := &fakeStorefront{err: secret}
	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc, `{ products { count } }`)

	require.NotEmpty(t, response.Errors)

	message := response.Errors[0].Message
	assert.NotContains(t, message, "postgres://", "the connection string must not leak to the client")
	assert.NotContains(t, message, "password")

	// The real force of the claim is here: the message is the very message the
	// REST surface writes for the SAME error. Writing the text in here as a
	// constant would make the test right when the core changes the masking
	// text, but it would let the surfaces diverge.
	envelope := restEnvelope(t, secret)
	assert.Equal(t, envelope.Error.Message, message)
	assert.Equal(t, envelope.Error.Code, response.Errors[0].Extensions["code"])
}

// TestInternalErrorTextAppearsNowhereInTheResponse exercises the masking not
// over the decoded response but over the RAW BYTES.
//
// [TestInternalErrorDoesNotLeakToTheClient] looks only at the errors[0].message
// field; yet a GraphQL response has more than one place to leak from:
// extensions, path, and the library one day starting to serialize the error the
// gqlerror wraps. This test's claim is simpler and stronger: the text must
// appear NOWHERE in the response.
//
// The things that can leak are concrete: the connection string (with the user
// name and the password), table and column names, file paths, the query text.
// All of them are masked in the core's kindPolicy; as long as the second read
// surface does not apply the same policy on its own (see errorPresenter) they
// stay masked here too.
func TestInternalErrorTextAppearsNowhereInTheResponse(t *testing.T) {
	t.Parallel()

	const secretText = "postgres://gobit:password@10.0.0.7:5432/gobit"

	secret := coreerrors.Internal("product_db_down",
		"connection dropped: "+secretText+" (table: products, query: SELECT * FROM products)")

	svc := &fakeStorefront{err: secret}
	rec := doRequest(t, identityWith([]string{"sc_1"}), svc, `{ products { count } }`, graph.Options{})

	body := rec.Body.String()
	require.Contains(t, body, "errors", "the error must really have been returned: %s", body)

	for _, leak := range []string{secretText, "password", "SELECT", "products,", "10.0.0.7"} {
		assert.NotContains(t, body, leak,
			"the text of the internal error must appear nowhere in the response: %s", body)
	}
}

// TestClientErrorIsReturnedAsIs verifies that errors belonging to the client
// are not masked.
//
// Masking applies only to server errors; masking a validation error would make
// it impossible for the client to fix its query.
func TestClientErrorIsReturnedAsIs(t *testing.T) {
	t.Parallel()

	open := coreerrors.Invalid("product_bad_query_param", "limit cannot be negative (given: -1)")

	svc := &fakeStorefront{err: open}
	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc, `{ products { count } }`)

	require.NotEmpty(t, response.Errors)

	envelope := restEnvelope(t, open)
	assert.Equal(t, envelope.Error.Message, response.Errors[0].Message)
	assert.Equal(t, "product_bad_query_param", response.Errors[0].Extensions["code"])
}

// TestUntypedErrorIsMaskedAndLogged verifies that an unclassified error is
// masked too and DOES NOT DISAPPEAR.
//
// This was the open branch: the presenter was asking "is it a
// *coreerrors.Error" and passing through whatever was not. Yet every layer
// below the storefront service can return an untyped error — the driver's error
// is the most common of these and exactly the most harmful: pq's message
// carries the connection string, the user name, the PASSWORD and the SQL that
// was executed. Measured, it was going to the client verbatim (status 200) and
// was not being written anywhere either.
//
// The core's rule is exactly the opposite and it is applied without writing a
// second definition: an untyped error counts as KindInternal, its message is
// REPLACED with a generic text and the real error is logged. The test holds all
// three halves of the claim — it did not leak, it said the same thing as REST,
// it landed in the log.
func TestUntypedErrorIsMaskedAndLogged(t *testing.T) {
	t.Parallel()

	const dsn = "pq: SSL connection error host=db.internal user=gobit " +
		"password=s3cr3t dbname=gobit; SELECT * FROM product_products WHERE id=$1"

	raw := errors.New(dsn)

	ctx, logs := identityWithLog([]string{"sc_1"})
	rec := doRequest(t, ctx, &fakeStorefront{err: raw}, `{ products { count } }`, graph.Options{})

	body := rec.Body.String()
	for _, leak := range []string{"password", "s3cr3t", "db.internal", "SELECT", "product_products"} {
		assert.NotContains(t, body, leak,
			"the text of the untyped error must not appear in the response: %s", body)
	}

	var response graphQLResponse
	require.NoError(t, json.Unmarshal([]byte(body), &response), "body: %s", body)
	require.NotEmpty(t, response.Errors)

	// The text is not written in here as a constant: the claim must be
	// "whatever REST says, the same".
	envelope := restEnvelope(t, raw)
	assert.Equal(t, envelope.Error.Message, response.Errors[0].Message)
	assert.Equal(t, envelope.Error.Code, response.Errors[0].Extensions["code"])

	assert.Contains(t, logs.text(), "s3cr3t",
		"a masked error must be logged for the operator; otherwise masking hides the fault")
}

// TestWrappedUntypedErrorIsAlsoMasked verifies that masking looks at the KIND
// rather than at the top of the chain.
//
// Service errors rarely arrive bare; intermediate layers wrap them with
// fmt.Errorf and the wrapper is untyped too. If the classification did not
// count a wrapped error as KindInternal as well, masking could be dodged with a
// single %w.
func TestWrappedUntypedErrorIsAlsoMasked(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("the catalog could not be read: %w",
		errors.New("pq: password=s3cr3t host=db.internal"))

	ctx, logs := identityWithLog([]string{"sc_1"})
	rec := doRequest(t, ctx, &fakeStorefront{err: wrapped}, `{ products { count } }`, graph.Options{})

	body := rec.Body.String()
	assert.NotContains(t, body, "s3cr3t")
	assert.NotContains(t, body, "the catalog could not be read")
	assert.Contains(t, logs.text(), "s3cr3t")
}

// TestDocumentErrorDoesNotPolluteTheServerLog verifies that protocol errors are
// NOT COUNTED as server errors.
//
// The claim is the other half of the masking: handing every error to the core
// would record the client's typo as a server fault at ERROR level, and because
// the surface is open it would be the client writing those lines. The split
// looks at the source; this test shows that the source really is separated.
func TestDocumentErrorDoesNotPolluteTheServerLog(t *testing.T) {
	t.Parallel()

	ctx, logs := identityWithLog([]string{"sc_1"})
	rec := doRequest(t, ctx, &fakeStorefront{}, `{ products { missingField } }`, graph.Options{})

	assert.Contains(t, rec.Body.String(), "missingField", "a validation error must not be masked")
	assert.Empty(t, logs.text(), "the client's query error must not be logged as a server error")
}

// TestQueryErrorIsNotMasked verifies that GraphQL's OWN errors come back as
// they are.
//
// Query parsing and schema validation errors are about the document the client
// WROTE; if they are masked the surface becomes impossible to debug — a client
// that sees "server error" instead of "Cannot query field" cannot fix its
// query.
func TestQueryErrorIsNotMasked(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}
	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc, `{ products { missingField } }`)

	require.NotEmpty(t, response.Errors)
	assert.Contains(t, response.Errors[0].Message, "missingField")
}

// TestQueryOverGETIsNotAccepted verifies that only the POST transport is bound
// to the endpoint.
//
// The decision and its rationale are in the [graph.NewHandler] documentation:
// because the response varies with the sales channel, GET's caching gain does
// not exist here, while its costs are real (the query landing in logs and in
// browser history, the URL length).
func TestQueryOverGETIsNotAccepted(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}

	req := httptest.NewRequest(http.MethodGet, graph.Path+"?query={products{count}}", http.NoBody)
	rec := httptest.NewRecorder()
	graph.NewHandler(svc, graph.Options{}).ServeHTTP(rec, req)

	assert.NotEqual(t, http.StatusOK, rec.Code)
	assert.Empty(t, svc.listOptions, "a query arriving over GET must not be executed")
}

// TestResponseByteCalibration measures the size the hardening never asks about.
//
// Until now the calibration table measured only the FIELD COUNT; yet what it
// was missing was exactly the bytes. This test is the measurement that brings
// the byte column into the table and it claims two things:
//
//   - The HEAVIEST legitimate document that gets through today's ceilings must
//     really be heavy (otherwise the measurement measures an empty catalog and
//     saying "the limit is generous" is meaningless).
//   - The same response must stay far below the default byte limit; if
//     [graph.Options] is narrowed one day, this test blows up before the
//     storefront's own query breaks.
func TestResponseByteCalibration(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{list: measurementCatalog(20)}
	document := `{ products { count offset limit items {` + allProductFields + `} } }`

	rec := doRequest(t, identityWith([]string{"sc_1"}), svc, document, graph.Options{})

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), `"errors"`,
		"the heaviest legitimate document must pass")

	bytesWritten := rec.Body.Len()
	assert.Greater(t, bytesWritten, 100<<10, "the measurement must measure a really heavy response")
	assert.Less(t, bytesWritten, graph.DefaultMaxResponseBytes/8,
		"the heaviest legitimate response must stay far below the byte limit")
}

// TestResponseSizeLimitReturnsAFullEnvelope verifies that a response hitting
// the limit IS NOT SENT HALF WAY.
//
// The decision was this: if the limit is exceeded while no byte of the body has
// gone out, the exceeding body is dropped and a COMPLETE, valid error envelope
// is written in its place. Sending half a JSON would break the client — it
// would either get a parse error and not know why, or mistake the truncated
// body for a short result.
//
// The force of the claim is in the "valid JSON" part: a truncated body might
// also not contain "errors", but it could not be decoded.
func TestResponseSizeLimitReturnsAFullEnvelope(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{list: measurementCatalog(20)}
	document := `{ products { items {` + allProductFields + `} } }`

	rec := doRequest(t, identityWith([]string{"sc_1"}), svc, document,
		graph.Options{MaxResponseBytes: 4 << 10})

	body := rec.Body.String()
	assert.Less(t, len(body), 4<<10, "a body exceeding the limit must not go to the client")
	assert.NotContains(t, body, "Soft cotton", "truncated catalog data must not leak")

	var response graphQLResponse
	require.NoError(t, json.Unmarshal([]byte(body), &response),
		"the response must be decodable, not half written: %s", body)

	require.NotEmpty(t, response.Errors)
	assert.Equal(t, "RESPONSE_LIMIT_EXCEEDED", response.Errors[0].Extensions["code"])
	assert.Contains(t, response.Errors[0].Message, "exceeds the limit")
}

// TestResponseSizeLimitLetsALegitimateResponseThrough verifies that the gate
// does not reject every response.
//
// The "reject when exceeded" test is incomplete on its own: a wrapper that
// never writes the body would pass it too. Where the counting ends only becomes
// clear when a response just under the limit gets through.
func TestResponseSizeLimitLetsALegitimateResponseThrough(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{list: measurementCatalog(1)}
	rec := doRequest(t, identityWith([]string{"sc_1"}), svc,
		`{ products { items { id handle title } } }`, graph.Options{MaxResponseBytes: 4 << 10})

	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), `"errors"`)
	assert.Contains(t, rec.Body.String(), "t-shirt-0")
}

// TestTokenLimitCutsTheDocumentWhileParsing verifies that a document which
// stays under the body limit but carries thousands of tokens is cut WHILE BEING
// PARSED.
//
// Measured: a __schema document with 302 aliases is 45,796 bytes, that is, it
// passes the 64 KiB body gate comfortably; its token count is 9,364. This is
// the cheapest gate — the document is rejected without being parsed to the end,
// so none of the gates after it has to run.
func TestTokenLimitCutsTheDocumentWhileParsing(t *testing.T) {
	t.Parallel()

	const subtree = `__schema{types{name kind description fields{name description ` +
		`args{name description}} inputFields{name description} enumValues{name description}}}`

	document := "{" + aliasedField(302, subtree) + "}"
	require.Less(t, len(document), 64<<10, "the document must be small enough to pass the body gate")

	svc := &fakeStorefront{}
	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc, document)

	require.NotEmpty(t, response.Errors)
	assert.Contains(t, response.Errors[0].Message, "token limit")
	assert.Empty(t, svc.listOptions)
}

// TestTokenLimitLetsALegitimateDocumentThrough verifies that the token ceiling
// does not touch the storefront's real documents.
//
// Measured: the heaviest legitimate query is 95 tokens, and a fragment-heavy
// document with ten root queries is 922. The ceiling is 8,192; that is, the
// limit is roughly nine times legitimate usage. This test preserves that
// margin — whoever narrows it sees here what they are giving up.
func TestTokenLimitLetsALegitimateDocumentThrough(t *testing.T) {
	t.Parallel()

	document := "{" + aliasedField(10, `products(limit: 2) { items {`+allProductFields+`} }`) + "}"

	svc := &fakeStorefront{}
	response, _ := runQueryWithOptions(t, identityWith([]string{"sc_1"}), svc, document,
		graph.Options{MaxComplexity: 1 << 20})

	require.Empty(t, response.Errors,
		"a fragment-heavy legitimate document must not hit the token limit")
	assert.Len(t, svc.listOptions, 10)
}

// TestSchemaNamesAreNotSuggestedWhenIntrospectionIsDisabled verifies that the
// switch REALLY does what it promises.
//
// The switch once turned off only __schema and hid the schema not at all: the
// validator was handing out names retail even while introspection was disabled.
// Measured — four separate rules, four separate leaks: a misspelled field, a
// misspelled type, a misspelled argument and a field left without a selection.
// Because the validator collects all the errors in a document into a SINGLE
// response, dozens of names could be tried in one request and the rate limit
// was no obstacle.
//
// The claim is about "Did you mean" because that is the instrument of the leak:
// the diagnostic sentence (which field is wrong) repeats what the client itself
// wrote, while the sentence that ENUMERATES NAMES reads the schema.
func TestSchemaNamesAreNotSuggestedWhenIntrospectionIsDisabled(t *testing.T) {
	t.Parallel()

	disabled := graph.Options{IntrospectionDisabled: true}

	documents := map[string]string{
		"unknown field":             `{ prodcts { count } }`,
		"unknown subfield":          `{ products { itemz { id } } }`,
		"field without a selection": `{ products { items } }`,
		"unknown type":              `fragment f on Prodct { id } { products { items { ...f } } }`,
		"unknown argument":          `{ products(limitt: 3) { count } }`,
	}

	for name, document := range documents {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := doRequest(t, identityWith([]string{"sc_1"}), &fakeStorefront{}, document, disabled)

			body := rec.Body.String()
			require.Contains(t, body, "errors", "the document must really be rejected: %s", body)
			assert.NotContains(t, body, "Did you mean",
				"the schema's names must not be enumerated while introspection is disabled: %s", body)
		})
	}

	// A second claim over one concrete name: the name "products" must not be
	// read back to the client that misspelled it.
	rec := doRequest(t, identityWith([]string{"sc_1"}), &fakeStorefront{},
		`{ prodcts { count } }`, disabled)
	assert.NotContains(t, rec.Body.String(), "products")
}

// TestSuggestionsArePreservedWhenIntrospectionIsEnabled verifies that turning
// it off is NOT FREE.
//
// Suggestions are a real developer convenience on an open surface, and because
// the default is ENABLED they are preserved today. That is why this test
// exists: the claim above would also pass with a fix that strips suggestions
// under every condition — and that fix would silently make the storefront
// developer's typo more expensive.
func TestSuggestionsArePreservedWhenIntrospectionIsEnabled(t *testing.T) {
	t.Parallel()

	response, _ := runQueryWithOptions(t, identityWith([]string{"sc_1"}), &fakeStorefront{},
		`{ prodcts { count } }`, graph.Options{})

	require.NotEmpty(t, response.Errors)
	assert.Contains(t, response.Errors[0].Message, `Did you mean "products"`)
}

// TestDocumentIsNotExecutedWhenIntrospectionIsDisabled verifies that an
// introspection query is stopped at our own gate.
//
// The reason is the error policy: gqlgen rejects introspection at EXECUTION
// time with a plain errors.New, and that error cannot be told apart from the
// untyped error a resolver returns — that is, [graph.NewHandler]'s masking rule
// rightly counts it as a server error and every attempt would write an ERROR
// line. Rejecting the document at the gate both gives the right message and
// stops the surface from being a log pipe.
func TestDocumentIsNotExecutedWhenIntrospectionIsDisabled(t *testing.T) {
	t.Parallel()

	ctx, logs := identityWithLog([]string{"sc_1"})
	rec := doRequest(t, ctx, &fakeStorefront{}, `{ __schema { queryType { name } } }`,
		graph.Options{IntrospectionDisabled: true})

	var response graphQLResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response), "body: %s", rec.Body.String())

	require.NotEmpty(t, response.Errors)
	assert.Equal(t, "INTROSPECTION_DISABLED", response.Errors[0].Extensions["code"])
	assert.Nil(t, response.Data, "the document must not be executed at all")
	assert.Empty(t, logs.text(), "an introspection attempt must not be logged as a server error")
}

// TestTypenameWorksWhenIntrospectionIsDisabled verifies that the gate does not
// close more than it should.
//
// __typename is NOT an introspection root: it is a leaf that exists on every
// type and returns a single string, and clients that normalize (Apollo, urql)
// add it themselves. Counting it against the quota would break every query of
// those clients in a deployment that disables introspection.
func TestTypenameWorksWhenIntrospectionIsDisabled(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}
	response, _ := runQueryWithOptions(t, identityWith([]string{"sc_1"}), svc,
		`{ __typename products { count } }`, graph.Options{IntrospectionDisabled: true})

	require.Empty(t, response.Errors)
	assert.Equal(t, "Query", response.Data["__typename"])
	assert.Len(t, svc.listOptions, 1)
}

// TestMalformedJSONDoesNotReflectTheBody verifies that a body which cannot be
// decoded DOES NOT COME BACK in the response.
//
// Measured: when gqlgen's POST transport cannot decode the JSON it appends the
// RAW BODY to the error message (transport/http_post.go), and because that
// message is untyped it was passing through as it was. That is, up to 64 KiB of
// attacker-controlled text was entering both the response and the logs of any
// middleware that records the response — not XSS (the Content-Type is JSON) but
// reflection and log pollution.
//
// The gate is not a text match: the transport's error arrives CODELESS (from
// parsing onwards gqlgen puts a code on every protocol error) and we write the
// text of a codeless error.
func TestMalformedJSONDoesNotReflectTheBody(t *testing.T) {
	t.Parallel()

	const secret = "SECRET_TEXT_AAA"

	req := httptest.NewRequest(http.MethodPost, graph.Path,
		strings.NewReader(`{"query": `+secret+`}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(identityWith([]string{"sc_1"}))

	rec := httptest.NewRecorder()
	graph.NewHandler(&fakeStorefront{}, graph.Options{}).ServeHTTP(rec, req)

	body := rec.Body.String()
	assert.NotContains(t, body, secret, "the body must not be reflected into the response: %s", body)

	var response graphQLResponse
	require.NoError(t, json.Unmarshal([]byte(body), &response), "body: %s", body)

	require.NotEmpty(t, response.Errors)
	assert.Equal(t, "REQUEST_DECODE_FAILED", response.Errors[0].Extensions["code"])
}
