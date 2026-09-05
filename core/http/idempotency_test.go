package http_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// countingHandler is a handler that counts how many times it was called and returns a fixed response.
type countingHandler struct {
	mu     sync.Mutex
	cagri  int
	status int
	body   string
}

// ServeHTTP counts the call and writes the configured response.
func (h *countingHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	h.mu.Lock()
	h.cagri++
	n := h.cagri
	h.mu.Unlock()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(h.status)
	_, _ = fmt.Fprintf(w, `{"data":{"id":"order_%d"},"body":%q}`, n, h.body)
}

// count reads the handler's call count safely.
func (h *countingHandler) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.cagri
}

// postRequest builds a POST request with the given key and body.
func postRequest(key, path, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if key != "" {
		r.Header.Set(corehttp.IdempotencyKeyHeader, key)
	}

	return r
}

// asPrincipal tags the request with the given caller's verified identity.
//
// In reality this context is built by [corehttp.RequireAdmin] /
// [corehttp.RequireStore]; in a test there is no need to imitate the
// authentication layer in between.
func asPrincipal(r *http.Request, kind, id string) *http.Request {
	return r.WithContext(corehttp.WithPrincipal(r.Context(),
		corehttp.Principal{ID: id, Kind: kind}))
}

// TestIdempotencyARetryDoesNotRunTheHandlerASecondTime verifies that a second
// request arriving with the same key and body does NOT run the handler at all.
//
// This is the whole point of this phase: a payment request retried after a network
// timeout must not create a second charge.
func TestIdempotencyARetryDoesNotRunTheHandlerASecondTime(t *testing.T) {
	t.Parallel()

	h := &countingHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, postRequest("idem_1", "/store/v1/orders", `{"cart":"c1"}`))

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, postRequest("idem_1", "/store/v1/orders", `{"cart":"c1"}`))

	assert.Equal(t, 1, h.count(), "the handler has to run only once")
	assert.Equal(t, http.StatusCreated, w2.Code)
	assert.JSONEq(t, w1.Body.String(), w2.Body.String(), "the response has to be replayed as it is")
	assert.Equal(t, "application/json; charset=utf-8", w2.Header().Get("Content-Type"))
	assert.Empty(t, w1.Header().Get(corehttp.IdempotencyReplayedHeader),
		"the first response must not be marked as a replay")
	assert.Equal(t, "true", w2.Header().Get(corehttp.IdempotencyReplayedHeader))
}

// TestIdempotencyADifferentBodyReturnsAConflict verifies that using the same key
// with a DIFFERENT body is rejected.
//
// Had the recorded response been replayed quietly, the client would get the record
// of order A while saying "create order B": silent data corruption.
func TestIdempotencyADifferentBodyReturnsAConflict(t *testing.T) {
	t.Parallel()

	h := &countingHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, postRequest("idem_1", "/store/v1/orders", `{"cart":"c1"}`))
	require.Equal(t, http.StatusCreated, w1.Code)

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, postRequest("idem_1", "/store/v1/orders", `{"cart":"BASKA"}`))

	assert.Equal(t, http.StatusConflict, w2.Code)
	assert.Contains(t, w2.Body.String(), corehttp.CodeIdempotencyConflict)
	assert.Equal(t, 1, h.count(), "a conflicting request must not reach the handler")
}

// TestIdempotencyADifferentPathReturnsAConflict verifies that the fingerprint is
// not limited to the body but covers the path and the query string too.
func TestIdempotencyADifferentPathReturnsAConflict(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"a different path":         "/store/v1/returns",
		"a different query string": "/store/v1/orders?expand=items",
	}

	for name, path := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := &countingHandler{status: http.StatusCreated}
			mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

			w1 := httptest.NewRecorder()
			mw.ServeHTTP(w1, postRequest("idem_1", "/store/v1/orders", `{"cart":"c1"}`))
			require.Equal(t, http.StatusCreated, w1.Code)

			w2 := httptest.NewRecorder()
			mw.ServeHTTP(w2, postRequest("idem_1", path, `{"cart":"c1"}`))

			assert.Equal(t, http.StatusConflict, w2.Code)
		})
	}
}

