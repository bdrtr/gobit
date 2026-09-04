package graph_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/introspection"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
)

// allProductFields is the selection set that asks for every field in the
// schema's PRODUCT tree.
//
// The calibration of the cost ceiling rests on it: the most expensive thing a
// client can legitimately ask for is all of the fields (code generators produce
// "select everything" documents). When a field is added to the schema it must
// be added here too; if it is not, the calibration test keeps passing but is no
// longer measuring the real heaviest document.
const allProductFields = `
  id handle title subtitle description thumbnail isGiftcard discountable
  weight length height width material originCountry collectionId metadata
  createdAt updatedAt
  variants {
    id productId title sku barcode ean upc manageInventory allowBackorder
    weight rank metadata createdAt updatedAt priceSet inventoryItem
    optionValues { id optionId value rank optionTitle }
  }
  options { id productId title rank values { id optionId value rank optionTitle } }
  images { id productId url rank metadata }
  tags { id value }
  categories { id name handle description parentId isActive isInternal rank }
`

// deepestDataQuery is the deepest DATA path the schema allows (5 levels).
const deepestDataQuery = `{ products { items { variants { optionValues { optionTitle } } } } }`

// aliasedStacking builds the document that repeats the same root query n times
// with aliases.
//
// This is GraphQL's multiplier with no counterpart in REST: the document below
// is a SINGLE HTTP request, that is, one tick for the rate limiter and n
// catalog queries for the server.
func aliasedStacking(n int) string {
	var document strings.Builder

	document.WriteString("{")

	for i := range n {
		document.WriteString(" a" + strconv.Itoa(i) + ": products { count }")
	}

	document.WriteString(" }")

	return document.String()
}

// aliasedField builds the selection list that selects the same field n times
// with aliases.
//
// The alias is GraphQL's only instrument for asking for the same field more
// than once in a selection set; all three of the measured attacks were using it
// (489 description, 302 __schema, 448 __type).
func aliasedField(n int, field string) string {
	return aliasedFieldWithPrefix("a", n, field)
}

// aliasedFieldWithPrefix builds the aliases with the given prefix.
//
// The prefix is needed because GraphQL validation rejects giving the SAME alias
// to DIFFERENT fields (OverlappingFieldsCanBeMerged): a document using "a0" in
// two separate fragments would die in validation without ever reaching the
// limit, and the test would measure another rule instead of the one it wants.
func aliasedFieldWithPrefix(prefix string, n int, field string) string {
	var selections strings.Builder

	for i := range n {
		selections.WriteString(" " + prefix + strconv.Itoa(i) + ": " + field)
	}

	return selections.String()
}

// repeatedDescription builds the document the finding measured: the query that
// asks for the DESCRIPTION of a page full of products n times.
//
// Its measured cost for 489 repetitions and a page of 100 is exactly 50,000 —
// that is, it SITS on the ceiling, does not exceed it, and would pass. Its
// response, when measured, was 204.9 MiB: this is exactly the place where the
// complexity model prices the field count and never asks about the bytes.
func repeatedDescription(repeat, page int) string {
	return `{ products(limit: ` + strconv.Itoa(page) + `) { items {` +
		aliasedField(repeat, "description") + `} } }`
}

// TestDepthLimitRejectsAnExceedingDocument verifies that a query exceeding the
// limit is never executed.
//
// The limit is lowered FOR THE TEST (3) because the schema is NOT CYCLIC today:
// the deepest legitimate path is 5 levels and exceeding the default limit with
// a valid document is impossible. The reason the limit exists is not today's
// schema anyway but the day a field refers back (variant -> product -> variants
// -> …); when that day comes the mechanism this test measures will already be
// in place.
func TestDepthLimitRejectsAnExceedingDocument(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}
	response, _ := runQueryWithOptions(t, identityWith([]string{"sc_1"}), svc,
		deepestDataQuery, graph.Options{MaxDepth: 3})

	require.NotEmpty(t, response.Errors)
	assert.Contains(t, response.Errors[0].Message, "depth")
	assert.Equal(t, "DEPTH_LIMIT_EXCEEDED", response.Errors[0].Extensions["code"])
	assert.Empty(t, svc.listOptions, "a document exceeding the limit must NEVER reach the service")
}

// TestDepthLimitLetsTheDocumentAtTheLimitThrough verifies that a document
// sitting exactly on the limit passes.
//
// The "reject when exceeded" test is incomplete on its own: a limit that
// rejects every document would pass it too. Where the counting ENDS only
// becomes clear when the document at the limit gets through.
func TestDepthLimitLetsTheDocumentAtTheLimitThrough(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}
	response, _ := runQueryWithOptions(t, identityWith([]string{"sc_1"}), svc,
		deepestDataQuery, graph.Options{MaxDepth: 5})

	require.Empty(t, response.Errors)
	assert.Len(t, svc.listOptions, 1)
}

