package errorsentry_test

import (
	"testing"

	"github.com/bdrtr/gobit/core/providertest"
	"github.com/bdrtr/gobit/plugins/errorsentry"
)

// TestTheProviderIsCompliant runs the published compliance suite.
//
// It is an EXTERNAL test on purpose: this is the shape a provider written
// outside this repository takes, and a suite the in-tree plugins called
// differently from an embedder would be a suite that drifts from the one people
// actually run (ADR 0025).
//
// The zero value is enough. The suite reads the identity and nothing else, and
// this provider's ID is a constant — so a fixture with a client, a key and an
// endpoint would be scaffolding for calls the suite does not make.
func TestTheProviderIsCompliant(t *testing.T) {
	t.Parallel()

	providertest.Identity(t, &errorsentry.Reporter{})
}
