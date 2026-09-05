//go:build smoke

package smoke

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
)

// concurrentInstanceCount is the number of instances started against the same
// empty database at the same time.
//
// It is three because that is exactly how the fault was seen: on the first
// deployment to Kubernetes with replicas:3, TWO pods went into a crash loop. Two
// instances would show the race as well, but three also answers the question
// "does EVERY losing instance carry on" — if the fix had rescued only the second
// instance, a two-instance test could not have seen that.
const concurrentInstanceCount = 3

// TestConcurrentStartupCreatesSingleAdmin is scenario B: three instances start
// against the SAME empty database at the same time, all three become healthy and
// the database holds exactly ONE admin.
//
// # The fault it catches
//
// The seeding step works as "create one if there is no user at all". When three
// instances start at the same time ALL THREE see "there is no user", all three
// try to create one and the email uniqueness constraint rejects two of them. In
// a setup that counts the conflict as an error, two instances die with
// "admin_bootstrap_failed"; the deployment looks broken even though the desired
// end state ("an admin exists") has been reached.
//
// # Why a unit test was not enough
//
// The seeding logic in cmd/server has a unit test written against a fake service
// and it covers the conflict branch too. But that test does not prove the
// conflict is REALLY produced: what produces the real race is three separate
// PROCESSES hitting the same PostgreSQL uniqueness constraint at the same time.
// This test sets up the race itself; if the fix is reverted (the
// errors.IsConflict branch in internal/app/setup.go) two instances fail to start
// and the test fails.
func TestConcurrentStartupCreatesSingleAdmin(t *testing.T) {
	dsn := scenarioDatabase(t)

	procs := make([]*proc, 0, concurrentInstanceCount)
	for i := range concurrentInstanceCount {
		cfg := baseSettings(dsn, freePort(t))
		cfg["ADMIN_BOOTSTRAP_EMAIL"] = seedEmail
		cfg["ADMIN_BOOTSTRAP_PASSWORD"] = seedPassword

		// The REAL configuration of a multi-instance deployment: the in-memory
		// guard backend does not work across instances (see the
		// config.GuardBackend godoc), so nobody uses it in a three-instance
		// setup. The scenario has to drive the wiring that is seen in
		// production, not the convenient one.
		cfg["GUARD_BACKEND"] = "redis"
		cfg["REDIS_URL"] = redisURL
		cfg["REDIS_KEY_PREFIX"] = "smoke-race-" + strconv.Itoa(i)

		procs = append(procs, startServer(t, cfg))
	}

	// The processes are started IN SEQUENCE but the race is real all the same:
	// exec.Start takes milliseconds, while the seeding step runs at the END of
	// startup — after the migrations of the core and the thirteen modules, that
	// is, seconds later. By then all three have caught up with each other.
	for i, s := range procs {
		s.waitForReady(startupTimeout)

		code, body := s.request(http.MethodGet, "/health", "")
		assert.Equal(t, http.StatusOK, code,
			"instance %d must be healthy; body: %s", i, body)
	}

	assert.Equal(t, int64(1), adminCount(t, dsn),
		"a concurrent startup must leave exactly ONE admin: the instances that "+
			"lose the race must skip the seeding, not create a second admin")

	// The token must be valid on all three instances: that is the proof that the
	// losing instances can work with the seeded admin too. An instance that is
	// "healthy but cannot be logged into" is a fault that a test looking at
	// /health could never see.
	token := fetchToken(t, procs[0], seedEmail, seedPassword)
	for i, s := range procs {
		code, body := s.request(http.MethodGet, "/admin/v1/auth/me", token)
		assert.Equal(t, http.StatusOK, code,
			"instance %d must accept the seeded admin's token; body: %s", i, body)
	}
}

// adminCount returns the number of non-deleted admins in the scenario database.
//
// The query goes DIRECTLY to the auth module's table; standing the module's
// service up would add setup cost without adding anything to what the test
// checks (the end state the three processes left behind in the database).
func adminCount(t *testing.T, dsn string) int64 {
	t.Helper()

	pool, err := db.New(t.Context(), db.DefaultConfig(dsn), nil)
	require.NoError(t, err, "could not connect to the scenario database")
	defer pool.Close()

	var count int64
	require.NoError(t,
		pool.Pool().QueryRow(t.Context(),
			"SELECT count(*) FROM auth_user WHERE deleted_at IS NULL").Scan(&count),
		"could not read the admin count")

	return count
}
