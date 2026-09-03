//go:build integration

package db_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/db"
)

// This file proves that opening a pool against a cluster which cannot fold
// non-ASCII case SAYS SO, and that a correctly created cluster stays quiet.
//
// # Why two containers
//
// One container cannot prove anything here. A probe hard-wired to "everything
// is fine" would pass a single-cluster test, and that is precisely the bug the
// probe exists to prevent — a silent all-clear. The claim is that the check
// DISCRIMINATES, so both sides have to be shown.
//
// # Why the assertion is on the LOG
//
// The log line is what reaches the operator. Asserting on a returned struct
// would prove the query works while leaving the thing an operator actually
// depends on — a warning they can see at startup — untested.

// caseFoldingWarning is the message [db.New] logs for a cluster that folds
// ASCII only. It is repeated here by hand on purpose: this test's job is to
// notice when the sentence an operator greps for changes.
const caseFoldingWarning = "folds ASCII case only"

// clusterWithLocale starts a Postgres container created with the given initdb
// locale and returns its DSN.
func clusterWithLocale(t *testing.T, locale string) string {
	t.Helper()

	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_locale"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
		testcontainers.WithEnv(map[string]string{
			"POSTGRES_INITDB_ARGS": "--encoding=UTF8 --locale=" + locale,
		}),
	)
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(ctr) })
	require.NoError(t, err, "the %s container could not be started", locale)

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	return dsn
}

// openAndCaptureLog opens a pool against the DSN and returns what was logged.
func openAndCaptureLog(t *testing.T, dsn string) string {
	t.Helper()

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	pool, err := db.New(context.Background(), db.DefaultConfig(dsn), log)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return buf.String()
}

// TestAnAsciiOnlyClusterWarnsAtStartup is the whole point of the check.
//
// A cluster created with --locale=C folds ASCII only, so the storefront's
// `title ILIKE '%q%'` filter and the search plugin's to_tsvector both stop
// matching non-ASCII text. Nothing else in the system notices: the query
// succeeds, returns nothing, and the search box looks empty rather than broken.
func TestAnAsciiOnlyClusterWarnsAtStartup(t *testing.T) {
	t.Parallel()

	output := openAndCaptureLog(t, clusterWithLocale(t, "C"))

	require.Contains(t, output, caseFoldingWarning,
		"a cluster that cannot fold non-ASCII case must say so at startup")

	line := logLineContaining(t, output, caseFoldingWarning)
	assert.Equal(t, false, line["pattern_matching"],
		"ILIKE is what the storefront's own ?q= filter uses")
	assert.Equal(t, false, line["full_text"],
		"to_tsvector is what the search plugin indexes with")
	assert.Contains(t, line["fix"], "C.UTF-8",
		"the warning has to name the fix; an operator cannot act on a diagnosis alone")
}

// TestAUtf8ClusterOpensQuietly proves the check discriminates.
//
// Without this half, a probe that always reported a problem would pass the test
// above, and every correctly configured installation would be told its search
// is broken — which is the fastest way to teach an operator to ignore warnings.
func TestAUtf8ClusterOpensQuietly(t *testing.T) {
	t.Parallel()

	output := openAndCaptureLog(t, clusterWithLocale(t, "C.UTF-8"))

	assert.NotContains(t, output, caseFoldingWarning,
		"a cluster that folds non-ASCII case correctly must not be warned about")
	assert.Contains(t, output, "the postgres connection pool is ready",
		"the pool still opens normally")
}

// TestAnIcuClusterIsStillWarnedAbout is the case that makes checking BOTH
// halves necessary rather than tidy.
//
// A cluster created with the ICU provider folds ILIKE correctly and does NOT
// fold to_tsvector, because the text-search parser keeps using the database
// CTYPE. A check that asked only "does ILIKE work" would hand this
// installation a clean bill of health while its product search stayed silently
// broken — the exact half-fix that looks like a fix.
//
// The two flags are therefore reported independently, so the warning tells an
// operator WHICH search path is affected instead of only that something is.
func TestAnIcuClusterIsStillWarnedAbout(t *testing.T) {
	t.Parallel()

	output := openAndCaptureLog(t, clusterWithLocale(t, "C --locale-provider=icu --icu-locale=tr-TR"))

	require.Contains(t, output, caseFoldingWarning,
		"folding ILIKE is not enough; the search index is what the plugin reads")

	line := logLineContaining(t, output, caseFoldingWarning)
	assert.Equal(t, true, line["pattern_matching"],
		"ICU does fold the pattern matcher, and the warning must say so")
	assert.Equal(t, false, line["full_text"],
		"ICU does NOT fold the text-search parser; this is the half that stays broken")
}

// logLineContaining returns the decoded JSON log record carrying the substring.
func logLineContaining(t *testing.T, output, needle string) map[string]any {
	t.Helper()

	for _, raw := range strings.Split(strings.TrimSpace(output), "\n") {
		if !strings.Contains(raw, needle) {
			continue
		}
		var line map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &line))

		return line
	}
	t.Fatalf("no log line carried %q", needle)

	return nil
}
