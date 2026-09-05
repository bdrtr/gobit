package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// This file exercises PRIVILEGE ESCALATION: a caller not being able to grant
// someone else (or a new key) a scope it does not have itself.
//
// The check exists in the route layer as well — the privileged endpoints
// require corehttp.ScopeAdmin — but one path remains that NEVER passes through
// there: code that embeds the module or calls the service directly. The gate in
// the service closes that path too, and should the scope map of the endpoints
// ever be loosened, it is the only defense left.

// narrowScope is the sample scope the caller carries in the tests.
//
// It was picked NOT from auth's own vocabulary but from another module's
// scopes: this is how it shows that the check looks not at a list of "scopes it
// recognizes" but at the caller's real scopes.
const narrowScope = "orders:read"

// newService sets up a service running on the fake repository.
func newService(t *testing.T) (*service.Service, *fakeRepo) {
	t.Helper()

	repo := &fakeRepo{}
	svc := service.New(repo, service.Options{
		Now:       func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) },
		JWTSecret: "test-signing-secret-long-enough",
	})
	return svc, repo
}

// scopedCtx produces a context with a verified identity carrying the given
// scopes.
//
// In production corehttp.RequireAdmin places this identity; placing it by hand
// in a test makes it possible to exercise the service's decision without the
// HTTP stack.
func scopedCtx(scopes ...string) context.Context {
	return corehttp.WithPrincipal(context.Background(), corehttp.Principal{
		ID:     "apikey_test",
		Kind:   "api_key",
		Scopes: scopes,
	})
}

// requireEscalationError verifies that the error is a privilege escalation
// rejection.
func requireEscalationError(t *testing.T, err error) {
	t.Helper()

	require.Error(t, err, "a privilege escalation attempt has to return an error")
	assert.True(t, errors.IsForbidden(err),
		"the error has to be translated to a 403; had it returned 401 the client would try to refresh its token: %v", err)
	assert.Equal(t, service.CodeScopeEscalation, errors.CodeOf(err))
}

// TestKeyCreationCannotExceedTheCallersScopes proves that a narrowly scoped
// caller cannot produce an API key with wider scopes than its own.
//
// This was the concrete shape of the fault: an sk_ key carrying only
// "orders:read" could produce a fully privileged successor with the body
// {"type":"secret","scopes":["admin"]}.
func TestKeyCreationCannotExceedTheCallersScopes(t *testing.T) {
	tests := map[string]struct {
		input  service.CreateAPIKeyInput
		reject bool
		want   []string
		reason string
	}{
		"admin request": {
			input:  service.CreateAPIKeyInput{Type: models.APIKeySecret, Title: "escalation", Scopes: []string{models.ScopeAdmin}},
			reject: true,
			reason: "the admin scope the caller does not have cannot be granted",
		},
		"the scope field was never filled in": {
			input:  service.CreateAPIKeyInput{Type: models.APIKeySecret, Title: "default"},
			reject: true,
			reason: "the default of a secret key is admin; saying 'I did not grant it' is not enough to have not granted it",
		},
		"another scope it does not hold": {
			input:  service.CreateAPIKeyInput{Type: models.APIKeySecret, Title: "side", Scopes: []string{"products:write"}},
			reject: true,
			reason: "an escalation does not have to be toward admin",
		},
		"its own scope": {
			input: service.CreateAPIKeyInput{Type: models.APIKeySecret, Title: "peer", Scopes: []string{narrowScope}},
			want:  []string{narrowScope},
		},
		"publishable key": {
			input: service.CreateAPIKeyInput{Type: models.APIKeyPublishable, Title: "storefront"},
			want:  []string{},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			svc, repo := newService(t)
			ctx := scopedCtx(narrowScope)

			_, plaintext, err := svc.CreateAPIKey(ctx, tt.input)

			if tt.reject {
				requireEscalationError(t, err)
				assert.Empty(t, plaintext, "no key MUST BE PRODUCED on a rejected request")
				assert.Zero(t, repo.writeCount, "%s", tt.reason)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, repo.lastKey.Scopes,
				"on an accepted request the resolved scopes have to be written as they are")
		})
	}
}

