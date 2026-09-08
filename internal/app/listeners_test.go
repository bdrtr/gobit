package app

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheOperatorListenersCloseWithoutWaitingForASignal proves the close
// function does not depend on the process context being canceled.
//
// serve returns on the FIRST error, and the commonest way a boot fails — an API
// port that is already bound — comes back from core/http.Server.Run without
// canceling anything. Both operator listeners are still up at that moment. If
// the wait hung on the process context, the deferred close would block until a
// SIGTERM that is not coming: the process would neither exit nor report the
// error it had already found. The context below is deliberately never canceled,
// and the budget is what turns that hang into a failure instead of a test run
// that never ends.
func TestTheOperatorListenersCloseWithoutWaitingForASignal(t *testing.T) {
	cfg := metricsConfig(freeAddr(t))
	cfg.ProfilingAddr = freeAddr(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	closeListeners := startOperatorListeners(ctx, cfg, http.NotFoundHandler(), discardLogger())

	waitUntilBound(t, cfg.MetricsAddr)
	waitUntilBound(t, cfg.ProfilingAddr)

	assert.True(t, waitedWithin(closeListeners, 10*time.Second),
		"the listeners must close on their own context, not on the process's")

	// Both ports are free again: closing waited for the listeners rather than
	// only asking them to stop.
	for _, addr := range []string{cfg.MetricsAddr, cfg.ProfilingAddr} {
		ln, err := net.Listen("tcp", addr)
		require.NoError(t, err, "%s must have been released", addr)
		require.NoError(t, ln.Close())
	}
}
