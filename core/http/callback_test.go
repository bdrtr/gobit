package http_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// testAck is the answer vocabulary of the test provider. It is deliberately
// NOT JSON: the
// measured provider reads the body, not the status, and this suite would not
// notice the core's envelope leaking onto the surface if the two looked alike.
var testAck = corehttp.CallbackAck{
	Accepted:    corehttp.CallbackResponse{Status: http.StatusOK, Body: "OK"},
	Duplicate:   corehttp.CallbackResponse{Status: http.StatusOK, Body: "DUP"},
	Rejected:    corehttp.CallbackResponse{Status: http.StatusForbidden, Body: "BAD_HASH"},
	Malformed:   corehttp.CallbackResponse{Status: http.StatusBadRequest, Body: "BAD_REQUEST"},
	Unavailable: corehttp.CallbackResponse{Status: http.StatusInternalServerError, Body: "RETRY"},
}

// callbackHarness is a mounted registry with a router in front of it.
type callbackHarness struct {
	registry *corehttp.CallbackRegistry
	router   chi.Router
	calls    *int
}

// newCallbackHarness builds a registry with one route and mounts it.
func newCallbackHarness(t *testing.T, opts corehttp.CallbackOptions,
	change ...func(*corehttp.CallbackRoute),
) *callbackHarness {
	t.Helper()

	calls := 0
	route := corehttp.CallbackRoute{
		Source:  "testpay",
		Path:    "/testpay/callback",
		Verify:  func(_ context.Context, r *http.Request, body []byte) error { return verifyTestBody(body) },
		Key:     testKey,
		Handler: func(w http.ResponseWriter, _ *http.Request) { calls++; _, _ = io.WriteString(w, "OK") },
		Ack:     testAck,
	}
	for _, apply := range change {
		apply(&route)
	}

	registry := corehttp.NewCallbackRegistry(opts)
	require.NoError(t, registry.Register(route))

	router := chi.NewRouter()
	router.Use(registry.Middleware())
	require.NoError(t, registry.Mount(router))

	return &callbackHarness{registry: registry, router: router, calls: &calls}
}

// post sends a callback body and returns what the surface answered.
func (h *callbackHarness) post(body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/testpay/callback", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	h.router.ServeHTTP(recorder, request)

	return recorder
}

// verifyTestBody stands in for a signature: a body must start with "signed:".
func verifyTestBody(body []byte) error {
	if !strings.HasPrefix(string(body), "signed:") {
		return errors.New("the signature does not match")
	}

	return nil
}

// testKey derives the identity from the first field and the content from both,
// the way a real route derives them from signature-covered fields only.
func testKey(_ *http.Request, body []byte) (identity, content []string, err error) {
	fields := strings.Split(strings.TrimPrefix(string(body), "signed:"), ",")
	if len(fields) == 0 || fields[0] == "" {
		return nil, nil, nil
	}

	return []string{fields[0]}, fields, nil
}

// TestAVerifiedCallbackReachesTheHandler is the baseline the rest measure against.
func TestAVerifiedCallbackReachesTheHandler(t *testing.T) {
	t.Parallel()

	harness := newCallbackHarness(t, corehttp.CallbackOptions{Store: newTestCallbackStore()})
	response := harness.post("signed:evt-1,paid")

	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "OK", response.Body.String())
	require.Equal(t, 1, *harness.calls)
}

// TestAForgedCallbackNeverReachesTheHandler is the whole reason this ring exists.
//
// The measured endpoint's only credential is a signature inside the body, so a
// verification that runs after the handler — or not at all — protects nothing.
func TestAForgedCallbackNeverReachesTheHandler(t *testing.T) {
	t.Parallel()

	harness := newCallbackHarness(t, corehttp.CallbackOptions{Store: newTestCallbackStore()})
	response := harness.post("forged:evt-1,paid")

	require.Equal(t, http.StatusForbidden, response.Code)
	require.Equal(t, "BAD_HASH", response.Body.String())
	require.Zero(t, *harness.calls, "a request that failed verification reached the handler")
}

