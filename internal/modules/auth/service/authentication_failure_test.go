package service_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/repository"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// TestAKeyTheDatabaseCouldNotLookUpIsNotRefused is ADR 0364 in the auth
// module: a key that is not on file is refused, and a lookup the database
// could not answer comes back with the database's class, which the core's
// guards answer 5xx rather than telling the client its key is invalid. On
// both surfaces a key is accepted on.
func TestAKeyTheDatabaseCouldNotLookUpIsNotRefused(t *testing.T) {
	t.Parallel()

	svc, repo := newService(t)
	interop := service.NewInterop(svc)
	authenticate := map[string]func() error{
		"store": func() error {
			_, err := interop.AuthenticateStore(t.Context(), "pk_unknown")
			return err
		},
		"admin": func() error {
			_, err := interop.AuthenticateAdmin(t.Context(), "Bearer", "sk_unknown")
			return err
		},
	}

	for surface, call := range authenticate {
		err := call()
		require.Error(t, err, surface)
		assert.True(t, errors.HasKind(err, errors.KindUnauthorized), "%s: a key not on file is refused: %v", surface, err)
		_, failed := corehttp.AuthenticatorFailure(err)
		assert.False(t, failed, "%s: a refusal is not a failure", surface)
	}

	repo.keyErr = errors.Internal(repository.CodeQueryFailed, "failed to connect")
	for surface, call := range authenticate {
		err := call()
		require.Error(t, err, surface)
		status, failed := corehttp.AuthenticatorFailure(err)
		assert.True(t, failed, "%s: a lookup the database could not answer is not a refusal: %v", surface, err)
		assert.Equal(t, http.StatusInternalServerError, status, surface)
	}
}

// TestASessionTheDatabaseCouldNotReadIsNotRefused is ADR 0364 on the session
// token the panel's cookie carries: when the user behind a sound token cannot
// be read, the failure keeps its class, so the panel keeps the session rather
// than signing the operator out.
func TestASessionTheDatabaseCouldNotReadIsNotRefused(t *testing.T) {
	t.Parallel()

	svc, interop, repo, _ := setupSession(t)
	token := obtainSessionToken(t, svc, sessionPassword)

	repo.userErr = errors.Internal(repository.CodeQueryFailed, "failed to connect")
	_, err := resolveSessionPrincipal(interop, token)
	require.Error(t, err)
	_, failed := corehttp.AuthenticatorFailure(err)
	assert.True(t, failed, "a user the database could not read is not a dead session: %v", err)

	repo.userErr = nil
	_, err = resolveSessionPrincipal(interop, token)
	assert.NoError(t, err, "the same token is good once the database answers")
}
