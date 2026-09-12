package storecredit_test

import (
	"log/slog"
	"testing"

	"github.com/bdrtr/gobit/core/providertest"
	"github.com/bdrtr/gobit/internal/modules/payment/storecredit"
)

// TestTheProviderIsCompliant runs the published compliance suite.
//
// It is called from the provider's OWN package for the reason the manual
// provider's is: that is how a provider written outside this repository runs it
// (ADR 0025), and a suite the in-tree providers use differently from an embedder
// is a suite that drifts.
func TestTheProviderIsCompliant(t *testing.T) {
	t.Parallel()

	providertest.Identity(t, newForCompliance(t))
}

// newForCompliance builds the provider with the least the constructor needs.
//
// The store is nil: the suite reads the identity and calls nothing that would
// touch it.
func newForCompliance(t *testing.T) *storecredit.Provider {
	t.Helper()

	return storecredit.New(nil, slog.New(slog.DiscardHandler))
}
