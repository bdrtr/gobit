//go:build smoke

package smoke

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file pins down WHICH paths the rate limit covers, in a real process.
//
// # Why a smoke scenario
//
// The coverage is given by a single line in the composition root (guardStack's
// GuardOptions.OpenPrefixes field) and a gap in that line brings nothing down:
// the endpoint keeps working, it just works without a quota. So the fault is
// exactly of this repository's recurring class — the rule is defined in one
// place, where it is applied is invisible. Only a run that REALLY fills the
// quota and looks at what happens can prove the coverage.
//
// # Why both directions are tested at once
//
// Had only the "is it under quota" question been asked, a stack that covered
// every path would have passed the test too — whereas health endpoints hitting
// the quota means THE ORCHESTRATOR pulls a healthy instance out of traffic, and
// that is a more expensive fault than having no quota at all (see the
// corehttp.GuardOptions.OpenPrefixes godoc). That is why the test asserts both
// what is covered and what is not.

// quotaLimit is the number of requests per minute the scenario allows.
//
// It is small so that the limit fills in a few requests: the aim is not to
// measure the limiter's ALGORITHM (that is the unit tests' job), but to see
// that the coverage is right.
const quotaLimit = 3

// TestQuotaCoverageInRealProcess verifies the rate limit's coverage on the
// identity-less endpoints: /openapi.json IS subject to the quota, /health and
// /ready are NOT.
//
// # Why /openapi.json is under quota
//
// Its client is a code generator or an IDE and it sends no header, so the
// endpoint is identity-less. But it is not free: every request walks the route
// tree and verifies that the cache is still valid, and when the tree changes
// every module's DTOs are turned into a schema again by reflection. Identity
// and quota are SEPARATE decisions.
//
// # Why /health and /ready are not under quota
//
// The client calling them is the orchestrator, and a late answer is read as
// "this instance is sick". A health endpoint that hits the quota gets a healthy
// instance pulled out of traffic — that is, the limit itself PRODUCES the
// fault.
func TestQuotaCoverageInRealProcess(t *testing.T) {
	dsn := scenarioDatabase(t)

	settings := baseSettings(dsn, freePort(t))
	settings["RATE_LIMIT_PER_MINUTE"] = "3"

	s := startServer(t, settings)
	s.waitForReady(startupTimeout)

	t.Run("/openapi.json is subject to the quota", func(t *testing.T) {
		// The first request must definitely be able to produce the document: if
		// no 200 arrives, the thing under test would be the document itself,
		// not the quota.
		status, body := s.request(http.MethodGet, openAPISmokePath, "")
		require.Equal(t, http.StatusOK, status,
			"the document endpoint must work on the first request; body: %s", body)

		assert.True(t, hitsQuota(t, s, openAPISmokePath),
			"/openapi.json MUST hit the rate limit. If it does not, the path is "+
				"not in guardStack's OpenPrefixes list and the endpoint becomes a "+
				"load that can be thrown at us without even paying the cost of "+
				"authentication")
	})

	t.Run("health endpoints are not under quota", func(t *testing.T) {
		for _, path := range []string{"/health", "/ready"} {
			assert.False(t, hitsQuota(t, s, path),
				"%s MUST NOT hit the rate limit: the one calling it is the "+
					"orchestrator, and a 429 means a healthy instance is pulled "+
					"out of traffic", path)
		}
	})
}

// openAPISmokePath is the path of the document endpoint.
//
// The path is written out by hand because its constant lives in the main
// package and the main package CANNOT be imported. If the two drift apart the
// test gets a 404 on the first request and the diagnostic message says so —
// that is, it does not silently start testing the wrong thing.
const openAPISmokePath = "/openapi.json"

// hitsQuota sends requests over the limit to the given path and reports whether
// a 429 was seen.
//
// HOW MANY requests fill the limit is NOT asserted: the bucket is time-based
// and the scenario's own warm-up requests count too. The assertion is only the
// "is this path being counted" question, and that question is answered
// definitively by a single 429.
func hitsQuota(t *testing.T, s *proc, path string) bool {
	t.Helper()

	for range quotaLimit * 3 {
		if status, _ := s.request(http.MethodGet, path, ""); status == http.StatusTooManyRequests {
			return true
		}
	}
	return false
}
