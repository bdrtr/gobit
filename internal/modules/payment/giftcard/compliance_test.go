package giftcard_test

import (
	"log/slog"
	"testing"

	"github.com/bdrtr/gobit/core/providertest"
	"github.com/bdrtr/gobit/internal/modules/payment/giftcard"
)

// TestTheProviderIsCompliant runs the published compliance suite, from the
// provider's own package as an embedder's provider runs it (ADR 0025).
func TestTheProviderIsCompliant(t *testing.T) {
	t.Parallel()

	// The store is nil: the suite reads the identity and touches nothing else.
	providertest.Identity(t, giftcard.New(nil, slog.New(slog.DiscardHandler)))
}
