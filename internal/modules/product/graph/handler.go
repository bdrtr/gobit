package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"strings"
	"sync/atomic"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/errcode"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// cacheEntryCount is the largest number of entries the cache of parsed query
// documents may hold.
//
// Storefront clients send the SAME document (only the variables differ) over
// and over; parsing and validating it again on every request is doing the same
// work every single time. The number is far larger than the variety of
// documents a storefront maintains (a few queries per page).
//
// THE ENTRY COUNT IS NOT A LIMIT ON ITS OWN, and it was once believed to be
// one: 100 entries, with no per-entry ceiling, means room for 100 x the
// document size. Measured: 100 documents of 65 KB — all of them rejected at the
// complexity limit, none of them ever reaching the service — left 171.8 MiB of
// PERMANENT heap after runtime.GC, that is, 26 times more than the 6.5 MB
// upload. That is why [maxCachedDocumentBytes] was added beside the number.
const cacheEntryCount = 100

// maxCachedDocumentBytes is the largest number of bytes a document may have to
// be allowed into the cache.
//
// The document's TEXT is used as the measure because it is the only thing that
// can be measured cheaply: the cache's key is the raw query text anyway, and
// the real size of the stored tree is a multiple of the text — but learning
// that factor means walking the tree, that is, redoing on every insertion the
// very work the cache saves.
//
// 8 KiB is FAR above the storefront's real documents: the heaviest legitimate
// query measured (default page x all fields of the schema) is 655 bytes, and a
// fragment-heavy storefront document is 6.3 KB. Being one eighth of the body
// limit (64 KiB) is deliberate: the body limit answers the question "is it
// worth parsing", this limit answers "is it worth STORING", and the threshold
// of the second is much lower — the cost of storing lasts not for the lifetime
// of the request but until it is evicted from the cache.
//
// A document below the limit is not stored automatically either; it must also
// have PASSED the limits (see [queryCache]).
const maxCachedDocumentBytes = 8 << 10

// maxQueryBytes is the upper limit of a single GraphQL request body.
//
// This limit does the work the depth and complexity limits CANNOT do: both of
// those can only be measured AFTER the document has been PARSED, that is, by
// the time they are reached the server has already read and parsed a 10 MiB
// "{a{a{a…" text. The cost of parsing is bounded only by the body limit.
//
// The value is smaller than the one on the REST side (1 MiB) and that is
// deliberate: the body over there carries a RECORD (a product with its
// variants and images), while the body here is a QUERY TEXT and the read
// surface's variables (identity, handle, page) are a few hundred bytes. 64 KiB
// is far above a large storefront query split into fragments.
//
// The limit is applied in TWO separate places and both are needed; the
// rationale is in [bodyLimit].
const maxQueryBytes = 64 << 10

// maxQueryTokens is the largest number of tokens a single document may be
// parsed into.
//
// It is the sibling of [maxQueryBytes] and closes the gap that one is forced to
// leave: the byte limit can only be applied while the body is being read,
// whereas the token limit works INSIDE THE PARSING and stops the parser the
// moment the limit is exceeded. So this is the cheapest gate — it rejects the
// document without parsing it to the end.
//
// The value was measured. A 64 KiB body can carry 32,000 tokens with the
// cheapest tokens ("a a a …"); yet the storefront's real documents are 95
// tokens (default page x all fields) and a fragment-heavy document with ten
// root queries is 922. 8,192 is both roughly nine times legitimate usage and a
// quarter of what the byte limit alone would allow.
//
// There are measured documents this gate catches on its own: a __schema with
// 302 aliases is 9,364 tokens and a __type with 448 aliases is 14,786 tokens,
// and both were getting through the body limit because they stayed under it
// (45,796 and 59,924 bytes).
//
// Like [maxQueryBytes] it is FIXED, not opened up to configuration: both bind
// the parsing of the document and loosening this family is not a capacity
// choice but opening the parser up to the client.
const maxQueryTokens = 8192

