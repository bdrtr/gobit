package service_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// registryWriters is how many registrations race at once.
//
// Eight is enough to make the contention real and short enough not to slow the
// suite: every writer is released at the same instant, so the lock is actually
// contended rather than merely present.
const registryWriters = 8

// TestTheRegistryKeepsItsPromiseUnderConcurrentUse RUNS the sentence in the
// type's own godoc.
//
// # Why this test exists
//
// `ProviderRegistry` promises it is safe for concurrent use: registration at
// boot, reading on every request. Measured on 2026-09-09, eleven packages make
// a promise like that and NOT ONE had a test running the promising type from
// two goroutines. The race detector only looks at code that actually ran
// concurrently, so the `-race` lane had never looked at these locks — proved
// by deleting the lock from Register, where the whole package stayed green.
//
// # What it asserts
//
// The detector's silence is not the claim on its own: it measures the path the
// test took, not the contract. The claim is that exactly ONE writer wins a
// contested id, every other gets a Conflict, and the provider that resolves
// afterwards is the winner itself. "The existing provider is kept" is an `if`
// on one goroutine; under concurrency it is what the lock promises.
func TestTheRegistryKeepsItsPromiseUnderConcurrentUse(t *testing.T) {
	registry := service.NewProviderRegistry()

	var start sync.WaitGroup
	start.Add(1)

	var done sync.WaitGroup
	done.Add(2 * registryWriters)

	winners := make(chan *fakeProvider, registryWriters)
	conflicts := make(chan error, registryWriters)

	for i := range registryWriters {
		// The contested writers all ask for the SAME id.
		go func() {
			defer done.Done()
			provider := newFakeProvider("contested")
			start.Wait()
			if err := registry.Register(provider); err != nil {
				conflicts <- err

				return
			}
			winners <- provider
		}()

		// A writer with an id of its own: these do not block each other, and
		// the set a reader sees GROWS while they run.
		go func() {
			defer done.Done()
			provider := newFakeProvider(fmt.Sprintf("separate-%d", i))
			start.Wait()
			assert.NoError(t, registry.Register(provider))
		}()
	}

	start.Done()
	done.Wait()
	close(winners)
	close(conflicts)

	require.Len(t, winners, 1,
		"exactly one of the %d writers racing for the same id had to win", registryWriters)
	require.Len(t, conflicts, registryWriters-1, "every losing writer had to get a conflict")

	for err := range conflicts {
		assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
		assert.Equal(t, service.CodeProviderExists, errors.CodeOf(err))
	}

	winner := <-winners
	resolved, err := registry.Get("contested")
	require.NoError(t, err)
	assert.Same(t, winner, resolved,
		"the resolved provider is not the one that WON the registration; "+
			"\"the existing one is kept\" did not hold under a concurrent write")

	assert.Len(t, registry.IDs(), registryWriters+1,
		"the contested id and %d separate ids were expected", registryWriters)
}
