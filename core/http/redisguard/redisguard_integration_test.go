//go:build integration

// This file needs a real Redis and only builds with `-tags=integration`
// (`make test-integration`), so `make test` stays fast and Docker-free.
package redisguard_test

import (
	"net/http"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/http/redisguard"
)

// redisImage is the Redis image the integration tests run against.
const redisImage = "redis:7-alpine"

// startRedis starts a Redis that lives for the test and returns its address.
func startRedis(t *testing.T) string {
	t.Helper()

	ctx := t.Context()
	container, err := tcredis.Run(ctx, redisImage)
	testcontainers.CleanupContainer(t, container)
	require.NoError(t, err, "the redis container could not be started")

	uri, err := container.ConnectionString(ctx)
	require.NoError(t, err, "the connection string could not be read")

	return uri
}

// openClient opens a NEW Redis client on the given connection string.
//
// A separate client stands in for a separate PROCESS: only a second connection
// can prove the counter and the record really are shared in Redis rather than
// sitting in one client's memory. That was exactly the failure of the in-memory
// implementations this package exists to fix.
func openClient(t *testing.T, uri string) *redis.Client {
	t.Helper()

	opts, err := redis.ParseURL(uri)
	require.NoError(t, err, "the connection string could not be parsed")

	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Ping(t.Context()).Err(), "redis could not be pinged")

	return client
}

// redisClient starts a Redis and returns its client, for tests needing only one.
func redisClient(t *testing.T) *redis.Client {
	t.Helper()
	return openClient(t, startRedis(t))
}

// defaultPrefix is the namespace prefix most of these tests use.
//
// Its value matches config.DefaultRedisKeyPrefix; because the namespace itself
// is not the subject of these tests, the prefix a real installation uses is the
// one picked.
const defaultPrefix = "gobit"

// Two DIFFERENT prefixes, used by the tests that exercise the namespace split.
//
// Both write to the SAME Redis instance and the SAME DB; the only thing telling
// them apart is the prefix. Using a separate DB or instance would make the test
// meaningless: what is being proven is precisely "do they separate inside one
// Redis".
const (
	stagingPrefix    = "gobit-staging"
	productionPrefix = "gobit-prod"
)

// --- The rate limiter ---

func TestARequestIsRefusedWhenTheQuotaIsSpent(t *testing.T) {
	const limit = 3

	lim, err := redisguard.NewLimiter(redisClient(t), defaultPrefix, limit, time.Minute)
	require.NoError(t, err)

	for i := range limit {
		d, err := lim.Allow(t.Context(), "client-a")
		require.NoError(t, err)
		assert.True(t, d.Allowed, "request %d is inside the quota and has to pass", i+1)
		assert.Equal(t, limit, d.Limit)
		assert.Equal(t, limit-i-1, d.Remaining, "the remaining allowance has to drop by one per request")
	}

	d, err := lim.Allow(t.Context(), "client-a")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "a request has to be refused once the quota is used up")
	assert.Zero(t, d.Remaining)
	assert.Positive(t, d.RetryAfter, "a refused request has to get a positive wait")
	assert.LessOrEqual(t, d.RetryAfter, time.Minute, "the wait cannot exceed the window")
}

func TestTheQuotaRenewsWhenTheWindowExpires(t *testing.T) {
	const window = time.Second

	lim, err := redisguard.NewLimiter(redisClient(t), defaultPrefix, 1, window)
	require.NoError(t, err)

	ilk, err := lim.Allow(t.Context(), "client-a")
	require.NoError(t, err)
	require.True(t, ilk.Allowed)

	refused, err := lim.Allow(t.Context(), "client-a")
	require.NoError(t, err)
	require.False(t, refused.Allowed, "the second request inside the window has to be refused")

	// A fixed window renews all at once when the counter's TTL expires; the
	// margin keeps the test from waking a hair too early on container latency.
	time.Sleep(window + 500*time.Millisecond)

	renewed, err := lim.Allow(t.Context(), "client-a")
	require.NoError(t, err)
	assert.True(t, renewed.Allowed, "the quota has to renew after the window expires")
	assert.Zero(t, renewed.Remaining, "one allowance has to come off the renewed quota too")
}

func TestDifferentClientsDoNotSpendEachOthersQuota(t *testing.T) {
	lim, err := redisguard.NewLimiter(redisClient(t), defaultPrefix, 1, time.Minute)
	require.NoError(t, err)

	a, err := lim.Allow(t.Context(), "client-a")
	require.NoError(t, err)
	require.True(t, a.Allowed)

	aAgain, err := lim.Allow(t.Context(), "client-a")
	require.NoError(t, err)
	require.False(t, aAgain.Allowed, "client-a spent its quota")

	b, err := lim.Allow(t.Context(), "client-b")
	require.NoError(t, err)
	assert.True(t, b.Allowed, "client-b's quota has to be independent of client-a's")
	assert.Zero(t, b.Remaining)
}