// NewHandler builds the HTTP handler of the GraphQL storefront endpoint with
// the given limits.
//
// For the rationale of the limits and the meaning of the zero value see
// [Options]; on this endpoint the cost is decided by whoever WRITES the
// request, so the limits are not a setting but the operating condition of the
// surface.
//
// # POST ONLY
//
// The GET transport was DELIBERATELY not added. GET's only real gain is
// intermediate caches and that gain does NOT EXIST here: the response varies
// with the request's publishable key, that is, with the sales channel. A shared
// cache would either have to vary by the key header (that is, cache almost
// nothing) or serve one storefront's catalog to another — which is exactly why
// the channel filter exists.
//
// In return two concrete costs would be paid: the whole query goes into the URL
// and lands in access logs, proxy logs and browser history; and long queries
// die at common proxy limits (~8 KiB) with a 414 the client cannot diagnose.
//
// The endpoint is registered with chi for POST only (see api/routes.go), so a
// GET request gets an honest 405 instead of gqlgen's "transport not supported"
// 400.
//
// # The ORDER of the gates
//
// Extensions are run in registration order and the order is not a preference
// but the consequence of two obligations.
//
// [selectionBudget] is FIRST because every gate after it walks the document
// tree and fragments can expand exponentially (see [DefaultMaxSelections]); no
// walk is safe until the size of the tree is bounded. After it the order goes
// from cheap to expensive: depth and the introspection root walk the tree once,
// field repetition builds one map per level, and complexity looks up the schema
// for every field.
//
// [cacheAdmission] is LAST and that too is the operating condition of
// [queryCache]: every gate that runs before it keeps the document it rejects
// out of the cache.
//
// # Protection
//
// Authentication, the rate limit and idempotency are NOT SET UP HERE; they come
// from the protection stack bound to the /store/v1 prefix (see
// corehttp.APIGuards). Repeating the stack here would create a second
// definition of the same rule.
func NewHandler(svc Storefront, opts Options) http.Handler {
	srv, _ := newServer(svc, opts)

	return bodyLimit(responseLimit(srv, opts.maxResponseBytes()))
}

// newServer sets up the gqlgen server and its query cache.
//
// The cache is ALSO returned because its contents are a BEHAVIORAL claim — a
// rejected document must not be stored (see [queryCache]) — and that claim
// cannot be observed from outside the handler. Writing a second setup for the
// test would mean the test verifying its own copy rather than the real
// registration order; and the order of the gates is the fix itself. The
// rationale for the order is in [NewHandler].
func newServer(svc Storefront, opts Options) (*handler.Server, *queryCache) {
	cfg := Config{Resolvers: NewResolver(svc)}
	complexityCosts(&cfg.Complexity)

	srv := handler.New(NewExecutableSchema(cfg))

	srv.AddTransport(transport.POST{})
	srv.SetParserTokenLimit(maxQueryTokens)

	cache := newQueryCache(cacheEntryCount, maxCachedDocumentBytes)
	srv.SetQueryCache(cache)

	srv.Use(selectionBudget{limit: opts.maxSelections()})
	srv.Use(depthLimit{
		limit:              opts.maxDepth(),
		introspectionLimit: opts.maxIntrospectionDepth(),
	})
	srv.Use(introspectionRootLimit{
		limit:    opts.maxIntrospectionRoots(),
		disabled: opts.IntrospectionDisabled,
	})
	srv.Use(fieldRepetitionLimit{limit: opts.maxFieldRepetition()})
	srv.Use(extension.FixedComplexityLimit(opts.maxComplexity()))

	// Introspection is ENABLED by default and a deployment may disable it; the
	// rationale for the decision is on the [Options.IntrospectionDisabled]
	// field. In gqlgen introspection is disabled when the extension is NOT
	// INSTALLED (the OperationContext's DisableIntrospection field is born
	// true), so the way to disable it is to never add the extension. The gate
	// that actually rejects the document is [introspectionRootLimit] above;
	// the omission here is the safety net behind it.
	//
	// Suggestions are bound to the SAME switch and that is not a preference but
	// the completion of the switch's promise: the validator's "Did you mean …?"
	// sentences hand out the schema's names retail (see
	// [Options.IntrospectionDisabled]). gqlgen's switch does not COMPUTE the
	// suggestion of the two rules it can reach at all; the sentence of the
	// rules it cannot reach is cut in [protocolError].
	if opts.IntrospectionDisabled {
		srv.SetDisableSuggestion(true)
	} else {
		srv.Use(extension.Introspection{})
	}

	srv.Use(cacheAdmission{cache: cache})

	srv.SetErrorPresenter(errorPresenter(opts))
	srv.SetRecoverFunc(recoverPanic)

	return srv, cache
}

