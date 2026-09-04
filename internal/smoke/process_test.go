//go:build smoke

package smoke

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// startupTimeout is the maximum time granted for the process to answer /health.
//
// It is generous: on a fresh database, startup applies the migrations of the
// core and of thirteen modules. Time spent on a slow CI runner is not a fault;
// the real fault is the process NEVER coming up, and this timeout catches it.
const startupTimeout = 90 * time.Second

// pollInterval is the wait between /health polls.
//
// There is NO FIXED SLEEP and there must not be: a test assuming "wait 3
// seconds, it must be up by now" fails in the wrong place on a slow runner and
// waits for nothing on a fast machine. Polling moves on the MOMENT it is ready.
const pollInterval = 100 * time.Millisecond

// requestTimeout is the time granted to a single HTTP request.
const requestTimeout = 5 * time.Second

// logTimeout is the maximum time granted for a log line to land in the buffer.
//
// It cannot be zero, and its rationale is a race: the application logs the
// diagnostic line BEFORE writing the response, but the side copying the pipe
// into the buffer is a SEPARATE goroutine of exec.Cmd. Reading the buffer once,
// the moment the response is in hand, would tie a scenario that has no fault to
// the speed of the machine.
const logTimeout = 5 * time.Second

// killTimeout is the time waited after the shutdown signal before the process
// is killed by force.
//
// It is longer than the application's own SHUTDOWN_TIMEOUT default (15s): the
// shutdown scenario measures that duration ITSELF, and a cleanup step cutting
// it short would corrupt the very thing being measured.
const killTimeout = 30 * time.Second

// settings are the environment variables handed to the server process.
type settings map[string]string

// logBuffer accumulates the process output in a way that is safe for concurrent
// writes.
//
// The lock is REQUIRED: given an io.Writer, exec.Cmd copies the pipe in its OWN
// goroutine, while the test reads the buffer with the process still running
// (to print the log on a timeout, for example). On a lockless bytes.Buffer this
// is a real race that blows up reliably under -race.
type logBuffer struct {
	ok sync.Mutex
	b  bytes.Buffer
}

// Write implements the io.Writer contract under the lock.
func (g *logBuffer) Write(p []byte) (int, error) {
	g.ok.Lock()
	defer g.ok.Unlock()

	return g.b.Write(p)
}

// String returns the output accumulated so far.
func (g *logBuffer) String() string {
	g.ok.Lock()
	defer g.ok.Unlock()

	return g.b.String()
}

// proc represents a running server binary and its output.
type proc struct {
	t   *testing.T
	cmd *exec.Cmd
	// addr is the base address the process listens on (http://127.0.0.1:port).
	addr string
	// stdout is the application's structured logs (the logger's default is
	// os.Stdout).
	stdout *logBuffer
	// stderr is where the "fatal:" line that stops startup and the OTel SDK
	// write; the misconfiguration assertions look here.
	stderr *logBuffer
	// finished is closed when Wait returns.
	finished chan struct{}
	// ok guards the two fields below; the goroutine calling Wait writes them.
	ok       sync.Mutex
	exitCode int
	exitErr  error
}

// freePort asks the operating system for a free TCP port.
//
// # Why not a fixed port
//
// Scenarios open servers on the same machine, sometimes at the same time (see
// yaris_test.go). A fixed port would drop the second process with "address
// already in use" and blame the fault on the test itself.
//
// # Why this way
//
// The application does not accept APP_PORT=0 (config.Validate wants 1-65535),
// which means we CANNOT let the operating system pick the port; we have to ask.
// Closing the listener and using the number leaves a theoretical race — between
// the close and the server binding, someone else could take the same port. In
// practice the Linux kernel walks the ephemeral port range in order, so a number
// just released is not handed out again immediately; besides, losing the race is
// not SILENT: the process dies at startup, [proc.waitForReady] times out and
// prints the process log.
func freePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "could not obtain a free port")

	addr, isTCP := listener.Addr().(*net.TCPAddr)
	require.True(t, isTCP, "a tcp address is expected from a tcp listener, got %T", listener.Addr())
	require.NoError(t, listener.Close(), "could not close the temporary listener")

	return addr.Port
}

