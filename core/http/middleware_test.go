package http_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// requestIDHeaderName is the request id header used in the tests.
const requestIDHeaderName = "X-Request-Id"

// maskedValue is the mark written into the access log in place of a masked value.
// The tests look for exactly this in the output; a constant sharpens the
// assertion by also ruling out masking "quietly turning into an empty string".
const maskedValue = "REDACTED"

// testLogger produces a JSON logger whose output goes to a buffer.
// It is used to prove what the logged fields really contain.
func testLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return log, &buf
}

// logRecords parses the JSON log lines in the buffer.
func logRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()

	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &rec), "the log line is not JSON: %s", line)
		out = append(out, rec)
	}
	return out
}

func TestRequestIDKeepsTheIncomingHeader(t *testing.T) {
	t.Parallel()

	var seen string
	h := corehttp.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = corehttp.RequestIDFromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set(requestIDHeaderName, "req_incoming_id")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, "req_incoming_id", seen, "the id in the context has to come from the incoming header")
	assert.Equal(t, "req_incoming_id", rec.Header().Get(requestIDHeaderName), "the response header has to reflect the incoming id")
}

func TestRequestIDProducesOneWithoutAHeader(t *testing.T) {
	t.Parallel()

	var seen []string
	h := corehttp.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = append(seen, corehttp.RequestIDFromContext(r.Context()))
	}))

	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	second := httptest.NewRecorder()
	h.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	require.Len(t, seen, 2)
	assert.NotEmpty(t, seen[0], "an id has to be produced")
	assert.NotEqual(t, seen[0], seen[1], "a separate id has to be produced for every request")
	assert.Equal(t, seen[0], first.Header().Get(requestIDHeaderName), "the produced id has to be written into the response header")
	assert.Equal(t, seen[1], second.Header().Get(requestIDHeaderName))
}

func TestRequestIDRejectsAHostileValue(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"a line break":        "abc\r\nX-Injected: evil",
		"a control character": "abc\x00def",
		"far too long":        strings.Repeat("a", 129),
		"whitespace only":     "   ",
		"non-ascii":           "identity-caf\u00e9",
	}

	for name, hostile := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var seen string
			h := corehttp.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				seen = corehttp.RequestIDFromContext(r.Context())
			}))

			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			req.Header.Set(requestIDHeaderName, hostile)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			assert.NotEqual(t, hostile, seen, "an invalid id must not be accepted as it is")
			assert.NotEmpty(t, seen, "a new one has to be produced in place of the rejected id")
			assert.Equal(t, seen, rec.Header().Get(requestIDHeaderName))
		})
	}
}

func TestRequestIDFromContextReturnsEmptyWhenEmpty(t *testing.T) {
	t.Parallel()

	assert.Empty(t, corehttp.RequestIDFromContext(t.Context()), "without the middleware the id has to be empty")
}

func TestRecovererReturnsA500JSONOnAPanic(t *testing.T) {
	t.Parallel()

	log, buf := testLogger()
	h := corehttp.RequestID(corehttp.Recoverer(log)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("unexpected state: secret-detay-123")
	})))

	req := httptest.NewRequest(http.MethodGet, "/patlayan", http.NoBody)
	req.Header.Set(requestIDHeaderName, "req_panik")
	rec := httptest.NewRecorder()

	require.NotPanics(t, func() { h.ServeHTTP(rec, req) }, "the panic must not get past the middleware")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))

	var body corehttp.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "the body has to be JSON: %s", rec.Body.String())
	assert.Equal(t, "internal_error", body.Error.Code)
	assert.Equal(t, "req_panik", body.Error.RequestID)
	assert.NotContains(t, rec.Body.String(), "secret-detail-123", "the panic text must not leak to the client")

	records := logRecords(t, buf)
	require.Len(t, records, 1)
	assert.Equal(t, "the handler panicked", records[0]["msg"])
	assert.Equal(t, "unexpected state: secret-detay-123", records[0]["panic"])
	assert.Contains(t, records[0]["stack"], "panic", "the stack trace has to be logged")
	assert.Equal(t, "req_panik", records[0]["request_id"])

	// The process is up: the same chain handles the next request normally.
	okRec := httptest.NewRecorder()
	corehttp.Recoverer(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(okRec, httptest.NewRequest(http.MethodGet, "/saglikli", http.NoBody))
	assert.Equal(t, http.StatusNoContent, okRec.Code)
}

