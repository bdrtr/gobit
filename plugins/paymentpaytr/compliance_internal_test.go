package paymentpaytr

import (
	"testing"

	"github.com/bdrtr/gobit/core/providertest"
)

// TestTheProviderIsCompliant runs the published compliance suite.
//
// It is an INTERNAL test because this plugin's provider type is unexported —
// what the plugin exports is the plugin, and the provider is built during
// Setup. An external test could not name the type, and exporting it to satisfy
// a test would widen the plugin's surface for no reader.
//
// The zero value is enough: the suite reads the identity and nothing else, and
// this provider's ID is a constant.
func TestTheProviderIsCompliant(t *testing.T) {
	t.Parallel()

	providertest.Identity(t, &provider{})
}
