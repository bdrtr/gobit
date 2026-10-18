package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository/authdb"
)

// ReviseAPIKeyScopes writes a live secret key's scopes only while they are,
// as a set, the ones read, and reports whether it wrote (ADR 0455). Nothing
// written means the key is gone, revoked or publishable, or its scopes were
// changed since; the caller reads the key to say which.
func (r *Repo) ReviseAPIKeyScopes(
	ctx context.Context, id string, read, next []string, now time.Time,
) (models.APIKey, bool, error) {
	if err := r.ready(); err != nil {
		return models.APIKey{}, false, err
	}
	// A nil slice would be sent as NULL, which no set contains and the column
	// does not take.
	read, next = append([]string{}, read...), append([]string{}, next...)

	row, err := r.q.ReviseAPIKeyScopes(ctx, authdb.ReviseAPIKeyScopesParams{
		Next: next, UpdatedAt: fromTime(now), ID: id, Read: read,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.APIKey{}, false, nil
	}
	if err != nil {
		return models.APIKey{}, false, wrapDB(err, "could not revise the scopes of the api key")
	}

	return toAPIKey(row), true, nil
}
