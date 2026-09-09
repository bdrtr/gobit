package service_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// contactCallers is how many callers reach the lazy surface at once.
const contactCallers = 8

// TestOrderContactsSurviveAFailedResolutionUnderConcurrency covers the LOCK
// that the single-threaded tests leave untouched.
//
// # Why the caller is concurrent by construction
//
// This wrapper is not reached from an HTTP handler. Its only caller is
// `Service.OrderPlaced`, bound with `bus.Subscribe` inside the notification
// module's Register — that is, inside `module.Registry.Bootstrap`'s
// `registerAll`. On the Redis bus a Subscribe starts its consumer goroutine
// immediately and the group is created at the head of the stream, so a backlog
// is delivered while later modules are still registering.
//
// That is exactly the window the wrapper's godoc argues about, and it is why it
// refuses sync.Once: a resolution that failed there would otherwise be
// permanent. `Bootstrap`'s own ordering guarantee does not reach this caller —
// it is written about ROUTES, and a subscriber is not a route.
//
// # What this buys over the single-threaded tests
//
// Measured on the sibling wrapper in plugins/searchpg: swapping the mutex for
// sync.Once turns the single-threaded test red too, so that mutation separates
// nothing. Deleting the LOCK and keeping the retry is the mutation that does —
// the single-threaded test stays green and the concurrent one reports a data
// race. Until this test existed, the `-race` lane had never run this lock.
func TestOrderContactsSurviveAFailedResolutionUnderConcurrency(t *testing.T) {
	t.Parallel()

	c := container.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	reader := service.NewOrderContacts(c)

	// Phase one: every caller arrives while the order module is not registered.
	failures := make(chan error, contactCallers)
	releaseTogether(contactCallers, func() {
		_, err := reader.OrderContactJSON(context.Background(), "order_01H")
		failures <- err
	})
	close(failures)

	require.Len(t, failures, contactCallers)
	for err := range failures {
		require.Error(t, err, "a read with no registration has to fail")
		assert.Equal(t, service.CodeContactUnavailable, errors.CodeOf(err))
	}

	// Phase two: order comes up and every caller tries again. A cache that kept
	// the FAILURE would answer all of these with the same error.
	require.NoError(t, c.Provide(service.OrderInteropName, &fakeContacts{body: testOrderBody}))

	results := make(chan error, contactCallers)
	releaseTogether(contactCallers, func() {
		_, err := reader.OrderContactJSON(context.Background(), "order_01H")
		results <- err
	})
	close(results)

	for err := range results {
		assert.NoError(t, err,
			"a failed resolution was kept: the lock stores only a SUCCESSFUL result, which "+
				"is the whole reason sync.Once was refused here")
	}
}

// releaseTogether starts n goroutines and releases them at the same instant.
//
// The release is what makes the contention real: goroutines started in a plain
// loop tend to run one after another, and an uncontended lock is a lock the
// race detector has nothing to say about.
func releaseTogether(n int, body func()) {
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
