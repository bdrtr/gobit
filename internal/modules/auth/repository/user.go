package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository/authdb"
)

// CreateUser writes a new admin user, TOGETHER WITH its login identity when one
// is given.
//
// The two are in a single transaction: a user left without an identity can
// never log in, and you notice it only on the first login attempt; an identity
// left without a user is orphaned. When identity is nil only the user is
// written — the password is assigned later with [Repo.SetPasswordHash].
//
// If the email is already in use, errors.Conflict is returned; the rule lives
// in the partial unique index in the database (see [IndexUserEmail]) and is not
// repeated on the application side.
func (r *Repo) CreateUser(
	ctx context.Context,
	u models.User,
	identity *models.AuthIdentity,
) (models.User, error) {
	if err := r.ready(); err != nil {
		return models.User{}, err
	}

	userMeta, err := fromMetadata(u.Metadata)
	if err != nil {
		return models.User{}, err
	}

	var created models.User
	txErr := r.inTx(ctx, func(q *authdb.Queries) error {
		row, insErr := q.InsertUser(ctx, authdb.InsertUserParams{
			ID:        u.ID,
			Email:     u.Email,
			FirstName: u.FirstName,
			LastName:  u.LastName,
			AvatarUrl: u.AvatarURL,
			Scopes:    u.Scopes,
			Metadata:  userMeta,
			CreatedAt: fromTime(u.CreatedAt),
		})
		if insErr != nil {
			return classifyUserWrite(insErr, u.Email, "could not create user")
		}

		created, insErr = toUser(row)
		if insErr != nil {
			return insErr
		}

		if identity == nil {
			return nil
		}

		identityMeta, metaErr := fromMetadata(identity.Metadata)
		if metaErr != nil {
			return metaErr
		}
		if _, idErr := q.InsertIdentity(ctx, authdb.InsertIdentityParams{
			ID:               identity.ID,
			UserID:           created.ID,
			Provider:         identity.Provider,
			ProviderIdentity: identity.ProviderIdentity,
			PasswordHash:     identity.PasswordHash,
			Metadata:         identityMeta,
			CreatedAt:        fromTime(identity.CreatedAt),
		}); idErr != nil {
			return classifyUserWrite(idErr, identity.ProviderIdentity, "could not create identity record")
		}
		return nil
	})
	if txErr != nil {
		return models.User{}, txErr
	}
	return created, nil
}

// GetUser returns the user by id; errors.NotFound when there is none.
func (r *Repo) GetUser(ctx context.Context, id string) (models.User, error) {
	if err := r.ready(); err != nil {
		return models.User{}, err
	}

	row, err := r.q.GetUser(ctx, id)
	if err != nil {
		return models.User{}, notFoundOr(err, CodeUserNotFound, "user not found: %s", id)
	}
	return toUser(row)
}

// GetUserByEmail returns the LIVE user by email; errors.NotFound when there is
// none.
//
// It is the first step of the login flow. The error message DOES CONTAIN the
// email, but that message does not go to the client: the login path does not
// leak the difference between "not found" and "wrong password" (see service,
// Login).
func (r *Repo) GetUserByEmail(ctx context.Context, email string) (models.User, error) {
	if err := r.ready(); err != nil {
		return models.User{}, err
	}

	row, err := r.q.GetUserByEmail(ctx, email)
	if err != nil {
		return models.User{}, notFoundOr(err, CodeUserNotFound,
			"no user found with the email %q", email)
	}
	return toUser(row)
}

// ListUsers returns the filtered and paginated user list together with the
// TOTAL number of records matching the filter.
func (r *Repo) ListUsers(
	ctx context.Context,
	filter models.UserFilter,
	limit, offset int64,
) ([]models.User, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListUsers(ctx, authdb.ListUsersParams{
		Email: filter.Email,
		Scope: filter.Scope,
		Lim:   toInt32(limit),
		Off:   toInt32(offset),
	})
	if err != nil {
		return nil, 0, wrapDB(err, "could not read the user list")
	}

	total, err := r.q.CountUsers(ctx, authdb.CountUsersParams{
		Email: filter.Email,
		Scope: filter.Scope,
	})
	if err != nil {
		return nil, 0, wrapDB(err, "could not read the user count")
	}

	users, err := toUsers(rows)
	if err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// GetUsersByIDs returns the users matching the given ids in a SINGLE query. No
// record is returned for an id that is not found; that is not an error.
func (r *Repo) GetUsersByIDs(ctx context.Context, ids []string) ([]models.User, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []models.User{}, nil
	}

	rows, err := r.q.ListUsersByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "could not read the users")
	}
	return toUsers(rows)
}