// TestIdempotencyTheHandlerCanReadTheBody verifies that the body consumed for
// the fingerprint is PUT BACK for the handler.
//
// Without putting it back, turning idempotency on would empty the body of every
// client sending a key and break all POSTs.
func TestIdempotencyTheHandlerCanReadTheBody(t *testing.T) {
	t.Parallel()

	var read string

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 64)
		n, _ := r.Body.Read(b)
		read = string(b[:n])
		w.WriteHeader(http.StatusOK)
	})

	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)
	mw.ServeHTTP(httptest.NewRecorder(), postRequest("idem_1", "/x", `{"cart":"c1"}`))

	assert.Equal(t, `{"cart":"c1"}`, read, "the handler has to be able to read the body in full")
}

// TestIdempotencyAServerErrorIsNotRecorded verifies that a 5xx response is not
// replayed and that the key can be retried.
//
// Had it been recorded, a transient 500 would turn into a permanent 500 for 24
// hours on the same key: a self-healing fault turned into a permanent one.
func TestIdempotencyAServerErrorIsNotRecorded(t *testing.T) {
	t.Parallel()

	h := &countingHandler{status: http.StatusInternalServerError}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, postRequest("idem_1", "/x", `{"a":1}`))
	require.Equal(t, http.StatusInternalServerError, w1.Code)

	// The server recovered.
	h.status = http.StatusCreated

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, postRequest("idem_1", "/x", `{"a":1}`))

	assert.Equal(t, http.StatusCreated, w2.Code, "it has to be retryable after a 5xx")
	assert.Equal(t, 2, h.count(), "the handler has to run a second time")
	assert.Empty(t, w2.Header().Get(corehttp.IdempotencyReplayedHeader))
}

// TestIdempotencyAClientErrorIsRecorded verifies that a 4xx response is replayed.
//
// A 4xx comes from the client and retrying gives the same result; recording it
// saves needless work and makes the response consistent.
func TestIdempotencyAClientErrorIsRecorded(t *testing.T) {
	t.Parallel()

	h := &countingHandler{status: http.StatusUnprocessableEntity}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	mw.ServeHTTP(httptest.NewRecorder(), postRequest("idem_1", "/x", `{"a":1}`))

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, postRequest("idem_1", "/x", `{"a":1}`))

	assert.Equal(t, http.StatusUnprocessableEntity, w2.Code)
	assert.Equal(t, 1, h.count())
	assert.Equal(t, "true", w2.Header().Get(corehttp.IdempotencyReplayedHeader))
}

// TestIdempotencyItIsRetryableAfterAPanic verifies that the key does NOT STAY
// LOCKED when the handler panics.
//
// Had it stayed locked, a single panic would make that key permanently unusable:
// the client could neither retry nor get a response.
func TestIdempotencyItIsRetryableAfterAPanic(t *testing.T) {
	t.Parallel()

	patlasin := true
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if patlasin {
			panic("the handler blew up")
		}

		w.WriteHeader(http.StatusCreated)
	})

	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	assert.Panics(t, func() {
		mw.ServeHTTP(httptest.NewRecorder(), postRequest("idem_1", "/x", `{"a":1}`))
	})

	patlasin = false

	w := httptest.NewRecorder()
	mw.ServeHTTP(w, postRequest("idem_1", "/x", `{"a":1}`))

	assert.Equal(t, http.StatusCreated, w.Code, "the key has to be released after a panic")
}

// TestIdempotencyARequestWithoutAKeyFlows verifies that a request sent without a
// key flows normally and is not recorded.
func TestIdempotencyARequestWithoutAKeyFlows(t *testing.T) {
	t.Parallel()

	h := &countingHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	for range 3 {
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, postRequest("", "/x", `{"a":1}`))
		require.Equal(t, http.StatusCreated, w.Code)
	}

	assert.Equal(t, 3, h.count(), "requests without a key must not be recorded")
}

