package http

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
)

// IdempotencyKeyHeader is the header the client marks a retry with.
const IdempotencyKeyHeader = "Idempotency-Key"

// IdempotencyReplayedHeader reports that the response was replayed from a record.
//
// It exists so the client can answer the question "did this really happen now,
// or earlier?"; without it the two attempts cannot be told apart.
const IdempotencyReplayedHeader = "Idempotency-Replayed"

// CodeIdempotencyConflict is the error code reporting that the same key was used
// with a DIFFERENT body.
const CodeIdempotencyConflict = "idempotency_key_reuse"

// CodeIdempotencyKeyTooLong is the error code reporting that the key exceeded the
// length limit.
//
// It has to be a SEPARATE code from [CodeIdempotencyConflict]: the two cases tell
// the client OPPOSITE things. The right reaction of a client seeing "reuse" is to
// produce a NEW key and try again; when a client rejected for a too-long key does
// that, the new key is long as well and the client loops forever. This code says
// "SHORTEN the key" and breaks the loop.
const CodeIdempotencyKeyTooLong = "idempotency_key_too_long"

// CodeIdempotencyInFlight is the error code reporting that a concurrent second
// request with the same key was rejected.
const CodeIdempotencyInFlight = "idempotency_in_flight"

// maxIdempotencyKeyLen is the upper bound on the accepted key length.
//
// An unbounded key is a memory/disk inflation vector whatever the store is. The
// limit applies to the raw header THE CLIENT sends; the key going to the store is
// longer because it is namespaced with the caller's identity (see
// [IdempotencyStore]).
const maxIdempotencyKeyLen = 255

// anonymousIdempotencyBucket is the namespace shared by requests whose identity is unresolved.
//
// ALL anonymous callers are in this single bucket; the reasoning is in the [Idempotency] godoc.
const anonymousIdempotencyBucket = "anon"

// idempotencyCloseTimeout is the maximum time given to the store writes that
// happen after the handler is done.
//
// Because the closing calls are CUT OFF from the request's context (see
// [closeContext]), nothing else is left to stop them; leaving them unbounded
// would hang the goroutine of a request whose response has long been sent on an
// unreachable store forever. Five seconds is far too long for a store writing a
// single row, and short enough to keep the server waiting at shutdown.
const idempotencyCloseTimeout = 5 * time.Second

// maxIdempotentBodyBytes is the maximum body size buffered on idempotent requests.
//
// We have to read the body to take its fingerprint; reading it unbounded would
// let a single request consume the server's memory.
const maxIdempotentBodyBytes = 1 << 20 // 1 MiB

// defaultIdempotencyTTL is how long the record is kept.
//
// 24 hours is the same as Stripe's built-in behavior: far too long for a client
// to retry within, short enough not to keep it forever.
const defaultIdempotencyTTL = 24 * time.Hour

// defaultIdempotencyBudget is the default byte budget the in-memory store may
// spend on completed records.
//
// 64 MiB was chosen between a measured floor and a measured ceiling: because the
// price [entryCharge] puts on a record with two headers and no body is 955 bytes,
// the budget corresponds to ~70,000 records, to ~22,000 records at a typical order
// response (~2 KiB), and to 63 records at a response on the
// [maxIdempotentBodyBytes] limit. The number of mutations a single-instance store
// produces in 24 hours is below the first two figures; an installation that falls
// to the third needs not a larger budget but a SHARED store (GUARD_BACKEND=redis).
//
// The value can be changed with IDEMPOTENCY_MAX_MEMORY_BYTES; its agreement with
// the envDefault in config is pinned by a test.
const defaultIdempotencyBudget int64 = 64 << 20

// entryFixedCharge is the number of bytes a single record holds OUTSIDE its body,
// key, fingerprint and headers.
//
// Measured (runtime.MemStats, after GC, 200,000 records): with a 44-byte key, a
// 32-byte fingerprint, an EMPTY body and NO headers, 323 bytes were held per
// record; because the key and the fingerprint are charged separately, the
// structural cost left over is ~250 bytes (the entry, the list node, the map
// slot). The constant was deliberately chosen HIGHER.
//
// The direction matters: undercharging would mean the limit told to the operator
// being quietly exceeded in reality. The [entryCharge] godoc gives the measured
// overcharge ratio.
const entryFixedCharge int64 = 320

// headerGroupCharge is the number of bytes charged for each GROUP OF EIGHT in a
// response header map.
//
// Measured: adding a single header to the same record brings 675-323 = 352 bytes
// per record, while the second and the eighth header brought NOTHING (it stayed
// flat at 675 bytes); after the ninth it rose to 1067 bytes. Go's map allocates
// its slots in GROUPS OF EIGHT, and the price of a header map grows with the
// number of groups, not with the number of headers. Putting a fixed price on each
// header would UNDERCHARGE a single-header record — that was exactly the mistake
// of the accounting before the measurement (charged/actual = 0.95).
const headerGroupCharge int64 = 448