func TestRecovererRethrowsErrAbortHandler(t *testing.T) {
	t.Parallel()

	log, buf := testLogger()
	h := corehttp.Recoverer(log)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	rec := httptest.NewRecorder()

	func() {
		defer func() {
			r := recover()
			require.NotNil(t, r, "ErrAbortHandler has to be re-thrown")
			assert.Equal(t, http.ErrAbortHandler, r, "the same panic value has to be kept")
		}()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/abort", http.NoBody))
	}()

	assert.Empty(t, rec.Body.String(), "no body must be written to an aborted request")
	assert.Empty(t, buf.String(), "the abort contract must not be logged")
}

// transparentWriter is an http.ResponseWriter wrapper that exposes no extra
// capability and only forwards. It imitates a middleware foreign to the chain
// (compression, say) getting in.
type transparentWriter struct {
	http.ResponseWriter
}

// transparentLayer wraps the given handler in a transparentWriter.
func transparentLayer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&transparentWriter{ResponseWriter: w}, r)
	})
}

func TestRecovererDoesNotBreakAHalfWrittenResponse(t *testing.T) {
	t.Parallel()

	const halfBody = `{"items":[1,2`

	// A handler that panics but has already started writing the body.
	yarimHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(halfBody))
		panic("blew up while writing the body")
	})

	tests := map[string]func(log *slog.Logger) http.Handler{
		"the recoverer on its own": func(log *slog.Logger) http.Handler {
			return corehttp.Recoverer(log)(yarimHandler)
		},
		"the recoverer outermost, the logger inside": func(log *slog.Logger) http.Handler {
			return corehttp.Recoverer(log)(corehttp.RequestLogger(log)(yarimHandler))
		},
		"a foreign wrapper got in between": func(log *slog.Logger) http.Handler {
			return corehttp.RequestLogger(log)(transparentLayer(corehttp.Recoverer(log)(transparentLayer(yarimHandler))))
		},
	}

	for name, kur := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			log, buf := testLogger()
			rec := httptest.NewRecorder()
			require.NotPanics(t, func() {
				kur(log).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream", http.NoBody))
			})

			assert.Equal(t, halfBody, rec.Body.String(),
				"no second body must be added after the response has started")
			assert.Equal(t, http.StatusOK, rec.Code, "a sent status cannot be changed")
			assert.Contains(t, buf.String(), "the handler panicked", "the panic still has to be logged")
		})
	}
}

func TestRequestLoggerRecordsTheStatusAndTheDuration(t *testing.T) {
	t.Parallel()

	log, buf := testLogger()
	const delay = 25 * time.Millisecond

	h := corehttp.RequestID(corehttp.RequestLogger(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(delay)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})))

	req := httptest.NewRequest(http.MethodPost, "/store/v1/orders", http.NoBody)
	req.Header.Set(requestIDHeaderName, "req_log")
	h.ServeHTTP(httptest.NewRecorder(), req)

	records := logRecords(t, buf)
	require.Len(t, records, 1)
	rec := records[0]

	assert.Equal(t, "request completed", rec["msg"])
	assert.Equal(t, http.MethodPost, rec["method"])
	assert.Equal(t, "/store/v1/orders", rec["path"])
	assert.InDelta(t, float64(http.StatusCreated), rec["status"], 0)
	assert.InDelta(t, float64(len(`{"ok":true}`)), rec["bytes"], 0, "the number of bytes written has to be right")
	assert.Equal(t, "req_log", rec["request_id"])

	duration, ok := rec["duration_ms"].(float64)
	require.True(t, ok, "duration_ms has to be a number: %#v", rec["duration_ms"])
	assert.GreaterOrEqual(t, duration, float64(delay.Milliseconds()), "the duration has to be at least the handler delay")
	assert.Less(t, duration, 10_000.0, "the duration has to be in milliseconds")
}