// TestDefaultDepthLetsTheLegitimateQueryThrough verifies that the deepest path
// of today's schema stays below the default limit.
//
// If the default is lowered one day or the schema deepens, the fault shows up
// here: otherwise the storefront's deepest query starts being rejected IN
// PRODUCTION and nobody remembers when the limit was narrowed.
func TestDefaultDepthLetsTheLegitimateQueryThrough(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}
	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc, deepestDataQuery)

	require.Empty(t, response.Errors)
	assert.Less(t, 5, graph.DefaultMaxDepth,
		"the deepest path of the schema must stay below the default")
}

// TestDepthLimitCannotBeEvadedWithFragments verifies that the same tree cannot
// escape the limit by being split into fragments.
//
// The escape route is real: if the depth count did not look inside fragment
// definitions, a client would make every level a separate fragment and drive
// the limit down to 1. The document below asks for the SAME tree as
// [deepestDataQuery], only its spelling differs.
func TestDepthLimitCannotBeEvadedWithFragments(t *testing.T) {
	t.Parallel()

	document := `
	  { products { ...listFields } }
	  fragment listFields on ProductList { items { ...productFields } }
	  fragment productFields on Product { variants { optionValues { optionTitle } } }
	`

	svc := &fakeStorefront{}
	response, _ := runQueryWithOptions(t, identityWith([]string{"sc_1"}), svc, document,
		graph.Options{MaxDepth: 3})

	require.NotEmpty(t, response.Errors)
	assert.Contains(t, response.Errors[0].Message, "depth")
	assert.Empty(t, svc.listOptions)
}

// TestAFragmentByItselfAddsNoLevel verifies that a document split into
// fragments does not consume the limit earlier than the flat spelling of the
// SAME tree.
//
// It is the reverse of the previous one and closes off that test also passing
// with an overly strict count: had the fragment spread itself COUNTED as a
// level, a client that split its query into fragments for readability — with no
// reason at all not to — would hit the limit.
func TestAFragmentByItselfAddsNoLevel(t *testing.T) {
	t.Parallel()

	document := `
	  { products { ...listFields } }
	  fragment listFields on ProductList { items { ...productFields } }
	  fragment productFields on Product { variants { optionValues { optionTitle } } }
	`

	svc := &fakeStorefront{}
	response, _ := runQueryWithOptions(t, identityWith([]string{"sc_1"}), svc, document,
		graph.Options{MaxDepth: 5})

	require.Empty(t, response.Errors, "the same tree is 5 levels in the flat spelling")
	assert.Len(t, svc.listOptions, 1)
}

// TestComplexityIsMultipliedByListLength verifies that the cost is multiplied
// by the NUMBER OF RECORDS asked for.
//
// What the test exercises is not a limit but the COST MODEL: the two documents
// below are field for field the same, only their limits differ. Had the list
// field been given a fixed cost, both would look like the same price — that is,
// exactly the expensive query would count as cheap and the limit would let
// through the very thing it must stop.
func TestComplexityIsMultipliedByListLength(t *testing.T) {
	t.Parallel()

	const ceiling = 5000

	cheap := `{ products(limit: 1) { items {` + allProductFields + `} } }`
	expensive := `{ products(limit: 100) { items {` + allProductFields + `} } }`

	cheapSvc := &fakeStorefront{}
	response, _ := runQueryWithOptions(t, identityWith([]string{"sc_1"}), cheapSvc, cheap,
		graph.Options{MaxComplexity: ceiling})

	require.Empty(t, response.Errors, "a document asking for a single record must pass")
	assert.Len(t, cheapSvc.listOptions, 1)

	expensiveSvc := &fakeStorefront{}
	response, _ = runQueryWithOptions(t, identityWith([]string{"sc_1"}), expensiveSvc, expensive,
		graph.Options{MaxComplexity: ceiling})

	require.NotEmpty(t, response.Errors,
		"the SAME document asking for a hundred times more records must not pass")
	assert.Contains(t, response.Errors[0].Message, "complexity")
	assert.Empty(t, expensiveSvc.listOptions,
		"a document exceeding the limit must NEVER reach the service")
}

// TestComplexityCeilingClampsALimitAboveThePageCeiling verifies that a limit
// above the page ceiling does not inflate the cost estimate.
//
// A client writing limit=100000 CANNOT get a hundred thousand records; the
// service pulls the page down to service.MaxLimit. Had the cost estimate not
// known that, a legitimate client's exaggerated limit would get the query
// rejected — while the server was never going to do that much work anyway.
func TestComplexityCeilingClampsALimitAboveThePageCeiling(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}
	response, _ := runQueryWithOptions(t, identityWith([]string{"sc_1"}), svc,
		`{ products(limit: 100000) { items { id handle } } }`,
		graph.Options{MaxComplexity: 1500})

	require.Empty(t, response.Errors)
	assert.Len(t, svc.listOptions, 1)
}