// bodyLimit wraps the handler with the request body limit.
//
// The limit cannot be set up INSIDE gqlgen: the server reads the body in its
// own transport and offers no hook for replacing the reader.
//
// # Two gates
//
// The declared size (Content-Length) is rejected HERE and the response is the
// core's error envelope — the same shape, the same code and the same request
// identity as every endpoint under /store/v1. The rule is this: the GraphQL
// envelope (data/errors) belongs only to documents that REACHED THE EXECUTOR;
// what comes before that — an unauthorized request, an unsupported method, a
// body that does not fit — is an ordinary HTTP error on this surface too. The
// endpoint already behaves that way: a request without a publishable key gets
// its 401 in the core envelope, and a GET request gets a 405 from chi.
//
// But Content-Length is a CLAIM: it does not exist at all on a chunked body and
// it can be wrong. That is why the real limit is applied by
// [net/http.MaxBytesReader]; a request that falls down that road gets the
// GraphQL envelope (200 + errors), so the second shape is visible only to a
// client that HIDES its size. The alternative was to read and count the whole
// body here — doing exactly what we want to avoid.
//
// Only the ENVELOPE changes, not the sentence: the moment the limit cuts is
// recorded in [bodyOverflow] and [transportError] states the same reason with
// the same number. Without the record the client would only see gqlgen's own
// sentence, and that sentence cannot be recognized on our side without parsing
// the transport's message.
func bodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxQueryBytes {
			corehttp.WriteError(r.Context(), w, coreerrors.Invalid(codeBodyTooLarge,
				"the GraphQL document is too large (at most %d bytes)", maxQueryBytes))

			return
		}

		state := &bodyOverflow{}
		r = r.WithContext(context.WithValue(r.Context(), bodyOverflowKey{}, state))

		// The response writer is handed over too, so that the server can end
		// the request cleanly when the limit is exceeded. Otherwise the
		// connection would hang with a half-read body. The RAW writer is given,
		// NOT the counting wrapper; when the stdlib can convert the writer to
		// its own internal interface it marks the connection, and an
		// intervening type would silently remove that behavior.
		r.Body = bodyReader{
			ReadCloser: http.MaxBytesReader(w, r.Body, maxQueryBytes),
			state:      state,
		}

		next.ServeHTTP(w, r)
	})
}

// bodyOverflowKey is the context key of the body overflow record.
//
// It has its own type: using a string as the key would leave the door open to
// reading a value another package wrote with the same string.
type bodyOverflowKey struct{}

// bodyOverflow carries whether the request exceeded the body limit.
//
// The information travels in the context because gqlgen's transport stands
// between the place that PRODUCES it ([bodyLimit]) and the place that NEEDS it
// ([transportError]): the transport buries the read error in its own sentence
// ("could not read request body: %+v") and digging the reason back out of it
// would mean defining the library's text a second time. The record is not a
// text but a MEASUREMENT.
//
// The field is atomic because no contract says the goroutine that reads the
// body and the goroutine that presents the error will be the same one.
type bodyOverflow struct{ exceeded atomic.Bool }

// bodyReader is the request body reader that records the MOMENT the limit cuts.
type bodyReader struct {
	io.ReadCloser

	state *bodyOverflow
}