// TestTwoProcessesShareOneQuota pins the reason this package exists.
//
// With the in-memory limiter every instance keeps its own counter, so the real
// limit in a two-instance installation would be DOUBLED. Here two limiters built
// over separate connections share a single quota.
func TestTwoProcessesShareOneQuota(t *testing.T) {
	uri := startRedis(t)

	birinci, err := redisguard.NewLimiter(openClient(t, uri), defaultPrefix, 2, time.Minute)
	require.NoError(t, err)
	ikinci, err := redisguard.NewLimiter(openClient(t, uri), defaultPrefix, 2, time.Minute)
	require.NoError(t, err)

	d1, err := birinci.Allow(t.Context(), "client-a")
	require.NoError(t, err)
	assert.True(t, d1.Allowed)
	assert.Equal(t, 1, d1.Remaining)

	d2, err := ikinci.Allow(t.Context(), "client-a")
	require.NoError(t, err)
	assert.True(t, d2.Allowed)
	assert.Zero(t, d2.Remaining, "the second instance has to see what the FIRST spent")

	d3, err := birinci.Allow(t.Context(), "client-a")
	require.NoError(t, err)
	assert.False(t, d3.Allowed, "the quota must not be multiplied by the instance count")
}

// --- Idempotency deposu ---

func TestBeginReturnsInFlightOnASecondCallWithTheSameKey(t *testing.T) {
	store, err := redisguard.NewIdempotencyStore(redisClient(t), defaultPrefix, time.Hour)
	require.NoError(t, err)

	record, done, err := store.Begin(t.Context(), "tenant-1:key", "print-1")
	require.NoError(t, err)
	assert.Nil(t, record, "a new key has to carry no record")
	assert.False(t, done)

	_, _, err = store.Begin(t.Context(), "tenant-1:key", "print-1")
	assert.ErrorIs(t, err, corehttp.ErrIdempotencyKeyInFlight,
		"a key in flight must not be reserved a second time")
}

func TestBeginReturnsTheRecordAfterComplete(t *testing.T) {
	store, err := redisguard.NewIdempotencyStore(redisClient(t), defaultPrefix, time.Hour)
	require.NoError(t, err)

	const key = "tenant-1:key"

	_, done, err := store.Begin(t.Context(), key, "izi-1")
	require.NoError(t, err)
	require.False(t, done)

	response := corehttp.IdempotentResponse{
		Status: http.StatusCreated,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"Location":     []string{"/store/v1/orders/order_01"},
		},
		Body:        []byte(`{"id":"order_01"}`),
		Fingerprint: "izi-1",
	}
	require.NoError(t, store.Complete(t.Context(), key, response))

	record, done, err := store.Begin(t.Context(), key, "izi-1")
	require.NoError(t, err)
	require.True(t, done, "a finished key has to return its record")
	require.NotNil(t, record)
	assert.Equal(t, response.Status, record.Status)
	assert.Equal(t, response.Header, record.Header, "the headers have to be stored as they are")
	assert.Equal(t, response.Body, record.Body)
	assert.Equal(t, response.Fingerprint, record.Fingerprint,
		"the fingerprint has to be stored; the middleware catches a key used by a "+
			"different request by looking at it")
}

// TestABinaryBodySurvivesIntact verifies that the body is not assumed to be text.
//
// The record is turned into JSON; were the body a string field, encoding/json
// would silently replace invalid UTF-8 bytes with U+FFFD and the replayed
// response would come back CORRUPTED. The byte slice here is deliberately not
// valid UTF-8.
func TestABinaryBodySurvivesIntact(t *testing.T) {
	store, err := redisguard.NewIdempotencyStore(redisClient(t), defaultPrefix, time.Hour)
	require.NoError(t, err)

	body := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0xff, 0xfe, 0x00}
	require.False(t, utf8.Valid(body), "the test data has to be invalid UTF-8")

	require.NoError(t, store.Complete(t.Context(), "tenant-1:binary", corehttp.IdempotentResponse{
		Status:      http.StatusOK,
		Header:      http.Header{"Content-Type": []string{"image/png"}},
		Body:        body,
		Fingerprint: "izi-1",
	}))

	record, done, err := store.Begin(t.Context(), "tenant-1:binary", "print-1")
	require.NoError(t, err)
	require.True(t, done)
	assert.Equal(t, body, record.Body, "a binary body has to survive byte for byte")
}

