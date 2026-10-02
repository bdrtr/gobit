package repository

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
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