// Read passes the read through and marks the limit overflow.
func (g bodyReader) Read(p []byte) (int, error) {
	n, err := g.ReadCloser.Read(p)

	// The type is checked, NOT the text: the stdlib reports the overflow with
	// its own error type and may change its sentence one day.
	var overflow *http.MaxBytesError
	if errors.As(err, &overflow) {
		g.state.exceeded.Store(true)
	}

	return n, err
}

// bodyExceeded reports whether the request exceeded the body limit.
//
// It returns false when there is no record: if [bodyLimit] is not in play (a
// server set up from inside the package) there is no overflow either.
func bodyExceeded(ctx context.Context) bool {
	state, ok := ctx.Value(bodyOverflowKey{}).(*bodyOverflow)

	return ok && state.exceeded.Load()
}

// responseLimit wraps the handler with the response byte limit.
//
// This gate asks a different question from the others and that is exactly why
// it is needed: depth, repetition and complexity look at the document and
// ESTIMATE the cost; what is counted here is the byte that ACTUALLY HAPPENED.
// However good an estimate is it cannot know the contents of a field, so the
// last word belongs to measurement (see [DefaultMaxResponseBytes]).
//
// The wrapper is set up OUTSIDE gqlgen because it cannot stop anything inside:
// gqlgen's server catches every panic in its own ServeHTTP and writes a 500,
// that is, the decision to drop the connection cannot be made there. It is made
// outside, and corehttp.Recoverer re-panics http.ErrAbortHandler so it reaches
// the stdlib.
func responseLimit(next http.Handler, limit int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter := &responseCounter{ResponseWriter: w, limit: limit, remaining: limit}

		next.ServeHTTP(counter, r)

		if counter.aborted {
			panic(http.ErrAbortHandler)
		}
	})
}

// errResponseTooLarge is the error returned to the writer when the response
// limit is exceeded.
//
// It looks like an ordinary write error and that is correct: for the caller the
// outcome is the same — the rest of the body will not go to the client.
var errResponseTooLarge = errors.New("graph: the response limit was exceeded")

// responseCounter is the http.ResponseWriter that counts the bytes written to
// the client.
//
// # What happens when the limit is hit
//
// The response is written as a STREAM, that is, the wrapper can only learn that
// it exceeded the limit when part of the body is already gone. Two cases are
// separated and in neither of them is HALF A JSON SENT:
//
//   - If no byte has gone out yet — which is always the case with gqlgen's POST
//     transport today, it encodes the response in one go and writes it with a
//     single Write — the exceeding body is DROPPED and a complete, valid error
//     envelope is written in its place. The client gets a response that states
//     the reason, not a broken document.
//   - If part of it has gone out, a complete document is no longer possible. In
//     that case the connection is DROPPED ([responseLimit] panics with
//     http.ErrAbortHandler). Sending half a JSON breaks the client: it either
//     gets a parse error and cannot know why, or — worse — mistakes the
//     truncated body for a short result. Dropping the connection is honest; the
//     client sees a transport error, which is exactly what happened.
//
// The second branch is UNREACHABLE today and stands on purpose: the
// http.ResponseWriter contract allows partial writes, and the day a streaming
// transport (SSE, @defer) is added to this endpoint the decision will already
// have been made here.
//
// # What it binds and what it does not
//
// The wrapper binds the EXHAUST, it does not bind MEMORY: gqlgen encodes the
// response into memory first (json.Marshal), that is, a 200 MiB response is
// already allocated before it gets here. The gate that binds memory is
// [fieldRepetitionLimit] and it rejects BEFORE execution. That is why the two
// do not replace each other: one prevents the work from being done, the other
// catches what the estimate missed.
type responseCounter struct {
	http.ResponseWriter

	limit     int
	remaining int
	written   bool
	exceeded  bool
	aborted   bool
}

// Write passes through the bytes that do not exceed the limit and rejects the
// ones that do.
func (y *responseCounter) Write(p []byte) (int, error) {
	if y.exceeded {
		return 0, errResponseTooLarge
	}

	if len(p) <= y.remaining {
		n, err := y.ResponseWriter.Write(p)
		y.remaining -= n

		if n > 0 {
			y.written = true
		}

		return n, err
	}

	y.exceeded = true

	if y.written {
		y.aborted = true

		return 0, errResponseTooLarge
	}

	// The error envelope is NOT COUNTED: the counter exists to limit the body
	// the client asked for, not the limit's own error message.
	if _, err := y.ResponseWriter.Write(overflowEnvelope(y.limit)); err != nil {
		return 0, err
	}

	return 0, errResponseTooLarge
}

