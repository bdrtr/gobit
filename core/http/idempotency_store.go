package http

// This file holds the in-memory idempotency store: the byte budget it runs
// under, the approximate charge model the budget is spent against and the
// eviction that runs once it is exceeded. It stands apart from the middleware
// because the middleware reaches it only through [IdempotencyStore] — none of
// the accounting below is visible from the ring, and the measurements pinned in
// these comments answer a different question ("what does one record cost in
// memory") than the middleware's ("which request may be replayed").

import (
	"bytes"
	"container/list"
	"context"
	"maps"
	"net/http"
	"sync"
	"time"
)

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
// The fingerprint is IGNORED here. It is compared on a FINISHED record only, and
// there it is read off [IdempotentResponse.Fingerprint], which
// [MemoryIdempotencyStore.Complete] stores; keeping a second copy on the
// reservation would be a value nothing reads.
//
// The COPY of the replayed record is taken OUTSIDE the lock; its measurement and
// reasoning are in [MemoryIdempotencyStore]'s lock section.
func (s *MemoryIdempotencyStore) Begin(
	_ context.Context, key, _ string,
) (*IdempotentResponse, bool, error) {
	rec, err := s.reserve(s.now(), key)
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
	now time.Time, key string,
) (*IdempotentResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.collect(now)

	g, ok := s.entry[key]
	if !ok {
		fresh := &entry{key: key, expiresAt: now.Add(s.ttl)}
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
