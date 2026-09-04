//go:build smoke

package smoke

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/product/graph"
)

// The LOW values of the GraphQL hardening limits used in this scenario.
//
// The values sit far below the defaults (10,000/20/2/15/4 MiB), and that is the
// single operating condition of the scenario: in a process running with the
// defaults no document ever hits a limit, so the question "is the setting wired?"
// stays UNANSWERED. Lowering the environment variable and seeing the rejection is
// the only proof that the wiring really holds.
//
// All five values are given together in ONE process and are calibrated so that
// they do not shadow one another; which document hits which gate is written in the
// comments of the subtests. Separate processes would prove the same thing, but at
// the cost of five startups plus five migration runs.
const (
	// graphSelectionLimit is the ceiling on the selection count after fragments are
	// expanded. The largest LEGITIMATE document of the scenario is 6 selections; 8
	// leaves it room.
	graphSelectionLimit = 8
	// graphFieldRepetitionLimit is the ceiling on repeating the same field under the
	// same object.
	graphFieldRepetitionLimit = 2
	// graphIntrospectionRootLimit is the ceiling on __schema/__type roots in a single
	// document.
	graphIntrospectionRootLimit = 1
	// graphIntrospectionDepthLimit is the depth ceiling of the introspection subtree.
	// It is three: "__schema { types { name } }" is exactly 3 deep and MUST PASS; the
	// response-byte scenario runs precisely that document.
	graphIntrospectionDepthLimit = 3
	// graphResponseByteLimit is the byte ceiling of a single response.
	//
	// 2 KiB is above every legitimate response of the scenario (a single-product list
	// is ~150 bytes) and above every error envelope (~200 bytes), and far below the
	// annotated introspection dump of the schema — so it cuts only the document that
	// is meant to be tested.
	graphResponseByteLimit = 2048
)

// graphResponse is as much of the GraphQL response envelope as the scenarios read.
//
// data is left RAW: every subtest expects the shape of its own document, and
// binding it to a single Go type would force two different queries to return the
// same structure.
type graphResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message    string         `json:"message"`
		Extensions map[string]any `json:"extensions"`
	} `json:"errors"`
}

// graphRequest sends a document to the GraphQL endpoint.
//
// The path is read from the [graph.Path] constant: the places that mount the
// endpoint and the places that describe it share that same constant. Written out
// by hand, the scenario would get a 404 from an endpoint that does not exist once
// the path changed, and could mistake that for "the protection works".
func (s *proc) graphRequest(key, document string) (status int, body string) {
	s.t.Helper()

	return s.storefrontRequest(http.MethodPost, graph.Path, key, map[string]string{"query": document})
}

// decodeGraphResponse decodes the response body into the GraphQL envelope.
func decodeGraphResponse(t *testing.T, body string) graphResponse {
	t.Helper()

	var resp graphResponse
	require.NoError(t, json.Unmarshal([]byte(body), &resp),
		"the GraphQL response could not be decoded; body: %s", body)

	return resp
}

// graphError verifies that the document was rejected with exactly ONE error and
// returns that error.
//
// Status code 200 is EXPECTED, and that is not slack but the contract of the
// endpoint: the limit codes are not registered with errcode.RegisterErrorType (the
// rationale is in graph/limits.go), so the protocol error lives in the errors array
// of the body. A code other than 200 would show that the request was caught by
// SOMETHING ELSE than the limit — by the protection stack, by the routing — and the
// scenario would not have tested the thing it means to test at all.
func graphError(t *testing.T, status int, body string) (message, errorCode string) {
	t.Helper()

	require.Equal(t, http.StatusOK, status,
		"a limit overrun has to be reported through the GraphQL envelope (200 + errors); body: %s", body)

	resp := decodeGraphResponse(t, body)
	require.Len(t, resp.Errors, 1, "exactly one limit error is expected; body: %s", body)

	codeValue, _ := resp.Errors[0].Extensions["code"].(string)

	return resp.Errors[0].Message, codeValue
}

