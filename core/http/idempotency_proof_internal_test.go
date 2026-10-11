package http

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// The store ring keyed on the request's proof (ADR 0439). Every shopper shares
// the publishable key's principal, and an order's id in the path is not the
// caller's access: a record has to be replayed only to a caller presenting the
// same proof, and a caller presenting another must neither read nor occupy it.

// proofHeader is where these tests' proof function reads a request's proof.
const proofHeader = "X-Test-Proof"

// proofCountingHandler answers 201 with a body naming the call.
type proofCountingHandler struct {
	mu    sync.Mutex
	calls int
}

func (h *proofCountingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.calls++
	n := h.calls
	h.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
	_, _ = fmt.Fprintf(w, `{"data":{"call":%d,"proof":%q}}`, n, r.Header.Get(proofHeader))
}

// provenRing is the store ring with a proof function reading proofHeader.
func provenRing(next http.Handler) http.Handler {
	return idempotency(NewMemoryIdempotencyStore(time.Hour, 0), func(r *http.Request) (string, error) {
		return r.Header.Get(proofHeader), nil
	})(next)
}

// storePost is a keyed storefront POST under the shared publishable key,
// presenting proof when it is not empty.
func storePost(key, proof, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/store/v1/orders/order_1/returns", strings.NewReader(body))
	r.Header.Set(IdempotencyKeyHeader, key)
	if proof != "" {
		r.Header.Set(proofHeader, proof)
	}

	return r.WithContext(WithPrincipal(r.Context(), Principal{ID: "pk_shop", Kind: "api_key"}))
}

// TestTwoCallersWithDifferentProofsNeverShareARecord is the gate: the owner's
// record is not replayed to a caller presenting another proof or none, and a
// stranger writing first under the owner's key does not occupy it.
func TestTwoCallersWithDifferentProofsNeverShareARecord(t *testing.T) {
	t.Parallel()

	const body = `{"lines":[{"order_line_item_id":"oli_1","quantity":1}],"reason":"private"}`
	h := &proofCountingHandler{}
	ring := provenRing(h)

	owner := httptest.NewRecorder()
	ring.ServeHTTP(owner, storePost("return-order_1", "token-of-the-owner", body))
	require.Equal(t, http.StatusCreated, owner.Code)

	for name, proof := range map[string]string{"no proof": "", "another proof": "token-of-a-stranger"} {
		stranger := httptest.NewRecorder()
		ring.ServeHTTP(stranger, storePost("return-order_1", proof, body))
		assert.Empty(t, stranger.Header().Get(IdempotencyReplayedHeader), "%s: the owner's record was replayed", name)
		assert.NotEqual(t, owner.Body.String(), stranger.Body.String(), "%s read the owner's response", name)
	}

	again := httptest.NewRecorder()
	ring.ServeHTTP(again, storePost("return-order_1", "token-of-the-owner", body))
	assert.Equal(t, "true", again.Header().Get(IdempotencyReplayedHeader), "the owner's own retry is replayed")
	assert.Equal(t, owner.Body.String(), again.Body.String())

	// The stranger writes first under the key the owner will use, with another
	// body; the owner is served, not answered the stranger's record or a 409.
	first := httptest.NewRecorder()
	ring.ServeHTTP(first, storePost("return2-order_1", "", `{"lines":[]}`))
	require.Equal(t, http.StatusCreated, first.Code)
	owners := httptest.NewRecorder()
	ring.ServeHTTP(owners, storePost("return2-order_1", "token-of-the-owner", body))
	assert.Equal(t, http.StatusCreated, owners.Code, "a stranger's record occupied the owner's key: %s", owners.Body.String())
	assert.Empty(t, owners.Header().Get(IdempotencyReplayedHeader))
	assert.Contains(t, owners.Body.String(), "token-of-the-owner")
}

// TestAProofThatCannotBeReadRefusesTheRequest: a write whose proof cannot be
// read is refused with the proof's own error and runs nothing. Served without
// a record, as the first amendment had it, a retry during an outage ran it
// twice; refused, the retry under the same key once the proof reads is the
// request's first run, and its own retry is replayed.
func TestAProofThatCannotBeReadRefusesTheRequest(t *testing.T) {
	t.Parallel()

	h := &proofCountingHandler{}
	ring := idempotency(NewMemoryIdempotencyStore(time.Hour, 0), func(r *http.Request) (string, error) {
		if r.Header.Get(proofHeader) == "unreadable" {
			return "", coreerrors.Unavailable("session_store_down", "the session store is down")
		}

		return r.Header.Get(proofHeader), nil
	})(h)

	for range 2 {
		rec := httptest.NewRecorder()
		ring.ServeHTTP(rec, storePost("k-outage", "unreadable", `{}`))
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "session_store_down")
	}
	assert.Zero(t, h.calls, "a write whose proof could not be read ran")

	first := httptest.NewRecorder()
	ring.ServeHTTP(first, storePost("k-outage", "token-of-the-owner", `{}`))
	assert.Equal(t, http.StatusCreated, first.Code)
	assert.Empty(t, first.Header().Get(IdempotencyReplayedHeader), "the refusal was recorded")
	retry := httptest.NewRecorder()
	ring.ServeHTTP(retry, storePost("k-outage", "token-of-the-owner", `{}`))
	assert.Equal(t, "true", retry.Header().Get(IdempotencyReplayedHeader))
	assert.Equal(t, 1, h.calls)
}

// TestAProofEntersTheStoreKeyAsADigest: the proof may be a credential, and the
// store key lives as long as the record.
func TestAProofEntersTheStoreKeyAsADigest(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "api_key:pk_shop", provenBucket("api_key:pk_shop", ""), "nothing proven keeps the bucket")
	narrowed := provenBucket("api_key:pk_shop", "ordtok_secret")
	assert.NotContains(t, narrowed, "ordtok_secret")
	assert.NotEqual(t, narrowed, provenBucket("api_key:pk_shop", "ordtok_other"))
}
