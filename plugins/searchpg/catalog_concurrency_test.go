package searchpg

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// catalogCallers is how many callers reach the lazy surface at once.
const catalogCallers = 8

// TestTheCatalogSurvivesAFailedResolutionUnderConcurrency runs the sentence the
// mutex was chosen for.
//
// # Why sync.Once is not here, and why that had never been RUN concurrently
//
// [catalog] deliberately does not use sync.Once, and its godoc says why: Once
// makes the first call's RESULT permanent, so one resolution that failed while
// product was not yet registered would leave search dead for the life of the
// process. That claim is already tested on ONE goroutine
// ([TestTheCatalogIsResolvedLazily]).
//
// It had never been run on several. Measured on 2026-09-09: this package had no
// test that started a goroutine at all, so the race detector — which only looks
// at code that actually ran concurrently — had never seen this lock. And the
// caller that makes it matter is concurrent by construction: the plugin's event
// subscription goes live inside plugin registration, and on the Redis bus a
// consumer goroutine starts the moment Subscribe is called.
//
// # What this test buys that the single-threaded one does not
//
// Both were measured. Replacing the mutex with sync.Once turns BOTH red, so
// that mutation proves nothing about this one. Deleting the LOCK while leaving
// the retry logic is the mutation that separates them: the single-threaded test
// stays green and this one reports a data race. That is the whole contribution
// — the lock itself is now covered, and it was not before.
func TestTheCatalogSurvivesAFailedResolutionUnderConcurrency(t *testing.T) {
	t.Parallel()

	c := container.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	k := newCatalog(c)

	// Phase one: every caller arrives while the registration is absent.
	failures := make(chan error, catalogCallers)
	runTogether(catalogCallers, func() {
		_, err := k.products(context.Background(), []string{"prod_1"}, nil)
		failures <- err
	})
	close(failures)

	require.Len(t, failures, catalogCallers)
	for err := range failures {
		require.Error(t, err, "a read with no registration has to fail")
		assert.Equal(t, codeCatalogMissing, coreerrors.CodeOf(err))
	}

	// Phase two: the module comes up and every caller tries again. A cache that
	// kept the FAILURE would answer all of these with the same error.
	fake := newFakeCatalog()
	fake.addProduct("prod_1", "Shirt", "")
	require.NoError(t, c.Provide(catalogInteropName, fake))

	results := make(chan error, catalogCallers)
	runTogether(catalogCallers, func() {
		_, err := k.products(context.Background(), []string{"prod_1"}, nil)
		results <- err
	})
	close(results)

	for err := range results {
		assert.NoError(t, err,
			"a failed resolution was kept: the lock is supposed to store only a SUCCESSFUL "+
				"result, which is the whole reason sync.Once was refused here")
	}
}

// runTogether releases n goroutines at the same instant and waits for them.
//
// The release is what makes the contention real: goroutines started in a loop
// without it tend to run one after another, and a lock that is never contended
// is a lock the race detector has nothing to say about.
func runTogether(n int, body func()) {
	var start sync.WaitGroup
	start.Add(1)

	var done sync.WaitGroup
	done.Add(n)

	for range n {
		go func() {
			defer done.Done()
			start.Wait()
			body()
		}()
	}

	start.Done()
	done.Wait()
}