// graphSettings builds the process environment of the GraphQL scenario.
//
// The limits are given through ENVIRONMENT VARIABLES, not in code: what is under
// test is exactly the question "does the value the operator wrote reach
// graph.Options?", and that chain (config tag → cmd/server wiring → module option)
// is complete only in a real process.
func graphSettings(dsn string, port int) settings {
	cfg := baseSettings(dsn, port)
	cfg["ADMIN_BOOTSTRAP_EMAIL"] = seedEmail
	cfg["ADMIN_BOOTSTRAP_PASSWORD"] = seedPassword

	cfg["GRAPHQL_MAX_SELECTIONS"] = strconv.Itoa(graphSelectionLimit)
	cfg["GRAPHQL_MAX_FIELD_REPETITION"] = strconv.Itoa(graphFieldRepetitionLimit)
	cfg["GRAPHQL_MAX_INTROSPECTION_ROOTS"] = strconv.Itoa(graphIntrospectionRootLimit)
	cfg["GRAPHQL_MAX_INTROSPECTION_DEPTH"] = strconv.Itoa(graphIntrospectionDepthLimit)
	cfg["GRAPHQL_MAX_RESPONSE_BYTES"] = strconv.Itoa(graphResponseByteLimit)

	return cfg
}

// publishStorefrontProduct opens a published product and attaches it to the sales
// channel.
//
// BOTH steps are mandatory: the storefront returns only "published" products, and
// only the ones visible in the channel of the request. If either one is skipped the
// GraphQL query returns an empty list and the scenario would stay green without
// proving anything.
func publishStorefrontProduct(t *testing.T, s *proc, token, channelID, title, handle string) {
	t.Helper()

	status, body := s.adminRequest(http.MethodPost, "/admin/v1/products", token, map[string]any{
		"handle": handle,
		"title":  title,
		"status": "published",
	})
	require.Equal(t, http.StatusCreated, status, "the product could not be opened; body: %s", body)

	product := zarfVerisi[struct {
		ID string `json:"id"`
	}](t, body)
	require.NotEmpty(t, product.ID, "the product has to return an id; body: %s", body)

	status, body = s.adminRequest(http.MethodPost, "/admin/v1/products/"+product.ID+"/sales-channels",
		token, map[string]any{"sales_channel_id": channelID})
	require.Equal(t, http.StatusOK, status,
		"the product could not be attached to the sales channel; body: %s", body)
}