// TestUserUpdateCannotExceedTheCallersScopes proves that a narrowly scoped
// caller cannot raise a user (itself included) to admin.
func TestUserUpdateCannotExceedTheCallersScopes(t *testing.T) {
	svc, repo := newService(t)
	ctx := scopedCtx(narrowScope)

	_, err := svc.UpdateUser(ctx, "user_1", service.UpdateUserInput{
		Scopes: []string{models.ScopeAdmin},
	})

	requireEscalationError(t, err)
	assert.Zero(t, repo.writeCount, "a rejected update must NEVER reach the repository")
}

// TestUserUpdateAllowsNarrowingScopes proves that the check blocks ONLY
// escalation.
//
// It has to be a separate test: a check that rejects every scope request passes
// the test above but also makes REMOVING a scope impossible and would close off
// the way to shut down a compromised account.
func TestUserUpdateAllowsNarrowingScopes(t *testing.T) {
	tests := map[string][]string{
		"the scopes are removed entirely": {},
		"the caller's own scope":          {narrowScope},
	}

	for name, scopes := range tests {
		t.Run(name, func(t *testing.T) {
			svc, repo := newService(t)
			ctx := scopedCtx(narrowScope)

			_, err := svc.UpdateUser(ctx, "user_1", service.UpdateUserInput{Scopes: scopes})

			require.NoError(t, err)
			assert.Equal(t, scopes, repo.lastPatch.Scopes)
		})
	}
}

// TestUserCreationCannotExceedTheCallersScopes proves that the same gate is
// shut on a new user as well.
//
// Had the update been closed and the creation left open, the escalation would
// simply split into two steps: first create a fully privileged user, then log
// in with it.
func TestUserCreationCannotExceedTheCallersScopes(t *testing.T) {
	svc, repo := newService(t)
	ctx := scopedCtx(narrowScope)

	// The scope field is NOT given at all: the default is full privilege and the
	// check has to run after the default has been applied.
	_, err := svc.CreateUser(ctx, service.CreateUserInput{Email: "new@example.com"}, "")

	requireEscalationError(t, err)
	assert.Zero(t, repo.writeCount, "a rejected creation must NEVER reach the repository")
}

// TestAdminCallerCanGrantEveryScope proves that a fully privileged caller is
// not restricted.
//
// The check rests on corehttp.Principal.HasScope and admin is the superior
// scope there; this test shows that the bond has not been broken. Had it
// broken, the admin surface would lock itself out.
func TestAdminCallerCanGrantEveryScope(t *testing.T) {
	svc, repo := newService(t)
	ctx := scopedCtx(corehttp.ScopeAdmin)

	_, _, err := svc.CreateAPIKey(ctx, service.CreateAPIKeyInput{
		Type:   models.APIKeySecret,
		Title:  "new admin key",
		Scopes: []string{models.ScopeAdmin},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{models.ScopeAdmin}, repo.lastKey.Scopes)

	_, err = svc.UpdateUser(ctx, "user_1", service.UpdateUserInput{
		Scopes: []string{"products:write"},
	})
	require.NoError(t, err, "an admin has to be able to grant a scope THAT IS NOT NAMED on itself either")
}

// TestCallWithoutIdentityIsAllowedForTheSeedStep proves that a call carrying no
// identity does not get caught by the check.
//
// The first administrator cannot be created over HTTP — the admin endpoints are
// already protected — and is born through a seed step. That step inherits its
// privileges from nobody, so it can escalate nobody; had the check been applied
// there, the system could never have been set up.
func TestCallWithoutIdentityIsAllowedForTheSeedStep(t *testing.T) {
	svc, repo := newService(t)
	ctx := context.Background()

	_, _, err := svc.CreateAPIKey(ctx, service.CreateAPIKeyInput{
		Type:  models.APIKeySecret,
		Title: "seed key",
	})
	require.NoError(t, err, "the seed step has to be able to produce a fully privileged key")
	assert.Equal(t, []string{models.ScopeAdmin}, repo.lastKey.Scopes)

	_, err = svc.CreateUser(ctx, service.CreateUserInput{Email: "first@example.com"}, "")
	require.NoError(t, err, "the seed step has to be able to create the first administrator")
	assert.Equal(t, []string{models.ScopeAdmin}, repo.lastUser.Scopes)
}
