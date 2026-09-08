package cart

import (
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
// It sits beside the flow wrappers on purpose, because the contrast is the
// decision. A missing FLOW is an error — a cart cannot be opened without one at
// all, and [TestCartOpeningFlowFailsClosedWhenMissing] next door is that rule.
// A missing IDENTITY is not: the cart opens fine without one, it just cannot
// say whose it is, and refusing there would take a working surface away from an
// installation that never bound a verifier.

// providedIdentity is an embedder's implementation reduced to its answer.
type providedIdentity struct{ id string }

var _ corehttp.Identity = providedIdentity{}

func (p providedIdentity) CustomerID(*http.Request) (string, error) { return p.id, nil }

// TestTheIdentityBindingIsEmptyWhenNothingIsRegistered is the non-breaking half
// of ADR 0057, at the place that decides it.
//
// The first draft of this record returned coreerrors.Unauthorized here, and an
// installation that had been selling for a year would have stopped being able
// to open a cart for any customer at all on upgrade — and with it the b2b
// spending limit, which only binds a cart naming one. A nil identity is what
// leaves the surface answering; the residue is stated in the record and warned
// about in the log rather than charged to the embedder.
func TestTheIdentityBindingIsEmptyWhenNothingIsRegistered(t *testing.T) {
	t.Parallel()

	binding := &identityBinding{c: container.New(nil), log: silentLog()}

	identity, err := binding.identity(t.Context())

	require.NoError(t, err,
		"an installation that bound no identity is not a broken one, and an error here "+
			"is a refusal on every cart that names a customer")
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

	binding := &identityBinding{c: c, log: silentLog()}
	identity, err := binding.identity(t.Context())

	require.NoError(t, err)
	require.NotNil(t, identity)

	request, err := http.NewRequest(http.MethodPost, "/store/v1/carts", http.NoBody)
	require.NoError(t, err)
	proven, err := identity.CustomerID(request)
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
	require.NoError(t, c.Provide(corehttp.IdentityName, foreignType{}))

	binding := &identityBinding{c: c, log: silentLog()}
	identity, err := binding.identity(t.Context())

	require.Error(t, err)
	assert.Nil(t, identity)
	assert.Equal(t, codeSetupFailed, coreerrors.CodeOf(err))
	assert.Equal(t, coreerrors.KindInternal, coreerrors.KindOf(err),
		"the container reports a type mismatch as KindInvalid; inheriting it would tell "+
			"a client its request was invalid when no client could have written it "+
			"differently")
}
