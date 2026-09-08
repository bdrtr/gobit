package b2b

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// This file holds the branch ADR 0057 turns on: what the storefront is handed
// when the installation bound NO verifier.
//
// It is here rather than in the api package because the answer is the
// container's. The api package can only be told "nothing is bound"; this is
// where that sentence is produced, and where a future change that turned it
// back into a refusal would land.

// providedIdentity is an embedder's implementation reduced to its answer.
type providedIdentity struct{ id string }

var _ corehttp.Identity = providedIdentity{}

func (p providedIdentity) CustomerID(*http.Request) (string, error) { return p.id, nil }

// notAnIdentity stands under the right NAME and does not satisfy the contract.
type notAnIdentity struct{}

// TestTheIdentityBindingIsEmptyWhenNothingIsRegistered is the non-breaking half
// of ADR 0057, at the place that decides it.
//
// An empty container is an installation that never read ADR 0008, and the
// answer it gets is a NIL identity and no error — not a refusal. The first
// draft of this record returned coreerrors.Unauthorized here, which turned
// every b2b storefront read in such an installation into a 401 on upgrade.
// A nil identity is what leaves those routes answering, and the residue is
// stated in the record instead of charged to the embedder.
func TestTheIdentityBindingIsEmptyWhenNothingIsRegistered(t *testing.T) {
	t.Parallel()

	binding := &identityBinding{c: container.New(nil), log: slog.New(slog.DiscardHandler)}

	identity, err := binding.identity(t.Context())

	require.NoError(t, err,
		"an installation that bound no identity is not a broken one, and an error here "+
			"is a 401 on every b2b storefront read it makes")
	assert.Nil(t, identity,
		"the handler decides what an absent verifier means, and it can only decide it "+
			"if the absence ARRIVES; a non-nil stand-in would be asked and would answer")
}

// TestTheIdentityBindingReturnsWhatTheEmbedderProvided is the other half.
//
// Without it the test above passes on a binding that resolves nothing ever,
// which is the shape of a closed hole that deleted the feature.
func TestTheIdentityBindingReturnsWhatTheEmbedderProvided(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide(corehttp.IdentityName, providedIdentity{id: "cus_1"}))

	binding := &identityBinding{c: c, log: slog.New(slog.DiscardHandler)}
	identity, err := binding.identity(t.Context())

	require.NoError(t, err)
	require.NotNil(t, identity)

	proven, err := identity.CustomerID(httpRequest())
	require.NoError(t, err)
	assert.Equal(t, "cus_1", proven)
}

// TestTheIdentityBindingRefusesAnIncompatibleType keeps the two absences apart.
//
// "Nobody registered one" is a deployment decision and answers with a nil.
// "Somebody registered the wrong thing under the published name" is a WIRING
// error, and folding it into the first would let a typo silently disable a
// check the installation believes it configured.
func TestTheIdentityBindingRefusesAnIncompatibleType(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide(corehttp.IdentityName, notAnIdentity{}))

	binding := &identityBinding{c: c, log: slog.New(slog.DiscardHandler)}
	identity, err := binding.identity(t.Context())

	require.Error(t, err)
	assert.Nil(t, identity)
	assert.Equal(t, codeSetupFailed, coreerrors.CodeOf(err))
	assert.Equal(t, coreerrors.KindInternal, coreerrors.KindOf(err),
		"the container reports a type mismatch as KindInvalid; inheriting it would tell "+
			"a client its request was invalid when no client could have written it "+
			"differently")
}

// httpRequest is a bare storefront request; the identities here read nothing
// out of it.
func httpRequest() *http.Request {
	r, _ := http.NewRequest(http.MethodGet, "/store/v1/b2b/customers/cus_1/company", http.NoBody)

	return r
}