// overflowEnvelope builds the GraphQL error envelope of the response limit
// overflow.
//
// The envelope is encoded through graphql.Response rather than by hand, so that
// the field names and their order stay the same as the ones gqlgen produces. A
// second envelope shape would make the client think they are two separate error
// classes.
func overflowEnvelope(limit int) []byte {
	gqlErr := gqlerror.Errorf("response exceeds the limit of %d bytes", limit)
	errcode.Set(gqlErr, codeResponseExceeded)

	// An encoding error is IMPOSSIBLE: the envelope carries only strings and
	// numbers. Even so the returned error is not swallowed; a fixed envelope is
	// written instead of an empty body — better that the client gets something
	// incomplete than nothing at all.
	body, err := json.Marshal(&graphql.Response{Errors: gqlerror.List{gqlErr}})
	if err != nil {
		return []byte(`{"errors":[{"message":"response too large"}],"data":null}`)
	}

	return body
}

// queryCache is the cache of parsed documents.
//
// gqlgen's lru.LRU is not used directly because the only thing it can count is
// the ENTRY COUNT, and the problem that was measured was about size (see
// [cacheEntryCount]). Two rules are added here:
//
//  1. SIZE — a document whose text is longer than [maxCachedDocumentBytes] is
//     not stored. Multiplied by the entry count, the cache's ceiling is now a
//     known number.
//  2. ADMISSION — a document first becomes a CANDIDATE and enters the cache
//     only if it passes all the limit gates.
//
// The reason for the second one is gqlgen's ordering: the executor adds the
// document to the cache IMMEDIATELY AFTER parsing and validating it, whereas
// the depth/repetition/complexity extensions run AFTER that. That is, a
// rejected document — one that never reaches the service — was also taking up
// room in the cache, and an attacker could evict the storefront's REAL
// documents from the cache with a single quota. Measured: 100 x 65 KB of
// rejected documents left 171.8 MiB of permanent heap after runtime.GC.
//
// The candidate travels in the request's own OperationContext: the ctx arriving
// at Add and the opCtx the extensions see belong to the same request, so the
// link can be made without putting shared state in between.
type queryCache struct {
	entries  *lru.LRU[*ast.QueryDocument]
	maxBytes int
}

// That gqlgen's cache contract is satisfied is pinned at compile time: if the
// signature drifts it cannot be handed to SetQueryCache and the endpoint does
// not silently end up cacheless, it does not compile.
var _ graphql.Cache[*ast.QueryDocument] = (*queryCache)(nil)

// cacheCandidateKey is the candidate's name inside the OperationContext.
//
// gqlgen's Stats.SetExtension map is a shared area; the name carries the
// package path so that it does not collide with another extension's.
const cacheCandidateKey = "product/graph.queryCacheCandidate"

// cacheCandidate is the document waiting to pass the limits.
type cacheCandidate struct {
	key      string
	document *ast.QueryDocument
}

// newQueryCache builds a cache with the given entry and byte limits.
func newQueryCache(entries, maxBytes int) *queryCache {
	return &queryCache{
		entries:  lru.New[*ast.QueryDocument](entries),
		maxBytes: maxBytes,
	}
}

// Get reads the document from the cache.
func (o *queryCache) Get(ctx context.Context, key string) (*ast.QueryDocument, bool) {
	return o.entries.Get(ctx, key)
}

