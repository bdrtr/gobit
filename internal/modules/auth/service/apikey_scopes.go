package service

import (
	"context"
	"log/slog"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// CodeAPIKeyScopesRevised refuses a change of a key's privileges when another
// writer changed them since the caller read them (ADR 0455).
const CodeAPIKeyScopesRevised = "auth_api_key_scopes_revised" //nolint:gosec // G101: an error CODE, not a credential

// ReviseAPIKeyScopes writes a secret key's privileges only while they are, as
// a set, the ones the caller read, and refuses with [CodeAPIKeyScopesRevised]
// when another writer changed them since (ADR 0455).
//
// The key's secret does not change: every copy of it holds the new privileges
// from its next request, since a key's privileges are read on every request.
// That is the point, a scope added without re-minting and redeploying the
// secret, and it is also why the rules of [Service.CreateAPIKey] hold here:
// no privilege the caller does not hold is granted, a publishable key carries
// none, and a secret key carries at least one, since a key with none is one
// to revoke. A revoked key is not revised. The write is an admin write, so
// the audit log records who made it.
func (s *Service) ReviseAPIKeyScopes(ctx context.Context, id string, read, next []string) (models.APIKey, error) {
	if err := s.ready(); err != nil {
		return models.APIKey{}, err
	}
	if err := requireID(id, models.APIKeyIDPrefix, "the api key identifier"); err != nil {
		return models.APIKey{}, err
	}
	read, err := normalizeScopes(append([]string{}, read...))
	if err != nil {
		return models.APIKey{}, err
	}
	next, err = normalizeScopes(append([]string{}, next...))
	if err != nil {
		return models.APIKey{}, err
	}
	if len(next) == 0 {
		return models.APIKey{}, errors.Invalid(CodeInvalidInput,
			"a secret key holds at least one scope; revoke a key that should hold none")
	}
	if err := requireGrantableScopes(ctx, next); err != nil {
		return models.APIKey{}, err
	}

	key, err := s.repo.GetAPIKey(ctx, id)
	if err != nil {
		return models.APIKey{}, err
	}
	if err := revisable(key); err != nil {
		return models.APIKey{}, err
	}

	revised, written, err := s.repo.ReviseAPIKeyScopes(ctx, id, read, next, s.clock())
	if err != nil {
		return models.APIKey{}, err
	}
	if !written {
		// Nothing was written: the key went, or was revoked, meanwhile, or
		// its privileges were changed since they were read.
		key, err = s.repo.GetAPIKey(ctx, id)
		if err != nil {
			return models.APIKey{}, err
		}
		if err := revisable(key); err != nil {
			return models.APIKey{}, err
		}

		return models.APIKey{}, errors.Conflict(CodeAPIKeyScopesRevised,
			"the privileges of api key %s were changed since they were read; read the key again", id)
	}

	s.log.InfoContext(ctx, "api key scopes revised",
		slog.String("api_key_id", id),
		slog.Any("from", key.Scopes),
		slog.Any("to", revised.Scopes),
	)

	return revised, nil
}

// revisable refuses a key whose privileges cannot be revised.
func revisable(key models.APIKey) error {
	if key.Type != models.APIKeySecret {
		return errors.Invalid(CodeAPIKeyTypeMismatch,
			"only a secret key holds privileges; %s is %q", key.ID, key.Type)
	}
	if key.IsRevoked() {
		return errors.Conflict(CodeAPIKeyRevoked,
			"api key %s is revoked; a revoked key's privileges are not revised", key.ID)
	}

	return nil
}