func TestTheKeyCanBeReservedAgainAfterAbort(t *testing.T) {
	store, err := redisguard.NewIdempotencyStore(redisClient(t), defaultPrefix, time.Hour)
	require.NoError(t, err)

	const key = "tenant-1:key"

	_, _, err = store.Begin(t.Context(), key, "izi-1")
	require.NoError(t, err)

	require.NoError(t, store.Abort(t.Context(), key))

	record, done, err := store.Begin(t.Context(), key, "izi-1")
	require.NoError(t, err, "a released key has to be reservable again")
	assert.Nil(t, record)
	assert.False(t, done)
}

// TestAbortDoesNotDeleteAFinishedRecord verifies that a late Abort does not
// destroy a replayable response.
//
// If the handler panics after writing the response the deferred Abort still
// runs; an implementation deleting unconditionally would drop the record and let
// a repeat request be handled from the start — that is, a second order.
func TestAbortDoesNotDeleteAFinishedRecord(t *testing.T) {
	store, err := redisguard.NewIdempotencyStore(redisClient(t), defaultPrefix, time.Hour)
	require.NoError(t, err)

	const key = "tenant-1:key"

	_, _, err = store.Begin(t.Context(), key, "izi-1")
	require.NoError(t, err)
	require.NoError(t, store.Complete(t.Context(), key, corehttp.IdempotentResponse{
		Status:      http.StatusCreated,
		Body:        []byte(`{"id":"order_01"}`),
		Fingerprint: "izi-1",
	}))

	require.NoError(t, store.Abort(t.Context(), key))

	record, done, err := store.Begin(t.Context(), key, "izi-1")
	require.NoError(t, err)
	require.True(t, done, "a finished record must not be deleted by Abort")
	require.NotNil(t, record)
	assert.Equal(t, http.StatusCreated, record.Status)
}

func TestTheRecordDisappearsWhenTheTTLExpires(t *testing.T) {
	const ttl = 800 * time.Millisecond

	store, err := redisguard.NewIdempotencyStore(redisClient(t), defaultPrefix, ttl)
	require.NoError(t, err)

	const key = "tenant-1:key"

	_, _, err = store.Begin(t.Context(), key, "izi-1")
	require.NoError(t, err)
	require.NoError(t, store.Complete(t.Context(), key, corehttp.IdempotentResponse{
		Status:      http.StatusCreated,
		Fingerprint: "izi-1",
	}))

	_, done, err := store.Begin(t.Context(), key, "izi-1")
	require.NoError(t, err)
	require.True(t, done, "the record has to stand before the ttl expires")

	time.Sleep(ttl + 500*time.Millisecond)

	record, done, err := store.Begin(t.Context(), key, "izi-1")
	require.NoError(t, err)
	assert.Nil(t, record, "the record has to be gone once the ttl expires")
	assert.False(t, done, "an expired key has to be reservable again")
}

// TestTwoProcessesSeeOneRecord pins this package's CORRECTNESS reason.
//
// With the in-memory store the second instance never sees the first's
// reservation; two requests with the same key landing on different instances are
// handled twice. Here the second instance sees both the reservation and the
// finished record.
func TestTwoProcessesSeeOneRecord(t *testing.T) {
	uri := startRedis(t)

	birinci, err := redisguard.NewIdempotencyStore(openClient(t, uri), defaultPrefix, time.Hour)
	require.NoError(t, err)
	ikinci, err := redisguard.NewIdempotencyStore(openClient(t, uri), defaultPrefix, time.Hour)
	require.NoError(t, err)

	const key = "tenant-1:key"

	_, _, err = birinci.Begin(t.Context(), key, "izi-1")
	require.NoError(t, err)

	_, _, err = ikinci.Begin(t.Context(), key, "izi-1")
	require.ErrorIs(t, err, corehttp.ErrIdempotencyKeyInFlight,
		"the second instance has to see the first's reservation")

	require.NoError(t, birinci.Complete(t.Context(), key, corehttp.IdempotentResponse{
		Status:      http.StatusCreated,
		Body:        []byte(`{"id":"order_01"}`),
		Fingerprint: "izi-1",
	}))

	record, done, err := ikinci.Begin(t.Context(), key, "izi-1")
	require.NoError(t, err)
	require.True(t, done, "the second instance has to read the record the first wrote")
	assert.Equal(t, []byte(`{"id":"order_01"}`), record.Body)
}

// --- The namespace split ---

