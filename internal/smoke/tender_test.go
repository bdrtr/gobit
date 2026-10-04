//go:build smoke

package smoke

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// manualProviderID is the manual provider's id, spelled here rather than
// imported for the reason [envelopeData] gives: the lane reads what the process
// answers, not what the module says it would.
const manualProviderID = "manual"

// TestAProductionInstallationOffersNoManualProvider is ADR 0283 through the
// real binary: a process started with APP_ENV=production does not offer the
// manual provider to a shopper. It offers the offline method it names, which
// is ADR 0284's setting reaching the payment module through the same root.
//
// The manual provider authorizes and captures whatever the caller names, so a
// shopper who could choose it would place a paid order without paying. The hop
// this lane holds and no other does is the composition root turning APP_ENV
// into the payment module's ManualProvider, negated: the module's tests hand
// the option in themselves and the e2e harness builds its own root. The
// development process of [TestTheB2BStorefrontRefusesAnUnverifiedClaimInARealProcess]
// reads the same list and finds the provider there, so this one cannot pass
// because the provider is gone everywhere.
func TestAProductionInstallationOffersNoManualProvider(t *testing.T) {
	dsn := scenarioDatabase(t)

	cfg := baseSettings(dsn, freePort(t))
	cfg["APP_ENV"] = "production"
	// Production refuses the development default; nothing dials this one, as
	// the guard and the bus are in memory.
	cfg["REDIS_URL"] = "redis://127.0.0.1:" + strconv.Itoa(freePort(t)) + "/0"
	cfg["ADMIN_BOOTSTRAP_EMAIL"] = seedEmail
	cfg["ADMIN_BOOTSTRAP_PASSWORD"] = seedPassword
	cfg["PAYMENT_OFFLINE_METHODS"] = "bank_transfer"

	s := startServer(t, cfg)
	s.waitForReady(startupTimeout)

	_, _, storefrontKey := setUpAdminHarness(t, s, "Smoke Production Channel")
	tenders := storefrontTenders(t, s, storefrontKey)
	require.NotEmpty(t, tenders, "the provider list read nothing, so it proves nothing")
	assert.NotContains(t, tenders, manualProviderID,
		"a production installation must not offer a tender that places a paid order without payment")
	assert.Contains(t, tenders, "bank_transfer",
		"the offline method the installation named is offered; its absence means "+
			"PAYMENT_OFFLINE_METHODS never reached the payment module")
}
