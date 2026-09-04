package graph

import (
	"context"
	"fmt"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/errcode"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// The GraphQL endpoint's risk is DIFFERENT from REST's, and these are the
// defaults that close it.
//
// In REST the SERVER decides the cost of a request: the path is fixed, the
// returned body is fixed, one request is one query. In GraphQL the CLIENT
// decides the cost — it writes the shape of the query. The rate limiter, on the
// other hand, counts the same thing on both surfaces: ONE request. The ways of
// making a thousand times the work with the same quota are closed one by one
// with separate gates:
//
//  1. EXPANSION — the size of the tree after the fragments are expanded
//     ([DefaultMaxSelections]). It runs BEFORE the other gates and protects
//     them.
//  2. DEPTH — nested fields ([DefaultMaxDepth]).
//  3. WIDTH/MULTIPLIER — the shallow but expensive query
//     ([DefaultMaxComplexity]).
//  4. REPETITION — the same field stacked under the same object
//     ([DefaultMaxFieldRepetition]).
//  5. INTROSPECTION — the __schema/__type tree that 2 and 3 cannot see
//     ([DefaultMaxIntrospectionRoots], [DefaultMaxIntrospectionDepth]).
//  6. PARSING — the size of the document itself ([maxQueryBytes],
//     [maxQueryTokens]; handler.go).
//  7. OUTPUT — the response's ACTUAL bytes ([DefaultMaxResponseBytes];
//     handler.go).
//
// Every one of them is needed separately because each catches a document the
// others cannot see, and that is not a guess but a MEASUREMENT:
//
//   - A document of depth 3 can carry hundreds of root queries with aliases;
//     complexity catches it.
//   - A document of low complexity can descend forever into a cyclic field;
//     depth catches it.
//   - Neither of them can give back the parsing cost of a 10 MiB body, because
//     a document can only be measured AFTER it has been PARSED; the body and
//     token limits catch it.
//
// # Why the field count is not enough: BYTES
//
// The complexity model prices the NUMBER OF FIELDS to be resolved, not the
// BYTES. The weight of a field in the response is determined not by its count
// but by its CONTENT, and aliases allow the same field to be selected an
// unlimited number of times:
//
//	products(limit: 100) { items { a0: description … a488: description } }
//
// This document's measured cost is 50,000, that is, it sits EXACTLY on the
// ceiling — because it does not exceed it, it would pass the old gates.
// Measured: an 8,729-byte request produced a 204.9 MiB response (24,620 times)
// and the rate limiter counted it as ONE request. With the default page (20
// products), 1500 aliases produced 125.7 MiB out of 27,415 bytes.
//
// This asymmetry DOES NOT EXIST in REST and the argument "the limit is not
// stricter than REST's" breaks exactly here: a REST client cannot ask for the
// same field 489 times, and the response of GET /store/v1/products?limit=100 is
// ~450 KiB with the same data. So what GraphQL adds is not more records but the
// repeated SERIALIZATION of the SAME record; that is closed only by
// [fieldRepetitionLimit] (by estimate, before execution) and the response byte
// limit (by what actually happens, while writing) working together.
//
// # Why introspection has its OWN gate
//
// The introspection tree was once outside both calculations: the depth count
// skipped the __schema/__type roots, and gqlgen's complexity walk also skips a
// field of type __Schema (complexity/complexity.go). That is, the measured
// depth was 0, the measured complexity was 0, and the operator had no setting
// to turn it off with.
//
// Measured: a __schema document with 302 aliases (45,796 bytes) produced a 5.00
// MiB response, and with Options{MaxDepth: 1, MaxComplexity: 1} the SAME
// document still returned 200 and still 5.00 MiB — while with the same settings
// the query "products { count }" was rejected with "depth 2 exceeds the limit
// of 1". The smallest legitimate data query was being rejected while a 5 MiB
// introspection flood went through.
//
// Today introspection IS COUNTED, but counted against its own ceiling
// ([DefaultMaxIntrospectionDepth]), and the number of roots is limited
// separately ([DefaultMaxIntrospectionRoots]). The separate ceiling is
// essential: the standard introspection query is 13 levels deep (the ofType
// chain), and had a single ceiling been used we would have had to raise the
// limit of the DATA surface above 13.
//
// The constants are EXPORTED so that their agreement with the envDefault tags
// in the core's configuration can be pinned by a test (see internal/arch):
// because the core CANNOT import modules (Principle 2.4), config cannot bind to
// these constants and repeats their values by hand. If they diverged, an
// embedded deployment (product can also be deployed on its own) would run with
// a limit other than the one written in the documentation. Today config reads
// only the depth, the complexity and the introspection switch; environment
// variables for the new gates are a separate change on the core side, and the
// constants being exported already marks the place that change will bind to.
const (
	// DefaultMaxDepth is the default upper limit on the number of fields that
	// may be nested in a single document.
	//
	// The deepest LEGITIMATE path in today's schema is 5
	// (products -> items -> variants -> optionValues -> optionTitle), so 10
	// leaves twice as much room. A more generous default was not chosen: the
	// reason the limit exists is not today's schema but TOMORROW's — the moment
	// a field refers back (variant -> product -> variants -> …) a query
	// descends not as far as the schema allows but as far as the client writes,
	// and every level multiplies the cost.
	//
	// The limit applies only to the DATA tree; introspection has its own
	// ceiling ([DefaultMaxIntrospectionDepth]).
	DefaultMaxDepth = 10

	// DefaultMaxComplexity is the estimated cost ceiling of a single document.
	//
	// The unit is "how many fields get resolved"; on list fields it is
	// MULTIPLIED BY THE NUMBER OF ELEMENTS, and root queries additionally count
	// as one database round trip (see [complexityCosts], [rootQueryCost]).
	//
	// The value was NOT GUESSED, it was measured; the documents and their
	// numbers are PINNED in the calibration table inside graph/limits_test.go
	// (see calibrationDocuments). The byte column was taken with the
	// measurement fixture in the same file (a product with a 4 KiB
	// description):
	//
	//	document                                       request   complexity   response
	//	product page (PDP, everything included)          643 B        2,368    6.8 KiB
	//	category list (24 products, card + price)        118 B        2,344   15.1 KiB
	//	ALL fields on the default page (20 products)     655 B       28,440    136 KiB
	//	ALL fields with limit=100                        667 B      138,200    680 KiB
	//	products { count } with 400 aliases            9.7 KiB      408,000    8.5 KiB
	//	description with 489 aliases (limit=100)       8.5 KiB       50,000  204.9 MiB
	//	description with 1500 aliases (20 products)   26.8 KiB       31,020  125.7 MiB
	//
	// The response column of the last two rows was measured BEFORE the gates
	// were added; today neither of them is executed. The row above them, "ALL
	// fields with limit=100", is the comparison point: pulling the same page
	// from REST also means a body of the same order (680 KiB). So what produced
	// 204.9 MiB is not more RECORDS but the repeated serialization of the same
	// record.
	//
	// 50,000 leaves comfortable room above the heaviest legitimate document
	// (28,440): when a field is added to the schema that query does not press
	// against the limit. A narrower ceiling would save today and force whoever
	// adds a field tomorrow into a configuration change.
	//
	// The last two rows of the table show what the ceiling DOES NOT MEASURE and
	// that is exactly why they were added: the document with 489 aliases sits
	// EXACTLY on 50,000 (it would pass, because the ceiling is not exceeded)
	// and produces a 204.9 MiB response. The one thing a model that estimates
	// cost cannot know is the CONTENT of a field; the gap is closed by
	// [DefaultMaxFieldRepetition] and [DefaultMaxResponseBytes].
	DefaultMaxComplexity = 50000

	// DefaultMaxFieldRepetition is the default upper limit on how many times
	// the same field may be selected under the same object.
	//
	// The count is SIBLING scoped: the (object, field) pairs in one selection
	// set are counted and aliases are IGNORED. "a0: description a1: description
	// …" repeats the same pair; "ofType { ofType { … } }", on the other hand, is
	// a single selection at each level and does not count as repetition. The
	// distinction matters: had the count been document-wide, the standard
	// introspection query (the TypeRef fragment carries __Type.ofType dozens of
	// times) would be rejected — measured, the highest repetition in sibling
	// scope is 1.
	//
	// 20 is not a measurement but the first comfortable number ABOVE legitimate
	// usage: a home page repeats the same root query with aliases for a few
	// storefront strips (featured, new, on sale…) and that does not go past the
	// fingers of one hand. There is no legitimate reason to ask for the SAME
	// field more than twice under the same product. The measured attacks had
	// 489, 1500, 302 and 448 repetitions; there is an order of magnitude
	// between the limit and legitimate usage.
	DefaultMaxFieldRepetition = 20

	// DefaultMaxIntrospectionRoots is the default upper limit on the number of
	// introspection ROOTS in a document.
	//
	// A root is a __schema or __type field at the top of the document. 2 was
	// chosen because no tool asks for __schema twice in the same document; the
	// tools that do ask (schema explorers) send at most one __schema and one
	// __type. The measured flood arrived with 302 roots.
	//
	// The gate OVERLAPS with [DefaultMaxFieldRepetition] but is not redundant:
	// the repetition limit would allow up to 20 roots, and 20 roots means about
	// a fifteenth of the measured 5.00 MiB in response. Because introspection
	// hands out the WHOLE surface in a single request, a narrower number is
	// correct here.
	DefaultMaxIntrospectionRoots = 2

	// DefaultMaxIntrospectionDepth is the default depth ceiling of the
	// introspection subtree.
	//
	// It is separate from the data ceiling and higher than it, because it was
	// measured: the standard introspection query client tools send (gqlgen's
	// introspection.Query) is 13 levels deep — the ofType chain descends that
	// far to unwrap the type wrappers (NonNull, List). Had a single ceiling
	// been used we would have had to raise the limit of the DATA surface above
	// 13 as well, and the loosening would have happened in the very place we
	// want to protect.
	//
	// 15 leaves two levels of room for that query. The subtree also has another
	// ceiling independent of ours: gqlparser's MaxIntrospectionDepth rule cuts
	// nested __Type lists (fields, interfaces, possibleTypes, inputFields) at
	// three levels.
	DefaultMaxIntrospectionDepth = 15

	// DefaultMaxResponseBytes is the largest number of bytes a single response
	// may write to the client.
	//
	// This gate asks a different question from the others: all of them look at
	// the document and ESTIMATE the cost, while this one COUNTS the bytes
	// written. However good an estimate is it cannot know the content of a
	// field — it prices a catalog with a 40 KiB description the same as one
	// with 400 bytes. Without a gate that looks at what actually happens there
	// is no upper bound.
	//
	// 4 MiB rests on measurement: the HEAVIEST legitimate response that gets
	// through today's ceilings (the default page x all fields, with products
	// whose description is 4 KiB) is 136 KiB, that is, the limit leaves roughly
	// 30 times the room — a catalog with long descriptions and rich metadata
	// stays comfortably below it. The measured attack, on the other hand, was
	// producing 204.9 MiB; the limit cuts it by more than 50 times.
	//
	// What happens when the limit is hit is a separate decision and its
	// rationale is in [responseCounter]: half a JSON is not sent.
	DefaultMaxResponseBytes = 4 << 20

	// DefaultMaxSelections is the default upper limit on how many selections a
	// document may carry after its fragments have been EXPANDED.
	//
	// This gate runs before the others and PROTECTS them. The reason was
	// measured: fragment expansion can be EXPONENTIAL and the document that
	// does it is small.
	//
	//	fragment f0 on Product { id }
	//	fragment f1 on Product { ...f0 ...f0 }
	//	fragment f2 on Product { ...f1 ...f1 }
	//	…
	//
	// The document is valid, there is NO cycle (that is the only thing
	// validation rejects) and at 26 levels it is 1,127 BYTES — but its
	// expansion is 2^26 selections. Measured: this document could not be
	// finished by the endpoint in ten seconds. Every calculation that walks the
	// tree was falling into the same trap: the depth count, the field
	// repetition count and gqlgen's own complexity walk
	// (complexity/complexity.go also descends into the fragment definition
	// without memoization). So the problem does not close by fixing a single
	// walk; the way it closes is to bound the size of the tree before ANY walk
	// starts.
	//
	// The count is budgeted: the moment the budget runs out the traversal is
	// cut SHORT, that is, this gate's own cost is the limit itself.
	//
	// 10,000 is just above the token limit ([maxQueryTokens], 8,192) and that
	// is deliberate: the expansion of a document that uses no fragments is
	// already smaller than its token count, that is, the limit only touches
	// documents whose EXPANSION is larger than their own text. The storefront's
	// heaviest legitimate document is just over 90 selections.
	DefaultMaxSelections = 10000
)

// collectionEstimate is the cost multiplier of list fields whose size CANNOT BE
// KNOWN IN ADVANCE.
//
// How many records products will return is read from its argument (see
// [pageSize]), but how many variants or how many images a product has is only
// known once the query RUNS; complexity, on the other hand, has to be computed
// before execution. What is left is an estimate, and the direction of the
// estimate matters: showing LESS THAN REALITY makes exactly the expensive query
// look cheap.
//
// 10 is not a measurement but the point where the estimate stops being cheap: a
// product with 40 variants is 4 times more expensive than the model says, but
// had the list field been given a fixed cost (1) the same product would look 40
// times cheaper — and the limit would let exactly that query through.
//
// Per-field multipliers (variant 10, image 5, tag 3…) were not attempted: a
// second cost model SILENTLY rejects legitimate queries when it breaks, and
// nobody remembers why one field's multiplier is different.
const collectionEstimate = 10

// rootQueryCost is the fixed cost of a root query (products, product).
//
// The rest of the model counts FIELD RESOLUTIONS, but the real price of a root
// query is not the fields resolved: each one is a separate round trip to the
// database, a COUNT over the filtered catalog and then link/batch reads. That
// cost DOES NOT DROP when the client selects fewer fields from the result.
//
// Without a fixed cost the limit would miss exactly the GraphQL-specific
// attack: a document with 400 aliases in the form
// "{ a: products { count } b: products { count } … }" is cheap by field count
// (each one is a single number) but makes the server run 400 catalog queries —
// and the rate limiter counts it as ONE request. In REST, loading the same
// weight means 400 requests, that is, spending 400 units of quota.
//
// 1000 is of the same order as a realistic category list query (~1,300): that
// is, a document carrying 30 root queries is priced like 30 category pages —
// which is exactly what it is.
const rootQueryCost = 1000

// The error codes of the limit overflows.
//
// The shape resembles gqlgen's UPPERCASE codes rather than the core's
// snake_case ones, and that is deliberate: these are not SERVICE errors but
// protocol errors reporting that the document was never executed, and their
// siblings (COMPLEXITY_LIMIT_EXCEEDED) already come from gqlgen in the same
// shape. Returning the same class in two different shapes would make the client
// think they are two separate error classes.
//
// The codes are NOT REGISTERED with errcode.RegisterErrorType; that call
// changes a process-wide map and would mean a single module changing the HTTP
// status code for everyone who uses the library. The cost is that the response
// returns 200 — which in GraphQL is the usual state anyway, with the error in
// the errors array in the body.
const (
	// codeDepthExceeded is the error code of a document that exceeds the depth
	// limit.
	codeDepthExceeded = "DEPTH_LIMIT_EXCEEDED"

	// codeFieldRepetitionExceeded is the error code of a document that repeats
	// the same field too many times under the same object.
	codeFieldRepetitionExceeded = "FIELD_REPETITION_LIMIT_EXCEEDED"

	// codeIntrospectionExceeded is the error code of a document that exceeds
	// one of the introspection gates.
	//
	// It gets a code SEPARATE from the depth overflow because what the client
	// has to do differs: simplifying a data query and splitting an
	// introspection query are not the same fix.
	codeIntrospectionExceeded = "INTROSPECTION_LIMIT_EXCEEDED"

	// codeIntrospectionDisabled is the error code of a document that asks for
	// __schema/__type while introspection is disabled.
	//
	// It is a code SEPARATE from the overflow and the distinction is real for
	// the client: the overflow means "ask for less", this code means "do not
	// ask at all in this deployment".
	codeIntrospectionDisabled = "INTROSPECTION_DISABLED"

	// codeResponseExceeded is the error code of a request that exceeds the
	// response byte limit.
	codeResponseExceeded = "RESPONSE_LIMIT_EXCEEDED"

	// codeSelectionBudgetExceeded is the error code of a document whose
	// fragment expansion exceeds the budget.
	codeSelectionBudgetExceeded = "SELECTION_BUDGET_EXCEEDED"
)

// Options holds the hardening settings of the GraphQL endpoint.
//
// THE ZERO VALUE IS VALID and yields the package defaults; a zero on a field
// does NOT mean "unlimited", it means "use the default". The distinction is
// deliberate: had zero meant "unlimited", a deployment that forgot to fill in
// the setting would open an unprotected endpoint and would do so without any
// error at all — that is the only way hardening disappears silently.
//
// There is NO "unlimited" option at all and that too is deliberate: an
// unlimited GraphQL endpoint means handing resource consumption over to the
// client. A limit CAN BE RAISED, it cannot be removed.
type Options struct {
	// MaxDepth is the upper limit on the number of fields that may be nested in
	// a single document; 0 means [DefaultMaxDepth].
	MaxDepth int

	// MaxComplexity is the estimated cost ceiling of a single document; 0 means
	// [DefaultMaxComplexity].
	MaxComplexity int

	// MaxFieldRepetition is how many times the same field may be selected under
	// the same object; 0 means [DefaultMaxFieldRepetition].
	MaxFieldRepetition int

	// MaxIntrospectionRoots is the upper limit on the number of __schema/__type
	// roots in a document; 0 means [DefaultMaxIntrospectionRoots].
	MaxIntrospectionRoots int

	// MaxIntrospectionDepth is the depth ceiling of the introspection subtree;
	// 0 means [DefaultMaxIntrospectionDepth].
	MaxIntrospectionDepth int

	// MaxResponseBytes is the largest number of bytes a single response may be;
	// 0 means [DefaultMaxResponseBytes].
	MaxResponseBytes int

	// MaxSelections is the upper limit on the number of selections in a
	// document after its fragments have been expanded; 0 means
	// [DefaultMaxSelections].
	MaxSelections int

	// IntrospectionDisabled turns introspection off.
	//
	// The field is named NEGATIVELY because the zero value must give the
	// package default, and the default is ENABLED: had it been "Introspection
	// bool", every handler set up with Options{} would silently disable
	// introspection and schema tools would go blind with no rationale in sight.
	//
	// # The rationale for the default
	//
	// Introspection hands out the whole surface in a SINGLE request; disabling
	// it looks at first glance like free hardening. For this storefront it is
	// not: the schema is a FILE that sits inside this repository
	// (graph/schema.graphqls) and every gobit deployment serves the same one.
	// Disabling it hides from client tools (code generators, IDE plugins,
	// schema-diff flows) something an attacker can read with "git clone". The
	// endpoint is already behind the publishable key and the rate limit, and
	// its cost is bound by the limits in this file.
	//
	// The sentence "the limits bind the cost" was once NOT TRUE: introspection
	// was outside both the depth and the complexity calculation, and turning
	// the switch off was the endpoint's only defense. Today introspection has
	// its own gates ([MaxIntrospectionRoots], [MaxIntrospectionDepth]), that
	// is, this switch is no longer an emergency valve but a surface decision.
	//
	// When the calculus CHANGES, the switch is here: a fork that adds its own
	// fields to the schema, or a deployment that does not want to announce the
	// surface it extended, turns it off in one line. The switch existing makes
	// "the surface is not visible" a DECISION rather than an accident.
	//
	// # The switch also turns off SUGGESTIONS
	//
	// Disabling introspection once DID NOT HIDE the schema and the switch was
	// not doing exactly what it promised: even with __schema closed, the
	// validator was handing out the schema's names retail. Measured (all of
	// them in a single request, in a single response):
	//
	//	prodcts             -> Did you mean "products" or "product"?
	//	itemz               -> Did you mean "items"?
	//	fragment on Prodct  -> Unknown type "Prodct". Did you mean "Product"?
	//	products(limitt: …) -> Unknown argument "limitt"… Did you mean "limit"?
	//
	// Because the validator collects ALL the errors in a document into a single
	// response, dozens of names can be tried in one request; the rate limit is
	// no obstacle to that, since it counts this as one request too.
	//
	// That is why the switch has two halves and both are set up in
	// [NewHandler]: gqlgen's SetDisableSuggestion does not COMPUTE the
	// suggestion of the two rules it can reach at all (levenshtein runs over
	// all the names of the type for every unknown field), while the sentence of
	// the rules it cannot reach is cut in [protocolError].
	//
	// What is closed is the ENUMERATION OF NAMES, not the GUESSING of them: an
	// invalid field still produces a validation error, that is, trying them one
	// by one is still possible. The only way to close that too would be to
	// erase validation messages entirely, and then the surface would become
	// impossible to debug for the legitimate client as well — what is closed is
	// the list that takes the attacker's work from n attempts down to one.
	IntrospectionDisabled bool
}

// maxDepth returns the depth limit to apply.
func (o Options) maxDepth() int {
	return limitOrDefault(o.MaxDepth, DefaultMaxDepth)
}

// maxComplexity returns the complexity limit to apply.
func (o Options) maxComplexity() int {
	return limitOrDefault(o.MaxComplexity, DefaultMaxComplexity)
}

// maxFieldRepetition returns the field repetition limit to apply.
func (o Options) maxFieldRepetition() int {
	return limitOrDefault(o.MaxFieldRepetition, DefaultMaxFieldRepetition)
}

// maxIntrospectionRoots returns the introspection root limit to apply.
func (o Options) maxIntrospectionRoots() int {
	return limitOrDefault(o.MaxIntrospectionRoots, DefaultMaxIntrospectionRoots)
}

// maxIntrospectionDepth returns the introspection depth limit to apply.
func (o Options) maxIntrospectionDepth() int {
	return limitOrDefault(o.MaxIntrospectionDepth, DefaultMaxIntrospectionDepth)
}

// maxResponseBytes returns the response byte limit to apply.
func (o Options) maxResponseBytes() int {
	return limitOrDefault(o.MaxResponseBytes, DefaultMaxResponseBytes)
}

// maxSelections returns the selection budget to apply.
func (o Options) maxSelections() int {
	return limitOrDefault(o.MaxSelections, DefaultMaxSelections)
}

// limitOrDefault returns the given setting, or the default if it is invalid.
//
// The reason there is a single helper is that the settings MULTIPLY: had every
// field carried its own if, forgetting the "0 falls back to the default" rule
// while adding a new limit would be only one line away — and the field where it
// was forgotten would silently become "unlimited".
func limitOrDefault(value, fallback int) int {
	if value <= 0 {
		return fallback
	}

	return value
}

// selectionBudget is the gqlgen extension that limits the size of the fragment
// expansion.
//
// It runs BEFORE the other gates and its only job is to protect them: every
// calculation after it walks the document tree, and because fragments can
// expand exponentially the size of that tree is independent of the size of the
// document. The measurement behind the rationale is in [DefaultMaxSelections].
type selectionBudget struct{ limit int }

var _ interface {
	graphql.HandlerExtension
	graphql.OperationContextMutator
} = selectionBudget{}

// ExtensionName returns the name of the extension.
func (selectionBudget) ExtensionName() string { return "SelectionBudget" }

// Validate verifies that the extension was set up with a valid limit.
func (s selectionBudget) Validate(graphql.ExecutableSchema) error {
	if s.limit < 1 {
		return fmt.Errorf("graph: the selection budget must be at least 1, got %d", s.limit)
	}

	return nil
}

// MutateOperationContext measures the document's expansion against the budget.
func (s selectionBudget) MutateOperationContext(
	_ context.Context,
	opCtx *graphql.OperationContext,
) *gqlerror.Error {
	remaining := s.limit
	if countSelections(opCtx.Operation.SelectionSet, &remaining) {
		return nil
	}

	gqlErr := gqlerror.Errorf(
		"operation expands to more than %d selections, which exceeds the limit", s.limit)
	errcode.Set(gqlErr, codeSelectionBudgetExceeded)

	return gqlErr
}

// countSelections counts the expanded tree until the budget runs out.
//
// The budget travels as what is REMAINING, not as a COUNTER, and the traversal
// is cut short the moment it runs out. The distinction is the same as the
// reason the gate exists: an implementation that counts first and compares
// afterwards would be forced to walk the exponential tree it is trying to
// measure all the way to the end — that is, while applying the limit it would
// do exactly the work the limit prevents.
func countSelections(selections ast.SelectionSet, remaining *int) bool {
	for _, selection := range selections {
		if *remaining <= 0 {
			return false
		}

		*remaining--

		var child ast.SelectionSet

		switch s := selection.(type) {
		case *ast.Field:
			child = s.SelectionSet
		case *ast.FragmentSpread:
			child = s.Definition.SelectionSet
		case *ast.InlineFragment:
			child = s.SelectionSet
		}

		if !countSelections(child, remaining) {
			return false
		}
	}

	return true
}

// depthLimit is the gqlgen extension that limits the nesting depth of the
// document.
//
// gqlgen has NO depth limit (it has a complexity limit) and the two do not
// replace each other: complexity measures the NUMBER of fields to be resolved
// and does not punish depth as long as the cost of every level on a cyclic path
// stays the same.
//
// It carries two ceilings because two separate trees are measured: the data
// tree and the introspection tree. The rationale is in
// [DefaultMaxIntrospectionDepth].
type depthLimit struct {
	limit              int
	introspectionLimit int
}

// That the extension satisfies gqlgen's contract is pinned at compile time: if
// the signature of MutateOperationContext drifts, the extension is silently
// never called.
var _ interface {
	graphql.HandlerExtension
	graphql.OperationContextMutator
} = depthLimit{}

// ExtensionName returns the name of the extension.
func (depthLimit) ExtensionName() string { return "DepthLimit" }

// Validate verifies that the extension was set up with a valid limit.
//
// gqlgen calls this method at SETUP time and its error blows up at startup; had
// the limit been checked at run time, an endpoint set up with a zero limit
// would reject every document and the fault would only show up on the first
// request.
func (d depthLimit) Validate(graphql.ExecutableSchema) error {
	if d.limit < 1 {
		return fmt.Errorf("graph: the depth limit must be at least 1, got %d", d.limit)
	}

	if d.introspectionLimit < 1 {
		return fmt.Errorf("graph: the introspection depth limit must be at least 1, got %d", d.introspectionLimit)
	}

	return nil
}

// MutateOperationContext measures the document's depth BEFORE EXECUTING it.
//
// The step runs AFTER parsing and validation (see the gqlgen executor): at that
// point the fragment definitions have been resolved and fragment CYCLES have
// been rejected by validation. The second one is vital here — a document with a
// cycle sends the recursion below into infinity, and in Go a stack overflow
// cannot be recovered from: it is not a panic but the death of the whole
// process.
//
// Fragments that are cycle-free but expand EXPONENTIALLY pass validation and
// would still lock up the walk below; they are bound by [selectionBudget],
// which runs BEFORE this extension. If the budget is removed, the recursion
// here is unbounded again.
func (d depthLimit) MutateOperationContext(
	_ context.Context,
	opCtx *graphql.OperationContext,
) *gqlerror.Error {
	data, introspection := depths(opCtx.Operation.SelectionSet)

	// The messages are IN ENGLISH and that is deliberate: their sibling, the
	// complexity error, is produced by gqlgen and we cannot choose its text.
	// Two limits speaking two different languages in the same document would
	// make the client think they are two separate error classes.
	if data > d.limit {
		gqlErr := gqlerror.Errorf("operation has depth %d, which exceeds the limit of %d", data, d.limit)
		errcode.Set(gqlErr, codeDepthExceeded)

		return gqlErr
	}

	if introspection > d.introspectionLimit {
		gqlErr := gqlerror.Errorf(
			"introspection selection has depth %d, which exceeds the limit of %d",
			introspection, d.introspectionLimit)
		errcode.Set(gqlErr, codeIntrospectionExceeded)

		return gqlErr
	}

	return nil
}

// depths returns the depths of the document's data and introspection trees
// SEPARATELY.
//
// The split is made only at the TOP and that is enough: __schema and __type are
// fields of Query in the schema and exist on no other type; that is, an
// introspection root can only appear in the document's topmost selection set
// (or in a fragment expanded into it). Further down, [selectionDepth] counts
// with a single rule.
func depths(selections ast.SelectionSet) (data, introspection int) {
	for _, selection := range selections {
		switch s := selection.(type) {
		case *ast.Field:
			depth := 1 + selectionDepth(s.SelectionSet)
			if isIntrospectionField(s.Name) {
				introspection = max(introspection, depth)
			} else {
				data = max(data, depth)
			}
		case *ast.FragmentSpread:
			childData, childIntrospection := depths(s.Definition.SelectionSet)
			data, introspection = max(data, childData), max(introspection, childIntrospection)
		case *ast.InlineFragment:
			childData, childIntrospection := depths(s.SelectionSet)
			data, introspection = max(data, childData), max(introspection, childIntrospection)
		}
	}

	return data, introspection
}

// selectionDepth returns the length of the longest field chain in the selection
// set.
//
// The counting rules:
//
//   - Every field is one level; leaf fields count too. "{ products { count } }"
//     is 2.
//   - A fragment (spread and inline) ADDS NO level: its contents are at the
//     level where the spread sits. Otherwise a client that split its query into
//     fragments would hit the limit while asking for the same tree — and with
//     no reason at all not to split it.
//
// There are NO EXCEPTIONS: introspection fields are counted too. Subjecting
// them to a separate ceiling happens not here but in [depths], which separates
// the trees at the top.
func selectionDepth(selections ast.SelectionSet) int {
	deepest := 0

	for _, selection := range selections {
		var depth int

		switch s := selection.(type) {
		case *ast.Field:
			depth = 1 + selectionDepth(s.SelectionSet)
		case *ast.FragmentSpread:
			depth = selectionDepth(s.Definition.SelectionSet)
		case *ast.InlineFragment:
			depth = selectionDepth(s.SelectionSet)
		}

		if depth > deepest {
			deepest = depth
		}
	}

	return deepest
}

// isIntrospectionField reports that the field is an introspection root.
//
// __typename is deliberately LEFT OUT: it is not a root but a leaf that exists
// on every type and returns a single string; counting it against the
// introspection quota would waste the quota of every client that normalizes
// (Apollo, urql — they add __typename themselves).
func isIntrospectionField(name string) bool {
	return name == "__schema" || name == "__type"
}

// introspectionRootLimit is the gqlgen extension that limits the number of
// __schema/__type roots in a document.
//
// It is an extension SEPARATE from depth because what it measures is separate:
// depth asks how far down the tree goes, this gate asks how many times the same
// tree was requested. Each of the 302 roots is shallow (4 levels in the
// measured document), that is, the depth gate could never see it.
//
// While introspection is DISABLED the same gate rejects the document
// altogether, and that is meant to get ahead of gqlgen's own behavior: gqlgen
// rejects the field at execution time with a plain errors.New, that error
// cannot be told apart from resolver errors and would rightly be counted as a
// server error by [errorPresenter] — that is, every introspection attempt would
// produce an ERROR line. Rejecting it here never executes the document and puts
// the decision next to the document's own gates.
type introspectionRootLimit struct {
	limit    int
	disabled bool
}

var _ interface {
	graphql.HandlerExtension
	graphql.OperationContextMutator
} = introspectionRootLimit{}

// ExtensionName returns the name of the extension.
func (introspectionRootLimit) ExtensionName() string { return "IntrospectionRootLimit" }

// Validate verifies that the extension was set up with a valid limit.
func (i introspectionRootLimit) Validate(graphql.ExecutableSchema) error {
	if i.limit < 1 {
		return fmt.Errorf("graph: the introspection root limit must be at least 1, got %d", i.limit)
	}

	return nil
}

// MutateOperationContext counts the introspection roots in the document.
func (i introspectionRootLimit) MutateOperationContext(
	_ context.Context,
	opCtx *graphql.OperationContext,
) *gqlerror.Error {
	roots := countIntrospectionRoots(opCtx.Operation.SelectionSet)
	if roots == 0 {
		return nil
	}

	if i.disabled {
		gqlErr := gqlerror.Errorf("introspection is disabled on this endpoint")
		errcode.Set(gqlErr, codeIntrospectionDisabled)

		return gqlErr
	}

	if roots <= i.limit {
		return nil
	}

	gqlErr := gqlerror.Errorf(
		"operation selects %d introspection roots, which exceeds the limit of %d", roots, i.limit)
	errcode.Set(gqlErr, codeIntrospectionExceeded)

	return gqlErr
}

// countIntrospectionRoots counts the introspection roots at the top of the
// document.
func countIntrospectionRoots(selections ast.SelectionSet) int {
	count := 0

	for _, selection := range selections {
		switch s := selection.(type) {
		case *ast.Field:
			if isIntrospectionField(s.Name) {
				count++
			}
		case *ast.FragmentSpread:
			count += countIntrospectionRoots(s.Definition.SelectionSet)
		case *ast.InlineFragment:
			count += countIntrospectionRoots(s.SelectionSet)
		}
	}

	return count
}

// fieldRepetitionLimit is the gqlgen extension that limits how many times the
// same field may be selected under the same object.
//
// It sees what the complexity gate CANNOT SEE. Complexity prices the number of
// fields, and "a0: description … a488: description" is 489 cheap fields; yet in
// the response it is a text SERIALIZED 489 times. Measured: with 100 products
// per page this document's cost is exactly 50,000 — it sits on the ceiling,
// does not exceed it, and would pass — and it was producing a 204.9 MiB
// response.
//
// The gate works together with [DefaultMaxResponseBytes] and does not replace
// it: this gate rejects BEFORE EXECUTION (the server does no work at all),
// while the other catches what the estimate missed, while writing.
type fieldRepetitionLimit struct{ limit int }

var _ interface {
	graphql.HandlerExtension
	graphql.OperationContextMutator
} = fieldRepetitionLimit{}

// ExtensionName returns the name of the extension.
func (fieldRepetitionLimit) ExtensionName() string { return "FieldRepetitionLimit" }

// Validate verifies that the extension was set up with a valid limit.
func (a fieldRepetitionLimit) Validate(graphql.ExecutableSchema) error {
	if a.limit < 1 {
		return fmt.Errorf("graph: the field repetition limit must be at least 1, got %d", a.limit)
	}

	return nil
}

// MutateOperationContext finds the most repeated field and compares it against
// the limit.
func (a fieldRepetitionLimit) MutateOperationContext(
	_ context.Context,
	opCtx *graphql.OperationContext,
) *gqlerror.Error {
	field, repeats := mostRepeatedField(opCtx.Operation.SelectionSet)
	if repeats <= a.limit {
		return nil
	}

	gqlErr := gqlerror.Errorf(
		"field %s is selected %d times under the same object, which exceeds the limit of %d",
		field, repeats, a.limit)
	errcode.Set(gqlErr, codeFieldRepetitionExceeded)

	return gqlErr
}

// mostRepeatedField returns the most repeated (object, field) pair and its
// repetition count.
//
// The count is SIBLING scoped: every selection set is counted within itself,
// and the levels below separately. Had the whole document been counted, the
// measurement would punish the wrong thing — in the standard introspection
// query __Type.ofType appears dozens of times in separate chains and none of
// them is a stack; whereas the shape of the attack is exactly a SIBLING stack
// (489 aliases in the same selection set).
//
// The alias DOES NOT ENTER the key: the attack's only instrument is the alias,
// and a count that looked at it would count nothing.
//
// The cost of the walk depends NOT on the document's text but on its expansion;
// exponentially expanding fragments would lock it up, and that is why
// [selectionBudget] runs before this extension. Fragment cycles have been
// rejected in validation; the rationale is in
// [depthLimit.MutateOperationContext].
func mostRepeatedField(selections ast.SelectionSet) (pair string, repeats int) {
	fields := siblingFields(selections)

	counter := make(map[string]int, len(fields))
	for _, field := range fields {
		key := fieldKey(field)

		counter[key]++
		if counter[key] > repeats {
			pair, repeats = key, counter[key]
		}
	}

	for _, field := range fields {
		childPair, childRepeats := mostRepeatedField(field.SelectionSet)
		if childRepeats > repeats {
			pair, repeats = childPair, childRepeats
		}
	}

	return pair, repeats
}

// siblingFields collects the fields of the selection set that are at the SAME
// level, expanding the fragments.
//
// Fragments add no level (see [selectionDepth]) and must add none here either:
// if a client that moved its stack into a fragment could escape the count, the
// gate would be decoration.
func siblingFields(selections ast.SelectionSet) []*ast.Field {
	var fields []*ast.Field

	for _, selection := range selections {
		switch s := selection.(type) {
		case *ast.Field:
			fields = append(fields, s)
		case *ast.FragmentSpread:
			fields = append(fields, siblingFields(s.Definition.SelectionSet)...)
		case *ast.InlineFragment:
			fields = append(fields, siblingFields(s.SelectionSet)...)
		}
	}

	return fields
}

// fieldKey names the field in the form "Type.field".
//
// The object name enters the key because the limit is not about "the same
// field" but about "the same field UNDER THE SAME OBJECT": selecting fields of
// the same name from different types with inline fragments is legitimate, and
// if they fell into a single counter a legitimate document would be rejected.
//
// ObjectDefinition is filled in after validation; it can only be empty for a
// field that is not in the schema, and such a document has already been
// rejected in validation. Even so nil is handled: a panic here would turn an
// invalid document into a 500.
func fieldKey(field *ast.Field) string {
	if field.ObjectDefinition == nil {
		return field.Name
	}

	return field.ObjectDefinition.Name + "." + field.Name
}

// complexityCosts multiplies the cost of list fields BY THE NUMBER OF ELEMENTS.
//
// gqlgen's default calculation gives every field 1 + the child cost; that is,
// "products(limit: 100) { items { … } }" and "products(limit: 1) { items { … } }"
// look like they cost the SAME. Yet the difference between them is exactly a
// hundredfold, and a cost model that makes the expensive query look cheap turns
// the limit into decoration.
//
// The multiplier comes from two sources:
//
//   - On products, FROM THE ARGUMENT: the client has said how many records will
//     be returned (see [pageSize]).
//   - On nested lists, FROM AN ESTIMATE: the count is known at run time,
//     whereas complexity is computed before execution (see
//     [collectionEstimate]).
//
// The multiplier is put on Query.products, NOT on ProductList.items: had it
// been put on both, it would rise to the SQUARE of the page size. The
// multiplier of products also covers the envelope's count/offset/limit fields —
// this overcount of a few units was not corrected, because it shifts the
// ceiling in the safe direction.
//
// Root queries additionally carry a fixed base ([rootQueryCost]): the
// multiplication prices only the RESOLVED FIELD, whereas the price of a root
// query is the database round trip and it does not drop when fewer fields are
// selected.
func complexityCosts(costs *ComplexityRoot) {
	costs.Query.Products = func(child int, limit, _ *int, _, _ *string) int {
		return rootQueryCost + pageSize(limit)*child
	}

	costs.Query.Product = func(child int, _, _ *string) int {
		return rootQueryCost + child
	}

	costs.Product.Variants = collectionCost
	costs.Product.Options = collectionCost
	costs.Product.Images = collectionCost
	costs.Product.Tags = collectionCost
	costs.Product.Categories = collectionCost
	costs.Option.Values = collectionCost
	costs.Variant.OptionValues = collectionCost
}

// collectionCost returns the cost of a list field whose size is unknown.
func collectionCost(child int) int {
	return collectionEstimate * child
}

// pageSize estimates the largest number of records a products call may return.
//
// It is not a second DEFINITION of the paging rule; it is an ESTIMATE of the
// result the service will apply, and if they diverge it is not the returned
// page but only the cost estimate that goes wrong. Even so it is read from the
// service's constants so that the estimate corrects itself when the ceiling
// changes.
//
// A negative limit also falls back to the default: the service will reject it,
// and the only thing to do here is not to zero out the cost (a zero cost would
// leave the limit unapplied).
func pageSize(limit *int) int {
	switch {
	case limit == nil || *limit <= 0:
		return service.DefaultLimit
	case *limit > service.MaxLimit:
		return service.MaxLimit
	default:
		return *limit
	}
}
