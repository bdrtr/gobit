package identity_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/core/identity"
)

// provenID is the customer the bound identity in these tests proves.
const provenID = "cust_PROVEN"

// setupCode is the module's own code for a wiring fault.
const setupCode = "test_module_setup_failed"

// stubIdentity is an embedder's implementation, reduced to its answer.
type stubIdentity struct{ id string }

func (s stubIdentity) CustomerID(*http.Request) (string, error) { return s.id, nil }

// request is a storefront request; its context carries the resolve.
func request() *http.Request {
	return httptest.NewRequest(http.MethodGet, "/store/v1/customers/"+provenID, http.NoBody)
}

// bind builds a binding over the container the way a module's Register does,
// its log written to the buffer.
func bind(c *container.Container, logged *bytes.Buffer) *identity.Binding {
	return identity.New(c, slog.New(slog.NewTextHandler(logged, nil)), "test", setupCode, "the routes refuse")
}

// TestAProofAskedOfNothingIsRefused is the closed-by-default row of the
// address book, a balance and a list of orders (ADR 0043).
//
// An empty identifier and no error would be the worst of the available
// failures: the handler would compare "" against the path, refuse with a
// MISMATCH, and send an operator hunting for a wrong session instead of a
// missing binding.
func TestAProofAskedOfNothingIsRefused(t *testing.T) {
	t.Parallel()

	proven, err := bind(container.New(nil), &bytes.Buffer{}).CustomerID(request())
	require.Error(t, err)
	assert.Empty(t, proven)
	assert.Equal(t, corehttp.CodeIdentityNotBound, errors.CodeOf(err),
		"the refusal has to name the binding that is missing")
	assert.True(t, errors.HasKind(err, errors.KindUnauthorized),
		"a deployment that bound nothing is a request nobody could vouch for, not a server fault")
}

// TestAHandlerAskingForTheIdentityIsHandedNothing is the cart's and b2b's
// door: the handler is told nobody is bound and decides (ADR 0125).
func TestAHandlerAskingForTheIdentityIsHandedNothing(t *testing.T) {
	t.Parallel()

	held, err := bind(container.New(nil), &bytes.Buffer{}).Identity(t.Context())
	require.NoError(t, err, "an installation that bound nothing is not a broken one")
	assert.Nil(t, held, "the absence has to ARRIVE for the handler to decide on it")
}

// TestABoundIdentityIsAskedAndItsAnswerReturned is the ordinary path, through
// both doors.
func TestABoundIdentityIsAskedAndItsAnswerReturned(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide(corehttp.IdentityName, stubIdentity{id: provenID}))

	binding := bind(c, &bytes.Buffer{})
	proven, err := binding.CustomerID(request())
	require.NoError(t, err)
	assert.Equal(t, provenID, proven)
	held, err := binding.Identity(t.Context())
	require.NoError(t, err)
	assert.Equal(t, stubIdentity{id: provenID}, held)
}

// TestTheDecisionIsMadeOnce pins the sync.Once by what it changes: an identity
// registered after the first request is not looked for again, so the answer
// depends on what the installation registered and not on when a request came.
//
// Counting a constructor's runs would not show it — the container keeps what
// it built, so a binding that resolved on every request would count one too.
func TestTheDecisionIsMadeOnce(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	binding := bind(c, &bytes.Buffer{})
	_, err := binding.CustomerID(request())
	require.Error(t, err)

	require.NoError(t, c.Provide(corehttp.IdentityName, stubIdentity{id: provenID}))
	_, err = binding.CustomerID(request())
	require.Error(t, err, "the first answer is remembered")
	assert.Equal(t, corehttp.CodeIdentityNotBound, errors.CodeOf(err))
}

// TestAWrongTypeIsAServerFaultNamingTheModule keeps a wiring mistake from
// being read as a client's or as a missing binding.
//
// The container reports a type mismatch as KindInvalid; inheriting it would
// answer a 422 for a request no client could have written differently.
func TestAWrongTypeIsAServerFaultNamingTheModule(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide(corehttp.IdentityName, "not an identity"))

	binding := bind(c, &bytes.Buffer{})
	for name, ask := range map[string]func() error{
		"a proof":      func() error { _, err := binding.CustomerID(request()); return err },
		"the identity": func() error { _, err := binding.Identity(t.Context()); return err },
	} {
		err := ask()
		require.Error(t, err, name)
		assert.True(t, errors.HasKind(err, errors.KindInternal), "%s: a wrong registration is a server fault: %v", name, err)
		assert.Equal(t, setupCode, errors.CodeOf(err), "%s: and it is the module's own setup fault", name)
		assert.Contains(t, err.Error(), "the test module")
	}
}

// TestTheOperatorIsWarnedInTheModulesWords: with nothing bound, the first
// request warns with the module's own sentence and names the module; bound,
// nothing is warned.
func TestTheOperatorIsWarnedInTheModulesWords(t *testing.T) {
	t.Parallel()

	var absent bytes.Buffer
	_, _ = bind(container.New(nil), &absent).Identity(t.Context())
	assert.Contains(t, absent.String(), "level=WARN")
	assert.Contains(t, absent.String(), "the routes refuse")
	assert.Contains(t, absent.String(), "module=test")

	c := container.New(nil)
	require.NoError(t, c.Provide(corehttp.IdentityName, stubIdentity{id: provenID}))
	var bound bytes.Buffer
	_, _ = bind(c, &bound).Identity(t.Context())
	assert.NotContains(t, bound.String(), "level=WARN")
	assert.Contains(t, bound.String(), "customer identity bound")
}