// Add takes the document as a CANDIDATE; it does not write it into the cache.
//
// The write is left to [queryCache.admit]. If there is no context belonging to
// a request (a call from outside gqlgen, for example from a test) the document
// is stored directly: holding the candidate back when there is no gate to admit
// it would mean disabling the cache entirely.
func (o *queryCache) Add(ctx context.Context, key string, document *ast.QueryDocument) {
	if len(key) > o.maxBytes {
		return
	}

	if !graphql.HasOperationContext(ctx) {
		o.entries.Add(ctx, key, document)

		return
	}

	graphql.GetOperationContext(ctx).Stats.SetExtension(cacheCandidateKey,
		cacheCandidate{key: key, document: document})
}

// admit writes the waiting candidate into the cache.
func (o *queryCache) admit(ctx context.Context, opCtx *graphql.OperationContext) {
	candidate, ok := opCtx.Stats.GetExtension(cacheCandidateKey).(cacheCandidate)
	if !ok {
		return
	}

	opCtx.Stats.SetExtension(cacheCandidateKey, nil)
	o.entries.Add(ctx, candidate.key, candidate.document)
}

// cacheAdmission is the gqlgen extension that takes a document which passed the
// limits into the cache.
//
// It REJECTS no document; the only thing it does is make having passed all the
// gates that run before it the condition for entering the cache. That is why it
// must be registered LAST (see [NewHandler]) — if it is registered earlier, the
// documents rejected by the gates behind it are stored again and the fix
// silently becomes ineffective.
type cacheAdmission struct{ cache *queryCache }

var _ interface {
	graphql.HandlerExtension
	graphql.OperationContextMutator
} = cacheAdmission{}

// ExtensionName returns the name of the extension.
func (cacheAdmission) ExtensionName() string { return "QueryCacheAdmission" }

// Validate verifies that the extension was set up with a cache.
func (o cacheAdmission) Validate(graphql.ExecutableSchema) error {
	if o.cache == nil {
		return errors.New("graph: the cache admission gate cannot be set up without a cache")
	}

	return nil
}

// MutateOperationContext admits the document into the cache.
func (o cacheAdmission) MutateOperationContext(
	ctx context.Context,
	opCtx *graphql.OperationContext,
) *gqlerror.Error {
	o.cache.admit(ctx, opCtx)

	return nil
}

// The codes of the errors the transport produces.
//
// From parsing onwards gqlgen puts a code on every protocol error
// (GRAPHQL_PARSE_FAILED, GRAPHQL_VALIDATION_FAILED) and this package's gates do
// so too; the only class left without a code is the transport, which fails
// before the document can even be READ. These two codes fill that gap, so that
// the client tells the "the document could not be read at all" case apart from
// the other protocol errors through extensions.code as well.
//
// The shape is UPPERCASE and the rationale is the same as for the limit codes
// (see limits.go): the document did not reach the executor, that is, these are
// not service errors but protocol errors.
const (
	// codeRequestBodyTooLarge is the code, in the GraphQL envelope, of a
	// request that exceeds the body limit.
	//
	// The SAME condition returns to a client that DECLARES its size with
	// Content-Length in the core's envelope and with the
	// product_graphql_body_too_large code; the two codes are one condition's
	// names in two envelopes. Why the envelopes differ is in [bodyLimit].
	codeRequestBodyTooLarge = "REQUEST_BODY_TOO_LARGE"

	// codeRequestDecodeFailed is the code of a request body that cannot be
	// decoded as JSON.
	codeRequestDecodeFailed = "REQUEST_DECODE_FAILED"
)

// suggestionPrefix is where gqlparser's suggestion sentence begins.
//
// All of the validator's suggestion helpers (SuggestListQuoted,
// SuggestListUnquoted, Suggestf and the inline fragment suggestion of
// fields_on_correct_type) append a single sentence to the END of the message,
// and all of them start with this string. That is why there is a single cut
// point: the diagnostic sentence (which field, which type) stays in place, only
// the part that ENUMERATES NAMES falls away.
const suggestionPrefix = " Did you mean"

