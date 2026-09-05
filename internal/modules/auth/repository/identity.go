package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository/authdb"
)

// GetIdentity returns the user's login identity for a provider; errors.NotFound
// when there is none.
//
// The returned record DOES CONTAIN password_hash. The value leaves this package
// but goes only to the bcrypt comparison; it is put into no log line, no error
// message and no API response.
func (r *Repo) GetIdentity(ctx context.Context, userID, provider string) (models.AuthIdentity, error) {
	if err := r.ready(); err != nil {
		return models.AuthIdentity{}, err
	}

	row, err := r.q.GetIdentityOfUser(ctx, authdb.GetIdentityOfUserParams{
		UserID:   userID,
		Provider: provider,
	})
	if err != nil {
		return models.AuthIdentity{}, notFoundOr(err, CodeIdentityNotFound,
			"user %s has no %q identity", userID, provider)
	}
	return toIdentity(row)
}

// SetPasswordHash writes the user's password hash; it CREATES the identity when
// there is none.
//
// Why a single method: a "set password" request must not behave differently
// depending on whether the user already had a password. With two separate
// methods the caller would run an existence query first every time, and in the
// gap between that query and the write two concurrent requests would try to
// create two identity rows; the second one would hit the uniqueness index and a
// meaningless conflict would be returned to the client.
//
// The hash itself is neither logged nor put into an error message.
//
// The write moves the record's updated_at to now. In this table that column is
// NOT a plain audit field: the service rejects session tokens minted before it
// (see the head of queries/identities.sql and service/session.go,
// sessionAnchor). So this call also closes the user's open sessions; to close
// the sessions without touching the password there is [Repo.RevokeSessions].
func (r *Repo) SetPasswordHash(
	ctx context.Context,
	userID, provider, providerIdentity, hash string,
	now time.Time,
) (models.AuthIdentity, error) {
	if err := r.ready(); err != nil {
		return models.AuthIdentity{}, err
	}

	var identity models.AuthIdentity
	txErr := r.inTx(ctx, func(q *authdb.Queries) error {
		existing, err := q.GetIdentityOfUser(ctx, authdb.GetIdentityOfUserParams{
			UserID:   userID,
			Provider: provider,
		})
		switch {
		case err == nil:
			row, upErr := q.UpdatePasswordHash(ctx, authdb.UpdatePasswordHashParams{
				ID:           existing.ID,
				PasswordHash: hash,
				UpdatedAt:    fromTime(now),
			})
			if upErr != nil {
				return notFoundOr(upErr, CodeIdentityNotFound,
					"identity record not found: %s", existing.ID)
			}
			identity, upErr = toIdentity(row)
			return upErr

		case errors.Is(err, pgx.ErrNoRows):
			meta, metaErr := fromMetadata(nil)
			if metaErr != nil {
				return metaErr
			}
			row, insErr := q.InsertIdentity(ctx, authdb.InsertIdentityParams{
				ID:               models.NewAuthIdentityID(now),
				UserID:           userID,
				Provider:         provider,
				ProviderIdentity: providerIdentity,
				PasswordHash:     hash,
				Metadata:         meta,
				CreatedAt:        fromTime(now),
			})
			if insErr != nil {
				return classifyUserWrite(insErr, providerIdentity, "could not create identity record")
			}
			identity, insErr = toIdentity(row)
			return insErr

		default:
			return wrapDB(err, "could not read the identity record")
		}
	})
	if txErr != nil {
		return models.AuthIdentity{}, txErr
	}
	return identity, nil
}