// TestAnOversizedBodyIsRefusedBeforeAnythingReadsIt bounds the one unbounded input.
func TestAnOversizedBodyIsRefusedBeforeAnythingReadsIt(t *testing.T) {
	t.Parallel()

	harness := newCallbackHarness(t, corehttp.CallbackOptions{Store: newTestCallbackStore()},
		func(rt *corehttp.CallbackRoute) { rt.MaxBodyBytes = 32 })
	response := harness.post("signed:" + strings.Repeat("x", 64))

	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Equal(t, "BAD_REQUEST", response.Body.String())
	require.Zero(t, *harness.calls)
}

// TestARetriedCallbackIsAnsweredFromTheRecord keeps a retry from paying twice.
func TestARetriedCallbackIsAnsweredFromTheRecord(t *testing.T) {
	t.Parallel()

	harness := newCallbackHarness(t, corehttp.CallbackOptions{Store: newTestCallbackStore()})

	first := harness.post("signed:evt-1,paid")
	second := harness.post("signed:evt-1,paid")

	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, http.StatusOK, second.Code)
	require.Equal(t, "OK", second.Body.String(), "the retry was not answered with the recorded body")
	require.Equal(t, "true", second.Header().Get("Idempotency-Replayed"))
	require.Equal(t, 1, *harness.calls, "the handler ran twice for one event")
}

// TestAContradictingRetryIsAcknowledgedAndNotApplied is the case a plain
// idempotency ring gets wrong.
//
// The same event id arriving with a different outcome is a real signal, not a
// client error — and it still has to be ACKNOWLEDGED, because a provider that
// reads the body would otherwise retry the contradiction forever.
func TestAContradictingRetryIsAcknowledgedAndNotApplied(t *testing.T) {
	t.Parallel()

	harness := newCallbackHarness(t, corehttp.CallbackOptions{Store: newTestCallbackStore()})

	harness.post("signed:evt-1,paid")
	second := harness.post("signed:evt-1,failed")

	require.Equal(t, http.StatusOK, second.Code, "a contradiction was refused; the provider will retry it forever")
	require.Equal(t, "DUP", second.Body.String())
	require.Equal(t, 1, *harness.calls, "the contradicting event was applied")
}

// TestTwoSourcesDoNotShareAReplayNamespace is why the key carries the source.
func TestTwoSourcesDoNotShareAReplayNamespace(t *testing.T) {
	t.Parallel()

	store := newTestCallbackStore()
	registry := corehttp.NewCallbackRegistry(corehttp.CallbackOptions{Store: store})

	calls := map[string]int{}
	for _, source := range []string{"alpha", "beta"} {
		require.NoError(t, registry.Register(corehttp.CallbackRoute{
			Source: source,
			Path:   "/" + source + "/callback",
			Verify: func(_ context.Context, _ *http.Request, body []byte) error { return verifyTestBody(body) },
			Key:    testKey,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				calls[strings.Split(r.URL.Path, "/")[1]]++
				_, _ = io.WriteString(w, "OK")
			},
			Ack: testAck,
		}))
	}

	router := chi.NewRouter()
	router.Use(registry.Middleware())
	require.NoError(t, registry.Mount(router))

	for _, source := range []string{"alpha", "beta"} {
		request := httptest.NewRequest(http.MethodPost, "/"+source+"/callback",
			strings.NewReader("signed:evt-1,paid"))
		router.ServeHTTP(httptest.NewRecorder(), request)
	}

	require.Equal(t, map[string]int{"alpha": 1, "beta": 1},
		calls, "one provider's event silenced another's; the replay key is not namespaced by source")
}

// TestAnUnreachableReplayWindowRefusesRatherThanRisksApplyingTwice states the
// direction this ring fails in.
func TestAnUnreachableReplayWindowRefusesRatherThanRisksApplyingTwice(t *testing.T) {
	t.Parallel()

	store := newTestCallbackStore()
	store.beginErr = errors.New("redis is unreachable")
	harness := newCallbackHarness(t, corehttp.CallbackOptions{Store: store})

	response := harness.post("signed:evt-1,paid")

	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Equal(t, "RETRY", response.Body.String())
	require.Zero(t, *harness.calls, "the event was applied with no replay window")
}