// headerGroupSize is the number of slots in one map group.
const headerGroupSize = 8

// headerValueCharge is what a header value holds beyond its string contents (the
// backing array of the single-element slice).
const headerValueCharge int64 = 16

// evictionLogInterval is the shortest interval at which budget eviction warnings
// are written.
//
// The first eviction is ALWAYS logged; the rest are throttled at this interval.
// Without the throttle, on an installation whose budget is permanently full every
// mutation request would produce a WARN line and the warning would drown the
// attention it is asking for in its own noise.
const evictionLogInterval = time.Minute

// ErrIdempotencyKeyInFlight reports that a second request arrived with the same
// key while one is still being processed.
var ErrIdempotencyKeyInFlight = errors.New("the idempotency key is in flight")

// IdempotentResponse is the record of the response to be replayed.
type IdempotentResponse struct {
	// Status kaydedilen HTTP durum kodudur.
	Status int
	// Header holds the recorded response headers.
	Header http.Header
	// Body is the recorded response body.
	Body []byte
	// Fingerprint is the caller+method+path+query+body fingerprint of the request;
	// it is stored to catch the key being reused with a different request.
	Fingerprint string
}

// IdempotencyStore holds the idempotency records.
//
// Implementations have to be safe for concurrent calls.
//
// The key it takes is not the RAW header the client sent: it is the form
// namespaced with the caller's identity (see [Idempotency]). It can therefore be
// longer than the 255-character limit imposed on the client, and a durable store
// has to define its column wide enough for the identity to fit.
type IdempotencyStore interface {
	// Begin tries to reserve the key for this request.
	//
	// If the key is new it returns (nil, false, nil) and the key is marked "in
	// flight". If a completed record exists it returns (record, true, nil).
	// If the key is in flight for another request it returns
	// [ErrIdempotencyKeyInFlight].
	Begin(ctx context.Context, key, fingerprint string) (*IdempotentResponse, bool, error)
	// Complete records the response of the key whose work is done.
	Complete(ctx context.Context, key string, resp IdempotentResponse) error
	// Abort undoes the reservation; no record is kept and the key can be retried.
	Abort(ctx context.Context, key string) error
}

