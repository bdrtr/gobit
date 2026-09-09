package manual_test

import (
	"log/slog"
	"testing"

	"github.com/bdrtr/gobit/core/providertest"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
)

// TestTheProviderIsCompliant runs the published compliance suite.
//
// The suite checks the identity every registry keys on: it is what an operator
// types into configuration and what durable rows record, and until this call
// existed nothing checked it at all. A provider whose identity carried a space
// would register under one string and answer with another, and the disagreement
// would surface as a startup failure on somebody's deploy.
//
// It is called from the provider's OWN package rather than from one central
// test, because that is how a provider written outside this repository runs it
// (ADR 0025: gobit is a library) — and a suite the in-tree providers use
// differently from an embedder is a suite that drifts.
func TestTheProviderIsCompliant(t *testing.T) {
	t.Parallel()

	providertest.Identity(t, newForCompliance(t))
}

// newForCompliance builds the provider with the least the constructor needs.
//
// The store is nil: the suite reads the identity and calls nothing that would
// touch it. A fake store here would be scaffolding for a call this suite does
// not make.
func newForCompliance(t *testing.T) *manual.Provider {
	t.Helper()

	return manual.New(nil, slog.New(slog.DiscardHandler))
}