// errorPresenter splits errors into two policies by their SOURCE.
//
// The split looks at the error's SOURCE, not its TYPE, and that is a fix: the
// condition was once "is it a *coreerrors.Error" and every untyped error —
// including pq's error carrying the connection string, the password and the SQL
// text — was going to the client AS IT WAS, and was not being logged at all.
// Yet the core's rule is exactly the opposite: an untyped error counts as
// KindInternal, its message is masked and the real error is recorded. The line
// "pass through whatever is not typed" was itself the SECOND DEFINITION it
// wanted to avoid — and a definition at odds with the core, at that.
//
// # The source is NOT DETERMINED by asking "is it a gqlerror"
//
// Almost every error arriving here is already a *gqlerror.Error: gqlgen WRAPS
// the resolver error with graphql.ErrorOnPath before handing it to the
// presenter (that is graphql.AddError's first act). So a split that looks at
// the type would count pq's error as a protocol error too — measured.
//
// The split looks at WHAT the gqlerror CARRIES: inside one produced as a
// wrapper stands a foreign error (gqlerror.WrapPath puts it in the Err field),
// while the document's own errors are built FROM SCRATCH with gqlerror.Errorf
// and wrap nothing. The question "is there something it wraps" is therefore
// exactly the question "did the GraphQL pipeline produce this error, or did it
// only dress it up".
//
// Two branches:
//
//   - PROTOCOL — a gqlerror that wraps nothing was produced by parsing,
//     validation, the limit gates or the transport; all of them are about the
//     request the client WROTE and masking them makes the surface impossible to
//     debug. It is left as it is, with two exceptions (see [protocolError]).
//   - SERVICE — everything else, TYPED OR NOT, is written through
//     corehttp.WriteError and the envelope it writes is read back. The rule for
//     which error may be handed to the client as it is is NOT written a SECOND
//     time here; the day they diverged, the second read surface would leak the
//     detail the first one hides (DSN, query text, file path).
//
// A side gain: the code, the message, the details and the request identity are
// the SAME on both surfaces; the client reads error codes from a single
// dictionary.
//
// [Options] IS TAKEN because the suggestion cut depends on the introspection
// switch (see [Options.IntrospectionDisabled]); it is bound while the server is
// being set up, not read again on every request.
func errorPresenter(opts Options) graphql.ErrorPresenterFunc {
	return func(ctx context.Context, err error) *gqlerror.Error {
		// The path and location information is taken from gqlgen's own
		// presenter; only it knows which field failed.
		presented := graphql.DefaultErrorPresenter(ctx, err)

		var protocol *gqlerror.Error
		if errors.As(err, &protocol) && protocol.Unwrap() == nil {
			return protocolError(ctx, presented, opts.IntrospectionDisabled)
		}

		return serviceError(ctx, presented, err)
	}
}

// protocolError presents an error belonging to the document to the client.
//
// These errors are NOT MASKED: they describe the request the client wrote, and
// a client that sees "server error" instead of "Cannot query field x" cannot
// fix its query. There are two exceptions and both are places where the message
// carries something the client DOES NOT ALREADY KNOW:
//
//  1. A CODELESS error comes from the transport and we do not write its text.
//     Measured: when today's POST transport cannot decode the JSON it appends
//     the RAW BODY to the message (transport/http_post.go), that is, up to
//     64 KiB of attacker-controlled text was entering the response and the logs
//     of any middleware that records the response. The text is replaced by
//     [transportError].
//  2. If introspection is DISABLED the suggestion sentence is cut; the
//     rationale is in [Options.IntrospectionDisabled].
func protocolError(
	ctx context.Context,
	presented *gqlerror.Error,
	introspectionDisabled bool,
) *gqlerror.Error {
	if code, _ := presented.Extensions["code"].(string); code == "" {
		transportError(ctx, presented)

		return presented
	}

	if introspectionDisabled {
		presented.Message = trimSuggestion(presented.Message)
	}

	return presented
}

// transportError replaces the message the transport wrote with our text.
//
// The two reasons are SEPARATED because what the client has to do differs:
// shrinking the body and sending valid JSON are not the same fix. The split is
// NOT a text match but the measurement [bodyLimit] recorded.
func transportError(ctx context.Context, presented *gqlerror.Error) {
	if bodyExceeded(ctx) {
		presented.Message = fmt.Sprintf(
			"request body exceeds the limit of %d bytes", maxQueryBytes)
		errcode.Set(presented, codeRequestBodyTooLarge)

		return
	}

	presented.Message = "request body is not valid JSON"
	errcode.Set(presented, codeRequestDecodeFailed)
}