// Idempotency produces middleware answering retries arriving with the same
// [IdempotencyKeyHeader] with the first response.
//
// With a nil store the middleware is a no-op. This rests on the same reasoning as
// [RateLimit]: rejecting all traffic because of an unconfigured infrastructure
// component would take down the very service it is protecting.
//
// It applies only to UNSAFE methods (POST, PUT, PATCH, DELETE). GET and HEAD are
// idempotent by definition already; recording them would only inflate the store.
//
// WITHOUT a key the request flows normally. Making the key mandatory would break
// every existing client overnight; the requirement has to be imposed separately,
// per endpoint.
//
// 5xx responses are NOT RECORDED: a server error may be transient and the client
// retrying is exactly what we want. Replaying a stuck 500 for 24 hours would turn
// a self-healing fault into a permanent one.
//
// This guard looks only at the STATUS CODE, and that is the only thing it can
// look at: deciding from the body would mean teaching this middleware the error
// shape of every surface — the rule leaves a single place at that moment and every
// new envelope requires rewriting it. The price is EXPLICIT: a surface reporting
// its internal error with a 200 as well falls OUTSIDE the guard and a transient
// fault is replayed for the whole TTL. Today the repository has exactly one such
// surface (the GraphQL storefront endpoint; by its contract it says 200 to every
// request it resolves) and the fix is not to make the record smarter but to take
// the endpoint out of the stack: see [GuardOptions.IdempotencyExempt].
//
// # The identity namespace
//
// Both the store key and the fingerprint are namespaced WITH THE CALLER'S IDENTITY
// (see [PrincipalFromContext]); that is why the middleware has to be installed
// AFTER authentication (see [APIGuards]). Were the raw header value the store key
// directly, two DIFFERENT callers picking an ordinary key like "1" or "order-1"
// would fall onto the same record: if the request is identical byte for byte the
// second caller replays THE FIRST ONE'S response — a cross-tenant data leak; if it
// differs they get a 409, that is, one caller occupies the other's key space.
//
// Requests whose identity is UNRESOLVED share a single COMMON bucket: on an
// unguarded endpoint all anonymous callers are in the same namespace and the two
// outcomes above are still possible there. This is a deliberate choice —
// separating anonymous requests by IP would BREAK idempotency without really
// binding the key to a tenant (an IP can be spoofed, a NAT is shared): a client
// retrying after its mobile network changed would not find its own record and
// would double-process at exactly the moment the guard was supposed to help.
//
// # Authenticating is not the same as SEPARATING CALLERS
//
// The logic above would end in one sentence — "endpoints whose key space has to
// belong to the tenant should sit behind authentication" — and that sentence gives
// the wrong answer IN THE STOREFRONT. /store/v1 is authenticated, but the identity
// resolved is not the shopper's, it is THE STORE'S: the publishable key is the
// same in every browser and the fact that it is not secret anyway is written in
// the [Authenticator.AuthenticateStore] godoc. That is, every customer in the
// storefront shares a SINGLE bucket, and what picks the record inside that bucket
// is a header the client chose.
//
// The storefront gets away with this thanks to two things. The first is that THE
// PATH goes into the fingerprint as well: the path of cart-scoped endpoints
// carries the cart id, so a second customer using the same key on their own cart
// gets a 409 rather than somebody else's data. The second is that the one endpoint
// left — cart CREATION — has been made EXEMPT from this ring: its path carries no
// capability and its response PRODUCES one, that is, a second customer arriving
// with the same key and the same body was being handed the first one's cart id.
// The reasoning and the measurement are in cmd/server's exemption list.
//
// The rule that follows: when installing this middleware on a new surface, the
// question to ask is not "is it authenticated" but "does the resolved identity
// name THE CALLER or the INSTALLATION the caller is connected to".
func Idempotency(store IdempotencyStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if store == nil {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ham := strings.TrimSpace(r.Header.Get(IdempotencyKeyHeader))
			if ham == "" || !idempotentMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			// Bodies handled as a STREAM are NOT buffered and no idempotency record is
			// taken either.
			//
			// The fingerprint requires reading the body IN FULL; on a file upload that
			// destroys the meaning of streaming (the same bytes both in memory and on
			// disk) and quietly changes the limit as well: the 1 MiB buffer here engages
			// BEFORE the upload endpoint's own (far larger) limit and the client gets a
			// "body too large" error somewhere below the limit it configured. Applying two
			// different limits to the same request would produce a fault where it cannot be
			// told which one is speaking.
			//
			// The price is EXPLICIT: a repeated multipart request is processed again. For
			// an upload that means a second file object — a duplicate record is cheaper
			// than a buffered stream and a wrong limit. An endpoint that really wants
			// idempotent uploads should derive the key from the content DIGEST rather than
			// from the body.
			if streamingBody(r) {
				next.ServeHTTP(w, r)
				return
			}

			if len(ham) > maxIdempotencyKeyLen {
				WriteError(r.Context(), w, coreerrors.Invalid(CodeIdempotencyKeyTooLong,
					"the idempotency key can be at most %d characters", maxIdempotencyKeyLen))
				return
			}

			body, err := readLimited(r)
			if err != nil {
				WriteError(r.Context(), w, err)
				return
			}

			// The key going to the store is not the RAW header but its form namespaced
			// with the caller's bucket; the reasoning is in the godoc's "The identity
			// namespace" section.
			kova := idempotencyBucket(r.Context())
			izi := fingerprint(kova, r, body)
			key := storeKey(kova, ham)

			rec, tamam, err := store.Begin(r.Context(), key, izi)

			switch {
			case errors.Is(err, ErrIdempotencyKeyInFlight):
				WriteError(r.Context(), w, coreerrors.Conflict(CodeIdempotencyInFlight,
					"a request with the same idempotency key is still being processed"))

				return
			case err != nil:
				WriteError(r.Context(), w, err)
				return
			}

			if tamam {
				replay(r.Context(), w, rec, izi)
				return
			}

			record(r.Context(), w, r, next, store, key, izi)
		})
	}
}

// record runs the handler and buffers the response, then writes it to the store.
//
// It is a separate function so that the defer undoing the reservation on a panic
// wraps the handler call exactly.
func record(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	next http.Handler,
	store IdempotencyStore,
	key, izi string,
) {
	rec := &recordingWriter{ResponseWriter: w, status: http.StatusOK}

	// If the handler panics or returns a 5xx the reservation has to be undone;
	// otherwise the key stays locked "in flight" and the client can never try again.
	tamamlandi := false

	defer func() {
		if tamamlandi {
			return
		}

		kapanis, iptal := closeContext(ctx)
		defer iptal()

		if err := store.Abort(kapanis, key); err != nil {
			LoggerFromContext(ctx).ErrorContext(ctx,
				"the idempotency reservation could not be undone, the key may stay locked",
				"error", err)
		}
	}()

	next.ServeHTTP(rec, r)

	if rec.status >= http.StatusInternalServerError {
		return
	}

	if rec.overflowed {
		// The response exceeded the buffer limit: recording a partial body and later
		// replaying it would hand the client a BROKEN response. Not recording it only
		// leads to the retry being processed again.
		LoggerFromContext(ctx).WarnContext(ctx,
			"the response exceeded the idempotency buffer limit, not recording it",
			"limit_bytes", maxIdempotentBodyBytes)

		return
	}

	kapanis, iptal := closeContext(ctx)
	defer iptal()

	if err := store.Complete(kapanis, key, IdempotentResponse{
		Status:      rec.status,
		Header:      rec.Header().Clone(),
		Body:        rec.buf.Bytes(),
		Fingerprint: izi,
	}); err != nil {
		// The response has ALREADY been written to the client; we can no longer return
		// an error. The only right thing left is to release the reservation: otherwise
		// the key stays "in flight" forever and the client can neither get a response
		// nor try again. The price of releasing is the possibility of the retry being
		// processed again — better than a permanent lock.
		LoggerFromContext(ctx).ErrorContext(ctx,
			"the idempotency record could not be written, releasing the key",
			"error", err)

		return
	}

	tamamlandi = true
}