func TestRequestLoggerUsesTheErrorLevelOnAFailure(t *testing.T) {
	t.Parallel()

	log, buf := testLogger()
	h := corehttp.RequestLogger(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	records := logRecords(t, buf)
	require.Len(t, records, 1)
	assert.Equal(t, "ERROR", records[0]["level"])
	assert.InDelta(t, float64(http.StatusServiceUnavailable), records[0]["status"], 0)
}

func TestRequestLoggerDoesNotLogSensitiveData(t *testing.T) {
	t.Parallel()

	const (
		bearerToken  = "bearer-very-secret-jwt-value"
		cookieValue  = "session-cookie-secret-value"
		queryToken   = "secret-token-value-in-the-query"
		apiKeyValue  = "sk-live-api-key"
		passwordText = "hunter2-parolasi"
	)

	log, buf := testLogger()
	h := corehttp.RequestLogger(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	target := "/store/v1/products?access_token=" + queryToken +
		"&api_key=" + apiKeyValue +
		"&password=" + passwordText +
		"&limit=20&offset=40"

	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("Cookie", "session="+cookieValue)
	h.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	require.NotEmpty(t, out, "the request has to be logged")

	// PROOF: no sensitive value appears in the real log output.
	for _, secret := range []string{bearerToken, cookieValue, queryToken, apiKeyValue, passwordText} {
		assert.NotContains(t, out, secret, "a sensitive value must not appear in the log output")
	}
	lower := strings.ToLower(out)
	assert.NotContains(t, lower, "authorization", "the Authorization header must never be logged")
	assert.NotContains(t, lower, "cookie", "the Cookie header must never be logged")

	// Non-sensitive query parameters are kept for observability.
	assert.Contains(t, out, "limit=20")
	assert.Contains(t, out, "offset=40")
	assert.Contains(t, out, maskedValue, "masking has to be applied")
}

func TestRequestLoggerKeepsTheEmailOutOfTheAccessLog(t *testing.T) {
	t.Parallel()

	// The auth module deliberately does not log the e-mail; the same value must not
	// leak from the HTTP layer's access log either. The filter is not made up:
	// GET /admin/v1/users reads the "email" query parameter.
	const (
		localPart = "customer.secret"
		domain    = "example-store.test"
	)

	log, buf := testLogger()
	h := corehttp.RequestLogger(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	query := url.Values{"email": {localPart + "@" + domain}, "limit": {"20"}}
	h.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/admin/v1/users?"+query.Encode(), http.NoBody))

	// The e-mail is searched for piece by piece: because "@" turns into "%40" under
	// percent encoding, looking for the full string would produce a test that passes
	// even when masking never ran at all.
	out := buf.String()
	assert.NotContains(t, out, localPart, "the local part of the e-mail must not land in the access log")
	assert.NotContains(t, out, domain, "the domain of the e-mail must not land in the access log")

	records := logRecords(t, buf)
	require.Len(t, records, 1)
	assert.Equal(t, "email="+maskedValue+"&limit=20", records[0]["query"],
		"the e-mail has to be masked, the pagination has to stay as it is")
}

// accessLogQuery passes a request through with the given raw query and returns
// the "query" field of the access record.
func accessLogQuery(t *testing.T, rawQuery string) string {
	t.Helper()

	log, buf := testLogger()
	h := corehttp.RequestLogger(log)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/users", http.NoBody)
	req.URL.RawQuery = rawQuery
	h.ServeHTTP(httptest.NewRecorder(), req)

	records := logRecords(t, buf)
	require.Len(t, records, 1, "exactly one access record is expected")
	query, ok := records[0]["query"].(string)
	require.True(t, ok, "the query field has to be a string: %#v", records[0]["query"])
	return query
}

func TestRequestLoggerMasksSensitiveQueryKeys(t *testing.T) {
	t.Parallel()

	const secretValue = "a-value-that-must-not-leak"

	tests := map[string]struct {
		key    string
		masked bool
	}{
		// Personal data.
		"an e-mail":                {key: "email", masked: true},
		"an upper-cased e-mail":    {key: "EMail", masked: true},
		"a compound e-mail filter": {key: "customer_email", masked: true},
		"a phone":                  {key: "phone", masked: true},
		"a Turkish phone":          {key: "telefon", masked: true},
		"a short phone":            {key: "tel", masked: true},
		"a national id number":     {key: "tckn", masked: true},
		"a card verification code": {key: "card_cvv", masked: true},
		// Credentials and tokens.
		"a password":              {key: "password", masked: true},
		"an upper-cased password": {key: "PASSWORD", masked: true},
		"a token":                 {key: "access_token", masked: true},
		"an api key":              {key: "api_key", masked: true},
		"a signature":             {key: "X-Signature", masked: true},
		// Deny-list: unrecognized, innocent names pass through for observability.
		"pagination":      {key: "limit", masked: false},
		"sorting":         {key: "sort", masked: false},
		"a status filter": {key: "status", masked: false},
		"an unknown key":  {key: "renk", masked: false},
		// "pin" appears inside "shipping": because short names are not searched for as
		// substrings, the shipping filter has to stay readable.
		"a shipping filter": {key: "shipping", masked: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			query := accessLogQuery(t, url.Values{tc.key: {secretValue}}.Encode())

			if tc.masked {
				assert.NotContains(t, query, secretValue, "the value of a sensitive key must not be logged")
				assert.Contains(t, query, maskedValue, "the value has to be replaced with the mask mark")
				return
			}
			assert.Contains(t, query, secretValue,
				"deny-list: an unrecognized key has to stay as it is in the access log")
		})
	}
}

func TestRequestLoggerMasksAMalformedQueryEntirely(t *testing.T) {
	t.Parallel()

	log, buf := testLogger()
	h := corehttp.RequestLogger(log)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.URL.RawQuery = "invalid=%zz&password=plain-value"
	h.ServeHTTP(httptest.NewRecorder(), req)

	records := logRecords(t, buf)
	require.Len(t, records, 1)
	assert.Equal(t, maskedValue, records[0]["query"], "an unparseable query has to be masked entirely")
	assert.NotContains(t, buf.String(), "plain-value")
}

func TestRequestLoggerWritesAnAccessRecordOnAPanicToo(t *testing.T) {
	t.Parallel()

	t.Run("when a panic passes through the chain", func(t *testing.T) {
		t.Parallel()

		log, buf := testLogger()
		h := corehttp.RequestLogger(log)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			panic("the handler blew up")
		}))

		assert.Panics(t, func() {
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/patlayan", http.NoBody))
		}, "RequestLogger must not swallow the panic")

		records := logRecords(t, buf)
		require.Len(t, records, 1, "a panicking request has to show up in the access log too")
		assert.Equal(t, "request completed", records[0]["msg"])
		assert.Equal(t, "/patlayan", records[0]["path"])
		assert.InDelta(t, float64(http.StatusInternalServerError), records[0]["status"], 0,
			"if the response never started, status 500 is recorded")
	})

	t.Run("when it is left with ErrAbortHandler", func(t *testing.T) {
		t.Parallel()

		log, buf := testLogger()
		h := corehttp.RequestLogger(log)(corehttp.Recoverer(log)(
			http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
				panic(http.ErrAbortHandler)
			})))

		assert.Panics(t, func() {
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/abort", http.NoBody))
		}, "the abort contract has to be re-thrown")

		records := logRecords(t, buf)
		require.Len(t, records, 1, "an aborted request must not drop out of the access log")
		assert.Equal(t, "request completed", records[0]["msg"])
		assert.Equal(t, "/abort", records[0]["path"])
	})
}

