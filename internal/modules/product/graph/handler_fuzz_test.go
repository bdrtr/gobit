package graph_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/product/graph"
)

// fuzzHandler is the storefront endpoint the target drives, built once.
//
// It is shared across executions on purpose. The handler carries a parsed-query
// CACHE, and a target that built a fresh one per document would never put two
// documents in the same cache — which is the state a real endpoint is always
// in, and the only state where one request can affect the next.
var fuzzHandler = sync.OnceValue(func() http.Handler {
	return graph.NewHandler(benchCatalogue(), graph.Options{})
})

// FuzzStorefrontDocument feeds arbitrary documents to the storefront endpoint.
//
// # Why this surface
//
// It is the only place in this repository where an ANONYMOUS caller decides the
// shape of the work: a publishable key opens the endpoint, and after that the
// document says how deep to go, how many fields to select and how much to
// return. Seven limits stand between that document and the server, and every
// one of them is computed by walking an attacker-controlled tree.
//
// # What it asserts, and why these three
//
//   - The endpoint answers JSON. A storefront client parses the body before it
//     reads the status, so a body that is not JSON is an unhandled path however
//     the status is spelled.
//   - The status is never 5xx. A document from the wire is INPUT; a server
//     error means something reached a path that did not expect it.
//   - The body never passes [graph.DefaultMaxResponseBytes]. This one is a
//     BACKSTOP and it is written down as such: against the catalog this
//     target serves, no document can reach it, because the field-repetition
//     ceiling caps one request at twenty pages of about 14 KB. It would start
//     asserting the day a limit moved or the fixture grew, and it costs a
//     length comparison until then.
//
// Nothing here asserts that a document RESOLVES. Almost every generated one is
// nonsense and answers with errors, which is correct behavior and not the
// property under test.
//
// # What made the first two assertions real
//
// Both are proved by a plausible defect on the SAME derived seed: writing the
// size refusal with net/http.Error instead of the core envelope answers plain
// text, and building it from an internal error instead of an invalid one
// answers 500. Neither mutation is reachable from a generated document, which
// is the whole argument for the three seeds below being derived.
func FuzzStorefrontDocument(f *testing.F) {
	f.Add(benchDocument)
	f.Add(`{__typename}`)
	f.Add(`{products(limit:1){items{id}}}`)
	f.Add(`{__schema{types{name fields{name type{name}}}}}`)
	f.Add(`query A{...F} fragment F on Query{__typename}`)
	f.Add(`{product(id:"x"){variants{priceSet inventoryItem}}}`)
	f.Add(`mutation{__typename}`)
	f.Add(`{`)
	f.Add(``)

	// The three seeds below are DERIVED, and the reason is measured: forty
	// seconds of generated documents against a handler whose limits had all
	// been mutated to "unlimited" produced NOTHING that broke any of the three
	// assertions. A generated document is nonsense long before it is large, so
	// the gates that only a LARGE or a DEEP document reaches are gates the
	// fuzzer does not arrive at. They are reached by construction instead.
	//
	//   - a body over the 64 KiB ceiling, which is answered by the core's error
	//     envelope rather than a GraphQL one and is therefore the one seed that
	//     exercises a different response shape;
	//   - a fragment bomb, 3^12 selections from 544 bytes, which is what the
	//     selection budget exists for;
	//   - an introspection document, which is measured by a second pair of
	//     ceilings the data tree never touches.
	f.Add(fuzzOversizeDocument())
	f.Add(fuzzFragmentBomb())
	f.Add(`{__schema{types{name fields{name type{name ofType{name ofType{name}}}}}}}`)

	f.Fuzz(func(t *testing.T, document string) {
		body := `{"query":` + strconv.Quote(document) + `}`

		rec := httptest.NewRecorder()
		fuzzHandler().ServeHTTP(rec, benchRequest(body))

		require.Lessf(t, rec.Code, http.StatusInternalServerError,
			"a document from the wire produced %d", rec.Code)

		answer := rec.Body.Bytes()
		require.LessOrEqualf(t, len(answer), graph.DefaultMaxResponseBytes,
			"the answer is %d bytes and the ceiling is %d", len(answer), graph.DefaultMaxResponseBytes)

		require.Truef(t, json.Valid(answer), "the answer is not JSON: %q", answer)
	})
}

// fuzzOversizeDocument builds a document past the 64 KiB request ceiling.
func fuzzOversizeDocument() string {
	return "{" + strings.Repeat("__typename ", 7_000) + "}"
}

// fuzzFragmentBomb builds a document whose expansion is exponential in its size.
//
// Twelve levels of three spreads each is 3^12 selections out of 544 bytes,
// which is the shape [graph.DefaultMaxSelections] exists to refuse. The numbers
// are small on purpose: the point is to cross the budget, and a bomb large
// enough to hurt an unprotected server would also make every fuzz execution
// that reaches it slow.
func fuzzFragmentBomb() string {
	var doc strings.Builder
	doc.WriteString("{...f0}")
	for level := range 12 {
		fmt.Fprintf(&doc, " fragment f%d on Query{", level)
		for range 3 {
			fmt.Fprintf(&doc, "...f%d ", level+1)
		}
		doc.WriteString("}")
	}
	doc.WriteString(" fragment f12 on Query{__typename}")

	return doc.String()
}
