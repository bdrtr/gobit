package job_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/job"
)

// fakeStore is an in-memory Store that records what the runner asked of it.
//
// The election it implements is the SAME rule the real one does — first claim
// of a (name, due) pair wins — so the tests below exercise the runner's use of
// the contract, not the database. That the PostgreSQL implementation really
// enforces it across processes is proved in jobpg's integration test, which is
// the only place it can be.
type fakeStore struct {
	mu sync.Mutex

	claimed  map[string]bool
	finished []job.Outcome
	held     map[int64]bool

	// lockRefused makes WithLock report the lock as taken by somebody else.
	lockRefused bool
	// claimErr and lockErr make the two calls fail.
	claimErr, lockErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{claimed: map[string]bool{}, held: map[int64]bool{}}
}

func (f *fakeStore) key(name string, due time.Time) string {
	return name + "@" + due.UTC().Format(time.RFC3339Nano)
}

func (f *fakeStore) Claim(_ context.Context, name string, due time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.claimErr != nil {
		return false, f.claimErr
	}
	k := f.key(name, due)
	if f.claimed[k] {
		return false, nil
	}
	f.claimed[k] = true

	return true, nil
}

func (f *fakeStore) Finish(_ context.Context, _ string, _ time.Time, outcome job.Outcome) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finished = append(f.finished, outcome)

	return nil
}

func (f *fakeStore) Last(context.Context, []string) (map[string]job.Run, error) {
	return map[string]job.Run{}, nil
}

func (f *fakeStore) WithLock(
	ctx context.Context, key int64, fn func(context.Context) error,
) (bool, error) {
	f.mu.Lock()
	if f.lockErr != nil {
		err := f.lockErr
		f.mu.Unlock()

		return false, err
	}
	if f.lockRefused || f.held[key] {
		f.mu.Unlock()

		return false, nil
	}
	f.held[key] = true
	f.mu.Unlock()

	defer func() {
		f.mu.Lock()
		delete(f.held, key)
		f.mu.Unlock()
	}()

	return true, fn(ctx)
}

func (f *fakeStore) outcomes() []job.Outcome {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]job.Outcome(nil), f.finished...)
}

// runUntil starts the runner, waits until cond holds, then stops it.
//
// Polling rather than sleeping a fixed time: the runner's first pass happens on
// its own goroutine, and Stop deliberately does NOT start work once the context
// is canceled — so a test that stops immediately observes a runner that never
// got to run. That is the correct SHUTDOWN behavior and the wrong thing to
// assert against, which is why the wait is explicit here.
func runUntil(t *testing.T, runner *job.Runner, cond func() bool) {
	t.Helper()

	runner.Start(t.Context())
	defer runner.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}

	t.Fatal("the runner did not reach the expected state within two seconds")
}

// counted builds a job that records how often it ran.
func counted(name string, every, maxRun time.Duration, runs *int, mu *sync.Mutex) job.Definition {
	return job.Definition{
		Name: name, Every: every, MaxRun: maxRun,
		Run: func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			*runs++

			return nil
		},
	}
}

// --- the definition's rules -------------------------------------------------

// TestADefinitionThatCouldNeverCatchUpIsRefused proves the MaxRun/Every rule.
//
// A run that outlasts its own interval means the next occurrence is due before
// this one finished, so the job is permanently behind and the listing never
// shows it caught up — a state that looks like a broken job and is actually a
// broken schedule.
func TestADefinitionThatCouldNeverCatchUpIsRefused(t *testing.T) {
	r := job.NewRegistry()

	err := r.Add(job.Definition{
		Name: "slow", Every: time.Minute, MaxRun: 2 * time.Minute,
		Run: func(context.Context) error { return nil },
	})

	require.Error(t, err)
	assert.Equal(t, job.CodeInvalidDefinition, coreerrors.CodeOf(err))
}

// TestADuplicateNameIsRefused proves two jobs cannot share a name.
//
// They would share an advisory lock AND a history row, so one of them would
// silently never run — and which one would depend on registration order.
func TestADuplicateNameIsRefused(t *testing.T) {
	r := job.NewRegistry()
	d := job.Definition{
		Name: "twice", Every: time.Hour, MaxRun: time.Minute,
		Run: func(context.Context) error { return nil },
	}

	require.NoError(t, r.Add(d))
	err := r.Add(d)

	require.Error(t, err)
	assert.Equal(t, job.CodeDuplicate, coreerrors.CodeOf(err))
}

