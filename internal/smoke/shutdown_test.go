//go:build smoke

package smoke

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shutdownTimeout is the SHUTDOWN_TIMEOUT value the scenario grants the process.
//
// It is shorter than the default (15s), and that is deliberate: the assertion is
// not "it shut down in a reasonable time" but "it shut down BEFORE the deadline it
// announced itself". A short limit shows that the graceful shutdown path really
// works, while a hung process fails the test sooner.
const shutdownTimeout = 10 * time.Second

// shutdownGrace is the observation margin granted ON TOP of the limit for the
// process to finish.
//
// Waiting only as long as the limit would not have been enough: a process that
// finishes exactly at the limit and a process that never finishes look the same,
// and the test would say "it did not finish" and so hide the real fault ("it
// finished late"). The margin makes the two distinguishable.
const shutdownGrace = 10 * time.Second

// TestSigtermPerformsGracefulShutdown is scenario E: a process that receives
// SIGTERM shuts down before its own SHUTDOWN_TIMEOUT and with EXIT CODE ZERO.
//
// # Why a real signal
//
// The whole shutdown path lives at the process level: signal.NotifyContext
// cancels the context, the HTTP server waits for the open requests, the container
// services shut down in reverse order and the tracing exporters flush the pending
// spans. None of that runs on a router driven by httptest; that is why
// internal/e2e can say nothing at all about shutdown.
//
// # Why the exit code
//
// A non-zero code is a FAILED shutdown as far as the orchestrator is concerned:
// docker stop and Kubernetes count it as a container error, they bump the restart
// counter, and even a release that shuts down cleanly looks like a "crash-loop".
//
// # Why the duration
//
// A process that falls back to SIGKILL leaves the open requests half-done.
// Kubernetes sends SIGKILL once terminationGracePeriodSeconds runs out; the
// application shutting down before its own limit is the proof that that point was
// never reached.
func TestSigtermPerformsGracefulShutdown(t *testing.T) {
	cfg := baseSettings(scenarioDatabase(t), freePort(t))
	cfg["SHUTDOWN_TIMEOUT"] = shutdownTimeout.String()

	s := startServer(t, cfg)
	s.waitForReady(startupTimeout)

	// Before the shutdown we verify that the process really is serving: shutting
	// down a process that never took a request is the easy case in which the
	// "waiting for the open connections" path never runs at all.
	status, body := s.request(http.MethodGet, "/health", "")
	require.Equal(t, http.StatusOK, status, "the process has to be healthy before the shutdown; body: %s", body)

	start := time.Now()
	s.sigterm()

	exitCode, finished := s.waitForExit(shutdownTimeout + shutdownGrace)
	elapsed := time.Since(start)

	require.True(t, finished,
		"the process did not shut down within %s of the SIGTERM, which means SIGKILL is needed\n%s",
		shutdownTimeout+shutdownGrace, s.logBuf())

	assert.Equal(t, 0, exitCode,
		"a graceful shutdown has to give a zero exit code; the orchestrator counts anything else as an error\n%s", s.logBuf())
	assert.Less(t, elapsed, shutdownTimeout,
		"the shutdown has to finish before the deadline it announced itself (%s), it took %s\n%s",
		shutdownTimeout, elapsed, s.logBuf())

	// The log has to say that the shutdown arrived by SIGNAL: a process that
	// finishes fast and with a zero code could just as well have fallen out of
	// main without ever seeing the signal.
	assert.True(t, s.logContains("the shutdown signal arrived"),
		"the process has to see the SIGTERM\n%s", s.logBuf())
	assert.True(t, s.logContains("the HTTP server is closed"),
		"the shutdown has to complete through the graceful path\n%s", s.logBuf())
}