// TestComplexityRejectsAliasedStacking verifies that hundreds of root queries
// in a single request are rejected with the DEFAULT settings.
//
// The rate limiter counts this document as ONE request, while the server would
// run four hundred catalog queries: this is exactly how load is applied without
// paying the quota, and it has no counterpart in REST (there, four hundred
// loads mean four hundred units of quota).
//
// Today the document hits the field repetition gate FIRST (Query.products 400
// times) and that is correct: both gates reject the same document, the cheaper
// one is in front. But the complexity ceiling's own calibration must still be
// measured — if the repetition gate is loosened one day, nobody would know
// whether the ceiling still holds. That is why the second claim MOVES the
// repetition gate OUT OF THE WAY and leaves the ceiling on its own.
func TestComplexityRejectsAliasedStacking(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}
	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc, aliasedStacking(400))

	require.NotEmpty(t, response.Errors)
	assert.Empty(t, svc.listOptions, "a document exceeding the limit must never be executed")

	ceilingOnly := &fakeStorefront{}
	response, _ = runQueryWithOptions(t, identityWith([]string{"sc_1"}), ceilingOnly,
		aliasedStacking(400), graph.Options{MaxFieldRepetition: 500})

	require.NotEmpty(t, response.Errors)
	assert.Contains(t, response.Errors[0].Message, "complexity",
		"with the repetition gate out of the way the complexity ceiling must catch it")
	assert.Empty(t, ceilingOnly.listOptions)
}

// TestDefaultCeilingLetsTheLegitimateDocumentThrough pins BOTH sides of the
// calibration.
//
// That a limit is in the right place is known only through two claims: the
// heaviest LEGITIMATE document must pass (otherwise the hardening breaks the
// storefront's own client) and a document a multiple of it must not (otherwise
// the limit is decoration). If the default ceiling is changed, this test says
// which side was sacrificed.
func TestDefaultCeilingLetsTheLegitimateDocumentThrough(t *testing.T) {
	t.Parallel()

	legitimate := `{ products { count offset limit items {` + allProductFields + `} } }`

	svc := &fakeStorefront{}
	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc, legitimate)

	require.Empty(t, response.Errors,
		"a document asking for all fields on the default page must pass")
	assert.Len(t, svc.listOptions, 1)

	excessive := `{ products(limit: 100) { count offset limit items {` + allProductFields + `} } }`

	excessiveSvc := &fakeStorefront{}
	response, _ = runQuery(t, identityWith([]string{"sc_1"}), excessiveSvc, excessive)

	require.NotEmpty(t, response.Errors,
		"a document asking for the same tree for a hundred records must not pass")
	assert.Empty(t, excessiveSvc.listOptions)
}

// TestInvalidLimitFallsBackToTheDefault verifies that a zero or negative
// setting DOES NOT MEAN "unlimited".
//
// That is the only way the hardening could disappear silently: a deployment
// that forgot to fill in the setting (or filled it in wrong) would open an
// unprotected endpoint without seeing any error. Zero cannot mean "reject every
// document" either — that would close the endpoint in another way; that is why
// the test claims both that the legitimate document passes and that the
// excessive one is rejected.
func TestInvalidLimitFallsBackToTheDefault(t *testing.T) {
	t.Parallel()

	broken := graph.Options{
		MaxDepth:              -1,
		MaxComplexity:         -1,
		MaxFieldRepetition:    -1,
		MaxIntrospectionRoots: -1,
		MaxIntrospectionDepth: -1,
		MaxResponseBytes:      -1,
	}

	svc := &fakeStorefront{}
	response, _ := runQueryWithOptions(t, identityWith([]string{"sc_1"}), svc, deepestDataQuery, broken)

	require.Empty(t, response.Errors, "an invalid setting must not break a legitimate query")
	assert.Len(t, svc.listOptions, 1)

	stackingSvc := &fakeStorefront{}
	response, _ = runQueryWithOptions(t, identityWith([]string{"sc_1"}), stackingSvc,
		aliasedStacking(400), broken)

	require.NotEmpty(t, response.Errors, "an invalid setting must NOT REMOVE the limit")
	assert.Empty(t, stackingSvc.listOptions)
}

// hugeDocument returns a VALID GraphQL document that exceeds the limit by a
// wide margin.
//
// The query itself is flawless; the reason it is rejected is not its shape but
// its SIZE.
func hugeDocument() string {
	return `{ product(handle: "` + strings.Repeat("x", 128<<10) + `") { id } }`
}

