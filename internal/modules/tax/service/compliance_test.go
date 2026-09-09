package service

import (
	"testing"

	"github.com/bdrtr/gobit/core/providertest"
)

// TestTheLocalProviderIsCompliant runs the published compliance suite.
//
// It is an INTERNAL test because this package's tests are internal already, and
// the provider's rate source is nil: the suite reads the identity and calls
// nothing that would consult it.
//
// The identity matters here for a reason the other providers do not share: a
// tax provider's id is recorded against the amounts a shop reports to an
// authority, so an identity that changed between the registration and the write
// would leave rows attributing a calculation to a provider nobody can name.
func TestTheLocalProviderIsCompliant(t *testing.T) {
	t.Parallel()

	providertest.Identity(t, NewLocalProvider(nil))
}