// closeContext produces the context for the store calls made after the handler is done.
//
// The request's own context CANNOT BE USED: if the client drops the connection
// (the browser tab closes, the load balancer times out) that context is canceled.
// Should the cancellation land exactly on the moment Complete/Abort runs, either
// the record is never written or the reservation cannot be undone and the key
// stays locked "in flight" — the client can neither get a response nor try again.
// Yet the handler has ALREADY run: the side effects (a charge, an order) have
// happened and preventing a retry from producing them a second time is exactly
// this record's job. That is, the closing operations are tied NOT to the
// request's lifetime but to the server's own.
//
// WithoutCancel cuts the cancellation off but keeps the values (the logger, the
// request id); the time limit then stops the cut-off call from hanging forever.
func closeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), idempotencyCloseTimeout)
}

// replay writes the recorded response to the client.
//
// If the fingerprint does not match the response is NOT replayed: sending a
// different request with the same key is a client-side mistake, and quietly
// returning the wrong response (another order's record, say) is silent data
// corruption.
func replay(ctx context.Context, w http.ResponseWriter, rec *IdempotentResponse, izi string) {
	if rec == nil {
		WriteError(ctx, w, coreerrors.Internal(defaultInternalCode,
			"the idempotency record came back empty"))
		return
	}

	if rec.Fingerprint != izi {
		WriteError(ctx, w, coreerrors.Conflict(CodeIdempotencyConflict,
			"this idempotency key has been used for a different request"))

		return
	}

	hedef := w.Header()
	for k, v := range rec.Header {
		hedef[k] = append([]string(nil), v...)
	}

	hedef.Set(IdempotencyReplayedHeader, "true")
	w.WriteHeader(rec.Status)
	// The replayed body is not client input, it is the response THIS server produced
	// earlier; its headers, Content-Type included, are replayed as they are. So it
	// carries no more risk than the first response did.
	_, _ = w.Write(rec.Body) //nolint:gosec // G705: the body is the response the server produced itself
}

// readLimited reads the request body in a bounded way and makes it readable again.
//
// A body exceeding the limit is rejected with KindInvalid, that is, the client
// sees a 422. The code RFC 9110 reserves for this case is 413 and it is more
// correct; 422 is nevertheless kept deliberately. The reason is that in this
// framework the status code is derived from the error CLASS rather than call by
// call (see [StatusFor]): returning a 413 requires adding a new Kind to
// core/errors, and that is a far wider decision than one middleware's need. Until
// that day the client's distinguishing handle is not the status but the
// "body_too_large" CODE — the code is the unchanging side of the contract, while
// the status can change when the class mapping changes.
func readLimited(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}

	// Try to read one byte past the limit so we can tell an overflow apart.
	body, err := io.ReadAll(io.LimitReader(r.Body, maxIdempotentBodyBytes+1))
	if err != nil {
		return nil, coreerrors.Invalid("invalid_body", "the request body could not be read")
	}

	if len(body) > maxIdempotentBodyBytes {
		return nil, coreerrors.Invalid("body_too_large",
			"an idempotent request body can be at most %d bytes", maxIdempotentBodyBytes)
	}

	// We consumed the body; put it back so the handler can read it.
	r.Body = io.NopCloser(bytes.NewReader(body))

	return body, nil
}

// idempotencyBucket produces the caller's namespace.
//
// Without an identity it returns the common bucket ALL anonymous callers SHARE;
// why it is not separated by IP is explained in the [Idempotency] godoc.
func idempotencyBucket(ctx context.Context) string {
	if p, ok := PrincipalFromContext(ctx); ok && p.ID != "" {
		return p.Kind + ":" + p.ID
	}

	return anonymousIdempotencyBucket
}

// storeKey combines the bucket and the client's key into a single store key.
//
// The LENGTH of the bucket is written first. Plain concatenation (bucket +
// separator + key) would not do: because the separator can appear in either part,
// the bucket "a:b" with the key "c" and the bucket "a" with the key "b:c" would
// fall onto the same string. Since THE CLIENT picks the key, that would open the
// namespace itself to the client — another door into the very leak we are trying
// to close.
func storeKey(kova, key string) string {
	return strconv.Itoa(len(kova)) + ":" + kova + ":" + key
}