// TestOversizedQueryBodyIsRejected verifies that a huge document is rejected
// without being parsed.
//
// This gate does the work the others CANNOT do: depth and complexity can only
// be measured AFTER the document has been parsed, that is, by the time they are
// reached the server would already have read and parsed the text.
//
// The response is not the GraphQL envelope but the CORE's error envelope: the
// document never reached the executor (for the rationale see the bodyLimit that
// graph.NewHandler wraps).
func TestOversizedQueryBodyIsRejected(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}
	rec := doRequest(t, identityWith([]string{"sc_1"}), svc, hugeDocument(), graph.Options{})

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	var envelope corehttp.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	assert.Equal(t, "product_graphql_body_too_large", envelope.Error.Code)

	assert.Empty(t, svc.singleSelectors, "a body exceeding the limit must NEVER reach the service")
}

// TestBodyHidingItsSizeIsRejectedToo verifies that a client which DOES NOT
// DECLARE its size cannot slip past the limit.
//
// Content-Length is the client's CLAIM; it does not exist at all on a chunked
// body. The first gate looks only at that claim, so on its own it is not a
// limit but only a decent error given to an honest client. The real limit is
// applied by the reader and that is what we exercise.
//
// The claim looks at OUR code and NOT at gqlgen's sentence, and that is
// deliberate: here too the client learns the reason and the NUMBER, only the
// envelope differs (see bodyLimit). A claim looking at the text would define
// the library's sentence a second time in this repository.
func TestBodyHidingItsSizeIsRejectedToo(t *testing.T) {
	t.Parallel()

	body, err := json.Marshal(map[string]any{"query": hugeDocument()})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, graph.Path, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(identityWith([]string{"sc_1"}))
	// The client hiding its size is imitated with this line.
	req.ContentLength = -1

	svc := &fakeStorefront{}
	rec := httptest.NewRecorder()
	graph.NewHandler(svc, graph.Options{}).ServeHTTP(rec, req)

	assert.Empty(t, svc.singleSelectors,
		"it must be cut while the body is read, the query must not run")

	var response graphQLResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response), "body: %s", rec.Body.String())

	require.NotEmpty(t, response.Errors)
	assert.Equal(t, "REQUEST_BODY_TOO_LARGE", response.Errors[0].Extensions["code"])
	assert.Contains(t, response.Errors[0].Message, strconv.Itoa(64<<10),
		"the client must learn the limit as a number")
}

// TestIntrospectionIsEnabledByDefault verifies that the schema is visible to
// client tools.
//
// The decision and its rationale are on the [graph.Options] field: the schema
// is a file that sits inside this repository, disabling it hides nothing from
// an attacker but blinds code generators. If a decision has no test, one day it
// gets turned off in the name of "hardening" and nobody knows what was lost.
func TestIntrospectionIsEnabledByDefault(t *testing.T) {
	t.Parallel()

	response, _ := runQuery(t, identityWith([]string{"sc_1"}), &fakeStorefront{},
		`{ __schema { queryType { name } } }`)

	require.Empty(t, response.Errors)
	assert.NotNil(t, response.Data["__schema"])
}

// TestIntrospectionCanBeDisabled verifies that the switch REALLY turns it off.
//
// The existence of the switch means nothing on its own; the way to disable it
// in gqlgen is to NEVER ADD an extension, and that is an easy detail to forget.
// That the data query still works while it is disabled is claimed separately:
// it was also possible to close the whole surface while disabling
// introspection.
func TestIntrospectionCanBeDisabled(t *testing.T) {
	t.Parallel()

	disabled := graph.Options{IntrospectionDisabled: true}

	response, _ := runQueryWithOptions(t, identityWith([]string{"sc_1"}), &fakeStorefront{},
		`{ __schema { queryType { name } } }`, disabled)

	require.NotEmpty(t, response.Errors,
		"the schema must not be readable while introspection is disabled")

	svc := &fakeStorefront{}
	response, _ = runQueryWithOptions(t, identityWith([]string{"sc_1"}), svc,
		`{ products { count } }`, disabled)

	require.Empty(t, response.Errors,
		"the data query must work while introspection is disabled")
	assert.Len(t, svc.listOptions, 1)
}

// TestIntrospectionQueryPassesWithTheDefaults verifies that the STANDARD
// introspection query client tools send works with the default settings.
//
// The query was measured: it is 13 levels deep (the ofType chain, to unwrap the
// type wrappers) and its most repeated field in sibling scope is selected once.
// That is, introspection's SEPARATE ceiling
// ([graph.DefaultMaxIntrospectionDepth]) exists for this query; had a single
// ceiling been used we would have had to raise the limit of the DATA surface
// above 13 and the loosening would have happened in the very place we want to
// protect.
//
// The introspection tree has one more limit and it is independent of our
// setting: gqlparser's MaxIntrospectionDepth rule cuts nested __Type lists at
// three levels.
func TestIntrospectionQueryPassesWithTheDefaults(t *testing.T) {
	t.Parallel()

	response, _ := runQuery(t, identityWith([]string{"sc_1"}), &fakeStorefront{},
		introspection.Query)

	require.Empty(t, response.Errors)
	assert.NotNil(t, response.Data["__schema"])
}