// TestAJobWithNoBoundIsRefused proves MaxRun is mandatory.
func TestAJobWithNoBoundIsRefused(t *testing.T) {
	r := job.NewRegistry()

	err := r.Add(job.Definition{
		Name: "unbounded", Every: time.Hour,
		Run: func(context.Context) error { return nil },
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "MaxRun")
}

// --- the occurrence ---------------------------------------------------------

// TestTheOccurrenceIsAnchoredToTheEpoch is what makes the election work at all.
//
// Two instances that started minutes apart must compute the SAME due instant,
// or the row they race for is never the same row and every replica runs every
// job. Anchoring to process start would give each one its own timeline.
func TestTheOccurrenceIsAnchoredToTheEpoch(t *testing.T) {
	const every = time.Hour

	// Two "instances", asking at different moments inside the same hour.
	first := job.Occurrence(every, time.Date(2026, 3, 14, 9, 3, 0, 0, time.UTC))
	second := job.Occurrence(every, time.Date(2026, 3, 14, 9, 58, 0, 0, time.UTC))

	assert.Equal(t, first, second,
		"two instances inside one interval have to compute the same occurrence")
	assert.Equal(t, time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC), first)

	next := job.Occurrence(every, time.Date(2026, 3, 14, 10, 0, 1, 0, time.UTC))
	assert.NotEqual(t, first, next, "the next interval is a different occurrence")
}

// TestTheLockKeyCarriesTheClass proves job keys cannot collide with the other
// advisory locks in the database.
//
// The sharpest case is class 0: golang-migrate occupies the whole of it with a
// uint32, and it waits on its lock with a context that cannot be canceled. A
// bare hash of a job name would land in that range and could block a boot
// migration on a wait nobody can interrupt.
func TestTheLockKeyCarriesTheClass(t *testing.T) {
	for _, name := range []string{"saga-watch", "a", "", "a much longer job name"} {
		key := job.LockKey(name)

		assert.Equal(t, job.LockClass, key>>32,
			"every job key has to sit in class %d; %q produced class %d",
			job.LockClass, name, key>>32)
		assert.Greater(t, key, int64(1)<<32,
			"a job key must never fall inside golang-migrate's class 0 range")
	}

	assert.NotEqual(t, job.LockKey("a"), job.LockKey("b"))
}

// --- the runner -------------------------------------------------------------

// TestOnlyOneRunnerRunsAnOccurrence is the election proof.
//
// Ten runners share one store, exactly as ten replicas share one database. The
// occurrence must be run once in total, not once each — and the failure this
// guards is the one that gets WORSE as an installation scales out.
func TestOnlyOneRunnerRunsAnOccurrence(t *testing.T) {
	store := newFakeStore()
	now := time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC)

	var mu sync.Mutex
	runs := 0

	runners := make([]*job.Runner, 0, 10)
	for range 10 {
		registry := job.NewRegistry()
		require.NoError(t, registry.Add(counted("nightly", time.Hour, time.Minute, &runs, &mu)))

		runner, err := job.New(job.Options{
			Registry: registry, Store: store,
			Now: func() time.Time { return now }, Tick: time.Millisecond,
		})
		require.NoError(t, err)
		runners = append(runners, runner)
	}

	var wg sync.WaitGroup
	for _, runner := range runners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runner.Start(t.Context())
		}()
	}
	wg.Wait()

	// Every runner has ticked many times over by now; if the election were
	// broken this is where the count would climb.
	time.Sleep(100 * time.Millisecond)
	for _, runner := range runners {
		runner.Stop()
	}

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, runs,
		"ten instances must run one occurrence ONCE in total, not once each")
}

// TestTheSameOccurrenceIsNotRunTwiceByOneRunner proves the row is consulted on
// every pass rather than only at start.
func TestTheSameOccurrenceIsNotRunTwiceByOneRunner(t *testing.T) {
	store := newFakeStore()
	now := time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC)

	var mu sync.Mutex
	runs := 0

	registry := job.NewRegistry()
	require.NoError(t, registry.Add(counted("nightly", time.Hour, time.Minute, &runs, &mu)))

	runner, err := job.New(job.Options{
		Registry: registry, Store: store,
		Now:  func() time.Time { return now },
		Tick: time.Millisecond,
	})
	require.NoError(t, err)

	runner.Start(t.Context())
	time.Sleep(50 * time.Millisecond)
	runner.Stop()

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, runs, "the clock did not move, so the occurrence did not change")
}

// TestAJobThatPanicsBecomesAFailedRun proves a panic neither kills the process
// nor disappears.
//
// The event bus applies the same rule to a subscriber. What matters here is the
// second half: the panic has to become a RECORDED failure, because a job that
// silently stopped and a job that never existed look identical in a listing.
func TestAJobThatPanicsBecomesAFailedRun(t *testing.T) {
	store := newFakeStore()
	now := time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC)

	registry := job.NewRegistry()
	require.NoError(t, registry.Add(job.Definition{
		Name: "panics", Every: time.Hour, MaxRun: time.Minute,
		Run: func(context.Context) error { panic("the job exploded") },
	}))

	runner, err := job.New(job.Options{
		Registry: registry, Store: store, Now: func() time.Time { return now },
	})
	require.NoError(t, err)

	require.NotPanics(t, func() {
		runUntil(t, runner, func() bool { return len(store.outcomes()) == 1 })
	}, "a panicking job must not take the process down")

	outcomes := store.outcomes()
	require.Len(t, outcomes, 1, "the panic has to be RECORDED, not swallowed")
	require.Error(t, outcomes[0].Err)
	assert.Equal(t, job.CodePanicked, coreerrors.CodeOf(outcomes[0].Err))
}