// TestAnEventInFlightIsToldToRetry keeps two concurrent deliveries from both
// being applied.
func TestAnEventInFlightIsToldToRetry(t *testing.T) {
	t.Parallel()

	store := newTestCallbackStore()
	store.beginErr = corehttp.ErrIdempotencyKeyInFlight
	harness := newCallbackHarness(t, corehttp.CallbackOptions{Store: store})

	response := harness.post("signed:evt-1,paid")

	require.Equal(t, "RETRY", response.Body.String())
	require.Zero(t, *harness.calls)
}

// TestAFailedHandlerIsNotRecorded keeps a transient fault from becoming permanent.
func TestAFailedHandlerIsNotRecorded(t *testing.T) {
	t.Parallel()

	store := newTestCallbackStore()
	attempts := 0
	harness := newCallbackHarness(t, corehttp.CallbackOptions{Store: store},
		func(rt *corehttp.CallbackRoute) {
			rt.Handler = func(w http.ResponseWriter, _ *http.Request) {
				attempts++
				if attempts == 1 {
					w.WriteHeader(http.StatusInternalServerError)

					return
				}
				_, _ = io.WriteString(w, "OK")
			}
		})

	first := harness.post("signed:evt-1,paid")
	second := harness.post("signed:evt-1,paid")

	require.Equal(t, http.StatusInternalServerError, first.Code)
	require.Equal(t, http.StatusOK, second.Code)
	require.Equal(t, "OK", second.Body.String(),
		"the retry was answered from a recorded FAILURE; the event can never be applied")
	require.Equal(t, 2, attempts)
}

// TestAThrottledCallbackIsToldToRetry gives the surface the quota it has none of.
func TestAThrottledCallbackIsToldToRetry(t *testing.T) {
	t.Parallel()

	harness := newCallbackHarness(t, corehttp.CallbackOptions{
		Store:   newTestCallbackStore(),
		Limiter: refusingLimiter{},
	})

	response := harness.post("signed:evt-1,paid")

	require.Equal(t, "RETRY", response.Body.String())
	require.Zero(t, *harness.calls, "a throttled callback still did the work")
}

// TestAnUnkeyableCallbackStillRuns keeps a payload this route cannot key from
// being refused on this repository's authority.
func TestAnUnkeyableCallbackStillRuns(t *testing.T) {
	t.Parallel()

	harness := newCallbackHarness(t, corehttp.CallbackOptions{Store: newTestCallbackStore()})
	response := harness.post("signed:")

	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, 1, *harness.calls)
}

// TestNothingIsGuardedBeforeMount states the direction the ring fails in while
// it is still being built.
func TestNothingIsGuardedBeforeMount(t *testing.T) {
	t.Parallel()

	registry := corehttp.NewCallbackRegistry(corehttp.CallbackOptions{Store: newTestCallbackStore()})
	require.NoError(t, registry.Register(corehttp.CallbackRoute{
		Source: "testpay", Path: "/testpay/callback",
		Verify:  func(context.Context, *http.Request, []byte) error { return nil },
		Key:     testKey,
		Handler: func(http.ResponseWriter, *http.Request) {},
		Ack:     testAck,
	}))

	router := chi.NewRouter()
	router.Use(registry.Middleware())

	request := httptest.NewRequest(http.MethodPost, "/testpay/callback", strings.NewReader("signed:x"))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNotFound, recorder.Code,
		"an unmounted callback answered; the route is not bound, so nothing could have run")
}