// TestIntrospectionDepthIsIndependentOfTheDataCeiling verifies that the two
// depth ceilings really work SEPARATELY.
//
// The claim has two sides and a one-sided version would be misleading: with the
// data ceiling lowered to 3 the 13-level introspection query MUST PASS (the
// separate ceiling is doing its job), but a 4-level DATA query MUST BE REJECTED
// with the same settings (the separation does not loosen the data gate).
func TestIntrospectionDepthIsIndependentOfTheDataCeiling(t *testing.T) {
	t.Parallel()

	narrow := graph.Options{MaxDepth: 3}

	response, _ := runQueryWithOptions(t, identityWith([]string{"sc_1"}), &fakeStorefront{},
		introspection.Query, narrow)
	require.Empty(t, response.Errors,
		"introspection's own ceiling must be independent of the data ceiling")

	svc := &fakeStorefront{}
	response, _ = runQueryWithOptions(t, identityWith([]string{"sc_1"}), svc, deepestDataQuery, narrow)

	require.NotEmpty(t, response.Errors,
		"the data ceiling must not loosen because of introspection")
	assert.Equal(t, "DEPTH_LIMIT_EXCEEDED", response.Errors[0].Extensions["code"])
	assert.Empty(t, svc.listOptions)
}

// TestIntrospectionDepthIsCutByItsOwnCeiling verifies that the introspection
// tree is now MEASURED at all.
//
// The depth count used to skip the __schema/__type roots, and gqlgen's
// complexity walk skips a field of type __Schema: that is, introspection's
// measured depth was 0 and its complexity 0, and the operator had no setting at
// all to narrow it with. This test claims that the gap has been closed.
func TestIntrospectionDepthIsCutByItsOwnCeiling(t *testing.T) {
	t.Parallel()

	shallow := graph.Options{MaxIntrospectionDepth: 2}

	response, _ := runQueryWithOptions(t, identityWith([]string{"sc_1"}), &fakeStorefront{},
		`{ __schema { queryType { name } } }`, shallow)

	require.NotEmpty(t, response.Errors,
		"a three-level introspection must exceed a two-level ceiling")
	assert.Equal(t, "INTROSPECTION_LIMIT_EXCEEDED", response.Errors[0].Extensions["code"])
	assert.Contains(t, response.Errors[0].Message, "introspection")

	// That the setting does not close the data surface is claimed separately:
	// it was also possible to narrow the whole endpoint while trying to narrow
	// introspection.
	svc := &fakeStorefront{}
	response, _ = runQueryWithOptions(t, identityWith([]string{"sc_1"}), svc,
		`{ products { count } }`, shallow)

	require.Empty(t, response.Errors)
	assert.Len(t, svc.listOptions, 1)
}

// TestIntrospectionRootStackingIsRejected verifies that hundreds of __schema
// roots in a single document are rejected.
//
// That was the measured flood: 302 aliased __schema roots produced a 5.00 MiB
// response out of a 45,796-byte request, and neither of the two old gates could
// see it — the document was returning 200 even with
// Options{MaxDepth: 1, MaxComplexity: 1}, while "products { count }" was being
// rejected with the same settings. The roots are shallow (four levels), that
// is, the depth gate could not have caught this document with any setting; what
// had to be counted is not how far the tree descends but HOW MANY TIMES it was
// asked for.
func TestIntrospectionRootStackingIsRejected(t *testing.T) {
	t.Parallel()

	document := "{" + aliasedField(302, "__schema { queryType { name } }") + "}"

	response, _ := runQuery(t, identityWith([]string{"sc_1"}), &fakeStorefront{}, document)

	require.NotEmpty(t, response.Errors)
	assert.Equal(t, "INTROSPECTION_LIMIT_EXCEEDED", response.Errors[0].Extensions["code"])
	assert.Contains(t, response.Errors[0].Message, "introspection roots")
	assert.Nil(t, response.Data["a0"], "a rejected document must never be executed")
}

// TestIntrospectionRootLimitLetsTheDocumentAtTheLimitThrough verifies that the
// default does not break the tools.
//
// The "reject when exceeded" test is incomplete on its own: a limit that
// rejects every introspection query would pass it too. Schema explorers may
// send one __schema and one __type in the same document; going below the
// default would break those tools.
func TestIntrospectionRootLimitLetsTheDocumentAtTheLimitThrough(t *testing.T) {
	t.Parallel()

	document := `{ __schema { queryType { name } } __type(name: "Product") { name } }`

	response, _ := runQuery(t, identityWith([]string{"sc_1"}), &fakeStorefront{}, document)

	require.Empty(t, response.Errors)
	assert.NotNil(t, response.Data["__schema"])
	assert.Equal(t, 2, graph.DefaultMaxIntrospectionRoots)
}