// TestGraphQLStorefrontSurfaceInRealProcess is scenario E: the GraphQL read surface
// works on the real binary, behind the real protection stack, and the five NEW
// hardening settings really do reach graph.Options.
//
// # Why a real process
//
// internal/e2e drives this endpoint with httptest and finds it green, but it builds
// the router ITSELF: it skips the wiring in the composition root (cmd/server's
// product.Options), the config parsing, the migrations at startup, the plugin
// loading and the real network. So it cannot answer the question "does the
// storefront of somebody who clones the repository and runs it answer GraphQL?".
//
// On the hardening side the gap is even more concrete: the five settings were NEWLY
// wired from the config through to graph.Options and that the wiring holds was
// tested NOWHERE. The unit tests prove the behavior of the gates, but they cannot
// prove that the setting ARRIVES there — delete the line in cmd/server and all of
// them stay green, while the installation runs with a limit other than the one
// written in the documentation.
//
// # ONE process
//
// All of the subtests drive the same process. Because the hardening values are
// fixed for the lifetime of the process, opening one process per setting would have
// meant five startups plus five migration runs; the values were instead calibrated
// so that they do not shadow one another (see [graphSelectionLimit] and its
// siblings). The gate order (selection budget → depth → introspection root → field
// repetition → complexity → response byte) is written in graph.NewHandler, and the
// documents were picked to match that order.
func TestGraphQLStorefrontSurfaceInRealProcess(t *testing.T) {
	s := startServer(t, graphSettings(scenarioDatabase(t), freePort(t)))
	s.waitForReady(startupTimeout)

	token, channelID, storefrontKey := setUpAdminHarness(t, s, "Smoke GraphQL Channel")

	const productTitle = "Smoke GraphQL Product"
	publishStorefrontProduct(t, s, token, channelID, productTitle, "smoke-graphql-product")

	t.Run("a request without a key returns 401", func(t *testing.T) {
		status, body := s.graphRequest("", "{ products(limit: 1) { count } }")

		assert.Equal(t, http.StatusUnauthorized, status,
			"a GraphQL request without a publishable key has to be rejected; body: %s", body)
		// The response must NOT be the GraphQL envelope but the error envelope of the
		// core: the protection cuts the request BEFORE it REACHES the executor and
		// returns the same shape as every endpoint under /store/v1. Seeing "data" in
		// the body would mean the request got past the protection and reached gqlgen.
		assert.NotContains(t, body, `"data"`,
			"an unauthenticated request must not reach the executor; body: %s", body)
	})

	t.Run("GET is not accepted", func(t *testing.T) {
		// The endpoint is registered with chi for POST only (see graph.NewHandler): a
		// GET request has to get an honest 405 instead of gqlgen's "transport not
		// supported" 400. The assertion can only be tested on the real router.
		status, body := s.storefrontRequest(http.MethodGet, graph.Path, storefrontKey, nil)

		assert.Equal(t, http.StatusMethodNotAllowed, status,
			"the GraphQL endpoint has to accept POST only; body: %s", body)
	})

	t.Run("the products query returns the catalog of the channel", func(t *testing.T) {
		status, body := s.graphRequest(storefrontKey,
			"{ products(limit: 5) { count items { id title handle } } }")
		require.Equal(t, http.StatusOK, status, "the products query has to return 200; body: %s", body)

		resp := decodeGraphResponse(t, body)
		require.Empty(t, resp.Errors, "a legitimate query has to run without errors; body: %s", body)

		var data struct {
			Products struct {
				Count int `json:"count"`
				Items []struct {
					ID     string `json:"id"`
					Title  string `json:"title"`
					Handle string `json:"handle"`
				} `json:"items"`
			} `json:"products"`
		}
		require.NoError(t, json.Unmarshal(resp.Data, &data),
			"the products data could not be decoded; body: %s", body)

		require.Equal(t, 1, data.Products.Count,
			"only the product attached to the channel has to be counted; body: %s", body)
		require.Len(t, data.Products.Items, 1, "the page has to carry a single product; body: %s", body)
		assert.Equal(t, productTitle, data.Products.Items[0].Title,
			"the returned product has to be the one opened through the admin endpoint; body: %s", body)
	})

	// Each of the subtests below sends a document that hits ONLY the gate it tests.
	// The BEHAVIOR of the gates is tested far more cheaply in the unit tests of the
	// graph package and is not repeated here; the only thing tested here is the
	// WIRING — environment variable → config field → cmd/server → graph.Options.
	t.Run("GRAPHQL_MAX_SELECTIONS is wired in the process", func(t *testing.T) {
		// Ten selections; the budget is eight. Because the gate runs BEFORE all the
		// others, the document cannot possibly hit another limit.
		status, body := s.graphRequest(storefrontKey,
			"{ products { count offset limit items { id handle title createdAt updatedAt } } }")

		message, errorCode := graphError(t, status, body)
		assert.Equal(t, "SELECTION_BUDGET_EXCEEDED", errorCode,
			"the selection budget has to be wired from the env variable; body: %s", body)
		assert.Contains(t, message, strconv.Itoa(graphSelectionLimit),
			"the message has to state the ENFORCED limit; if the default (10000) shows up, the "+
				"env variable never reached graph.Options at all; body: %s", body)
	})

	t.Run("GRAPHQL_MAX_FIELD_REPETITION is wired in the process", func(t *testing.T) {
		// Four selections (under the budget), "count" three times under the same
		// object; the ceiling is two. Aliases are ignored in the count, so all three
		// are the same pair.
		status, body := s.graphRequest(storefrontKey,
			"{ products(limit: 1) { count a: count b: count } }")

		message, errorCode := graphError(t, status, body)
		assert.Equal(t, "FIELD_REPETITION_LIMIT_EXCEEDED", errorCode,
			"the field repetition limit has to be wired from the env variable; body: %s", body)
		assert.Contains(t, message, strconv.Itoa(graphFieldRepetitionLimit),
			"the message has to state the ENFORCED limit; if the default (20) shows up, the "+
				"env variable never reached graph.Options at all; body: %s", body)
	})

	t.Run("GRAPHQL_MAX_INTROSPECTION_DEPTH is wired in the process", func(t *testing.T) {
		// A SINGLE introspection root four levels deep: the root count stays under the
		// limit (1), so the only thing that can reject the document is the DEPTH. Since
		// the two gates share the same error code, the message is what tells them apart.
		status, body := s.graphRequest(storefrontKey, "{ __schema { queryType { fields { name } } } }")

		message, errorCode := graphError(t, status, body)
		assert.Equal(t, "INTROSPECTION_LIMIT_EXCEEDED", errorCode,
			"the introspection depth has to be wired from the env variable; body: %s", body)
		assert.Contains(t, message, "depth",
			"the reason for the rejection has to be the DEPTH; if the root count message shows "+
				"up, the document never hit the gate that was meant to be tested; body: %s", body)
		assert.Contains(t, message, strconv.Itoa(graphIntrospectionDepthLimit),
			"the message has to state the ENFORCED limit; if the default (15) shows up, the "+
				"env variable never reached graph.Options at all; body: %s", body)
	})

	t.Run("GRAPHQL_MAX_INTROSPECTION_ROOTS is wired in the process", func(t *testing.T) {
		// Two roots, both of them under the depth ceiling: the only possible reason for
		// the rejection is the ROOT COUNT.
		status, body := s.graphRequest(storefrontKey,
			`{ __schema { queryType { name } } t: __type(name: "Product") { name } }`)

		message, errorCode := graphError(t, status, body)
		assert.Equal(t, "INTROSPECTION_LIMIT_EXCEEDED", errorCode,
			"the introspection root limit has to be wired from the env variable; body: %s", body)
		assert.Contains(t, message, "introspection roots",
			"the reason for the rejection has to be the ROOT COUNT; if the depth message shows "+
				"up, the document never hit the gate that was meant to be tested; body: %s", body)
		assert.Contains(t, message, strconv.Itoa(graphIntrospectionRootLimit),
			"the message has to state the ENFORCED limit; if the default (2) shows up, the "+
				"env variable never reached graph.Options at all; body: %s", body)
	})

	t.Run("GRAPHQL_MAX_RESPONSE_BYTES is wired in the process", func(t *testing.T) {
		// The document passes all the EARLIER gates (4 selections, 3 depth, 1 root, no
		// repetition) and it EXECUTES; the only thing that cuts it is the byte count it
		// actually produces. The type descriptions of the schema alone are many times
		// over 2 KiB.
		status, body := s.graphRequest(storefrontKey, "{ __schema { types { name description } } }")

		message, errorCode := graphError(t, status, body)
		assert.Equal(t, "RESPONSE_LIMIT_EXCEEDED", errorCode,
			"the response byte limit has to be wired from the env variable; body: %s", body)
		assert.Contains(t, message, strconv.Itoa(graphResponseByteLimit),
			"the message has to state the ENFORCED limit; if the default (4194304) shows up, the "+
				"env variable never reached graph.Options at all; body: %s", body)

		// Half a JSON document is NEVER SENT: the overflowing body is thrown away and a
		// complete, valid error envelope is written in its place. That graphError was
		// able to decode the body is the proof of that; the assertion here says the
		// process is still standing as well.
		assert.False(t, s.happened(), "the response limit must not bring the process down\n%s", s.logBuf())
	})
}