// TestIdempotencySafeMethodsAreNotRecorded verifies that a GET is not recorded.
//
// A GET is idempotent already; recording it only inflates the store and creates a
// risk of serving stale data by accident.
func TestIdempotencySafeMethodsAreNotRecorded(t *testing.T) {
	t.Parallel()

	h := &countingHandler{status: http.StatusOK}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	for range 2 {
		r := httptest.NewRequest(http.MethodGet, "/x", http.NoBody)
		r.Header.Set(corehttp.IdempotencyKeyHeader, "idem_1")

		w := httptest.NewRecorder()
		mw.ServeHTTP(w, r)
		require.Equal(t, http.StatusOK, w.Code)
	}

	assert.Equal(t, 2, h.count(), "GET kaydedilmemeli")
}

// TestIdempotencyANilStoreIsANoOp verifies that an unconfigured store does not
// cut off traffic.
func TestIdempotencyANilStoreIsANoOp(t *testing.T) {
	t.Parallel()

	h := &countingHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(nil)(h)

	w := httptest.NewRecorder()
	mw.ServeHTTP(w, postRequest("idem_1", "/x", `{"a":1}`))

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, 1, h.count())
}

// TestIdempotencyAnOverlongKeyIsRejected verifies that an unbounded key is kept
// from inflating the store.
//
// That the rejection carries its OWN code is proven here as well: had it returned
// the "reuse" code, the client's right reaction (producing a fresh key and trying
// again) would be an endless loop — every fresh key produced would be long too.
func TestIdempotencyAnOverlongKeyIsRejected(t *testing.T) {
	t.Parallel()

	h := &countingHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	w := httptest.NewRecorder()
	mw.ServeHTTP(w, postRequest(strings.Repeat("a", 256), "/x", `{"a":1}`))

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), corehttp.CodeIdempotencyKeyTooLong)
	assert.NotContains(t, w.Body.String(), corehttp.CodeIdempotencyConflict,
		"a length rejection must not be confused with the reuse code")
	assert.Zero(t, h.count(), "a rejected request must not reach the handler")
}

// TestIdempotencyAnOverlargeBodyIsRejected verifies that unbounded body reading
// is prevented.
//
// Without the limit a single request could consume the server's memory for the
// sake of taking a fingerprint.
func TestIdempotencyAnOverlargeBodyIsRejected(t *testing.T) {
	t.Parallel()

	h := &countingHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	w := httptest.NewRecorder()
	mw.ServeHTTP(w, postRequest("idem_1", "/x", strings.Repeat("x", (1<<20)+1)))

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	// The client's branching handle is the CODE, not the status: moving to the 413
	// RFC 9110 reserves for this case requires changing the error class mapping, and
	// when that day comes the status may change (see readLimited).
	assert.Contains(t, w.Body.String(), "body_too_large")
	assert.Zero(t, h.count())
}

// TestIdempotencyAConcurrentSecondRequestConflicts verifies that a second request
// arriving IN PARALLEL with the same key gets a 409 instead of waiting.
//
// Not waiting is deliberate: queueing the two requests would hang the second one
// as well if the first is slow. A client getting a 409 can back off and retry.
func TestIdempotencyAConcurrentSecondRequestConflicts(t *testing.T) {
	t.Parallel()

	basladi := make(chan struct{})
	proceed := make(chan struct{})

	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(basladi)
		<-proceed
		w.WriteHeader(http.StatusCreated)
	})

	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	ilk := make(chan int, 1)

	go func() {
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, postRequest("idem_1", "/x", `{"a":1}`))
		ilk <- w.Code
	}()

	<-basladi

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, postRequest("idem_1", "/x", `{"a":1}`))

	assert.Equal(t, http.StatusConflict, w2.Code)
	assert.Contains(t, w2.Body.String(), corehttp.CodeIdempotencyInFlight)

	close(proceed)
	assert.Equal(t, http.StatusCreated, <-ilk)
}

