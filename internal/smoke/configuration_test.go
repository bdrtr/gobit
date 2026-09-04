//go:build smoke

package smoke

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/plugins/paymentstripe"
)

// TestBadConfigurationStopsAtStartup is scenario C: every flawed configuration
// stops AT STARTUP, with a non-zero exit code and an UNDERSTANDABLE message.
//
// # Why the exit code and the message together
//
// Had only the exit code been asserted, we would have settled for "it blew up
// somewhere" and a startup that blew up for the WRONG reason would have passed
// the test too. Had only the message been asserted, a setup that prints the
// message and then keeps on starting would go uncaught — and that is the most
// dangerous state of all: the operator sees the warning in the logs, believes the
// system is running, and ships to production with an incomplete configuration.
//
// # Why startup, not the first request
//
// All seven faults COULD HAVE BEEN deferred silently: an unknown plugin can be
// ignored, a missing key can blow up on the first payment, the shared guard can
// quietly fall back to in-memory, an unknown provider name can be made to wait
// until the first notification or the first upload. The price is the same in
// every one of them: the fault becomes visible DAYS after it left the hands of
// the person who wrote the configuration, most often in the middle of a customer
// request.
//
// # Why the provider scenarios are here
//
// The last two scenarios (the notification and the file provider) are the part of
// the configuration that is validated LAST in ORDER: both are checked AFTER the
// modules have come up, the workflows have been wired and the plugins have been
// started. So if the process dies on these, every setup step up to that point ran
// BEFORE the listener OPENED and ran SYNCHRONOUSLY. Quietly moving a setup step
// onto the background (into a goroutine) turns a setup failure from "startup
// stopped" into "500 on the first request", and these scenarios catch exactly
// that drift.
//
// # What it does not cover
//
// A configuration that makes workflow setup (cmd/server, registerWorkflows)
// ITSELF fail cannot be WRITTEN today: the workflows only resolve names the
// modules register unconditionally (cart/pricing/region/customer/order/payment/...
// and the core services) and no environment variable turns those modules'
// registration off. So the "workflow setup failed" state can only be produced by
// a CODE change; the closest guarantee here is the two checks above, which run
// AFTER workflow setup.
func TestBadConfigurationStopsAtStartup(t *testing.T) {
	// A CLOSED port is used for the unreachable Redis: the address is formally
	// valid, so the error is not "the URL could not be parsed" but really
	// "could not connect". That distinction is exactly what is under test.
	closedPort := freePort(t)

	cases := map[string]struct {
		// edit changes the base settings with the scenario's flaw.
		edit func(settings)
		// keys are the texts that MUST appear in stderr: one is the machine
		// code of the fault, the other is the name of the setting the operator
		// is going to fix.
		keys []string
	}{
		"unknown plugin name": {
			edit: func(s settings) {
				s["PLUGINS"] = "no-such-plugin"
			},
			keys: []string{"plugin_unknown", "no-such-plugin"},
		},
		"plugin is there but its setting is not": {
			edit: func(s settings) {
				// STRIPE_API_KEY is DELIBERATELY not given; since the process
				// environment is built from scratch (see env), it cannot leak
				// in from the shell either.
				s["PLUGINS"] = paymentstripe.Name
			},
			keys: []string{"STRIPE_API_KEY", paymentstripe.Name},
		},
		"shared guard backend unreachable": {
			edit: func(s settings) {
				s["GUARD_BACKEND"] = "redis"
				s["REDIS_URL"] = "redis://127.0.0.1:" + strconv.Itoa(closedPort) + "/0"
			},
			keys: []string{"redis_unreachable"},
		},
		"half a seed configuration": {
			edit: func(s settings) {
				// The password is DELIBERATELY missing: the state where the
				// operator wrote one of the two variables and forgot the other.
				s["ADMIN_BOOTSTRAP_EMAIL"] = seedEmail
			},
			keys: []string{"ADMIN_BOOTSTRAP_EMAIL", "ADMIN_BOOTSTRAP_PASSWORD"},
		},
		"no signing secret in a shared environment": {
			edit: func(s settings) {
				s["APP_ENV"] = "staging"
				delete(s, "JWT_SECRET")
			},
			keys: []string{"JWT_SECRET", "staging"},
		},
		"unknown notification provider": {
			edit: func(s settings) {
				// The name is picked so that no plugin and no out-of-the-box
				// provider can satisfy it: what is under test is not the "wrong
				// name" but the wrong name being caught AT STARTUP.
				s["NOTIFICATION_PROVIDER"] = "no-such-provider"
			},
			keys: []string{"notification_provider_unknown", "NOTIFICATION_PROVIDER"},
		},
		"unknown file provider": {
			edit: func(s settings) {
				s["FILE_PROVIDER"] = "no-such-provider"
			},
			keys: []string{"file_provider_unknown", "FILE_PROVIDER"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// Every subtest gets its OWN database: some of the flaws blow up
			// AFTER the migrations, and a shared database would carry the trace
			// of a half-finished startup over into the next scenario.
			cfg := baseSettings(scenarioDatabase(t), freePort(t))
			tc.edit(cfg)

			code, stderr := mustStopAtStartup(t, cfg, startupTimeout)

			assert.NotZero(t, code,
				"a bad configuration must give a non-zero exit code; stderr:\n%s", stderr)

			for _, key := range tc.keys {
				assert.Contains(t, stderr, key,
					"stderr must tell the operator what to fix; stderr:\n%s", stderr)
			}
		})
	}
}
