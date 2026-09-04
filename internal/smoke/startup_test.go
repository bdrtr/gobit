//go:build smoke

package smoke

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authapi "github.com/bdrtr/gobit/internal/modules/auth/api"
)

// TestColdStartBecomesUsableWithAnEmptyDatabase is scenario A: a process opened
// against an empty database becomes USABLE without a single manual step.
//
// # Why a real process
//
// internal/e2e drives the same endpoints with httptest and finds them all green,
// but it builds the router ITSELF: it applies the migrations by hand, never runs
// the seed step and never loads the config. So it cannot answer the question "can
// somebody who clones the repository and runs it log in?" — this test asks exactly
// that, and walks the flow the README describes step by step.
func TestColdStartBecomesUsableWithAnEmptyDatabase(t *testing.T) {
	cfg := baseSettings(scenarioDatabase(t), freePort(t))
	cfg["ADMIN_BOOTSTRAP_EMAIL"] = seedEmail
	cfg["ADMIN_BOOTSTRAP_PASSWORD"] = seedPassword

	s := startServer(t, cfg)
	s.waitForReady(startupTimeout)

	t.Run("health endpoints", func(t *testing.T) {
		code, body := s.request(http.MethodGet, "/health", "")
		assert.Equal(t, http.StatusOK, code, "/health must return 200; body: %s", body)

		code, body = s.request(http.MethodGet, "/ready", "")
		require.Equal(t, http.StatusOK, code, "/ready must return 200; body: %s", body)

		// That /ready INCLUDES the postgres check is verified separately: if the
		// check list is empty the endpoint always returns 200, and the
		// orchestrator would keep sending traffic to this instance even while the
		// database is down.
		var ready struct {
			Status string `json:"status"`
			Checks map[string]struct {
				Status string `json:"status"`
				Error  string `json:"error,omitempty"`
			} `json:"checks"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &ready),
			"could not decode the /ready response; body: %s", body)

		assert.Equal(t, "ok", ready.Status, "readiness status must be ok")
		require.Contains(t, ready.Checks, "postgres",
			"/ready must include the postgres check; body: %s", body)
		assert.Equal(t, "ok", ready.Checks["postgres"].Status,
			"the postgres check must be ok; body: %s", body)
	})

	t.Run("README flow: login, token, protected endpoint", func(t *testing.T) {
		token := fetchToken(t, s, seedEmail, seedPassword)

		code, body := s.request(http.MethodGet, "/admin/v1/auth/me", token)
		assert.Equal(t, http.StatusOK, code,
			"the seeded admin must be able to reach a protected endpoint; body: %s", body)
	})

	t.Run("unauthenticated admin request gets 401", func(t *testing.T) {
		code, body := s.request(http.MethodGet, "/admin/v1/users", "")
		assert.Equal(t, http.StatusUnauthorized, code,
			"an unauthenticated admin request must be rejected; body: %s", body)
	})

	// An undefined admin path must return 401 as well, NOT 404: a 404 hands the
	// "this endpoint does not exist" fact to an unauthenticated caller, and the
	// admin surface's endpoint map leaks out through exactly such a difference in
	// responses. The guard must run BEFORE the path's EXISTENCE.
	t.Run("an undefined admin path returns 401, not 404", func(t *testing.T) {
		code, body := s.request(http.MethodGet, "/admin/v1/no-such-endpoint", "")
		assert.Equal(t, http.StatusUnauthorized, code,
			"an undefined admin path must not leak the endpoint map; body: %s", body)
	})
}

// TestMigrateSubcommandsRunWithoutStartingTheServer walks the operator surface with
// the REAL binary, in the order an operator would follow.
//
// # Why this is worth a process
//
// The unit test in cmd/server audits from the source that there is exactly ONE
// branch in the dispatch that can reach serve; the integration tests prove that a
// rollback moves the ledger. Neither can answer what this scenario asks, because
// both of them run INSIDE THE TEST BINARY: does the shipped executable, given these
// arguments, EXIT — and does it leave the port alone?
//
// MEASURED on the v0.7.0 binary: it did not. `gobit migrate status` and
// `gobit --help` loaded the configuration, applied every forward migration, bound
// the configured port and stayed up until they were killed. Every argument was a
// deployment.
//
// # Why one scenario and not five
//
// The steps SHARE the database and each one leaves the next a state; the sharing is
// exactly the point: a status table is only trustworthy if its numbers move when a
// rollback moves them. Split into separate tests, each would want its own container
// and would only ever see a single state.
func TestMigrateSubcommandsRunWithoutStartingTheServer(t *testing.T) {
	port := freePort(t)
	cfg := baseSettings(scenarioDatabase(t), port)

	t.Run("on an empty database status reports every owner and exits", func(t *testing.T) {
		result := runCommand(t, cfg, "migrate", "status")

		require.Zero(t, result.exitCode,
			"a fresh database is not an error condition\n%s", result.logBuf())
		assert.Contains(t, result.stdout, "OWNER", "the status table was not printed")
		assert.Contains(t, result.stdout, "cart",
			"a module the server migrates is missing from the report\n%s", result.logBuf())
		assert.Contains(t, result.stdout, "nothing applied",
			"an empty database must be reported in words too, not only as a 0\n%s", result.logBuf())

		nothingIsListening(t, port)
	})

	t.Run("an unrecognized command exits non-zero and starts nothing", func(t *testing.T) {
		result := runCommand(t, cfg, "migrate-down")

		assert.NotZero(t, result.exitCode,
			"an unrecognized argument was accepted\n%s", result.logBuf())
		assert.Contains(t, result.stderr, "unknown command",
			"the refusal must land where a script looks — on stderr\n%s", result.logBuf())

		nothingIsListening(t, port)
	})

	// The server is started only NOW and with NO ARGUMENTS: everything above ran
	// with the same binary, the same environment and the same port, so the only
	// difference between "nothing is listening" and "the shop is up" is the
	// arguments.
	s := startServer(t, cfg)
	s.waitForReady(startupTimeout)

	t.Run("status reports the versions startup applied", func(t *testing.T) {
		result := runCommand(t, cfg, "migrate", "status")

		require.Zero(t, result.exitCode, "%s", result.logBuf())
		assert.Contains(t, result.stdout, "applied",
			"startup moved the schema forward, the report does not show it\n%s", result.logBuf())
		assert.NotContains(t, result.stdout, "DIRTY",
			"a clean startup left a dirty ledger behind it\n%s", result.logBuf())
	})

	t.Run("a rollback without confirmation changes nothing", func(t *testing.T) {
		result := runCommand(t, cfg, "migrate", "down", "cart")

		assert.NotZero(t, result.exitCode,
			"a rollback without confirmation reported SUCCESS; a script that forgot the "+
				"flag would believe it had rolled back\n%s", result.logBuf())
		assert.Contains(t, result.stdout, "REFUSED", "%s", result.logBuf())
		assert.Contains(t, result.stdout, "-confirm cart",
			"the refusal must spell out the command that carries on, letter for letter\n%s",
			result.logBuf())

		after := runCommand(t, cfg, "migrate", "status")
		assert.Contains(t, cartRow(t, after.stdout), "applied",
			"the ledger moved without a confirmation\n%s", after.logBuf())
	})

	t.Run("a confirmed rollback moves the ledger", func(t *testing.T) {
		result := runCommand(t, cfg, "migrate", "down", "cart", "-confirm", "cart")

		require.Zero(t, result.exitCode, "%s", result.logBuf())
		assert.Contains(t, result.stdout, "is now at version",
			"the rollback must report the version it READ BACK\n%s", result.logBuf())

		after := runCommand(t, cfg, "migrate", "status")
		assert.Contains(t, cartRow(t, after.stdout), "nothing applied",
			"the confirmed rollback did not reach the ledger\n%s", after.logBuf())
	})
}

// cartRow returns the cart owner's row from the status table.
//
// Looking at the whole table would pass on any owner's word: across sixteen rows
// "applied" is found whether or not the owner that changed is cart.
func cartRow(t *testing.T, table string) string {
	t.Helper()

	for _, line := range strings.Split(table, "\n") {
		if strings.HasPrefix(line, "cart ") {
			return line
		}
	}

	t.Fatalf("no cart row in the status table:\n%s", table)

	return ""
}

// fetchToken obtains a session token from the login endpoint.
//
// The body is built by hand, the auth module's DTO is NOT imported: this test
// exercises the CONTRACT that travels over the wire, and sharing a Go type would
// let the test stay green even if a field name changed. The path, on the other
// hand, is read from the constant (authapi.LoginPath); there is no leak risk there
// — on the contrary, writing the path out by hand would drift in two places.
func fetchToken(t *testing.T, s *proc, email, password string) string {
	t.Helper()

	body, err := json.Marshal(map[string]string{"email": email, "password": password})
	require.NoError(t, err, "could not encode the login body")

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		s.addr+authapi.LoginPath, bytes.NewReader(body))
	require.NoError(t, err, "could not build the login request")
	req.Header.Set("Content-Type", "application/json")

	code, respBody := s.send(req)
	require.Equal(t, http.StatusOK, code, "login must return 200; body: %s", respBody)

	var envelope struct {
		Data struct {
			Token     string `json:"token"`
			TokenType string `json:"token_type"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(respBody), &envelope),
		"could not decode the login response; body: %s", respBody)
	require.NotEmpty(t, envelope.Data.Token, "login must return a token; body: %s", respBody)
	assert.Equal(t, "Bearer", envelope.Data.TokenType,
		"the client must learn which scheme to use from the response")

	return envelope.Data.Token
}