// UpdateUser updates the given fields of the user.
//
// If the email changes, the provider_identity field of the login identity is
// updated IN THE SAME TRANSACTION: the two sit in separate columns but express
// the same thing, and if they diverge the user CANNOT log in with the new
// address.
func (r *Repo) UpdateUser(
	ctx context.Context,
	id string,
	patch models.UserPatch,
	now time.Time,
) (models.User, error) {
	if err := r.ready(); err != nil {
		return models.User{}, err
	}

	meta, err := patchMetadata(patch.Metadata)
	if err != nil {
		return models.User{}, err
	}

	var updated models.User
	txErr := r.inTx(ctx, func(q *authdb.Queries) error {
		row, upErr := q.UpdateUser(ctx, authdb.UpdateUserParams{
			Email:     patch.Email,
			FirstName: patch.FirstName,
			LastName:  patch.LastName,
			AvatarUrl: patch.AvatarURL,
			Scopes:    patch.Scopes,
			Metadata:  meta,
			UpdatedAt: fromTime(now),
			ID:        id,
		})
		if upErr != nil {
			if errors.Is(upErr, pgx.ErrNoRows) {
				return errors.NotFound(CodeUserNotFound, "user not found: %s", id)
			}
			return classifyUserWrite(upErr, derefOr(patch.Email), "could not update user")
		}

		updated, upErr = toUser(row)
		if upErr != nil {
			return upErr
		}

		if patch.Email == nil {
			return nil
		}
		if syncErr := q.SyncIdentityProviderIdentity(ctx, authdb.SyncIdentityProviderIdentityParams{
			UserID:           id,
			Provider:         models.ProviderEmailPass,
			ProviderIdentity: *patch.Email,
			UpdatedAt:        fromTime(now),
		}); syncErr != nil {
			return classifyUserWrite(syncErr, *patch.Email, "could not update the login identity")
		}
		return nil
	})
	if txErr != nil {
		return models.User{}, txErr
	}
	return updated, nil
}

// DeleteUser soft-deletes the user together with its login identities.
//
// The two are in the SAME transaction, and what matters is not the order but
// ATOMICITY: a user whose identity stayed live could still log in after being
// deleted.
func (r *Repo) DeleteUser(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	return r.inTx(ctx, func(q *authdb.Queries) error {
		if _, err := q.SoftDeleteUser(ctx, authdb.SoftDeleteUserParams{
			ID:        id,
			DeletedAt: fromTime(now),
		}); err != nil {
			return notFoundOr(err, CodeUserNotFound, "user not found: %s", id)
		}
		if err := q.SoftDeleteIdentitiesOfUser(ctx, authdb.SoftDeleteIdentitiesOfUserParams{
			UserID:    id,
			DeletedAt: fromTime(now),
		}); err != nil {
			return wrapDB(err, "could not delete the login identities of the user")
		}
		return nil
	})
}

// classifyUserWrite classifies a write error with respect to email conflicts.
//
// Both the auth_user and the auth_identity uniqueness indexes express the same
// fact: this email is already in use. Reducing the two to a single code spares
// the caller from having to know which table spoke.
func classifyUserWrite(err error, email, message string) error {
	if err == nil {
		return nil
	}
	switch ConstraintName(err) {
	case IndexUserEmail, IndexIdentityProvider:
		return errors.Wrap(err, errors.KindConflict, CodeEmailTaken,
			"the email %q is already in use", email)
	case IndexIdentityUserProvider:
		// This conflict is NOT an email conflict and is not presented as one:
		// even when the email is free, the user already has an identity with
		// that provider. Landing here means two concurrent "set password"
		// requests tried to open the same identity; one of them writes, the
		// other stops here (see [Repo.SetPasswordHash]).
		return errors.Wrap(err, errors.KindConflict, CodeDuplicate,
			"the user already has an identity with this provider")
	}
	return wrapDB(err, "%s", message)
}

// derefOr dereferences the pointer; returns the empty string when it is nil.
func derefOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