// RevokeSessions moves the session anchor of ALL of the user's providers to the
// now moment and drops ALL OF THEIR OPEN SESSIONS.
//
// The only column written is updated_at. In this table that column is NOT an
// audit field: the service rejects session tokens minted before it (see the
// head of queries/identities.sql and service/session.go, sessionAnchor). Logout
// is entirely this write; there is no "session record" to drop, what drops is
// the validity of the tokens.
//
// # The provider is NOT A PARAMETER
//
// The table keeps one row per provider ((user_id, provider) uniqueness) and the
// caller has no choice of "which provider is being logged out of": all of the
// rows are advanced and the advanced ones are returned. Had a single provider
// been selectable, the day OAuth is added logout would not drop that provider's
// tokens, and it would do so silently. Today there is a single live provider,
// so the returned slice has one element; what changes is not the behavior but
// the behavior on the day the second row is added.
//
// The password and the lock counters are PRESERVED: logging out does not change
// the password, and had the counter been reset the logout endpoint would become
// the way to clear the login lock (rationale in queries/identities.sql).
//
// If the user has no live identity at all, errors.NotFound is returned;
// returning success with an empty slice would present a logout that dropped
// nothing as a success.
func (r *Repo) RevokeSessions(
	ctx context.Context,
	userID string,
	now time.Time,
) ([]models.AuthIdentity, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.q.RevokeSessions(ctx, authdb.RevokeSessionsParams{
		UserID:    userID,
		UpdatedAt: fromTime(now),
	})
	if err != nil {
		return nil, wrapDB(err, "could not close the sessions of user %s", userID)
	}
	if len(rows) == 0 {
		// A multi-row UPDATE does NOT produce pgx.ErrNoRows, it returns an
		// empty slice; the "no rows" case is therefore checked by hand. Left to
		// notFoundOr, the logout of a user without an identity would silently
		// succeed.
		return nil, errors.NotFound(CodeIdentityNotFound,
			"user %s has no login identity at all", userID)
	}
	return toIdentities(rows)
}

// SessionAnchor returns the user's MOST RECENT session anchor; errors.NotFound
// when the user has no identity at all.
//
// The returned value is not that of a SINGLE provider: it is the furthest one
// among all of the user's identities (rationale in queries/identities.sql,
// GetSessionAnchor). Because logout advances all of them at once, these two
// endpoints apply the same rule; had they diverged, the anchor written by
// logout would never be read.
//
// The identity row is NOT returned, only the timestamp: that is the only thing
// the caller needs, and handing over the whole row would carry password_hash
// outside the repository boundary on a path that never needs it.
func (r *Repo) SessionAnchor(ctx context.Context, userID string) (time.Time, error) {
	if err := r.ready(); err != nil {
		return time.Time{}, err
	}

	anchor, err := r.q.GetSessionAnchor(ctx, userID)
	if err != nil {
		return time.Time{}, notFoundOr(err, CodeIdentityNotFound,
			"user %s has no login identity at all", userID)
	}
	return toTime(anchor), nil
}

// RegisterLoginFailure counts a failed login attempt ATOMICALLY and locks the
// identity until the lockUntil moment once the threshold is reached.
//
// Doing the increment in SQL is not a preference but a necessity: had the
// number been read here and written back, hundreds of attempts arriving at the
// same time would all read the same value and the lock would never engage (see
// queries/identities.sql).
//
// While the counter is written, updated_at is LEFT UNTOUCHED: that column is
// the anchor of session revocation, and had it advanced, a single failed
// attempt would drop all of the victim's sessions (rationale at the head of
// queries/identities.sql).
func (r *Repo) RegisterLoginFailure(
	ctx context.Context,
	identityID string,
	threshold int,
	lockUntil, now time.Time,
) (models.AuthIdentity, error) {
	if err := r.ready(); err != nil {
		return models.AuthIdentity{}, err
	}

	row, err := r.q.RegisterLoginFailure(ctx, authdb.RegisterLoginFailureParams{
		ID:          identityID,
		Threshold:   toInt32(int64(threshold)),
		LockedUntil: fromTime(lockUntil),
		Now:         fromTime(now),
	})
	if err != nil {
		return models.AuthIdentity{}, notFoundOr(err, CodeIdentityNotFound,
			"identity record not found: %s", identityID)
	}
	return toIdentity(row)
}

// RegisterLoginSuccess clears the attempt counter and the lock on a successful
// login and writes the moment of the last login.
//
// updated_at does not advance here either: a new login must not close the
// user's sessions on other devices (rationale at the head of
// queries/identities.sql).
func (r *Repo) RegisterLoginSuccess(ctx context.Context, identityID string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if err := r.q.RegisterLoginSuccess(ctx, authdb.RegisterLoginSuccessParams{
		ID:          identityID,
		LastLoginAt: fromTime(now),
	}); err != nil {
		return wrapDB(err, "could not update the login record")
	}
	return nil
}