// TestFieldRepetitionRejectsResponseSizeStacking repeats the finding's MEASURED
// document: the query that stays under the complexity ceiling but pushes the
// response into hundreds of megabytes.
//
// The document is this, and it is flawless:
//
//	products(limit: 100) { items { a0: description … a488: description } }
//
// Its measured cost is exactly 50,000: it SITS on the ceiling of 50,000, does
// not exceed it, and would therefore pass. Measured: an 8,729-byte request
// produced a 204.9 MiB response (24,620 times) and the rate limiter counted it
// as ONE request. It has no counterpart in REST — there the same field cannot
// be asked for 489 times, and the response for the same data is ~450 KiB.
func TestFieldRepetitionRejectsResponseSizeStacking(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}
	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc, repeatedDescription(489, 100))

	require.NotEmpty(t, response.Errors)
	assert.Equal(t, "FIELD_REPETITION_LIMIT_EXCEEDED", response.Errors[0].Extensions["code"])
	assert.Contains(t, response.Errors[0].Message, "Product.description")
	assert.Empty(t, svc.listOptions, "a document exceeding the limit must NEVER reach the service")
}

// TestComplexityDoesNotCatchFieldRepetition measures why the previous one IS
// NEEDED.
//
// With the new gate moved out of the way the SAME document passes: that is,
// what stopped it was not the complexity ceiling and could not have been — the
// model prices the NUMBER of fields, not the BYTES. Without this claim it could
// comfortably be argued one day that the repetition gate is unnecessary (that
// complexity catches it anyway).
func TestComplexityDoesNotCatchFieldRepetition(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}
	response, _ := runQueryWithOptions(t, identityWith([]string{"sc_1"}), svc,
		repeatedDescription(489, 100), graph.Options{MaxFieldRepetition: 500})

	require.Empty(t, response.Errors, "the complexity ceiling NEVER saw this document")
	assert.Len(t, svc.listOptions, 1)
}

// TestFieldRepetitionLetsLegitimateAliasesThrough verifies that the limit does
// not break a real client.
//
// The alias is a legitimate instrument: a home page repeats the same root query
// for a few storefront strips (featured, new arrivals, on sale). Had the limit
// rejected that document, the hardening would break the very storefront it
// wants to protect.
func TestFieldRepetitionLetsLegitimateAliasesThrough(t *testing.T) {
	t.Parallel()

	document := `{
	  featured: products(limit: 4) { items { id title } }
	  newArrivals: products(limit: 4, q: "new") { items { id title } }
	  onSale: products(limit: 4, q: "sale") { items { id title } }
	}`

	svc := &fakeStorefront{}
	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc, document)

	require.Empty(t, response.Errors)
	assert.Len(t, svc.listOptions, 3)
}

// TestFieldRepetitionCannotBeEvadedWithFragments verifies that stacking cannot
// escape the limit by being split into fragments.
//
// The escape route is real and is the same as the one for depth: if the count
// did not look inside fragment definitions, a client would spread its
// repetitions across fragments and reset the counter. The document below places
// 30 repetitions into a single selection set, only its spelling is split in
// two.
func TestFieldRepetitionCannotBeEvadedWithFragments(t *testing.T) {
	t.Parallel()

	document := `
	  { products { items { ...first ...second } } }
	  fragment first on Product {` + aliasedFieldWithPrefix("a", 15, "description") + `}
	  fragment second on Product {` + aliasedFieldWithPrefix("b", 15, "description") + `}
	`

	svc := &fakeStorefront{}
	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc, document)

	require.NotEmpty(t, response.Errors)
	assert.Equal(t, "FIELD_REPETITION_LIMIT_EXCEEDED", response.Errors[0].Extensions["code"])
	assert.Empty(t, svc.listOptions)
}

// TestFieldRepetitionIsSiblingScoped verifies that the count is sibling scoped
// and NOT document-wide.
//
// The distinction is not a detail but the condition for the gate being usable:
// had the whole document been counted, the standard introspection query would
// be rejected — the TypeRef fragment carries __Type.ofType dozens of times in
// separate chains and none of them is a stack. The shape of the attack is a
// SIBLING stack and the measured document was exactly that.
func TestFieldRepetitionIsSiblingScoped(t *testing.T) {
	t.Parallel()

	narrow := graph.Options{MaxFieldRepetition: 2}

	response, _ := runQueryWithOptions(t, identityWith([]string{"sc_1"}), &fakeStorefront{},
		introspection.Query, narrow)

	require.Empty(t, response.Errors,
		"nested chains are not siblings; the count must not mistake them for a stack")

	svc := &fakeStorefront{}
	response, _ = runQueryWithOptions(t, identityWith([]string{"sc_1"}), svc,
		`{ products { items { a: title b: title c: title } } }`, narrow)

	require.NotEmpty(t, response.Errors, "three repetitions in the same set must be counted")
	assert.Empty(t, svc.listOptions)
}

