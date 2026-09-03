package redisguard

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// DefaultIdempotencyTTL is how long a record is kept by default.
//
// It matches corehttp.NewMemoryIdempotencyStore's default; because the two
// stores are interchangeable, behaving differently on the same input would be a
// treacherous trap.
const DefaultIdempotencyTTL = 24 * time.Hour

// The record's state is read from the mark at the start of the value.
//
// Putting the state in a separate field (a "state" key inside the JSON, say)
// would force the Lua side to decode cjson just to decide whether Abort may
// delete the record. The mark is the value's first two bytes: Abort answers "is
// this reservation still in flight" with a single string.sub comparison.
const (
	// markInFlight is the mark of a record that is reserved but not finished; the
	// fingerprint of the request holding the reservation follows it.
	markInFlight = "i:"
	// markDone is the mark of a finished record; the JSON body follows it.
	markDone = "c:"
)

// beginScript reserves the key or returns the value already there.
//
// SET NX makes the reservation itself atomic: with two requests arriving at
// once only one gets true and the other reads the record. GET being in the SAME
// script is required; as a separate round trip the key's TTL could expire
// between SET NX failing and GET running, GET would come back empty and we would
// land in an undecidable state: "there is no record but I do not hold the
// reservation".
//
// A new reservation returns an EMPTY STRING. Because every stored value starts
// with a mark ([markInFlight] or [markDone]), an empty string cannot be confused
// with a real record. Lua's false (that is, Redis's nil) could not be used for
// this: an empty GET falls to the same nil and "I took the reservation" could
// not be told from "the key disappeared" — taking the first for the second lets
// both requests own the key.
var beginScript = redis.NewScript(`
if redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2], 'NX') then
  return ''
end
return redis.call('GET', KEYS[1])
`)