// TestIdempotencySeparatesKeys verifies that different keys do not affect each
// other.
func TestIdempotencySeparatesKeys(t *testing.T) {
	t.Parallel()

	h := &countingHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	for _, k := range []string{"a", "b", "c"} {
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, postRequest(k, "/x", `{"a":1}`))
		require.Equal(t, http.StatusCreated, w.Code)
	}

	assert.Equal(t, 3, h.count())
}

// TestIdempotencyASingleRunUnderARace verifies under the race detector that many
// parallel requests arriving with the same key do not run the handler MORE THAN
// ONCE.
func TestIdempotencyASingleRunUnderARace(t *testing.T) {
	t.Parallel()

	h := &countingHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	var wg sync.WaitGroup

	for range 32 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			mw.ServeHTTP(httptest.NewRecorder(), postRequest("idem_1", "/x", `{"a":1}`))
		}()
	}

	wg.Wait()

	assert.Equal(t, 1, h.count(), "the same key has to be processed only once")
}

// failingStore is a fake store returning an error on the Complete call.
type failingStore struct {
	ic          *corehttp.MemoryIdempotencyStore
	completeErr error
}

// Begin delegates the call to the real store.
func (d *failingStore) Begin(
	ctx context.Context, key, fp string,
) (*corehttp.IdempotentResponse, bool, error) {
	return d.ic.Begin(ctx, key, fp)
}

// Complete returns the configured error and does NOT WRITE the record.
func (d *failingStore) Complete(
	_ context.Context, _ string, _ corehttp.IdempotentResponse,
) error {
	return d.completeErr
}

// Abort delegates the call to the real store.
func (d *failingStore) Abort(ctx context.Context, key string) error {
	return d.ic.Abort(ctx, key)
}

// TestIdempotencyTheKeyIsNotLeftLockedWhenTheRecordCannotBeWritten verifies that
// the key is RELEASED when the store cannot write.
//
// Without the release the key would stay "in flight" forever and the client could
// neither get a response nor retry: a single store error would kill that key
// permanently.
func TestIdempotencyTheKeyIsNotLeftLockedWhenTheRecordCannotBeWritten(t *testing.T) {
	t.Parallel()

	store := &failingStore{
		ic:          corehttp.NewMemoryIdempotencyStore(time.Hour, 0),
		completeErr: errors.New("the store could not be written"),
	}

	h := &countingHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(store)(h)

	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, postRequest("idem_1", "/x", `{"a":1}`))
	require.Equal(t, http.StatusCreated, w1.Code, "the response has to reach the client anyway")

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, postRequest("idem_1", "/x", `{"a":1}`))

	assert.NotEqual(t, http.StatusConflict, w2.Code, "the key must not stay locked")
	assert.Equal(t, http.StatusCreated, w2.Code)
	assert.Equal(t, 2, h.count(), "if the record could not be written it has to be processed again")
}

// TestIdempotencyAnOverlargeResponseIsNotRecorded verifies that a response
// exceeding the buffer limit reaches the client IN FULL but is not recorded.
//
// Recording a partial buffer and replaying it later would hand the client a
// truncated, broken body; that is far worse than processing the retry again.
func TestIdempotencyAnOverlargeResponseIsNotRecorded(t *testing.T) {
	t.Parallel()

	const size = (1 << 20) + 100

	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, size))
	})

	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, postRequest("idem_1", "/x", `{"a":1}`))
	assert.Equal(t, size, w1.Body.Len(), "the client has to get the full response")

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, postRequest("idem_1", "/x", `{"a":1}`))

	assert.Empty(t, w2.Header().Get(corehttp.IdempotencyReplayedHeader),
		"an unrecorded response must not be replayed")
	assert.Equal(t, size, w2.Body.Len(), "the retry has to get the full response too")
}