// TestFieldRepetitionCountsDifferentTypesSeparately verifies that the counter
// counts not "the same field" but "the same field UNDER THE SAME OBJECT".
//
// Four separate types in the schema carry an "id" field; had they all fallen
// into a single counter, an ordinary storefront query — one asking for the
// product, variant, image and category identities together — would hit the
// limit.
func TestFieldRepetitionCountsDifferentTypesSeparately(t *testing.T) {
	t.Parallel()

	document := `{ products { items { id variants { id } images { id } categories { id } } } }`

	svc := &fakeStorefront{}
	response, _ := runQueryWithOptions(t, identityWith([]string{"sc_1"}), svc, document,
		graph.Options{MaxFieldRepetition: 1})

	require.Empty(t, response.Errors,
		"fields of the same name on different types must be counted separately")
	assert.Len(t, svc.listOptions, 1)
}

// calibrationDocuments holds the MEASURED documents of the hardening table.
//
// The table is repeated in the README and in the [graph.DefaultMaxComplexity]
// godoc; this is its single SOURCE. If the text of the documents is not written
// here, the numbers in the table turn after a while into folklore nobody can
// verify — indeed the product page row of the old table was not counting the
// root query cost at all (it said 1,400, and when measured it came out 2,368).
var calibrationDocuments = map[string]struct {
	document   string
	complexity int
}{
	"product page (PDP, everything included)": {
		document:   `{ product(handle: "t-shirt") {` + allProductFields + `} }`,
		complexity: 2368,
	},
	"category list (24 products, card fields + price)": {
		document: `{ products(limit: 24) { count items { id handle title thumbnail ` +
			`variants { id title sku priceSet inventoryItem } } } }`,
		complexity: 2344,
	},
	"ALL fields on the default page (20 products x whole tree)": {
		document:   `{ products { count offset limit items {` + allProductFields + `} } }`,
		complexity: 28440,
	},
	"ALL fields with limit=100": {
		document:   `{ products(limit: 100) { count offset limit items {` + allProductFields + `} } }`,
		complexity: 138200,
	},
	"products { count } with 400 aliases": {
		document:   aliasedStacking(400),
		complexity: 408000,
	},
	"description with 489 aliases (limit=100)": {
		document:   repeatedDescription(489, 100),
		complexity: 50000,
	},
	"description with 1500 aliases (default page)": {
		document:   `{ products { items {` + aliasedField(1500, "description") + `} } }`,
		complexity: 31020,
	},
}

// measuredComplexity reads the document's complexity from gqlgen's OWN
// calculation.
//
// The number is not produced by a second calculation; the ceiling is pulled
// down to 1 and gqlgen writes the value it found into the message while
// rejecting. Writing a second calculation would mean the table measuring not
// the model but the test's model.
//
// The other gates are MOVED OUT OF THE WAY: some of the measured documents (400
// aliases, 489 repetitions) hit the field repetition gate first today, and in
// that case the complexity would never be reported.
func measuredComplexity(t *testing.T, document string) int {
	t.Helper()

	response, _ := runQueryWithOptions(t, identityWith([]string{"sc_1"}), &fakeStorefront{},
		document, graph.Options{
			MaxComplexity:      1,
			MaxDepth:           1 << 20,
			MaxFieldRepetition: 1 << 20,
		})

	require.NotEmpty(t, response.Errors, "with a ceiling of 1 every document must be rejected")

	match := complexityPattern.FindStringSubmatch(response.Errors[0].Message)
	require.Len(t, match, 2, "gqlgen must report the complexity in the message: %s",
		response.Errors[0].Message)

	measured, err := strconv.Atoi(match[1])
	require.NoError(t, err)

	return measured
}

// complexityPattern pulls the number out of gqlgen's complexity error.
var complexityPattern = regexp.MustCompile(`complexity (\d+)`)

// TestComplexityCalibration verifies that every number in the table is STILL
// correct.
//
// When the calibration is a number measured once and written into the
// documentation, every change to the cost model silently falsifies the table:
// nobody notices when 28,400 became 40,000, and the sentence "the ceiling
// leaves twice the room above the heaviest legitimate document" stops being
// true one day. This test ties that sentence to a measurement.
func TestComplexityCalibration(t *testing.T) {
	t.Parallel()

	for name, entry := range calibrationDocuments {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, entry.complexity, measuredComplexity(t, entry.document))
		})
	}
}