// fingerprint derives the request's identity from the caller, the method, the path
// and the body.
//
// The query string is included too: two POSTs to the same path with different
// filters are different requests.
//
// The bucket goes into the mix as well. Because the store key is already separated
// by the bucket this is an EXTRA defense: on a store implementation that builds
// the namespace wrongly or carries rows written under an old schema, even if
// another caller's record reached us the fingerprint would not match and that
// response would not be replayed.
func fingerprint(kova string, r *http.Request, body []byte) string {
	h := sha256.New()
	h.Write([]byte(kova))
	h.Write([]byte{0})
	h.Write([]byte(r.Method))
	h.Write([]byte{0})
	h.Write([]byte(r.URL.Path))
	h.Write([]byte{0})
	h.Write([]byte(r.URL.RawQuery))
	h.Write([]byte{0})
	h.Write(body)

	return hex.EncodeToString(h.Sum(nil))
}

// streamingBody reports whether the request's body is of a kind that has to be
// handled as a stream.
//
// Today only multipart. The distinction is made from the Content-Type because the
// decision has to be made WITHOUT READING the body — making it after reading
// means having done exactly the buffering we are trying to avoid.
func streamingBody(r *http.Request) bool {
	return strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/")
}

// idempotentMethod reports whether the method needs an idempotency record.
func idempotentMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// recordingWriter is the wrapper that both writes the response to the client and buffers it.
type recordingWriter struct {
	http.ResponseWriter
	status  int
	yazildi bool
	// overflowed reports that the body exceeded the buffer limit; an overflowing response is not recorded.
	overflowed bool
	buf        bytes.Buffer
}

// WriteHeader records the status code and forwards it.
func (w *recordingWriter) WriteHeader(status int) {
	if w.yazildi {
		return
	}

	w.status = status
	w.yazildi = true
	w.ResponseWriter.WriteHeader(status)
}

// Write writes the body both into the buffer and to the client.
func (w *recordingWriter) Write(b []byte) (int, error) {
	if !w.yazildi {
		w.WriteHeader(http.StatusOK)
	}

	// The client gets the full response either way; only the RECORD is bounded.
	if !w.overflowed {
		if w.buf.Len()+len(b) > maxIdempotentBodyBytes {
			w.overflowed = true
			w.buf.Reset()
		} else {
			w.buf.Write(b)
		}
	}

	return w.ResponseWriter.Write(b)
}

