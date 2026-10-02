package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// CodeUserScopesRevised refuses a change of a user's privileges when another
// writer changed them since the caller read them (ADR 0347).
const CodeUserScopesRevised = "auth_user_scopes_revised"

// ReviseUserScopes writes the user's privileges only while they are, as a
// set, the ones the caller read, and refuses with [CodeUserScopesRevised]
// when another writer changed them since (ADR 0347). As through
// [Service.UpdateUser], a privilege the caller does not hold cannot be
// granted, and admin is not taken from the last live user holding it (ADR
// 0346).
func (s *Service) ReviseUserScopes(ctx context.Context, id string, read, next []string) (models.User, error) {
	if err := s.ready(); err != nil {
		return models.User{}, err
	}
	if err := requireID(id, models.UserIDPrefix, "the user identifier"); err != nil {
		return models.User{}, err
	}
	read, err := normalizeScopes(append([]string{}, read...))
	if err != nil {
		return models.User{}, err
	}
	next, err = normalizeScopes(append([]string{}, next...))
	if err != nil {
		return models.User{}, err
	}
	if err := requireGrantableScopes(ctx, next); err != nil {
		return models.User{}, err
	}

	user, revised, err := s.repo.ReviseUserScopes(ctx, id, read, next, s.clock())
	if err != nil || revised {
		return user, err
	}

	// Nothing was written: the user is gone, or their privileges were
	// changed since.
	if _, err := s.repo.GetUser(ctx, id); err != nil {
		return models.User{}, err
	}

	return models.User{}, errors.Conflict(CodeUserScopesRevised,
		"the privileges of user %s were changed since they were read; draw the page again", id)
}
