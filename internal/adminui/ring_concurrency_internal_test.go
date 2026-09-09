package adminui

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// ringReaders is how many readers hammer the ring while it is being bound.
const ringReaders = 8

// ringReadsPerReader is how many reads each of them makes.
//
// The number is what turns a single lucky interleaving into a real overlap:
// with one read apiece the binding would usually land before or after all of
// them, and the window this test exists for would go unvisited.
const ringReadsPerReader = 200

// TestTheRingIsReadableWhileItIsBeingBound runs the sentence in [Ring]'s godoc.
//
// # What the promise is
//
// "Safe for concurrent use: binding happens once, reading on every request."
// The binding happens during boot and the reads are admin requests, so the two
// genuinely overlap in a running process: a request can arrive in the window
// between the router being built and the panel being bound.
//
// # What is asserted
//
// Every read answers ONE of exactly two things — the unbound rejection, or the
// panel that was bound. There is no third answer, and in particular no read may
// return a nil panel with a nil error, which is the shape a torn read would
// take. The race detector covers the rest, and until this test existed it had
// never looked here: measured on 2026-09-09, this package had no test that
// started a goroutine at all.
func TestTheRingIsReadableWhileItIsBeingBound(t *testing.T) {
	t.Parallel()

	var ring Ring
	panel := &UI{}

	var start sync.WaitGroup
	start.Add(1)

	var done sync.WaitGroup
	done.Add(ringReaders + 1)

	for range ringReaders {
		go func() {
			defer done.Done()
			start.Wait()
			for range ringReadsPerReader {
				bound, err := ring.panel()
				switch {
				case err != nil:
					assert.Nil(t, bound, "an unbound ring must not hand out a panel")
					assert.Equal(t, CodeNotBound, errors.CodeOf(err))
				default:
					assert.Same(t, panel, bound,
						"the ring handed out a panel that was never bound")
				}
			}
		}()
	}

	go func() {
		defer done.Done()
		start.Wait()
		ring.Bind(panel)
	}()

	start.Done()
	done.Wait()

	// After the binding the answer is settled: no reader may still be told the
	// ring is unbound.
	bound, err := ring.panel()
	require.NoError(t, err)
	assert.Same(t, panel, bound)
}