// baseSettings is the smallest working configuration of a scenario.
//
// Scenarios take this and change ONLY the key they are testing; keeping the
// common base here reduces the answer to "why did this scenario behave
// differently" to a single line of difference.
func baseSettings(dsn string, port int) settings {
	return settings{
		"APP_ENV":  "development",
		"APP_PORT": strconv.Itoa(port),

		"DATABASE_URL": dsn,
		// The JWT secret is given EXPLICITLY: without it cmd/server generates a
		// random secret specific to every startup, and in a multi-instance
		// scenario that means tokens invalid from one instance to the next.
		"JWT_SECRET": smokeJWTSecret,

		// The text format is for DIAGNOSTICS only: when a scenario fails the
		// process log is printed into the test, and JSON lines would make it
		// harder to read.
		"LOG_FORMAT": "text",
		"LOG_LEVEL":  "info",
	}
}

// smokeJWTSecret is the signing secret the scenarios share.
//
// It is LONGER than 32 characters so that it does not trip config.Validate's
// length gate in the shared-environment (staging) scenarios.
const smokeJWTSecret = "smoke-test-signing-secret-longer-than-32-bytes"

// The first administrator identity the seed scenarios share.
//
// The password is longer than 16 characters: in local development only the auth
// module's floor of 12 applies, but the shared-environment scenarios must pass
// through config.MinBootstrapPasswordLen as well.
const (
	seedEmail    = "admin@gobit.test"
	seedPassword = "smoke-seed-password-42"
)

// startServer starts the server binary and does NOT WAIT for it to be ready.
//
// The process is ALWAYS stopped through t.Cleanup: an escaped process leads to
// testcontainers being unable to shut the database down and to the CI runner
// hanging.
func startServer(t *testing.T, cfg settings) *proc {
	t.Helper()

	port, err := strconv.Atoi(cfg["APP_PORT"])
	require.NoError(t, err, "APP_PORT must be a number: %q", cfg["APP_PORT"])

	s := &proc{
		t:        t,
		addr:     fmt.Sprintf("http://127.0.0.1:%d", port),
		stdout:   &logBuffer{},
		stderr:   &logBuffer{},
		finished: make(chan struct{}),
	}

	s.cmd = exec.Command(binaryPath)
	s.cmd.Env = env(cfg)
	s.cmd.Stdout = s.stdout
	s.cmd.Stderr = s.stderr

	require.NoError(t, s.cmd.Start(), "could not start the server process")

	go func() {
		err := s.cmd.Wait()

		s.ok.Lock()
		s.exitErr = err
		s.exitCode = s.cmd.ProcessState.ExitCode()
		s.ok.Unlock()

		close(s.finished)
	}()

	t.Cleanup(s.stop)

	return s
}

// env builds the process's environment variable list FROM SCRATCH.
//
// The environment of whoever runs the test is NOT INHERITED, and this is
// mandatory for the correctness of the scenarios: cmd/server reads the plugin
// settings from os.Environ(), so a STRIPE_API_KEY sitting in the developer's
// shell would silently pass the "no key" scenario. The same holds for
// DATABASE_URL and PLUGINS.
//
// PATH and HOME are inherited: neither one changes the application's behavior,
// but they are the minimum environment that starting a process and the DNS/TLS
// root store expect.
func env(cfg settings) []string {
	out := make([]string, 0, len(cfg)+2)

	for _, name := range []string{"PATH", "HOME"} {
		if value, found := os.LookupEnv(name); found {
			out = append(out, name+"="+value)
		}
	}

	for name, value := range cfg {
		out = append(out, name+"="+value)
	}

	return out
}

// waitForReady POLLS until the process returns 200 for /health.
//
// On a timeout the process LOG is printed into the test: without it the reason a
// scenario failed in CI ("port taken", "migration lock", "config error") cannot
// be understood, and the only information would be "timeout".
func (s *proc) waitForReady(timeout time.Duration) {
	s.t.Helper()

	client := &http.Client{Timeout: requestTimeout}
	deadline := time.Now().Add(timeout)

	for {
		if s.happened() {
			code, exitErr := s.exitStatus()
			s.t.Fatalf("the process died at startup (exit code %d, %v)\n%s",
				code, exitErr, s.logBuf())
		}

		resp, err := client.Get(s.addr + "/health")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				return
			}
		}

		if time.Now().After(deadline) {
			s.t.Fatalf("the process did not answer /health within %s (last error: %v)\n%s",
				timeout, err, s.logBuf())
		}

		time.Sleep(pollInterval)
	}
}