// TestLimitersWithDifferentPrefixesDoNotSpendEachOthersQuota verifies that the
// rate limit counters of two INSTALLATIONS sharing one Redis are separated.
//
// With a fixed prefix there was no such split: with staging and production on
// one Redis, staging's load test eats production's quota and real clients there
// get 429s. The counters are kept through one client, that is in the SAME Redis
// DB; the only thing separating them is the prefix.
func TestLimitersWithDifferentPrefixesDoNotSpendEachOthersQuota(t *testing.T) {
	client := redisClient(t)

	staging, err := redisguard.NewLimiter(client, stagingPrefix, 1, time.Minute)
	require.NoError(t, err)
	production, err := redisguard.NewLimiter(client, productionPrefix, 1, time.Minute)
	require.NoError(t, err)

	// The SAME limit key is deliberate: two installations taking requests from
	// one IP is ordinary, and only this shows the split rests on the PREFIX
	// rather than on the key.
	const key = "client-a"

	ilk, err := staging.Allow(t.Context(), key)
	require.NoError(t, err)
	require.True(t, ilk.Allowed)

	again, err := staging.Allow(t.Context(), key)
	require.NoError(t, err)
	require.False(t, again.Allowed, "staging spent its own quota")

	productionDecision, err := production.Allow(t.Context(), key)
	require.NoError(t, err)
	assert.True(t, productionDecision.Allowed,
		"the quota another prefix's installation spent must not affect this one")
	assert.Zero(t, productionDecision.Remaining, "production used the first allowance of its own quota")
}

// TestStoresWithDifferentPrefixesDoNotSeeEachOthersRecords verifies that the
// idempotency records of two INSTALLATIONS sharing one Redis are separated.
//
// This is the heavier of the two failures: with a fixed prefix the response
// staging wrote for a key would come back as the RESPONSE to a production
// request with the same key — the client gets the id of an order it never
// placed and its real request is never handled.
func TestStoresWithDifferentPrefixesDoNotSeeEachOthersRecords(t *testing.T) {
	client := redisClient(t)

	staging, err := redisguard.NewIdempotencyStore(client, stagingPrefix, time.Hour)
	require.NoError(t, err)
	production, err := redisguard.NewIdempotencyStore(client, productionPrefix, time.Hour)
	require.NoError(t, err)

	const key = "tenant-1:key"

	_, _, err = staging.Begin(t.Context(), key, "izi-staging")
	require.NoError(t, err)

	// The reservation belongs to the namespace too: were the key staging holds
	// to block production, one environment's traffic could stop the other's.
	record, done, err := production.Begin(t.Context(), key, "izi-production")
	require.NoError(t, err, "a reservation under another prefix must not block this store")
	assert.Nil(t, record)
	assert.False(t, done)

	require.NoError(t, staging.Complete(t.Context(), key, corehttp.IdempotentResponse{
		Status:      http.StatusCreated,
		Body:        []byte(`{"id":"staging_01"}`),
		Fingerprint: "izi-staging",
	}))
	require.NoError(t, production.Complete(t.Context(), key, corehttp.IdempotentResponse{
		Status:      http.StatusCreated,
		Body:        []byte(`{"id":"uretim_01"}`),
		Fingerprint: "izi-production",
	}))

	stagingRecord, done, err := staging.Begin(t.Context(), key, "izi-staging")
	require.NoError(t, err)
	require.True(t, done)
	assert.Equal(t, []byte(`{"id":"staging_01"}`), stagingRecord.Body,
		"every installation has to read ITS OWN response")

	productionRecord, done, err := production.Begin(t.Context(), key, "izi-production")
	require.NoError(t, err)
	require.True(t, done)
	assert.Equal(t, []byte(`{"id":"uretim_01"}`), productionRecord.Body,
		"one installation's response must not go to the other's client")
}

// TestTheDefaultPrefixKeepsTheOldKeyShape verifies that BACKWARD COMPATIBILITY
// was kept while the prefix became configurable.
//
// Had the key shape changed, every rate limit counter and every in-flight
// idempotency record of an upgraded installation would become invisible at
// once; every repeat request in the air at that moment is handled a second time,
// that is, a second order. So the expected keys are not derived from the
// constant but written BY HAND: if the constant changes the test fails and the
// cost of the change becomes visible.
func TestTheDefaultPrefixKeepsTheOldKeyShape(t *testing.T) {
	client := redisClient(t)

	lim, err := redisguard.NewLimiter(client, defaultPrefix, 5, time.Minute)
	require.NoError(t, err)

	_, err = lim.Allow(t.Context(), "client-a")
	require.NoError(t, err)

	counter, err := client.Get(t.Context(), "gobit:rl:client-a").Result()
	require.NoError(t, err, "the counter has to be written in the old key shape")
	assert.Equal(t, "1", counter)

	store, err := redisguard.NewIdempotencyStore(client, defaultPrefix, time.Hour)
	require.NoError(t, err)

	_, _, err = store.Begin(t.Context(), "tenant-1:key", "print-1")
	require.NoError(t, err)

	require.NoError(t, client.Get(t.Context(), "gobit:idem:tenant-1:key").Err(),
		"the idempotency record has to be written in the old key shape")
}