// TestBothSidesOfTheCalibrationAreSeparated exercises the table's
// "passes/does not pass" column with the DEFAULT settings.
//
// The complexity number is not a decision on its own; the table's real claim is
// which document passes. The heaviest legitimate document must pass (otherwise
// the hardening breaks the storefront's own client), the excessive ones must
// not (otherwise the limit is decoration).
func TestBothSidesOfTheCalibrationAreSeparated(t *testing.T) {
	t.Parallel()

	passing := []string{
		"product page (PDP, everything included)",
		"category list (24 products, card fields + price)",
		"ALL fields on the default page (20 products x whole tree)",
	}

	catalog := measurementCatalog(1)

	for _, name := range passing {
		// The fixture IS NEEDED: the schema's required fields come back null on
		// an empty product and the test would start measuring the missing data
		// rather than the limit.
		svc := &fakeStorefront{list: catalog, single: catalog.Items[0]}
		response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc,
			calibrationDocuments[name].document)

		assert.Empty(t, response.Errors, "%s must pass", name)
	}

	failing := []string{
		"ALL fields with limit=100",
		"products { count } with 400 aliases",
		"description with 489 aliases (limit=100)",
		"description with 1500 aliases (default page)",
	}

	for _, name := range failing {
		svc := &fakeStorefront{}
		response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc,
			calibrationDocuments[name].document)

		require.NotEmpty(t, response.Errors, "%s must not pass", name)
		assert.Empty(t, svc.listOptions, "%s must not reach the service", name)
		assert.Empty(t, svc.singleSelectors, "%s must not reach the service", name)
	}
}

// foldingFragmentDocument builds a fragment chain that expands itself twice at
// every level.
//
// The document is VALID and contains NO cycle — that is the only thing
// validation rejects. Even so its expansion is 2^level selections: 26 levels
// write 1,127 bytes and expand 67 million selections.
func foldingFragmentDocument(level int) string {
	var document strings.Builder

	document.WriteString("{ products { items { ...f" + strconv.Itoa(level) + " } } }\n")
	document.WriteString("fragment f0 on Product { id }\n")

	for i := 1; i <= level; i++ {
		child := "...f" + strconv.Itoa(i-1)
		document.WriteString("fragment f" + strconv.Itoa(i) + " on Product { " + child + " " + child + " }\n")
	}

	return document.String()
}

// TestSelectionBudgetRejectsAFoldingFragment verifies that an exponentially
// expanding fragment chain cannot lock the endpoint up.
//
// Measured: this document is 1,127 BYTES and before the fix the request was not
// finishing in ten seconds. It was not a single calculation that fell into the
// trap — the depth count, the field repetition count and gqlgen's own
// complexity walk all three descended into the fragment definition without
// memoization. That is why the fix binds not a single walk but the SIZE of the
// tree, and runs before all the other gates.
//
// The test carries no timeout claim; had it done so it would be unreliable on a
// slow machine. Instead it claims something stronger: the document IS REJECTED
// and does not reach the service. If the limit is removed the test does not get
// slower, it HANGS — and go test says so with its own timeout.
func TestSelectionBudgetRejectsAFoldingFragment(t *testing.T) {
	t.Parallel()

	document := foldingFragmentDocument(26)
	require.Less(t, len(document), 2<<10, "the smallness of the document is the finding itself")

	svc := &fakeStorefront{}
	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc, document)

	require.NotEmpty(t, response.Errors)
	assert.Equal(t, "SELECTION_BUDGET_EXCEEDED", response.Errors[0].Extensions["code"])
	assert.Empty(t, svc.listOptions, "a rejected document must not reach the service")
}

// TestSelectionBudgetLetsALegitimateFragmentDocumentThrough verifies that the
// budget does not break an ordinary client that uses fragments.
//
// The fragment is the instrument of a client that keeps its query readable, and
// expanding the same fragment in several places is common. The budget must not
// touch that document; if it did, the fix would break the storefront it
// protects.
func TestSelectionBudgetLetsALegitimateFragmentDocumentThrough(t *testing.T) {
	t.Parallel()

	document := `
	  {
	    featured: products(limit: 4) { items { ...card } }
	    newArrivals: products(limit: 4, q: "new") { items { ...card } }
	    onSale: products(limit: 4, q: "sale") { items { ...card } }
	  }
	  fragment card on Product {
	    id handle title thumbnail
	    variants { ...variantFields }
	  }
	  fragment variantFields on Variant { id title sku priceSet inventoryItem }
	`

	svc := &fakeStorefront{}
	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc, document)

	require.Empty(t, response.Errors)
	assert.Len(t, svc.listOptions, 3)
}