// request makes an HTTP request to the process and returns the status code and
// the body.
func (s *proc) request(method, path, token string) (code int, body string) {
	s.t.Helper()

	req, err := http.NewRequestWithContext(s.t.Context(), method, s.addr+path, http.NoBody)
	require.NoError(s.t, err, "could not build the request: %s %s", method, path)

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	return s.send(req)
}

// send sends a ready-made request and returns the status code and the body.
func (s *proc) send(req *http.Request) (code int, body string) {
	s.t.Helper()

	client := &http.Client{Timeout: requestTimeout}

	resp, err := client.Do(req)
	require.NoError(s.t, err, "request failed: %s %s\n%s",
		req.Method, req.URL.Path, s.logBuf())
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(s.t, err, "could not read the response body")

	return resp.StatusCode, string(raw)
}

// sigterm sends SIGTERM to the process.
//
// This is the signal the orchestrators (Kubernetes, systemd, docker stop) send;
// a graceful-shutdown claim proves something only if it is tested with the same
// signal.
func (s *proc) sigterm() {
	s.t.Helper()

	require.NoError(s.t, s.cmd.Process.Signal(syscall.SIGTERM), "could not send SIGTERM")
}

// waitForExit waits for the process to finish and returns the exit code.
//
// The second return value is whether the process FINISHED within the given
// time; the caller must be able to tell "finished late" apart from "finished
// with a non-zero code".
func (s *proc) waitForExit(timeout time.Duration) (exitCode int, finished bool) {
	s.t.Helper()

	select {
	case <-s.finished:
		code, _ := s.exitStatus()

		return code, true
	case <-time.After(timeout):
		return 0, false
	}
}

// exitStatus reads the process's exit code and Wait's error under the lock.
//
// The error is returned too because the exit code alone stays incomplete: on a
// process that dies by a signal the code is -1 and only the error says what
// killed it ("signal: killed"). Carrying both in the diagnostic message makes a
// scenario that fails in CI distinguishable at a single glance.
func (s *proc) exitStatus() (code int, err error) {
	s.ok.Lock()
	defer s.ok.Unlock()

	return s.exitCode, s.exitErr
}

// happened reports whether the process has finished.
func (s *proc) happened() bool {
	select {
	case <-s.finished:
		return true
	default:
		return false
	}
}

// logBuf merges the process's two streams into a single text for diagnostics.
//
// The streams are labeled SEPARATELY: the misconfiguration assertions look only
// at stderr, and a merged text would render the question "did the fatal line
// really land on stderr" unanswerable.
func (s *proc) logBuf() string {
	return fmt.Sprintf("--- stdout ---\n%s--- stderr ---\n%s--------------\n",
		s.stdout.String(), s.stderr.String())
}

// stop shuts the process down; t.Cleanup calls it from here.
//
// SIGTERM is tried first, but if the time runs out it falls back to SIGKILL: a
// scenario's hanging process holds the ports and the database connections of the
// following scenarios and in the end hangs the CI runner.
func (s *proc) stop() {
	if s.happened() {
		return
	}

	_ = s.cmd.Process.Signal(syscall.SIGTERM)

	select {
	case <-s.finished:
	case <-time.After(killTimeout):
		_ = s.cmd.Process.Kill()
		<-s.finished
	}
}

// mustStopAtStartup starts the process and waits for it to stop AT STARTUP.
//
// All of the misconfiguration scenarios use this path; the returned values are
// the exit code and stderr. If the process stays up the test FAILS: "it came up
// with a wrong configuration" is exactly the fault meant to be caught.
func mustStopAtStartup(t *testing.T, cfg settings, timeout time.Duration) (exitCode int, stderr string) {
	t.Helper()

	s := startServer(t, cfg)

	code, finished := s.waitForExit(timeout)
	if !finished {
		t.Fatalf("the process did not stop within %s; it CAME UP with a wrong configuration\n%s",
			timeout, s.logBuf())
	}

	return code, s.stderr.String()
}

