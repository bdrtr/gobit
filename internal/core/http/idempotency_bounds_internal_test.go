package http

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/config"
)

// boundsResponse builds a completed response whose charged size is known.
func boundsResponse(bodyBytes int) IdempotentResponse {
	return IdempotentResponse{
		Status:      200,
		Header:      http.Header{"Content-Type": {"application/json"}},
		Body:        bytes.Repeat([]byte("a"), bodyBytes),
		Fingerprint: "0123456789abcdef0123456789abcdef",
	}
}

// boundsWrite reserves a key and completes it, failing the test on error.
func boundsWrite(t *testing.T, ctx context.Context, s *MemoryIdempotencyStore, key string, bodyBytes int) {
	t.Helper()

	_, _, err := s.Begin(ctx, key, "0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	require.NoError(t, s.Complete(ctx, key, boundsResponse(bodyBytes)))
}

// boundsReplays reports whether the key still has a replayable record.
func boundsReplays(t *testing.T, ctx context.Context, s *MemoryIdempotencyStore, key string) bool {
	t.Helper()

	resp, ok, err := s.Begin(ctx, key, "0123456789abcdef0123456789abcdef")
	require.NoError(t, err)

	if ok {
		require.NotNil(t, resp)
	}

	return ok
}

// TestMemoryStoreDropsOldestRecordWhenBudgetFills pins the eviction rule.
//
// The rule is a trade, not a detail: the dropped key's retry is processed a
// second time. A test that only checked "memory stays bounded" would pass just
// as well if the NEWEST record were dropped, and that choice would break the
// guarantee for the request that is most likely to be retried right now.
func TestMemoryStoreDropsOldestRecordWhenBudgetFills(t *testing.T) {
	ctx := context.Background()
	// The keys are the SAME LENGTH on purpose: they are charged by length, so
	// keys of different lengths would leave the budget a byte short of full and
	// the boundary below would not be a boundary at all.
	sample := boundsResponse(1024)
	charge := entryCharge("key-a", &sample)
	store := NewMemoryIdempotencyStore(time.Hour, 2*charge)

	boundsWrite(t, ctx, store, "key-a", 1024)
	boundsWrite(t, ctx, store, "key-b", 1024)

	require.Equal(t, store.budget, store.charge, "the two records must fill the budget EXACTLY")

	// Nothing may be dropped while the budget is only reached, not exceeded.
	assert.True(t, boundsReplays(t, ctx, store, "key-a"), "a full but not overflowing budget must drop nothing")
	assert.True(t, boundsReplays(t, ctx, store, "key-b"))

	boundsWrite(t, ctx, store, "key-c", 1024)

	assert.False(t, boundsReplays(t, ctx, store, "key-a"), "the OLDEST record must be the one dropped")
	assert.True(t, boundsReplays(t, ctx, store, "key-b"), "a younger record must survive")
	assert.True(t, boundsReplays(t, ctx, store, "key-c"), "the record just written must survive")
	assert.LessOrEqual(t, store.charge, store.budget, "charge must stay within the budget after eviction")
}

// TestMemoryStoreKeepsChargeAndQueueInStep proves the three structures that
// hold a record (map, queue, charge) are never left disagreeing.
//
// They are separate structures with separate updates, so a record can leak from
// one and not the others; a leak in the queue is invisible in behavior until
// the day expiry walks it, and a leak in the charge shrinks the budget forever.
func TestMemoryStoreKeepsChargeAndQueueInStep(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryIdempotencyStore(time.Hour, 64<<20)

	for i := range 200 {
		boundsWrite(t, ctx, store, "key-"+strconv.Itoa(i), i*16)
	}

	// Rewriting keys must not double-charge; overwriting is the path a retried
	// handler takes when the first attempt was recorded.
	for i := range 50 {
		require.NoError(t, store.Complete(ctx, "key-"+strconv.Itoa(i), boundsResponse(32)))
	}

	var want int64
	for _, g := range store.entry {
		want += g.charge
	}

	assert.Equal(t, want, store.charge, "the store's charge must equal the sum of its records")
	assert.Equal(t, len(store.entry), store.queue.Len(), "every record must sit in the queue exactly once")
	assert.Equal(t, 200, len(store.entry))
}

// TestMemoryStoreDoesNotEvictInFlightReservations pins the one entry eviction
// must not touch.
//
// Dropping a reservation would let a concurrent second request with the same
// key through, which is precisely the double execution the whole middleware
// exists to prevent — and it would not even free memory, because reservations
// carry no charge.
func TestMemoryStoreDoesNotEvictInFlightReservations(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryIdempotencyStore(time.Hour, 4096)

	_, ok, err := store.Begin(ctx, "in-flight", "0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	require.False(t, ok)

	// Overflow the budget many times over while the reservation is the oldest
	// entry in the queue.
	for i := range 20 {
		boundsWrite(t, ctx, store, "key-"+strconv.Itoa(i), 2048)
	}

	_, _, err = store.Begin(ctx, "in-flight", "0123456789abcdef0123456789abcdef")
	assert.ErrorIs(t, err, ErrIdempotencyKeyInFlight, "an in-flight reservation must survive eviction")
	assert.LessOrEqual(t, store.charge, store.budget)
}

// TestMemoryStoreExpiryStopsAtFirstLiveRecord pins the prefix walk.
//
// The walk is what made expiry cost independent of the map size (measured: a
// full-map sweep took 50.3 ms at a million records while holding the process's
// only idempotency lock). It is correct only because the queue is ordered by
// expiry, so the test checks the record BEHIND the first live one is still
// there — a walk that stopped one entry too early, or a full scan with the
// wrong predicate, would show up here.
func TestMemoryStoreExpiryStopsAtFirstLiveRecord(t *testing.T) {
	ctx := context.Background()
	clock := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	store := NewMemoryIdempotencyStore(10*time.Minute, 64<<20)
	store.now = func() time.Time { return clock }

	boundsWrite(t, ctx, store, "old", 128)
	clock = clock.Add(6 * time.Minute)
	boundsWrite(t, ctx, store, "young", 128)

	// 11 minutes after the first write: "old" is past its TTL, "young" is not.
	clock = clock.Add(5 * time.Minute)

	assert.False(t, boundsReplays(t, ctx, store, "old"), "a record past its TTL must not replay")
	assert.True(t, boundsReplays(t, ctx, store, "young"), "a record inside its TTL must still replay")
}

// TestMemoryStoreExpiresWithoutWaitingForASweepInterval pins that expiry is not
// throttled.
//
// The full-map sweep it replaced ran at most once a minute, so a record could
// keep replaying for up to a minute past the TTL the operator configured — a
// protection LONGER than the one documented. The clock here moves less than
// that old interval, so restoring the throttle fails this test.
func TestMemoryStoreExpiresWithoutWaitingForASweepInterval(t *testing.T) {
	ctx := context.Background()
	clock := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	store := NewMemoryIdempotencyStore(10*time.Second, 64<<20)
	store.now = func() time.Time { return clock }

	boundsWrite(t, ctx, store, "key", 128)
	clock = clock.Add(11 * time.Second)

	assert.False(t, boundsReplays(t, ctx, store, "key"))
}

// TestMemoryStoreReleasesEverythingWhenRecordsExpire pins that expiry frees the
// budget as well as the map.
//
// A record removed from the map but left in the charge would shrink the usable
// budget on every sweep until the store evicted live records for memory it was
// no longer holding.
func TestMemoryStoreReleasesEverythingWhenRecordsExpire(t *testing.T) {
	ctx := context.Background()
	clock := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	store := NewMemoryIdempotencyStore(time.Minute, 64<<20)
	store.now = func() time.Time { return clock }

	for i := range 100 {
		boundsWrite(t, ctx, store, "key-"+strconv.Itoa(i), 512)
	}

	require.Positive(t, store.charge)

	clock = clock.Add(2 * time.Minute)
	require.False(t, boundsReplays(t, ctx, store, "probe"))

	assert.Zero(t, store.charge, "expired records must give their charge back")
	assert.Equal(t, 1, len(store.entry), "only the probe's own reservation may remain")
	assert.Equal(t, 1, store.queue.Len())
}

// TestMemoryStoreReordersARewrittenRecord pins that rewriting a record moves it
// to the BACK of the expiry queue.
//
// Rewriting extends the record's life, so leaving it in place would break the
// ordering the prefix walk depends on: expiry would stop at the rewritten
// record and every EXPIRED record behind it would keep replaying — a protection
// quietly longer than the TTL the operator configured.
func TestMemoryStoreReordersARewrittenRecord(t *testing.T) {
	ctx := context.Background()
	clock := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	store := NewMemoryIdempotencyStore(10*time.Minute, 64<<20)
	store.now = func() time.Time { return clock }

	boundsWrite(t, ctx, store, "rewritten", 128)
	clock = clock.Add(time.Minute)
	boundsWrite(t, ctx, store, "behind", 128)

	// The rewrite lands after "behind" in time, so it must land after it in the
	// queue as well.
	clock = clock.Add(time.Minute)
	require.NoError(t, store.Complete(ctx, "rewritten", boundsResponse(128)))

	// 11.5 minutes in: "behind" is past its TTL, the rewritten record is not.
	clock = clock.Add(9*time.Minute + 30*time.Second)

	assert.False(t, boundsReplays(t, ctx, store, "behind"),
		"a record behind a rewritten one must still expire on time")
	assert.True(t, boundsReplays(t, ctx, store, "rewritten"),
		"rewriting a record must extend its life")
}

// TestMemoryStoreReplayHandsOutIsolatedCopies pins that a caller cannot write
// through a replayed record into the store.
//
// The copy is taken OUTSIDE the lock now, which is safe only while a published
// record is never mutated in place. A caller holding the stored record itself
// would break that rule from the other end: the next replay would serve
// whatever the previous caller did to it.
func TestMemoryStoreReplayHandsOutIsolatedCopies(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryIdempotencyStore(time.Hour, 64<<20)
	boundsWrite(t, ctx, store, "key", 8)

	first, ok, err := store.Begin(ctx, "key", "0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	require.True(t, ok)

	first.Status = 500
	first.Body[0] = 'Z'
	first.Header.Set("Content-Type", "text/plain")

	second, ok, err := store.Begin(ctx, "key", "0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	require.True(t, ok)

	assert.Equal(t, 200, second.Status)
	assert.Equal(t, byte('a'), second.Body[0], "the stored body must not be writable by a caller")
	assert.Equal(t, "application/json", second.Header.Get("Content-Type"))
}

// TestMemoryStoreAbortLeavesACompletedRecordAlone pins the other half of
// Abort's rule, which had no test: only an UNFINISHED reservation is removed.
//
// Dropping a completed record here would undo the whole guarantee. Abort is the
// failure path — a handler that returned an error, a panic, a canceled
// request — and it can reach a key whose response was already recorded (a
// completed handler whose write failed afterwards, a retry landing while the
// first request unwinds). Deleting the record would let the next retry run the
// handler a SECOND time: one more order, one more charge, the exact duplicate
// this middleware exists to stop.
//
// Verified by mutation: dropping the "g.resp == nil" condition passes every
// other test in the package.
func TestMemoryStoreAbortLeavesACompletedRecordAlone(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryIdempotencyStore(time.Hour, 64<<20)

	boundsWrite(t, ctx, store, "key", 128)
	require.NoError(t, store.Abort(ctx, "key"))

	assert.True(t, boundsReplays(t, ctx, store, "key"),
		"a completed record must survive an abort; deleting it would let the retry run the handler again")
	assert.Equal(t, 1, store.queue.Len(), "the record must stay in the expiry queue too")
	assert.Positive(t, store.charge, "the record's charge must stay on the budget")
}

// TestMemoryStoreAbortReleasesQueueSlot pins that an aborted reservation leaves
// nothing behind in the queue.
func TestMemoryStoreAbortReleasesQueueSlot(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryIdempotencyStore(time.Hour, 64<<20)

	_, _, err := store.Begin(ctx, "key", "0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	require.NoError(t, store.Abort(ctx, "key"))

	assert.Empty(t, store.entry)
	assert.Equal(t, 0, store.queue.Len(), "an aborted reservation must leave the queue too")

	_, ok, err := store.Begin(ctx, "key", "0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	assert.False(t, ok, "the key must be reusable after an abort")
}

// TestMemoryStoreWarnsOnFirstEvictionThenThrottles pins the operator's only
// live signal that the cap is biting.
//
// Both halves matter. Without the first warning the cap is silent, which this
// repository forbids; without the throttle a permanently full store writes one
// WARN per mutating request and the signal drowns in its own noise.
func TestMemoryStoreWarnsOnFirstEvictionThenThrottles(t *testing.T) {
	var buf bytes.Buffer

	ctx := WithLogger(context.Background(),
		slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	clock := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	store := NewMemoryIdempotencyStore(time.Hour, 4096)
	store.now = func() time.Time { return clock }

	for i := range 10 {
		boundsWrite(t, ctx, store, "key-"+strconv.Itoa(i), 1024)
		clock = clock.Add(time.Second)
	}

	first := buf.String()
	require.Equal(t, 1, strings.Count(first, "budget_bytes"),
		"eviction must warn once and then stay quiet inside the throttle window")
	assert.Contains(t, first, "\"budget_bytes\":4096")
	assert.Contains(t, first, "dropped_total")
	assert.Contains(t, first, "IDEMPOTENCY_MAX_MEMORY_BYTES")

	// Past the throttle window the warning returns, and it reports every record
	// dropped while it was silent.
	clock = clock.Add(evictionLogInterval)
	boundsWrite(t, ctx, store, "later", 1024)

	second := strings.TrimPrefix(buf.String(), first)
	require.Equal(t, 1, strings.Count(second, "budget_bytes"))

	var line struct {
		SinceLastWarning int `json:"dropped_since_last_warning"`
		Total            int `json:"dropped_total"`
	}

	require.NoError(t, json.Unmarshal([]byte(second), &line))
	assert.Greater(t, line.SinceLastWarning, 1,
		"the second warning must account for every record dropped while it was throttled")
	assert.Greater(t, line.Total, line.SinceLastWarning,
		"the running total must include the drops reported by the first warning")
}

// TestMemoryStoreReplayRacesWithARewrite is the guard for the one invariant the
// out-of-lock copy rests on.
//
// Begin hands the caller the STORED record and copies it after the mutex is
// released; that is only safe because a published record is never written
// again — Complete swaps in a new pointer rather than updating the struct in
// place. The invariant is stated in the reserve godoc, but a sentence is not a
// guard: an in-place update compiles, passes every functional test in this
// package, and corrupts a response a concurrent replay is in the middle of
// copying.
//
// This test only fails under -race, which is why it exists here rather than as
// an assertion: `make test` and CI both run -race, and this is the shape of bug
// that never reproduces in a single-threaded run.
func TestMemoryStoreReplayRacesWithARewrite(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryIdempotencyStore(time.Hour, 0)
	boundsWrite(t, ctx, s, "shared", 4096)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				resp, ok, err := s.Begin(ctx, "shared", "0123456789abcdef0123456789abcdef")
				if err != nil || !ok {
					continue
				}
				// Read the whole copy: a corrupted body must be touched, not
				// just received.
				_ = len(resp.Body) + len(resp.Header)
			}
		}()
	}

	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				require.NoError(t, s.Complete(ctx, "shared", boundsResponse(4096)))
			}
		}()
	}

	wg.Wait()
}

// TestChargeCountsEveryPartOfARecord pins the accounting formula.
//
// Every term is a term because a record really holds it; a missing term is a
// budget that is quietly larger than the number the operator set. The header
// terms are pinned per GROUP rather than per header because that is what was
// measured: header nine, not header two, is what costs the next allocation.
func TestChargeCountsEveryPartOfARecord(t *testing.T) {
	base := entryCharge("k", &IdempotentResponse{})
	assert.Positive(t, base, "an empty record still occupies memory and must still be charged")

	longerKey := entryCharge("kk", &IdempotentResponse{})
	assert.Equal(t, base+1, longerKey, "the key's own bytes must be charged")

	withFingerprint := entryCharge("k", &IdempotentResponse{Fingerprint: "abcd"})
	assert.Equal(t, base+4, withFingerprint)

	withBody := entryCharge("k", &IdempotentResponse{Body: bytes.Repeat([]byte("a"), 5000)})
	assert.Equal(t, base+5000, withBody)

	oneHeader := entryCharge("k", &IdempotentResponse{Header: http.Header{"Ab": {"c"}}})
	assert.Greater(t, oneHeader-base, headerGroupCharge,
		"a header map costs its group allocation PLUS the strings in it")

	longerName := entryCharge("k", &IdempotentResponse{Header: http.Header{"Abcdefgh": {"c"}}})
	assert.Equal(t, oneHeader+6, longerName, "a header NAME's bytes must be charged")

	longerValue := entryCharge("k", &IdempotentResponse{Header: http.Header{"Ab": {"cdefg"}}})
	assert.Equal(t, oneHeader+4, longerValue, "a header VALUE's bytes must be charged")

	twoValues := entryCharge("k", &IdempotentResponse{Header: http.Header{"Ab": {"c", "d"}}})
	assert.Greater(t, twoValues, oneHeader+1,
		"a second value costs its own slot as well as its byte")

	full := http.Header{}
	for i := range headerGroupSize {
		full["Ab"+strconv.Itoa(i)] = []string{"c"}
	}

	fullGroup := entryCharge("k", &IdempotentResponse{Header: full})
	secondHeader := entryCharge("k", &IdempotentResponse{
		Header: http.Header{"Ab0": {"c"}, "Ab1": {"c"}},
	}) - entryCharge("k", &IdempotentResponse{Header: http.Header{"Ab0": {"c"}}})

	full["Ab8"] = []string{"c"}
	ninthHeader := entryCharge("k", &IdempotentResponse{Header: full}) - fullGroup

	assert.Equal(t, secondHeader+headerGroupCharge, ninthHeader,
		"the ninth header must open a second group; the second must not")
}

// TestBudgetDefaultAgreesWithConfiguration keeps the two declarations of the
// same number from drifting.
//
// The store's default is what a caller that passes 0 gets (tests, embedders);
// the config default is what a deployment gets. If they drift, the number in
// this package's godoc describes neither.
func TestBudgetDefaultAgreesWithConfiguration(t *testing.T) {
	field, ok := reflect.TypeFor[config.Config]().FieldByName("IdempotencyMaxMemoryBytes")
	require.True(t, ok, "config must still carry the budget field")

	declared, err := strconv.ParseInt(field.Tag.Get("envDefault"), 10, 64)
	require.NoError(t, err)

	assert.Equal(t, defaultIdempotencyBudget, declared,
		"IDEMPOTENCY_MAX_MEMORY_BYTES's default must match the store's own default")
	// The floor is not compared to a number but EXERCISED, because the number
	// on its own says nothing: a budget equal to the largest buffered body is
	// one byte short of holding a record OF that body — the key, the
	// fingerprint, the headers and the structural cost ride along with it, and
	// the record is evicted the instant it is written. That configuration is
	// exactly what the floor exists to reject, and asserting equality was how
	// the floor came to permit it (measured: at 1 MiB the record does not
	// replay).
	require.Greater(t, config.MinIdempotencyMemoryBytes, int64(maxIdempotentBodyBytes),
		"a budget that cannot hold one maximum-size record is the silently broken configuration the floor rejects")

	ctx := context.Background()
	floor := NewMemoryIdempotencyStore(0, config.MinIdempotencyMemoryBytes)
	boundsWrite(t, ctx, floor, strings.Repeat("k", maxIdempotencyKeyLen), maxIdempotentBodyBytes)

	assert.True(t, boundsReplays(t, ctx, floor, strings.Repeat("k", maxIdempotencyKeyLen)),
		"at the smallest accepted budget a maximum-size response must still be replayable")
}