// TestRegisterRefusesWhatItCannotGuard checks every startup refusal.
func TestRegisterRefusesWhatItCannotGuard(t *testing.T) {
	t.Parallel()

	sound := func() corehttp.CallbackRoute {
		return corehttp.CallbackRoute{
			Source: "testpay", Path: "/testpay/callback",
			Verify:  func(context.Context, *http.Request, []byte) error { return nil },
			Key:     testKey,
			Handler: func(http.ResponseWriter, *http.Request) {},
			Ack:     testAck,
		}
	}

	cases := map[string]func(*corehttp.CallbackRoute){
		"no source":       func(rt *corehttp.CallbackRoute) { rt.Source = " " },
		"relative path":   func(rt *corehttp.CallbackRoute) { rt.Path = "testpay/callback" },
		"pattern path":    func(rt *corehttp.CallbackRoute) { rt.Path = "/testpay/{id}" },
		"wildcard path":   func(rt *corehttp.CallbackRoute) { rt.Path = "/testpay/*" },
		"no verification": func(rt *corehttp.CallbackRoute) { rt.Verify = nil },
		"no key":          func(rt *corehttp.CallbackRoute) { rt.Key = nil },
		"no handler":      func(rt *corehttp.CallbackRoute) { rt.Handler = nil },
		"no accepted answer": func(rt *corehttp.CallbackRoute) {
			rt.Ack.Accepted = corehttp.CallbackResponse{}
		},
		"no retry answer": func(rt *corehttp.CallbackRoute) {
			rt.Ack.Unavailable = corehttp.CallbackResponse{}
		},
	}

	for name, break_ := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			route := sound()
			break_(&route)
			require.Error(t, corehttp.NewCallbackRegistry(corehttp.CallbackOptions{}).Register(route),
				"a callback with %s was accepted; it would be reachable and unguarded", name)
		})
	}
}

// TestOnePathCannotHaveTwoProviders refuses the collision at startup.
func TestOnePathCannotHaveTwoProviders(t *testing.T) {
	t.Parallel()

	registry := corehttp.NewCallbackRegistry(corehttp.CallbackOptions{})
	route := corehttp.CallbackRoute{
		Source: "alpha", Path: "/shared/callback",
		Verify:  func(context.Context, *http.Request, []byte) error { return nil },
		Key:     testKey,
		Handler: func(http.ResponseWriter, *http.Request) {},
		Ack:     testAck,
	}

	require.NoError(t, registry.Register(route))
	route.Source = "beta"
	require.Error(t, registry.Register(route))
}

// TestRegisteringAfterMountIsLoud keeps a late route from being silently unbound.
func TestRegisteringAfterMountIsLoud(t *testing.T) {
	t.Parallel()

	registry := corehttp.NewCallbackRegistry(corehttp.CallbackOptions{})
	require.NoError(t, registry.Mount(chi.NewRouter()))

	require.Error(t, registry.Register(corehttp.CallbackRoute{
		Source: "late", Path: "/late/callback",
		Verify:  func(context.Context, *http.Request, []byte) error { return nil },
		Key:     testKey,
		Handler: func(http.ResponseWriter, *http.Request) {},
		Ack:     testAck,
	}), "a route registered after mounting was accepted; it would never be bound")
}

// refusingLimiter always says no.
type refusingLimiter struct{}

// Allow refuses every key.
func (refusingLimiter) Allow(context.Context, string) (corehttp.Decision, error) {
	return corehttp.Decision{Allowed: false, Limit: 1, RetryAfter: time.Second}, nil
}

// testCallbackStore is an in-test idempotency store with injectable faults.
type testCallbackStore struct {
	mu       sync.Mutex
	records  map[string]corehttp.IdempotentResponse
	inFlight map[string]bool
	beginErr error
}

// newTestCallbackStore builds an empty store.
func newTestCallbackStore() *testCallbackStore {
	return &testCallbackStore{
		records:  map[string]corehttp.IdempotentResponse{},
		inFlight: map[string]bool{},
	}
}

// Begin reserves the key or returns what was recorded for it.
func (s *testCallbackStore) Begin(_ context.Context, key, _ string) (
	*corehttp.IdempotentResponse, bool, error,
) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.beginErr != nil {
		return nil, false, s.beginErr
	}
	if record, done := s.records[key]; done {
		return &record, true, nil
	}
	if s.inFlight[key] {
		return nil, false, corehttp.ErrIdempotencyKeyInFlight
	}
	s.inFlight[key] = true

	return nil, false, nil
}

// Complete records the answer.
func (s *testCallbackStore) Complete(_ context.Context, key string,
	response corehttp.IdempotentResponse,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.inFlight, key)
	s.records[key] = response

	return nil
}

// Abort releases the reservation.
func (s *testCallbackStore) Abort(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.inFlight, key)

	return nil
}