// logContains reports whether the text occurs in either of the two streams.
func (s *proc) logContains(text string) bool {
	return strings.Contains(s.stdout.String(), text) ||
		strings.Contains(s.stderr.String(), text)
}

// waitForLog POLLS until the text appears in one of the two streams.
//
// The rationale for polling is in the [logTimeout] godoc; the rationale for
// there being no fixed sleep is the same as [proc.waitForReady]. On a timeout
// the process LOG is printed: without it the only information would be "the line
// did not appear", and the line never being written could not be told apart from
// it being written late.
func (s *proc) waitForLog(text string, timeout time.Duration) {
	s.t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		if s.logContains(text) {
			return
		}

		if time.Now().After(deadline) {
			s.t.Fatalf("%q did not appear in the process log within %s\n%s",
				text, timeout, s.logBuf())
		}

		time.Sleep(pollInterval)
	}
}

// commandTimeout is the maximum time granted to a NON-SERVER run of the binary.
//
// It is generous: `migrate status` reads one version per owner and every read
// makes a round trip to a container that was started cold. What this timeout
// catches is not slowness but the real fault: a run that never returns because
// it turned into a SERVER.
const commandTimeout = 60 * time.Second

// commandResult is the result of a single non-server run.
type commandResult struct {
	exitCode int
	stdout   string
	stderr   string
}

// runCommand runs the server binary WITH ARGUMENTS and waits for it to FINISH.
//
// It was deliberately not built on top of [startServer]: that helper starts the
// process and leaves it UP — the shape that is right for a server and wrong
// here. A subcommand that does not exit is exactly the fault meant to be caught,
// so the waiting is not left to the scenario's memory, it is part of the
// harness.
//
// The environment is built with [env], that is, in the SAME way as the server
// scenarios': the migrate commands load the EXACT SAME configuration the server
// loads, and a scenario handing them another environment would not have driven
// the path taken in production.
func runCommand(t *testing.T, cfg settings, args ...string) commandResult {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), commandTimeout)
	defer cancel()

	var stdout, stderr logBuffer

	cmd := exec.CommandContext(ctx, binaryPath, args...)
	cmd.Env = env(cfg)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	require.NotErrorIs(t, ctx.Err(), context.DeadlineExceeded,
		"`%s` did not exit within %s; a subcommand that keeps running is a subcommand that "+
			"turned into a server\n%s", strings.Join(args, " "), commandTimeout,
		commandResult{stdout: stdout.String(), stderr: stderr.String()}.logBuf())

	// A nil ProcessState means the process NEVER ran (missing binary, broken
	// environment) and no scenario expects that. A non-zero exit code, on the
	// contrary, is what a few scenarios DO EXPECT, which means err on its own
	// cannot be the criterion.
	if cmd.ProcessState == nil {
		t.Fatalf("could not run the binary with the arguments %v: %v\n--- stderr ---\n%s",
			args, err, stderr.String())
	}

	return commandResult{
		exitCode: cmd.ProcessState.ExitCode(),
		stdout:   stdout.String(),
		stderr:   stderr.String(),
	}
}

// logBuf merges the two streams into a single text for diagnostics; why the
// streams are labeled SEPARATELY is written in the [proc.logBuf] godoc.
func (c commandResult) logBuf() string {
	return fmt.Sprintf("--- stdout ---\n%s--- stderr ---\n%s--------------\n", c.stdout, c.stderr)
}

// nothingIsListening verifies that there is NO process bound to the port.
//
// This is the only real proof of the claim that subcommands do not start a
// server: an exit code tells not what a command did but how it ended.
func nothingIsListening(t *testing.T, port int) {
	t.Helper()

	conn, err := net.DialTimeout("tcp",
		net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), requestTimeout)
	if err == nil {
		_ = conn.Close()
		t.Fatalf("something is listening on port %d; the subcommand started a SERVER", port)
	}
}