// TestIdempotencyADroppedKeyIsProcessedAgainOnceTheBudgetIsFull verifies that
// when the memory budget fills up, the retry of the DROPPED record runs the
// handler again.
//
// The test exists to make the PRICE of the limit visible. The sentence "memory was
// bounded" reads as if it were free; its price is that a retry arriving with a
// dropped key creates a second order. This behavior is not an accident but a
// deliberate choice (see the corehttp.MemoryIdempotencyStore godoc: dropping
// rather than rejecting), and the proof of that choice can only be given here,
// where the middleware sees it.
func TestIdempotencyADroppedKeyIsProcessedAgainOnceTheBudgetIsFull(t *testing.T) {
	t.Parallel()

	h := &countingHandler{status: http.StatusCreated}
	// Each of these responses is charged ~937 bytes; the budget takes TWO records,
	// and when the third is written the oldest is dropped.
	store := corehttp.NewMemoryIdempotencyStore(time.Hour, 2000)
	mw := corehttp.Idempotency(store)(h)

	for _, key := range []string{"idem_1", "idem_2", "idem_3"} {
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, postRequest(key, "/store/v1/orders", `{"cart":"c1"}`))
		require.Equal(t, http.StatusCreated, w.Code)
	}

	require.Equal(t, 3, h.count())

	// The RETRY of the first key: because its record was dropped it is processed again.
	w4 := httptest.NewRecorder()
	mw.ServeHTTP(w4, postRequest("idem_1", "/store/v1/orders", `{"cart":"c1"}`))

	assert.Equal(t, 4, h.count(), "a retry arriving with a dropped key is processed again")
	assert.Empty(t, w4.Header().Get(corehttp.IdempotencyReplayedHeader),
		"a dropped record must not be marked as replayed")

	// The third key is still guarded: the limit does not turn the WHOLE guard off, it
	// only lets go of the oldest record.
	w5 := httptest.NewRecorder()
	mw.ServeHTTP(w5, postRequest("idem_3", "/store/v1/orders", `{"cart":"c1"}`))

	assert.Equal(t, 4, h.count(), "the retry of a record that was not dropped must not run the handler")
	assert.Equal(t, "true", w5.Header().Get(corehttp.IdempotencyReplayedHeader))
}

// TestIdempotencyAnotherCallersResponseIsNotReplayed verifies that TWO DIFFERENT
// callers picking the same key do not see each other's record.
//
// Because the requests are identical byte for byte, without the namespace the
// second caller would replay the first one's response (another tenant's order id,
// say): a cross-tenant data leak. Considering ordinary keys like "1" or "order-1",
// this is not an edge case but the expected one.
func TestIdempotencyAnotherCallersResponseIsNotReplayed(t *testing.T) {
	t.Parallel()

	h := &countingHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, asPrincipal(postRequest("1", "/store/v1/orders", `{"cart":"c1"}`), "user", "usr_1"))
	require.Equal(t, http.StatusCreated, w1.Code)

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, asPrincipal(postRequest("1", "/store/v1/orders", `{"cart":"c1"}`), "user", "usr_2"))

	assert.Equal(t, http.StatusCreated, w2.Code)
	assert.Empty(t, w2.Header().Get(corehttp.IdempotencyReplayedHeader),
		"the second caller's request is not a replay")
	assert.NotEqual(t, w1.Body.String(), w2.Body.String(),
		"every caller has to get THEIR OWN response")
	assert.Equal(t, 2, h.count(), "the requests of two different callers both have to be processed")

	// The first caller's own retry still has to be replayed: the namespace must not
	// break the behavior it is guarding.
	w3 := httptest.NewRecorder()
	mw.ServeHTTP(w3, asPrincipal(postRequest("1", "/store/v1/orders", `{"cart":"c1"}`), "user", "usr_1"))

	assert.Equal(t, "true", w3.Header().Get(corehttp.IdempotencyReplayedHeader))
	assert.JSONEq(t, w1.Body.String(), w3.Body.String())
	assert.Equal(t, 2, h.count(), "one's own retry must not be processed again")
}