// TestGraphQLLimitsStopStartupOnZeroAndNegativeValues proves that the hardening
// cannot be switched off SILENTLY.
//
// # Why a separate assertion
//
// The "0 = unlimited" reading exists in NONE of these settings, and its absence is
// a deliberate decision (see config.Config.GraphQLMaxDepth): a limit can be raised,
// it cannot be removed. But the decision is one line of code, and if that line is
// deleted no unit test fails — what happens is that the installation comes up with
// an unprotected endpoint and never says so. The scenario closes exactly that
// silence.
//
// # Why five processes and why they are cheap
//
// config.Load returns on the first error, so a single setting can be tested in a
// single process. The cost is low: the gate closes before the database is touched
// AT ALL and the process dies within milliseconds; the total of the five stays far
// below a single normal startup.
//
// Every subtest still gets its OWN database. The reason is the possibility that the
// gate is lifted one day: if the admin database were shared, a process that managed
// to come up would apply its migrations there and carry the fault over to the
// neighboring scenarios.
func TestGraphQLLimitsStopStartupOnZeroAndNegativeValues(t *testing.T) {
	// The zero and the negative values were SPREAD ACROSS the variables: testing both
	// of them on every variable would double the process count and would measure the
	// same code branch (value < 1) for the tenth time.
	cases := map[string]struct {
		variable string
		value    string
	}{
		"selection budget zero":        {"GRAPHQL_MAX_SELECTIONS", "0"},
		"field repetition negative":    {"GRAPHQL_MAX_FIELD_REPETITION", "-1"},
		"introspection root zero":      {"GRAPHQL_MAX_INTROSPECTION_ROOTS", "0"},
		"introspection depth negative": {"GRAPHQL_MAX_INTROSPECTION_DEPTH", "-5"},
		"response byte zero":           {"GRAPHQL_MAX_RESPONSE_BYTES", "0"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := baseSettings(scenarioDatabase(t), freePort(t))
			cfg[tc.variable] = tc.value

			code, stderr := mustStopAtStartup(t, cfg, startupTimeout)

			assert.NotZero(t, code,
				"an invalid limit has to give a non-zero exit code; stderr:\n%s", stderr)
			assert.Contains(t, stderr, tc.variable,
				"stderr has to tell the operator WHICH setting to fix; stderr:\n%s", stderr)
			assert.Contains(t, stderr, "has to be at least 1",
				"the message has to say that the limit cannot be removed; stderr:\n%s", stderr)
		})
	}
}