// trimSuggestion drops the sentence that ENUMERATES NAMES from a validation
// message.
func trimSuggestion(message string) string {
	if i := strings.Index(message, suggestionPrefix); i >= 0 {
		return message[:i]
	}

	return message
}

// serviceError presents the error with the core's error policy.
//
// The body is NOT REBUILT here: it is written through corehttp.WriteError and
// the envelope it writes is read back. Masking and logging both happen inside
// that call, that is, an untyped error counts as KindInternal on this surface
// too and the real text goes only to the log.
func serviceError(ctx context.Context, presented *gqlerror.Error, err error) *gqlerror.Error {
	capture := &responseCapture{headers: http.Header{}}
	corehttp.WriteError(ctx, capture, err)

	var envelope corehttp.ErrorResponse
	if json.Unmarshal(capture.body.Bytes(), &envelope) != nil {
		// Landing here means the core could not decode its own envelope.
		// Rather than SWALLOWING the error we return the form gqlgen presented;
		// but because its message may be unmasked it is reduced to the name of
		// the kind.
		presented.Message = errorKind(err).String()

		return presented
	}

	presented.Message = envelope.Error.Message
	presented.Extensions = map[string]any{"code": envelope.Error.Code}

	if envelope.Error.RequestID != "" {
		presented.Extensions["request_id"] = envelope.Error.RequestID
	}

	if len(envelope.Error.Details) > 0 {
		presented.Extensions["details"] = envelope.Error.Details
	}

	return presented
}

// errorKind determines the kind of the error with the SAME rule as the core.
//
// An untyped (and a typed-nil) error counts as KindInternal; corehttp.StatusFor
// works on the same assumption. The kind's name is used only on the road where
// the envelope could not be decoded, that is, it is the last resort for the
// text that goes to the client — an unclassified error coming out of there as a
// client error would be a leak in the masking.
func errorKind(err error) coreerrors.Kind {
	var typed *coreerrors.Error
	if coreerrors.As(err, &typed) && typed != nil {
		return typed.Kind
	}

	return coreerrors.KindInternal
}

// recoverPanic writes resolver panics to the structured logger.
//
// gqlgen's default prints the stack trace straight to os.Stderr; because logs
// in this repository are structured with slog, that line carries no request
// identity and does not enter the collector properly either.
//
// A panic produces TWO lines and that is deliberate: the line here carries the
// stack trace (it exists nowhere else), while the one from the
// corehttp.WriteError that [errorPresenter] calls carries the request identity
// and what was returned to the client.
func recoverPanic(ctx context.Context, panicValue any) error {
	corehttp.LoggerFromContext(ctx).ErrorContext(ctx, "graphql resolver panicked",
		"panic", panicValue,
		"stack", string(debug.Stack()),
		"request_id", corehttp.RequestIDFromContext(ctx),
	)

	return coreerrors.Internal(codePanic, "the graphql request could not be processed")
}

// responseCapture takes the response corehttp.WriteError writes into memory.
//
// httptest.ResponseRecorder was NOT USED: that package belongs to the test
// binary and using it in production code would carry test helpers into the
// server binary. The surface needed is three methods anyway.
type responseCapture struct {
	headers http.Header
	body    bytes.Buffer
}

// Header returns the headers to be written.
func (y *responseCapture) Header() http.Header { return y.headers }

// WriteHeader IGNORES the status code.
//
// The HTTP status of a GraphQL response is 200; the kind of the error is
// understood by looking at the code in the body. Keeping the code and writing
// it into extensions would mean reporting to the client a status code it will
// never see.
func (y *responseCapture) WriteHeader(int) {}

// Write takes the body into memory.
func (y *responseCapture) Write(p []byte) (int, error) { return y.body.Write(p) }
