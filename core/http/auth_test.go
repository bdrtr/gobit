package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// TestTheAdminScopeCoversEveryModuleScope pins the rule the whole admin surface
// stands on.
//
// Every module route names a scope of its own — "review:read", "product:write"
// — and a user is created with ONE scope, [ScopeAdmin]. The two meet in
// [Principal.HasScope], where admin satisfies anything. Nothing else connects
// them: no module knows the auth module's default and no test walked the pair.
//
// Remove that branch and the tree does not fail to compile and no unit test of a
// module notices. What happens is that every admin request answers 403 —
// including the panel's, whose screens would then show an empty list under a
// heading, which is the shape of failure this repository keeps paying for.
func TestTheAdminScopeCoversEveryModuleScope(t *testing.T) {
	t.Parallel()

	operator := corehttp.Principal{Scopes: []string{corehttp.ScopeAdmin}}

	// The names are spelled out rather than imported: core knows no module
	// (Principle 2.4), and what is asserted is that the RULE is about any scope
	// at all rather than about a list somebody keeps here.
	for _, scope := range []string{
		"review:read", "review:write", "product:write", "order:read",
		// A privilege no module of gobit's own demands: contrib/identity-session's
		// credential route (ADR 0434), granted over the API like personal-data:*.
		"customer-credential:write",
		"a-scope-nobody-has-invented-yet",
	} {
		assert.True(t, operator.HasScope(scope),
			"an operator carrying only %q was refused %q; every admin request in the tree "+
				"would answer 403 and every panel screen would show an empty list",
			corehttp.ScopeAdmin, scope)
	}

	// The counterpart, and it is what makes the loop above mean something: a
	// principal without admin is held to the scope it actually carries.
	narrow := corehttp.Principal{Scopes: []string{"review:read"}}
	assert.True(t, narrow.HasScope("review:read"))
	assert.False(t, narrow.HasScope("review:write"),
		"a principal with one scope was granted another; the scope check is not a check")
	assert.False(t, corehttp.Principal{}.HasScope("review:read"),
		"a principal with NO scope was granted one")
}

// TestAScopeGuardSaysWhatItDemands is the reading ADR 0263's document relies
// on: the guard RequireScope returns names its privilege, still guards, and
// no other middleware is taken for one.
func TestAScopeGuardSaysWhatItDemands(t *testing.T) {
	t.Parallel()

	guard := corehttp.RequireScope("product:read")
	scope, ok := corehttp.ScopeDemandedBy(guard)
	assert.True(t, ok)
	assert.Equal(t, "product:read", scope)

	_, ok = corehttp.ScopeDemandedBy(func(next http.Handler) http.Handler { return next })
	assert.False(t, ok, "a middleware that is not a guard demands nothing")

	reached := false
	handler := guard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	handler.ServeHTTP(rec, req.WithContext(corehttp.WithPrincipal(req.Context(),
		corehttp.Principal{ID: "usr_1", Kind: "user", Scopes: []string{"order:read"}})))
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "product:read")
	assert.False(t, reached, "the guard still refuses the principal without the privilege")
}