// deadlineWriter is a fake writer providing only SetWriteDeadline.
// Because the wrapper does not define this method, http.ResponseController can
// only reach it by walking the Unwrap chain.
type deadlineWriter struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

// SetWriteDeadline records the requested time.
func (d *deadlineWriter) SetWriteDeadline(t time.Time) error {
	d.deadline = t
	return nil
}

func TestResponseWriterUnwrapOpensTheResponseControllerPath(t *testing.T) {
	t.Parallel()

	log, _ := testLogger()
	wanted := time.Now().Add(42 * time.Second)

	var err error
	// Two wrapper layers: it has to walk to the end of the chain.
	h := corehttp.RequestLogger(log)(corehttp.Recoverer(log)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			err = http.NewResponseController(w).SetWriteDeadline(wanted)
		})))

	under := &deadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(under, httptest.NewRequest(http.MethodGet, "/long-response", http.NoBody))

	require.NoError(t, err, "without Unwrap ResponseController cannot find the underlying writer")
	assert.Equal(t, wanted, under.deadline, "the deadline has to reach the underlying writer")
}

func TestResponseWriterSwallowsASecondWriteHeader(t *testing.T) {
	t.Parallel()

	log, buf := testLogger()
	h := corehttp.RequestLogger(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.WriteHeader(http.StatusInternalServerError)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/urunler", http.NoBody))

	assert.Equal(t, http.StatusCreated, rec.Code, "the sent status must not change")

	records := logRecords(t, buf)
	require.Len(t, records, 1)
	assert.InDelta(t, float64(http.StatusCreated), records[0]["status"], 0,
		"the logged status has to reflect the first WriteHeader call")
}

func TestResponseWriterCountsFlushAsStartingTheResponse(t *testing.T) {
	t.Parallel()

	log, _ := testLogger()
	var hasFlusher bool
	h := corehttp.Recoverer(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f, ok := w.(http.Flusher)
		hasFlusher = ok
		if ok {
			f.Flush()
		}
		panic("broke off mid-stream")
	}))

	rec := httptest.NewRecorder()
	require.NotPanics(t, func() {
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/olaylar", http.NoBody))
	})

	assert.True(t, hasFlusher, "the wrapper must not hide http.Flusher")
	assert.True(t, rec.Flushed, "Flush alttaki writer'a iletilmeli")
	assert.Empty(t, rec.Body.String(), "Flush starts the response; no error body must be written on top")
}