// TestIdempotencyAnotherCallerDoesNotOccupyTheKeySpace verifies that a second
// caller using the same key with a DIFFERENT body does not get a 409.
//
// Without the namespace one caller would occupy the other's key space with the key
// it picked: the other side would get a 409 for its own request and could never
// use that key again.
func TestIdempotencyAnotherCallerDoesNotOccupyTheKeySpace(t *testing.T) {
	t.Parallel()

	h := &countingHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, asPrincipal(postRequest("order-1", "/x", `{"cart":"c1"}`), "api_key", "ak_1"))
	require.Equal(t, http.StatusCreated, w1.Code)

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, asPrincipal(postRequest("order-1", "/x", `{"cart":"BASKA"}`), "api_key", "ak_2"))

	assert.Equal(t, http.StatusCreated, w2.Code, "the second caller has to be in their own key space")
	assert.Equal(t, 2, h.count())
}

// TestIdempotencyTheSameCallerWithTheSameKeyConflicts verifies that the namespace
// does NOT BLIND conflict detection.
//
// If the same caller is using the same key with a different request, that is still
// a client mistake and it has to return a 409.
func TestIdempotencyTheSameCallerWithTheSameKeyConflicts(t *testing.T) {
	t.Parallel()

	h := &countingHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, asPrincipal(postRequest("1", "/x", `{"cart":"c1"}`), "user", "usr_1"))
	require.Equal(t, http.StatusCreated, w1.Code)

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, asPrincipal(postRequest("1", "/x", `{"cart":"BASKA"}`), "user", "usr_1"))

	assert.Equal(t, http.StatusConflict, w2.Code)
	assert.Contains(t, w2.Body.String(), corehttp.CodeIdempotencyConflict)
	assert.Equal(t, 1, h.count())
}

// TestIdempotencyCallersWithoutAnIdentityShareTheBucket verifies that anonymous
// callers on an unguarded endpoint share a SINGLE namespace, that is, they can
// replay each other's responses.
//
// This is not a flaw but a documented limit: separating anonymous requests by IP
// would break idempotency without binding the key to a tenant (an IP can be
// spoofed, a NAT is shared) — a client retrying after its network changed would
// not find its own record. The test pins the behavior so it cannot change quietly.
func TestIdempotencyCallersWithoutAnIdentityShareTheBucket(t *testing.T) {
	t.Parallel()

	h := &countingHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, postRequest("1", "/x", `{"cart":"c1"}`))
	require.Equal(t, http.StatusCreated, w1.Code)

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, postRequest("1", "/x", `{"cart":"c1"}`))

	assert.Equal(t, "true", w2.Header().Get(corehttp.IdempotencyReplayedHeader),
		"requests without an identity are in the common bucket")
	assert.Equal(t, 1, h.count())

	// A caller with an identity is not affected by that common bucket.
	w3 := httptest.NewRecorder()
	mw.ServeHTTP(w3, asPrincipal(postRequest("1", "/x", `{"cart":"c1"}`), "user", "usr_1"))

	assert.Empty(t, w3.Header().Get(corehttp.IdempotencyReplayedHeader),
		"a caller with an identity must not replay the anonymous bucket's record")
	assert.Equal(t, 2, h.count())
}

// closeState is what was read AT CALL TIME from a closing call's context.
//
// The context itself is not stored: the middleware cancels the context it built as
// soon as the call returns (defer), that is, a test looking at it afterwards would
// see "canceled" either way and could not prove the fix.
type closeState struct {
	// called reports whether the call was made at all.
	called bool
	// err is the value of ctx.Err() at call time; it has to be nil.
	err error
	// bounded reports whether the context carries a time limit.
	bounded bool
}

// closeCapturingStore records which context Complete/Abort were reached with.
type closeCapturingStore struct {
	ic *corehttp.MemoryIdempotencyStore

	mu       sync.Mutex
	complete closeState
	abort    closeState
}