// Unwrap opens the wrapped writer so http.ResponseController works.
func (w *recordingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// entry is the state of a single key in the in-memory store.
type entry struct {
	// key is the key in the map and is held ON THE ENTRY ITSELF as well.
	//
	// Both expiry and budget eviction work from the FRONT of the queue list; there is
	// no other way back to the map from there. Putting the key on the list separately
	// would store the same string twice.
	key string
	// resp is the completed response; it is nil while in flight.
	resp *IdempotentResponse
	// fingerprint is the fingerprint given at reservation time.
	fingerprint string
	// expiresAt is the end of the record's validity.
	expiresAt time.Time
	// charge is this entry's byte cost deducted from the budget.
	//
	// On incomplete reservations it is ZERO; the reasoning is in
	// [MemoryIdempotencyStore]'s budget section. It is stored on the entry because
	// recomputing it while deducting would permanently shift the budget if the
	// response had changed in between.
	charge int64
	// node is the entry's node in the queue list.
	node *list.Element
}

// MemoryIdempotencyStore is the idempotency store running in process memory.
//
// It is for single-instance installations and tests. In a horizontally scaled
// deployment every instance holds its own record; two requests with the same key
// landing on different instances are processed TWICE. A multi-instance
// installation needs a shared store (Postgres or Redis) — unlike the situation in
// the rate limiter, this is a CORRECTNESS problem, not a speed one.
//
// # The memory budget
//
// The store keeps a byte budget for COMPLETED records (see
// [NewMemoryIdempotencyStore]) and DROPS the OLDEST record once the budget is
// exceeded.
//
// Without a budget the only limit was the TTL, and that limit stopped the growth
// nowhere: THE CLIENT picks the key that opens a record, the record lives 24 hours
// and the response body can be as large as [maxIdempotentBodyBytes] (1 MiB). The
// budgetless store was measured (runtime.MemStats, after GC): 10,000 records with
// a 1 KiB body held 15.51 MiB, 10,000 records with a 64 KiB body held 630.69 MiB,
// 1,000 records with a 1 MiB body held 999.58 MiB; even a record with an EMPTY
// body and no headers is 323 bytes. Under the same load the number of records
// dropped after 24 hours was ZERO (50,000 records were written and the clock
// advanced 23 hours; the map stayed at 50,001). With the default rate limit of 600
// requests/minute a single client can open 864,000 records in 24 hours.
//
// With the budget the SAME load was measured (with a 64 MiB budget): at 10,000
// records with a 64 KiB body, 63.67 MiB is held instead of 630.69 MiB and 1,009
// records stay in the map; at 1,000 records with a 1 MiB body, 62.04 MiB instead
// of 999.58 MiB and 63 records. The memory held stays BELOW the budget because the
// accounting deliberately overcharges (see [entryCharge]). The price is ~5% extra
// memory per record (a record with a 1 KiB body went from 1626 to 1706 bytes): the
// queue node, and the key held a second time on the entry.
//
// The budget covers RECORDS only. Reservations still being processed are not
// charged: they have no body (a few hundred bytes) and their number is already
// bounded by the number of requests the server carries at once. Were they charged,
// a load that cannot be evicted (see the rule below) could fill the budget on its
// own and the limit told to the operator would lose its meaning.
//
// # Why it DROPS the OLDEST rather than REJECTING
//
// All three options had a price:
//
//   - Doing nothing: the process dies with an OOM. The price is ALL the records at
//     once, plus every request being processed at that moment.
//   - REJECTING the new request once the budget is full: the guarantee stays whole,
//     but what fills the store is a header THE CLIENT CHOOSES — any client could
//     shut down the whole mutation traffic of the store with made-up keys. A memory
//     fault would turn into an availability fault that is free to trigger.
//   - Dropping the oldest: the price is that a request arriving AGAIN with the
//     dropped key is processed again, that is, a duplicate side effect.
//
// The third was chosen because its price is the same one ALREADY paid at the TTL
// limit: an expiring record is deleted anyway and a retry arriving after that is
// processed again. Eviction brings that deletion EARLIER. That is also why the
// oldest is picked — the one closest to expiring is the record with the least of
// its guard left.
//
// The trade-off is NOT SILENT: the first eviction, and after that with
// [evictionLogInterval] throttling, is logged at WARN (see
// [MemoryIdempotencyStore.Complete]), the budget is written at startup by
// cmd/server and it is documented in .env.example.
//
// # The queue list
//
// Alongside the map the records also sit in a linked list, in ASCENDING order of
// expiresAt. The list makes two things cheaper at once, and both were measured:
//
//   - Eviction. Looking for the oldest in the map would be O(n) per request.
//   - Expiry. The old form scanned the WHOLE map, and the scan ran while holding
//     the process's SINGLE lock: 50.3 ms at 1,000,000 records, 2.13 ms at 100,000.
//     Now only the expiring PREFIX is walked, that is, the cost is proportional to
//     the number of records really deleted rather than to the map size: 188 ns and
//     164 ns at those same two map sizes, that is, INDEPENDENT of size (benchmark,
//     same machine, no records to delete).
//
// What keeps the ordering standing is that THE CLOCK DOES NOT GO BACKWARD: both
// reservation and completion put the entry at the END of the list and both set
// expiresAt from the current time. If the clock goes backward the list stops being
// sorted and expiry stops early; the result is that a few records live LONGER than
// they deserve. That is the safe direction — the guard does not weaken — and
// memory is still bounded by the budget.
//
// # What is NOT DONE under the lock
//
// The store has a single mutex and EVERY mutation request goes through it. That is
// why only map and list operations are done under the lock; the two pieces of work
// that take as long as the response body are kept outside:
//
//   - Copying the record. The body is copied both while writing (see
//     [MemoryIdempotencyStore.Complete]) and while replaying (see
//     [MemoryIdempotencyStore.reserve]), and the copy can go up to 1 MiB.
//   - The budget accounting. [entryCharge] walks the headers.
//
// Measured (same machine, 16 goroutines): concurrent REPLAYING of records with a 1
// MiB body took 50.1-52.7 µs/op with the copy under the lock and 34.5-40.8 µs/op
// outside it; concurrent WRITING with a 64 KiB body went from 5.26-5.49 µs to
// 4.27-4.73 µs. The gain comes from the parallelism the lock releases; the copy
// itself does not get cheaper.
//
// The price of this is that on a replay the record in the store is touched after
// the lock is released; the immutability rule that makes it safe is in the
// [MemoryIdempotencyStore.reserve] godoc.
type MemoryIdempotencyStore struct {
	// ttl is how long the records are kept.
	ttl time.Duration
	// budget is the total byte limit of the completed records.
	budget int64
	// now reads the time; it is a field so tests can advance the clock.
	now func() time.Time

	mu    sync.Mutex
	entry map[string]*entry
	// queue holds the entries in ascending order of expiresAt; the oldest is at the front.
	queue *list.List
	// charge is the total byte cost of the completed records according to [entryCharge].
	charge int64
	// evictedTotal is the total number of records dropped because of the budget.
	evictedTotal int64
	// evictionsPending is the number of records dropped since the last warning.
	evictionsPending int
	// evictionLogAt is the earliest moment the next warning may be written.
	evictionLogAt time.Time
}

// NewMemoryIdempotencyStore builds an in-memory store with the given retention
// and memory budget.
//
// If ttl is zero or negative [defaultIdempotencyTTL] is used; if budget is zero or
// negative [defaultIdempotencyBudget] is used.
//
// budget is the total byte limit of the completed records and the oldest record is
// dropped once it is exceeded; what it means and why dropping was preferred to
// rejecting is in the [MemoryIdempotencyStore] godoc.
//
// A budget smaller than [maxIdempotentBodyBytes] (1 MiB) may be given but is
// MEANINGLESS: the moment a single response approaching that size is written it
// exceeds the budget and is dropped right away, that is, large responses can never
// be replayed. On the configuration path this is rejected at startup by
// config.Validate; the constructor does not restrict it, so that tests can use
// small budgets deliberately.
func NewMemoryIdempotencyStore(ttl time.Duration, budget int64) *MemoryIdempotencyStore {
	if ttl <= 0 {
		ttl = defaultIdempotencyTTL
	}

	if budget <= 0 {
		budget = defaultIdempotencyBudget
	}

	return &MemoryIdempotencyStore{
		ttl:    ttl,
		budget: budget,
		now:    time.Now,
		entry:  make(map[string]*entry),
		queue:  list.New(),
	}
}

// Budget returns the byte budget the store IS RUNNING under.
//
// The accessor exists so the composition root can test that the number coming from
// the configuration REALLY REACHES the store. There is a state that stays silent
// when it is not tested, and it was measured: a binding point passing zero to the
// constructor runs on the default budget while the startup log keeps writing the
// number from the configuration, that is, the operator reads a limit that is NOT
// in force. The same class of bug had been found by mutation on the pool's
// MaxConns; it is closed here as well.
func (s *MemoryIdempotencyStore) Budget() int64 { return s.budget }

// Begin reserves the key or returns the existing record.
//
// The COPY of the replayed record is taken OUTSIDE the lock; its measurement and
// reasoning are in [MemoryIdempotencyStore]'s lock section.
func (s *MemoryIdempotencyStore) Begin(
	_ context.Context, key, fp string,
) (*IdempotentResponse, bool, error) {
	rec, err := s.reserve(s.now(), key, fp)
	if rec == nil || err != nil {
		return nil, false, err
	}

	// Return a copy: if the caller changes the returned record the store must not break.
	kopya := *rec
	kopya.Header = rec.Header.Clone()
	kopya.Body = bytes.Clone(rec.Body)

	return &kopya, true, nil
}

// reserve reserves the key or returns the record to be replayed DIRECTLY (without
// copying).
//
// The pointer returned is the record in the store itself, and the caller copies it
// after the lock is released. The only thing making this safe is that a published
// record NEVER CHANGES again: [MemoryIdempotencyStore.write] REPLACES the entry's
// resp field with a new pointer and never updates the struct it points at in
// place. If that rule is broken the copying here races.
func (s *MemoryIdempotencyStore) reserve(
	now time.Time, key, fp string,
) (*IdempotentResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.collect(now)

	g, ok := s.entry[key]
	if !ok {
		fresh := &entry{key: key, fingerprint: fp, expiresAt: now.Add(s.ttl)}
		fresh.node = s.queue.PushBack(fresh)
		s.entry[key] = fresh

		return nil, nil
	}

	if g.resp == nil {
		return nil, ErrIdempotencyKeyInFlight
	}

	return g.resp, nil
}

// Complete records the response.
//
// If the record overflows the budget the oldest records are dropped and this is
// logged at WARN: the first eviction always, the rest with [evictionLogInterval]
// throttling. The warning is written OUTSIDE the lock — if the log writer blocks,
// the process's single idempotency lock must not block with it.
func (s *MemoryIdempotencyStore) Complete(
	ctx context.Context, key string, resp IdempotentResponse,
) error {
	// The copy and the accounting are prepared OUTSIDE the lock; their measurement
	// and reasoning are in [MemoryIdempotencyStore]'s lock section.
	kopya := resp
	kopya.Header = make(http.Header, len(resp.Header))
	maps.Copy(kopya.Header, resp.Header)
	kopya.Body = bytes.Clone(resp.Body)

	report, total := s.write(s.now(), key, &kopya, entryCharge(key, &kopya))
	if report > 0 {
		LoggerFromContext(ctx).WarnContext(ctx,
			"the idempotency memory budget is full, dropping the oldest records",
			"budget_bytes", s.budget,
			"dropped_since_last_warning", report,
			"dropped_total", total,
			"consequence", "a retry arriving with a dropped key is processed AGAIN",
			"remedy", "GUARD_BACKEND=redis or a larger IDEMPOTENCY_MAX_MEMORY_BYTES")
	}

	return nil
}

// write places the record, applies the budget and returns the numbers for the warning.
//
// If report is greater than zero the caller has to write the warning; the
// throttling is applied here so the decision is made with the counters held under
// the lock.
func (s *MemoryIdempotencyStore) write(
	now time.Time, key string, kopya *IdempotentResponse, charge int64,
) (report int, total int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	g, ok := s.entry[key]
	if !ok {
		// A completion arriving without a reservation is written too; the reasoning is
		// in redisguard's Complete godoc: the handler ran and its side effects happened.
		g = &entry{key: key}
		g.node = s.queue.PushBack(g)
		s.entry[key] = g
	} else {
		s.charge -= g.charge
		s.queue.MoveToBack(g.node)
	}

	g.resp = kopya
	g.fingerprint = kopya.Fingerprint
	g.expiresAt = now.Add(s.ttl)
	g.charge = charge
	s.charge += g.charge

	dusen := s.fitBudget()
	if dusen == 0 {
		return 0, s.evictedTotal
	}

	s.evictedTotal += int64(dusen)
	s.evictionsPending += dusen

	if now.Before(s.evictionLogAt) {
		return 0, s.evictedTotal
	}

	report = s.evictionsPending
	s.evictionsPending = 0
	s.evictionLogAt = now.Add(evictionLogInterval)

	return report, s.evictedTotal
}

// Abort undoes the reservation.
//
// Only an INCOMPLETE reservation is deleted: deleting a completed record would
// mean a late Abort destroying a replayable response.
func (s *MemoryIdempotencyStore) Abort(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if g, ok := s.entry[key]; ok && g.resp == nil {
		s.remove(g)
	}

	return nil
}

// collect deletes the expired records. The caller must be holding s.mu.
//
// It walks only the PREFIX of the list and stops at the first entry that has not
// expired; which assumption the ordering rests on is in the
// [MemoryIdempotencyStore] godoc.
//
// It runs on every [MemoryIdempotencyStore.Begin] and that is deliberate. The old
// form throttled the scan to once a minute, because the scan walked the WHOLE map;
// the price of the throttle was that an expired record kept BEING REPLAYED for up
// to a minute — a guard longer than what the TTL tells the operator. Because
// walking the prefix makes the throttle unnecessary, that divergence closed too.
func (s *MemoryIdempotencyStore) collect(now time.Time) {
	for e := s.queue.Front(); e != nil; e = s.queue.Front() {
		g, ok := e.Value.(*entry)
		if !ok || !now.After(g.expiresAt) {
			return
		}

		s.remove(g)
	}
}

// fitBudget drops the oldest RECORDS if the budget is exceeded and returns their
// count. The caller must be holding s.mu.
//
// Incomplete reservations are skipped: they are not charged to the budget (so
// dropping them would not relieve it) and dropping them would cost far more — if
// the reservation of a request being processed is deleted, a second request
// arriving AT THE SAME TIME goes through as well, that is, the double processing
// that was to be prevented happens at exactly that moment.
func (s *MemoryIdempotencyStore) fitBudget() int {
	dusen := 0

	for e := s.queue.Front(); e != nil && s.charge > s.budget; {
		next := e.Next()

		if g, ok := e.Value.(*entry); ok && g.resp != nil {
			s.remove(g)
			dusen++
		}

		e = next
	}

	return dusen
}

// remove takes the entry out of the map and the queue and deducts its charge from
// the budget. The caller must be holding s.mu.
func (s *MemoryIdempotencyStore) remove(g *entry) {
	s.queue.Remove(g.node)
	delete(s.entry, g.key)
	s.charge -= g.charge
}

// entryCharge computes a record's byte cost to be deducted from the budget.
//
// The body, the key, the fingerprint and the header names/values are charged by
// their string LENGTHS; the rest of the record by measured constants (see
// [entryFixedCharge], [headerGroupCharge]).
//
// The result is an ESTIMATE, not an exact byte count; following Go's allocator size
// classes and the map's internal layout exactly with a formula is not possible.
// The DIRECTION of the estimate was pinned by measurement: in every shape below
// the price charged is larger than the bytes REALLY held (runtime.MemStats, after
// GC, same machine):
//
//	shape                                  real   charged   ratio
//	key 44, body 0, headers 0             323 B     396 B    1.23
//	key 44, body 0, headers 2             675 B     955 B    1.41
//	key 44, body 0, headers 8             675 B    1284 B    1.90
//	key 44, body 0, headers 10           1067 B    1842 B    1.73
//	key 44, body 2 KiB, headers 2        2731 B    3003 B    1.10
//	key 44, body 64 KiB, headers 2      66214 B   66491 B    1.00
//
// Overcharging is not free — the budget holds fewer records than it could — but the
// other direction means an OOM; the reasoning is in [entryFixedCharge]. The ratio
// approaches 1 as the body grows, because on large records almost the whole price
// is the body and the body is measured EXACTLY.
func entryCharge(key string, resp *IdempotentResponse) int64 {
	charge := entryFixedCharge +
		int64(len(key)) +
		int64(len(resp.Fingerprint)) +
		int64(len(resp.Body))

	if len(resp.Header) > 0 {
		grup := (len(resp.Header) + headerGroupSize - 1) / headerGroupSize
		charge += int64(grup) * headerGroupCharge
	}

	for ad, degerler := range resp.Header {
		charge += int64(len(ad))
		for _, value := range degerler {
			charge += headerValueCharge + int64(len(value))
		}
	}

	return charge
}
