package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// sessionIdentity is what an embedder's implementation looks like.
//
// It is declared OUTSIDE the package that publishes the contract and its
// signature names nothing but the standard library — which is the property that
// makes the contract satisfiable at all, and the only property this file is
// about. A cookie stands in for the embedder's real proof; what it reads is
// beside the point, and that is the point.
type sessionIdentity struct{}

func (sessionIdentity) CustomerID(r *http.Request) (string, error) {
	cookie, err := r.Cookie("session")
	if err != nil {
		return "", err
	}

	return cookie.Value, nil
}

// TestAnIdentityIsSatisfiableWithTheStandardLibraryAlone is the compile-time
// half of ADR 0043's seam, asserted where it can be read.
//
// The assignment below is the whole test: if [corehttp.Identity] ever grows a
// method, a parameter or a result that a type declared OUTSIDE this package
// cannot name, this file stops compiling.
//
// # What it catches that the arch audits do not
//
// Not an internal/ type. internal/arch's
// TestNoPublishedPackageImportsAnInternalOne refuses core/http any import of
// internal/ AT ALL, which is stricter than the signature rule this file could
// state, and its own godoc gives the same reasoning. Saying otherwise here
// would be claiming a guard the file already has.
//
// What this one holds is the case that audit is blind to: an UNEXPORTED type of
// core/http's own in the signature. It imports nothing forbidden and compiles
// perfectly — and it still leaves an outside module unable to implement the
// interface, because [sessionIdentity] is declared in a separate package and
// cannot name it either. That is the whole of this test's unique value, and it
// is narrower than "any type from this repository".
//
// It is not a substitute for the out-of-tree compile in internal/arch, which is
// the real proof; it is the copy that fails in the package being edited, in the
// same second as the edit.
func TestAnIdentityIsSatisfiableWithTheStandardLibraryAlone(t *testing.T) {
	t.Parallel()

	var identity corehttp.Identity = sessionIdentity{}

	req := httptest.NewRequest(http.MethodGet, "/store/v1/customers/cus_1", http.NoBody)
	req.AddCookie(&http.Cookie{Name: "session", Value: "cus_1"})

	proven, err := identity.CustomerID(req)

	require.NoError(t, err)
	assert.Equal(t, "cus_1", proven)
}

// TestAnIdentityRegistersAndResolvesUnderTheCoreName pins the OTHER half of the
// seam: the embedder binds by NAME, from an ordinary module.
//
// Registering and resolving is two published calls wide, and this test is what
// says the pair works for THIS name and THIS interface. The resolve asks for the
// interface, not for the concrete type, which is what lets the customer module
// hold the contract without ever knowing who implements it.
//
// The name being a constant is what the test is standing in front of. It is a
// cross-boundary contract that no compiler compares when either side spells it
// as a literal, and a renamed slot makes every address book request refuse —
// loudly, but with nothing saying why.
func TestAnIdentityRegistersAndResolvesUnderTheCoreName(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide(corehttp.IdentityName, sessionIdentity{}))

	resolved, err := container.Resolve[corehttp.Identity](c, corehttp.IdentityName)

	require.NoError(t, err, "an identity provided under %q has to resolve as the interface",
		corehttp.IdentityName)
	assert.NotNil(t, resolved)
}
