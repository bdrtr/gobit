package repository

import (
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository/authdb"
)

// CodeLastAdministrator refuses a write that would leave no live user holding
// admin (ADR 0346).
const CodeLastAdministrator = "auth_last_administrator"

// keepAnAdministrator refuses, inside the transaction of the write it guards,
// a write that takes admin from the user when they are the last live user
// holding it (ADR 0346).
//
// The first administrator is seeded only into an installation with no users
// at all (internal/app's seedAdmin), so a shop whose last administrator lost
// admin could be managed again only by editing the database. Every live
// administrator is locked, in the order of their ids, so two writes taking it
// from the last two cannot both see the other one left: the second waits, and
// is answered from the row as the first left it.
func keepAnAdministrator(ctx context.Context, q *authdb.Queries, id string) error {
	ids, err := q.LockLiveAdministrators(ctx)
	if err != nil {
		return wrapDB(err, "could not lock the administrators")
	}
	if len(ids) == 1 && ids[0] == id {
		return errors.Conflict(CodeLastAdministrator,
			"user %s is the last who holds admin; give it to another user first", id)
	}

	return nil
}

// ReviseUserScopes writes the user's scopes only while they are, as a set,
// the ones read, and reports whether it wrote (ADR 0347). Taking admin from
// the last live user holding it is refused as any write's is (ADR 0346).
func (r *Repo) ReviseUserScopes(
	ctx context.Context, id string, read, next []string, now time.Time,
) (models.User, bool, error) {
	if err := r.ready(); err != nil {
		return models.User{}, false, err
	}
	// A nil slice would be sent as NULL, which no set contains and the column
	// does not take.
	read, next = append([]string{}, read...), append([]string{}, next...)

	var revised models.User
	var written bool
	err := r.inTx(ctx, func(q *authdb.Queries) error {
		if !slices.Contains(next, models.ScopeAdmin) {
			if err := keepAnAdministrator(ctx, q, id); err != nil {
				return err
			}
		}
		row, err := q.ReviseUserScopes(ctx, authdb.ReviseUserScopesParams{
			Next: next, UpdatedAt: fromTime(now), ID: id, Read: read,
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return wrapDB(err, "could not revise the scopes of the user")
		}
		revised, err = toUser(row)
		written = err == nil

		return err
	})
	if err != nil {
		return models.User{}, false, err
	}

	return revised, written, nil
}
