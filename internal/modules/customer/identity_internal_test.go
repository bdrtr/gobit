package customer

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The test is INTERNAL because [identityBinding] is unexported and has to stay
// that way: it is wiring, not a surface. Reaching it from outside would mean
// exporting the wrapper, and an exported wrapper invites an embedder to build
// one instead of registering an identity under corehttp.IdentityName, which is
// the seam the whole decision rests on.

// provenID is the customer the bound identity in these tests proves.
const provenID = "cust_PROVEN"

// stubIdentity is an embedder's implementation, reduced to its answer.
type stubIdentity struct{ id string }

func (s stubIdentity) CustomerID(*http.Request) (string, error) { return s.id, nil }

// request is the request handed to the wrapper. Its context is what carries the
// resolve, so it is a real one rather than nil.
func request() *http.Request {
	return httptest.NewRequest(http.MethodGet, "/store/v1/customers/"+provenID, http.NoBody)
}

// newBinding builds the wrapper over a container, the way Register does.
func newBinding(c *container.Container) *identityBinding {
	return &identityBinding{c: c, log: slog.New(slog.DiscardHandler)}
}

// TestAnUnboundIdentityRefusesInsteadOfAnsweringEmpty is the closed-by-default
// row, held at the wrapper rather than at the handler.
//
// The handler has its own nil check and this is the second half of the same
// answer: an installation that never registers the name must not be able to
// reach the address book through a wrapper that shrugged. Returning an empty
// identifier and no error here would be the worst of the available failures —
// the handler would compare "" against the path, refuse with a MISMATCH, and
// send an operator hunting for a wrong session instead of a missing binding.
func TestAnUnboundIdentityRefusesInsteadOfAnsweringEmpty(t *testing.T) {
	t.Parallel()

	binding := newBinding(container.New(nil))

	proven, err := binding.CustomerID(request())

	require.Error(t, err)
	assert.Empty(t, proven)
	assert.Equal(t, corehttp.CodeIdentityNotBound, errors.CodeOf(err),
		"the refusal has to name the binding that is missing")
	assert.True(t, errors.HasKind(err, errors.KindUnauthorized),
		"a deployment that bound nothing is not a server fault; it is a request nobody "+
			"could vouch for")
}

// TestABoundIdentityIsAskedAndItsAnswerReturned is the ordinary path.
func TestABoundIdentityIsAskedAndItsAnswerReturned(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide(corehttp.IdentityName, stubIdentity{id: provenID}))

	proven, err := newBinding(c).CustomerID(request())

	require.NoError(t, err)
	assert.Equal(t, provenID, proven)
}

// TestTheNameIsResolvedONCEEvenAcrossManyRequests pins the sync.Once.
//
// The lazy resolve exists so that registration order stays free; doing it on
// every request would put a container lookup on the hot path of every address
// read, and — worse — would make the answer depend on WHEN the request arrived
// rather than on what the installation registered.
func TestTheNameIsResolvedONCEEvenAcrossManyRequests(t *testing.T) {
	t.Parallel()

	built := 0
	c := container.New(nil)
	// A lazy constructor rather than a ready value: the container runs it on
	// the FIRST resolve, so counting its runs is what says the wrapper resolved
	// once. A ready value would be handed back without proving anything.
	require.NoError(t, c.Provide(corehttp.IdentityName,
		func(*container.Container) (any, error) {
			built++

			return stubIdentity{id: provenID}, nil
		}))

	binding := newBinding(c)
	for range 3 {
		_, err := binding.CustomerID(request())
		require.NoError(t, err)
	}

	assert.Equal(t, 1, built, "the identity is resolved on FIRST use and remembered")
}

// TestAnIdentityRegisteredUnderTheWrongTypeIsAServerFault keeps a wiring
// mistake from being read as a client mistake.
//
// The container reports a type mismatch as KindInvalid, and inheriting that
// kind would answer the shopper with a 422 — "your request is invalid" — for a
// request no client could have written differently. The installation is what is
// wrong, so the answer is a 500 and the log carries the name.
func TestAnIdentityRegisteredUnderTheWrongTypeIsAServerFault(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide(corehttp.IdentityName, "not an identity"))

	_, err := newBinding(c).CustomerID(request())

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal),
		"a name registered under the wrong type is a wiring error, not a bad request")
	assert.NotEqual(t, corehttp.CodeIdentityNotBound, errors.CodeOf(err),
		"a WRONG registration must not be reported as a MISSING one; the two are fixed "+
			"in different places")
}
