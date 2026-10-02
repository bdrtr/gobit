//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
)

// TestAnOperatorRemovesAUserInThePanel is ADR 0349 on the production wiring:
// a user's page removes them through the registered `auth.admin` surface, so
// they are found no more, while the operator's own removal is refused and
// leaves them.
func TestAnOperatorRemovesAUserInThePanel(t *testing.T) {
	ctx := t.Context()
	root := corehttp.WithPrincipal(ctx, corehttp.Principal{ID: "usr_root", Kind: "user", Scopes: []string{corehttp.ScopeAdmin}})
	n := fixtureCounter.Add(1)
	owner, err := authSvc.CreateUser(root, authsvc.CreateUserInput{
		Email: fmt.Sprintf("e2e-remover-%d@example.com", n), Scopes: []string{corehttp.ScopeAdmin},
	}, mfaTestPassword)
	require.NoError(t, err)
	clerk, err := authSvc.CreateUser(root, authsvc.CreateUserInput{
		Email: fmt.Sprintf("e2e-removed-%d@example.com", n), Scopes: []string{"order:read"},
	}, mfaTestPassword)
	require.NoError(t, err)

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	send := func(path string) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(http.MethodPost, path, http.NoBody)
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: owner.ID, Kind: "user", Scopes: []string{corehttp.ScopeAdmin},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	removed := send(adminui.UsersPath + "/" + clerk.ID + "/remove")
	require.Equal(t, http.StatusSeeOther, removed.Code, removed.Body.String())
	_, err = authSvc.GetUser(ctx, clerk.ID)
	assert.True(t, errors.IsNotFound(err), "the removed user is found no more: %v", err)

	self := send(adminui.UsersPath + "/" + owner.ID + "/remove")
	require.Equal(t, http.StatusUnprocessableEntity, self.Code, self.Body.String())
	assert.Contains(t, self.Body.String(), "you cannot remove yourself here")
	_, err = authSvc.GetUser(ctx, owner.ID)
	require.NoError(t, err, "the operator is still there")
}