// Begin delegates the call to the real store.
func (d *closeCapturingStore) Begin(
	ctx context.Context, key, fp string,
) (*corehttp.IdempotentResponse, bool, error) {
	return d.ic.Begin(ctx, key, fp)
}

// Complete records the state of the context and delegates the write to the real store.
func (d *closeCapturingStore) Complete(
	ctx context.Context, key string, resp corehttp.IdempotentResponse,
) error {
	d.mu.Lock()
	d.complete = readState(ctx)
	d.mu.Unlock()

	return d.ic.Complete(ctx, key, resp)
}

// Abort records the state of the context and undoes the reservation in the real store.
func (d *closeCapturingStore) Abort(ctx context.Context, key string) error {
	d.mu.Lock()
	d.abort = readState(ctx)
	d.mu.Unlock()

	return d.ic.Abort(ctx, key)
}

// closes reads the recorded states safely.
func (d *closeCapturingStore) closes() (complete, abort closeState) {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.complete, d.abort
}

// readState extracts the fields to be examined from the context.
func readState(ctx context.Context) closeState {
	_, bounded := ctx.Deadline()

	return closeState{called: true, err: ctx.Err(), bounded: bounded}
}

// TestIdempotencyTheRecordIsWrittenEvenIfTheClientDisconnects verifies that the
// record can be written even when the client has dropped the connection.
//
// Had we written with the request's context, a dropped connection would cancel
// Complete: even though the handler ran (the charge was made) no record would be
// formed and the thing protecting the retry from a second charge would be lost.
func TestIdempotencyTheRecordIsWrittenEvenIfTheClientDisconnects(t *testing.T) {
	t.Parallel()

	store := &closeCapturingStore{ic: corehttp.NewMemoryIdempotencyStore(time.Hour, 0)}

	ctx, iptal := context.WithCancel(t.Context())
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The client dropped the connection while the response was being written.
		iptal()
		w.WriteHeader(http.StatusCreated)
	})

	mw := corehttp.Idempotency(store)(h)

	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, postRequest("idem_1", "/x", `{"a":1}`).WithContext(ctx))
	require.Equal(t, http.StatusCreated, w1.Code)

	complete, _ := store.closes()
	require.True(t, complete.called, "Complete has to be called")
	assert.NoError(t, complete.err, "the closing context must not be affected by the request's cancellation")
	assert.True(t, complete.bounded, "a context cut off from cancellation must not be left unbounded")

	// If the record really was written it is replayed.
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, postRequest("idem_1", "/x", `{"a":1}`))

	assert.Equal(t, "true", w2.Header().Get(corehttp.IdempotencyReplayedHeader))
}

// TestIdempotencyTheReservationIsUndoneEvenIfTheClientDisconnects verifies that
// the reservation can be undone even when the client has dropped the connection.
//
// Had Abort been called with a canceled context the key would stay locked "in
// flight": the client could neither get a response nor retry.
func TestIdempotencyTheReservationIsUndoneEvenIfTheClientDisconnects(t *testing.T) {
	t.Parallel()

	store := &closeCapturingStore{ic: corehttp.NewMemoryIdempotencyStore(time.Hour, 0)}

	ctx, iptal := context.WithCancel(t.Context())
	h := &countingHandler{status: http.StatusInternalServerError}

	mw := corehttp.Idempotency(store)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			iptal()
			h.ServeHTTP(w, r)
		}))

	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, postRequest("idem_1", "/x", `{"a":1}`).WithContext(ctx))
	require.Equal(t, http.StatusInternalServerError, w1.Code)

	_, abort := store.closes()
	require.True(t, abort.called, "the reservation has to be undone after a 5xx")
	assert.NoError(t, abort.err, "the closing context must not be affected by the request's cancellation")
	assert.True(t, abort.bounded, "a context cut off from cancellation must not be left unbounded")

	// If the reservation really was released the key can be retried.
	h.status = http.StatusCreated

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, postRequest("idem_1", "/x", `{"a":1}`))

	assert.Equal(t, http.StatusCreated, w2.Code, "the key must not stay locked")
}
