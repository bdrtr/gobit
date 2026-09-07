package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
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
// The reasoning and the measurement are in internal/app's exemption list.
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

	return readAtMost(r, maxIdempotentBodyBytes)
}

// readAtMost reads a bounded body and puts it back for the handler.
//
// It is shared by the two rings that need the WHOLE body before the handler
// runs: the idempotency fingerprint and the callback signature check. Sharing
// it also keeps them from reading the body twice — only one ring can consume
// and restore it, and this is the function that does.
func readAtMost(r *http.Request, limit int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}

	// Try to read one byte past the limit so we can tell an overflow apart.
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return nil, coreerrors.Invalid("invalid_body", "the request body could not be read")
	}

	if int64(len(body)) > limit {
		return nil, coreerrors.Invalid("body_too_large",
			"the request body can be at most %d bytes", limit)
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
