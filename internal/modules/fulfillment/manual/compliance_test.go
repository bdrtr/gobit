package manual_test

import (
	"log/slog"
	"testing"

	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/core/providertest"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/manual"
)

// TestTheProviderIsCompliant runs the published compliance suite.
//
// The suite checks the identity every registry keys on, and the three shipment
// rules: the destination is not returned in the data, a repeated create opens
// no second shipment, a second cancel does not fail (ADR 0202). The identity is
// what an operator types into configuration and what durable rows record, and
// until this call existed nothing checked it at all. A provider whose identity carried a space
// would register under one string and answer with another, and the disagreement
// would surface as a startup failure on somebody's deploy.
//
// It is called from the provider's OWN package rather than from one central
// test, because that is how a provider written outside this repository runs it
// (ADR 0025: gobit is a library) — and a suite the in-tree providers use
// differently from an embedder is a suite that drifts.
func TestTheProviderIsCompliant(t *testing.T) {
	t.Parallel()

	providertest.Fulfillment(t, manual.New(newMemStore(), slog.New(slog.DiscardHandler)),
		coreprovider.CreateFulfillmentInput{Reference: "ful_compliance", OptionID: "so_compliance"})
}