// TestAFailedRunIsStillRecorded proves a failure is not hidden.
//
// Recording only successes would make the listing claim the job has not run
// since its last success — which reads as "it stopped" rather than "it is
// failing", and sends whoever looks to the wrong question.
func TestAFailedRunIsStillRecorded(t *testing.T) {
	store := newFakeStore()
	now := time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC)
	wanted := errors.New("the job could not do its work")

	registry := job.NewRegistry()
	require.NoError(t, registry.Add(job.Definition{
		Name: "fails", Every: time.Hour, MaxRun: time.Minute,
		Run: func(context.Context) error { return wanted },
	}))

	runner, err := job.New(job.Options{
		Registry: registry, Store: store, Now: func() time.Time { return now },
	})
	require.NoError(t, err)

	runUntil(t, runner, func() bool { return len(store.outcomes()) == 1 })

	outcomes := store.outcomes()
	require.Len(t, outcomes, 1)
	assert.ErrorIs(t, outcomes[0].Err, wanted)
}

// TestARefusedLockRunsNothing proves the liveness half is respected.
//
// A lock held elsewhere means somebody is running the job RIGHT NOW. Running it
// anyway would defeat the whole point of taking a lock.
func TestARefusedLockRunsNothing(t *testing.T) {
	store := newFakeStore()
	store.lockRefused = true
	now := time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC)

	var mu sync.Mutex
	runs := 0

	registry := job.NewRegistry()
	require.NoError(t, registry.Add(counted("busy", time.Hour, time.Minute, &runs, &mu)))

	runner, err := job.New(job.Options{
		Registry: registry, Store: store, Now: func() time.Time { return now },
	})
	require.NoError(t, err)

	runner.Start(t.Context())
	time.Sleep(30 * time.Millisecond)
	runner.Stop()

	mu.Lock()
	defer mu.Unlock()
	assert.Zero(t, runs, "a job whose lock is held elsewhere must not run")
	assert.Empty(t, store.outcomes(), "and nothing may be recorded for it")
}

// TestRunNowRefusesWhenTheJobIsAlreadyRunning proves the hand-run path takes the
// same lock.
//
// An operator running a job by hand during an incident, while the scheduler is
// running it, is exactly the collision the lock exists for — and it is the
// moment somebody is most likely to be impatient.
func TestRunNowRefusesWhenTheJobIsAlreadyRunning(t *testing.T) {
	store := newFakeStore()
	store.lockRefused = true

	registry := job.NewRegistry()
	var mu sync.Mutex
	runs := 0
	require.NoError(t, registry.Add(counted("busy", time.Hour, time.Minute, &runs, &mu)))

	runner, err := job.New(job.Options{Registry: registry, Store: store})
	require.NoError(t, err)

	err = runner.RunNow(t.Context(), "busy")

	require.Error(t, err)
	assert.True(t, coreerrors.IsConflict(err),
		"a job already running is a conflict, not a server fault: %v", err)
	assert.Zero(t, runs)
}

// TestRunNowRefusesAnUnknownJob proves a typo is an error rather than silence.
func TestRunNowRefusesAnUnknownJob(t *testing.T) {
	runner, err := job.New(job.Options{Registry: job.NewRegistry(), Store: newFakeStore()})
	require.NoError(t, err)

	err = runner.RunNow(t.Context(), "no-such-job")

	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err))
}

// TestAStoreFailureDoesNotStopTheRunner proves one bad pass is not fatal.
//
// A database blip must not end scheduled work for the lifetime of the process:
// the next tick has to try again.
func TestAStoreFailureDoesNotStopTheRunner(t *testing.T) {
	store := newFakeStore()
	store.claimErr = errors.New("the database is unreachable")
	now := time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC)

	registry := job.NewRegistry()
	var mu sync.Mutex
	runs := 0
	require.NoError(t, registry.Add(counted("nightly", time.Hour, time.Minute, &runs, &mu)))

	runner, err := job.New(job.Options{
		Registry: registry, Store: store,
		Now: func() time.Time { return now }, Tick: time.Millisecond,
	})
	require.NoError(t, err)

	require.NotPanics(t, func() {
		runner.Start(t.Context())
		time.Sleep(20 * time.Millisecond)

		// The blip clears; the very next tick has to run the job.
		store.mu.Lock()
		store.claimErr = nil
		store.mu.Unlock()

		time.Sleep(30 * time.Millisecond)
		runner.Stop()
	})

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, runs, "the runner has to recover once the store does")
}

// TestStopIsIdempotent proves shutdown can be called twice.
func TestStopIsIdempotent(t *testing.T) {
	runner, err := job.New(job.Options{Registry: job.NewRegistry(), Store: newFakeStore()})
	require.NoError(t, err)

	runner.Start(t.Context())
	require.NotPanics(t, func() {
		runner.Stop()
		runner.Stop()
	})
}