// hijackWriter is a fake writer providing Hijack.
type hijackWriter struct {
	*httptest.ResponseRecorder
	cagrildi bool
}

// Hijack records having been called; it does not produce a real connection.
func (h *hijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.cagrildi = true
	return nil, bufio.NewReadWriter(bufio.NewReader(strings.NewReader("")), bufio.NewWriter(io.Discard)), nil
}

func TestResponseWriterDoesNotHideHijack(t *testing.T) {
	t.Parallel()

	log, _ := testLogger()
	var (
		hijacker bool
		err      error
	)

	// If it panics after the connection is taken over, the Recoverer must not write a body.
	h := corehttp.RequestLogger(log)(corehttp.Recoverer(log)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hj, ok := w.(http.Hijacker)
			hijacker = ok
			if !ok {
				return
			}
			_, _, err = hj.Hijack()
			panic("blew up after the upgrade")
		})))

	under := &hijackWriter{ResponseRecorder: httptest.NewRecorder()}
	require.NotPanics(t, func() {
		h.ServeHTTP(under, httptest.NewRequest(http.MethodGet, "/ws", http.NoBody))
	})

	assert.True(t, hijacker, "the wrapper must not hide http.Hijacker")
	assert.NoError(t, err)
	assert.True(t, under.cagrildi, "Hijack alttaki writer'a iletilmeli")
	assert.Empty(t, under.Body.String(), "no error body must be written to a taken-over connection")
}

func TestResponseWriterReturnsAnErrorWhenHijackIsUnsupported(t *testing.T) {
	t.Parallel()

	log, _ := testLogger()
	var err error
	h := corehttp.RequestLogger(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		require.True(t, ok)
		_, _, err = hj.Hijack()
	}))

	// httptest.ResponseRecorder Hijack desteklemez.
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ws", http.NoBody))

	require.Error(t, err)
	assert.ErrorIs(t, err, http.ErrNotSupported, "an unsupported hijack has to be reported openly")
}