// abortScript deletes an UNFINISHED reservation only.
//
// An unconditional DEL would let a late Abort (the defer that runs when a
// handler panics, say) destroy a response that had already been written; with
// that record deleted a repeat request is handled from the start, and the double
// processing idempotency exists to prevent happens by exactly that route.
var abortScript = redis.NewScript(`
local existing = redis.call('GET', KEYS[1])
if existing and string.sub(existing, 1, string.len(ARGV[1])) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

// record is the stored form of a finished response in Redis.
//
// Body is a []byte and encoding/json writes it as BASE64; the ~33% overhead is
// deliberate. Were the field a string, json.Marshal would silently replace
// invalid UTF-8 bytes with U+FFFD: the replayed response of an endpoint
// returning a PDF, an image or protobuf would come back CORRUPTED, and the
// corruption would only be noticed at the client.
type record struct {
	Status      int         `json:"status"`
	Header      http.Header `json:"header,omitempty"`
	Body        []byte      `json:"body,omitempty"`
	Fingerprint string      `json:"fingerprint"`
}

// IdempotencyStore keeps the idempotency records in Redis.
//
// A record lives in one key, in one string: the state mark plus either the
// fingerprint or the JSON body. Reserving and reading are one atomic step, so of
// two requests arriving at once with the same key only one does the work — which
// instance they landed on makes no difference.
//
// # The key shape
//
// Records are written to "<prefix>:idem:<key>"; with the default prefix the key
// "tenant-1:abc" lands on the record "gobit:idem:tenant-1:abc". The prefix comes
// from the constructor and is what separates two installations sharing one Redis
// (see the package godoc).
//
// The key reaching the store is already namespaced with the caller's identity and
// may be longer than the 255 characters imposed on the client (see the
// corehttp.IdempotencyStore godoc); because key length is not a problem in Redis
// it is not shortened but appended to the prefix as it is. Shortening it (by
// hashing, say) would bring a collision risk, and two colliding keys mean two
// DIFFERENT requests seeing each other's response.
type IdempotencyStore struct {
	client *redis.Client
	// prefix is the FULL prefix of the record keys (for example "gobit:idem:").
	//
	// It is built once in the constructor so the namespace prefix and the section
	// name are not joined on every call.
	prefix string
	// ttl is how long the records are kept.
	ttl time.Duration
	// ttlMs is the ttl in milliseconds; it is derived once in the constructor so
	// the script does not have to recompute it on every call.
	ttlMs int64
}

var _ corehttp.IdempotencyStore = (*IdempotencyStore)(nil)

// NewIdempotencyStore builds the Redis-backed store with the given retention.
//
// keyPrefix is the namespace prefix of the records; they are written to
// "<keyPrefix>:idem:<key>". [validatePrefix] checks its shape and an invalid
// prefix is an ERROR.
// Unlike the ttl the PREFIX does NOT fall back to a default, and the difference
// is deliberate: an invalid ttl costs a record that lives longer or shorter than
// expected, while an invalid prefix costs two installations sharing the SAME
// namespace — that is, one's response going to the other's client. Fixing it
// silently would bring back the very failure being fixed.
//
// With a zero or negative ttl [DefaultIdempotencyTTL] is used, which is what
// corehttp.NewMemoryIdempotencyStore does too. With a nil client it returns an
// error.
func NewIdempotencyStore(client *redis.Client, keyPrefix string, ttl time.Duration) (*IdempotencyStore, error) {
	if client == nil {
		return nil, coreerrors.Invalid(CodeInvalidConfig, "redis istemcisi nil olamaz")
	}

	if err := validatePrefix(keyPrefix); err != nil {
		return nil, err
	}

	if ttl <= 0 {
		ttl = DefaultIdempotencyTTL
	}

	// Redis's finest resolution is the millisecond; a shorter ttl would turn
	// "PX 0" into a command error.
	ttl = max(ttl, time.Millisecond)

	return &IdempotencyStore{
		client: client,
		prefix: keyPrefix + separator + idempotencySection + separator,
		ttl:    ttl,
		ttlMs:  ttl.Milliseconds(),
	}, nil
}

// Begin tries to reserve the key for this request.
//
// For a new key it returns (nil, false, nil) and marks the key "in flight"; with
// a finished record it returns (record, true, nil); while another request is
// working on the key it returns corehttp.ErrIdempotencyKeyInFlight.
//
// fingerprint is written into the reservation mark. No decision reads it today —
// corehttp.Idempotency compares the fingerprint on a FINISHED record only — but
// the answer to "which request holds this key" has to be readable with a plain
// redis-cli GET while diagnosing a problem in production.
func (s *IdempotencyStore) Begin(
	ctx context.Context, key, fingerprint string,
) (*corehttp.IdempotentResponse, bool, error) {
	value, err := beginScript.Run(ctx, s.client,
		[]string{s.prefix + key},
		markInFlight+fingerprint,
		s.ttlMs,
	).Text()

	switch {
	case coreerrors.Is(err, redis.Nil):
		// SET NX failed but GET came back empty. Because the script runs
		// atomically this is not expected; counting it as a "new reservation"
		// would let TWO requests believe they own the key, so an error is returned.
		return nil, false, coreerrors.Unavailable(CodeIdempotencyStoreFailed,
			"the idempotency key could neither be reserved nor read")
	case err != nil:
		return nil, false, coreerrors.Wrap(err, coreerrors.KindUnavailable,
			CodeIdempotencyStoreFailed, "the idempotency key could not be reserved")
	}

	switch {
	case value == "":
		return nil, false, nil
	case strings.HasPrefix(value, markInFlight):
		return nil, false, corehttp.ErrIdempotencyKeyInFlight
	case !strings.HasPrefix(value, markDone):
		// Somebody outside this package wrote the key (a prefix clash) or the
		// record format changed. Returning an error beats taking a value we do not
		// recognize for a record and trying to decode it: a record decoded wrong
		// can go to the client as ANOTHER request's response.
		return nil, false, coreerrors.Internal(CodeIdempotencyStoreFailed,
			"the state mark of the idempotency record was not recognized")
	}

	var k record
	if err := json.Unmarshal([]byte(strings.TrimPrefix(value, markDone)), &k); err != nil {
		return nil, false, coreerrors.Wrap(err, coreerrors.KindInternal,
			CodeIdempotencyStoreFailed, "the idempotency record could not be decoded")
	}

	return &corehttp.IdempotentResponse{
		Status:      k.Status,
		Header:      k.Header,
		Body:        k.Body,
		Fingerprint: k.Fingerprint,
	}, true, nil
}

// Complete stores the response of a key whose work is done.
//
// It does NOT REQUIRE the reservation to still be there; the record is written
// unconditionally: the handler ran and its side effects happened, so the record
// existing matters more than the reservation having disappeared (because its TTL
//
// The TTL restarts from the moment the record is written. The client's retry
// window has to count from the moment the RESPONSE was born, not the
// reservation; otherwise a long-running handler would shorten the client's
// remaining retention by its own running time.
func (s *IdempotencyStore) Complete(
	ctx context.Context, key string, resp corehttp.IdempotentResponse,
) error {
	ham, err := json.Marshal(record{
		Status:      resp.Status,
		Header:      resp.Header,
		Body:        resp.Body,
		Fingerprint: resp.Fingerprint,
	})
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindInternal,
			CodeIdempotencyStoreFailed, "the idempotency record could not be turned into JSON")
	}

	if err := s.client.Set(ctx, s.prefix+key, markDone+string(ham), s.ttl).Err(); err != nil {
		return coreerrors.Wrap(err, coreerrors.KindUnavailable,
			CodeIdempotencyStoreFailed, "the idempotency record could not be written")
	}

	return nil
}

// Abort releases the reservation; the key can be tried again.
//
// Only a value carrying the [markInFlight] mark is deleted (see [abortScript]).
//
// Two CONSECUTIVE reservations cannot be told apart: A reserves the key, the TTL
// expires, B reserves the same key and then A's Abort deletes B's reservation.
// Closing that would need a random token per reservation, and the
// Abort'un onu geri vermesini gerektirirdi; corehttp.IdempotencyStore'un
// Abort(ctx, key) signature has no room for one; widening the interface for this
// implementation alone would tie the abstraction to Redis. It is unnecessary in
// practice too: for the case to arise the TTL (24 hours by default) has to expire
// while ONE more request is still running.
func (s *IdempotencyStore) Abort(ctx context.Context, key string) error {
	if err := abortScript.Run(ctx, s.client,
		[]string{s.prefix + key},
		markInFlight,
	).Err(); err != nil {
		return coreerrors.Wrap(err, coreerrors.KindUnavailable,
			CodeIdempotencyStoreFailed, "the idempotency reservation could not be released")
	}

	return nil
}
