package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository/authdb"
)

// CreateAPIKey writes an API key record with NO channel link.
//
// The only secret field written is [models.APIKey.TokenHash]; the plain text
// does NOT PASS through this signature and never reaches the database.
//
// For a publishable key that will be linked to channels,
// [Repo.CreateAPIKeyWithChannels] must be used; the rationale is there.
func (r *Repo) CreateAPIKey(ctx context.Context, k models.APIKey) (models.APIKey, error) {
	return r.CreateAPIKeyWithChannels(ctx, k, nil)
}

// CreateAPIKeyWithChannels writes the key and its sales channel links in a
// SINGLE transaction.
//
// Atomicity here is not a nicety but a requirement of irreversibility: had the
// write been split into two separate transactions and had one of the links
// failed, what would be left behind is a key row whose plain text NEVER REACHED
// THE CALLER. That row is unusable because it was given to nobody, and it
// cannot be completed because its plain text can never be produced again — it
// is a garbage record that can only be cleaned up by hand. When the transaction
// is rolled back, on the other hand, nothing is left behind.
//
// While the link is made, the channel is required to be LIVE (see
// queries/sales_channels.sql, LockLiveSalesChannel).
func (r *Repo) CreateAPIKeyWithChannels(
	ctx context.Context,
	k models.APIKey,
	channelIDs []string,
) (models.APIKey, error) {
	if err := r.ready(); err != nil {
		return models.APIKey{}, err
	}
	if len(channelIDs) == 0 {
		// A single statement is already atomic on its own; opening a
		// transaction here would only add an extra BEGIN/COMMIT round to every
		// key write.
		return insertKey(ctx, r.q, k)
	}

	var created models.APIKey
	if err := r.inTx(ctx, func(q *authdb.Queries) error {
		var err error
		if created, err = insertKey(ctx, q, k); err != nil {
			return err
		}
		for _, channelID := range channelIDs {
			// The time of the link is the key's creation moment: because the
			// two are written in one transaction, reading a separate "now"
			// would only show the same event with two different timestamps.
			if err := linkChannel(ctx, q, created.ID, channelID, k.CreatedAt); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return models.APIKey{}, err
	}
	return created, nil
}

// insertKey writes the key row; it can also run inside the caller's transaction.
func insertKey(ctx context.Context, q *authdb.Queries, k models.APIKey) (models.APIKey, error) {
	row, err := q.InsertAPIKey(ctx, authdb.InsertAPIKeyParams{
		ID:        k.ID,
		Type:      k.Type.String(),
		Title:     k.Title,
		TokenHash: k.TokenHash,
		Redacted:  k.Redacted,
		Scopes:    k.Scopes,
		CreatedBy: k.CreatedBy,
		CreatedAt: fromTime(k.CreatedAt),
	})
	if err != nil {
		if ConstraintName(err) == IndexTokenHash {
			// Landing here means the 256-bit generator collided; in practice
			// that is impossible, and it cannot be passed over silently.
			return models.APIKey{}, errors.Wrap(err, errors.KindConflict, CodeDuplicate,
				"the api key hash is already recorded: %s", k.ID)
		}
		return models.APIKey{}, wrapDB(err, "could not create api key")
	}
	return toAPIKey(row), nil
}

// GetAPIKey returns the key by id; errors.NotFound when there is none.
func (r *Repo) GetAPIKey(ctx context.Context, id string) (models.APIKey, error) {
	if err := r.ready(); err != nil {
		return models.APIKey{}, err
	}

	row, err := r.q.GetAPIKey(ctx, id)
	if err != nil {
		return models.APIKey{}, notFoundOr(err, CodeAPIKeyNotFound, "api key not found: %s", id)
	}
	return toAPIKey(row), nil
}

// GetAPIKeyByHash returns the key matching the given hash; errors.NotFound when
// there is none.
//
// REVOKED keys are returned as well: keeping revocation a separate and explicit
// branch is what makes the claim "a revoked key is rejected" provable by a test
// (see queries/api_keys.sql).
//
// The hash DOES NOT APPEAR in the error message; even though leaking it would
// be harmless, letting a secret field land in a log must not become a habit.
func (r *Repo) GetAPIKeyByHash(ctx context.Context, tokenHash string) (models.APIKey, error) {
	if err := r.ready(); err != nil {
		return models.APIKey{}, err
	}

	row, err := r.q.GetAPIKeyByHash(ctx, tokenHash)
	if err != nil {
		return models.APIKey{}, notFoundOr(err, CodeAPIKeyNotFound, "api key not found")
	}
	return toAPIKey(row), nil
}

// ListAPIKeys returns the filtered and paginated key list together with the
// TOTAL number of records matching the filter.
func (r *Repo) ListAPIKeys(
	ctx context.Context,
	filter models.APIKeyFilter,
	limit, offset int64,
) ([]models.APIKey, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	var keyType *string
	if filter.Type != nil {
		value := filter.Type.String()
		keyType = &value
	}

	rows, err := r.q.ListAPIKeys(ctx, authdb.ListAPIKeysParams{
		KeyType: keyType,
		Revoked: filter.Revoked,
		Lim:     toInt32(limit),
		Off:     toInt32(offset),
	})
	if err != nil {
		return nil, 0, wrapDB(err, "could not read the api key list")
	}

	total, err := r.q.CountAPIKeys(ctx, authdb.CountAPIKeysParams{
		KeyType: keyType,
		Revoked: filter.Revoked,
	})
	if err != nil {
		return nil, 0, wrapDB(err, "could not read the api key count")
	}
	return toAPIKeys(rows), total, nil
}

// RevokeAPIKey revokes the key; if it is already revoked, errors.Conflict is
// returned.
//
// Returning a silent no-op on an already revoked key would either let the
// revocation time shift with the second call or make the caller believe "I
// revoked it".
func (r *Repo) RevokeAPIKey(
	ctx context.Context,
	id, revokedBy string,
	now time.Time,
) (models.APIKey, error) {
	if err := r.ready(); err != nil {
		return models.APIKey{}, err
	}

	row, err := r.q.RevokeAPIKey(ctx, authdb.RevokeAPIKeyParams{
		ID:        id,
		RevokedAt: fromTime(now),
		RevokedBy: revokedBy,
	})
	if err == nil {
		return toAPIKey(row), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return models.APIKey{}, wrapDB(err, "could not revoke api key")
	}

	// No row came back: the key either does not exist or has already been
	// revoked. To tell the two apart we read the record; without the
	// distinction the caller could not choose between "not there" and "already
	// closed".
	existing, getErr := r.GetAPIKey(ctx, id)
	if getErr != nil {
		return models.APIKey{}, getErr
	}
	return models.APIKey{}, errors.Conflict(CodeAlreadyRevoked,
		"api key has already been revoked: %s", existing.ID)
}

// DeleteAPIKey soft-deletes the key and removes its sales channel links.
//
// The links are deleted in the SAME transaction: because a soft delete is an
// UPDATE, the foreign key CASCADE does not engage and the link rows would keep
// showing a deleted key as linked to a channel.
func (r *Repo) DeleteAPIKey(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	return r.inTx(ctx, func(q *authdb.Queries) error {
		if _, err := q.SoftDeleteAPIKey(ctx, authdb.SoftDeleteAPIKeyParams{
			ID:        id,
			DeletedAt: fromTime(now),
		}); err != nil {
			return notFoundOr(err, CodeAPIKeyNotFound, "api key not found: %s", id)
		}
		if err := q.DeleteLinksOfAPIKey(ctx, id); err != nil {
			return wrapDB(err, "could not delete the channel links of the api key")
		}
		return nil
	})
}

// MarkAPIKeyUsed updates the key's last-use moment APPROXIMATELY.
//
// The staleBefore threshold thins out the write: had the column been written on
// every request, authentication on a hot publishable key would turn into a
// bottleneck writing to a single row (see queries/api_keys.sql).
func (r *Repo) MarkAPIKeyUsed(ctx context.Context, id string, usedAt, staleBefore time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if err := r.q.MarkAPIKeyUsed(ctx, authdb.MarkAPIKeyUsedParams{
		ID:          id,
		UsedAt:      fromTime(usedAt),
		StaleBefore: fromTime(staleBefore),
	}); err != nil {
		return wrapDB(err, "could not update the last-use moment of the api key")
	}
	return nil
}

// LinkSalesChannel links a publishable key to a sales channel.
//
// Repeating the same link is NOT an error (a link is a set). A key that does
// not exist returns errors.Invalid through a foreign key violation, and a
// channel that does not exist or has been soft-deleted returns errors.NotFound.
//
// # The CHANNEL is protected; the KEY is not, and that was measured
//
// The lock below covers one side of the link only. On 2026-09-06 the other side
// was produced: the service reads the key ([Repo.GetAPIKey], its own autocommit
// statement) to decide that it is publishable, and a [Repo.DeleteAPIKey]
// landing between that read and this write leaves a link row for a key the
// delete had just unlinked. The foreign key does not object, for the same
// reason it does not object anywhere else in this module — a soft delete leaves
// the row physically in place.
//
// It is NOT fixed and the measurement says why: the row is unreachable. Every
// road to it reads the live key first — service.SalesChannelsOfAPIKey through
// GetAPIKey, the store identity through GetAPIKeyByHash — and both filter
// deleted keys, so the orphan is visible to nobody and gives nobody anything.
// The fix would be one more FOR SHARE, on api_key, taken BEFORE the channel's
// (the order matters: DeleteAPIKey walks api_key then the link rows, so any
// flow taking the channel first would close a waiting cycle). That is a new
// lock ordering constraint on a table pair whose two delete paths already
// contend on the same link rows, and buying it for a row nobody can read is the
// wrong trade until something can read the row.
func (r *Repo) LinkSalesChannel(ctx context.Context, apiKeyID, channelID string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	// The check and the write are in a SINGLE transaction: had the liveness of
	// the channel been asked in a separate round, a soft delete performed in
	// between would still leave a dead link behind.
	return r.inTx(ctx, func(q *authdb.Queries) error {
		return linkChannel(ctx, q, apiKeyID, channelID, now)
	})
}

// linkChannel writes the link after verifying that the channel is LIVE.
//
// The foreign key alone is not enough: because the row of a soft-deleted
// channel stays in place, it passes the FK, but the read queries filter it out.
// Had such a link been made, the publishable key would be BORN DEAD — it looks
// linked, reaches no channel at all and could not establish a store identity
// (see queries/sales_channels.sql, LockLiveSalesChannel).
func linkChannel(
	ctx context.Context,
	q *authdb.Queries,
	apiKeyID, channelID string,
	now time.Time,
) error {
	if _, err := q.LockLiveSalesChannel(ctx, channelID); err != nil {
		return notFoundOr(err, CodeSalesChannelNotFound,
			"sales channel not found: %s", channelID)
	}
	if err := q.LinkAPIKeySalesChannel(ctx, authdb.LinkAPIKeySalesChannelParams{
		ApiKeyID:       apiKeyID,
		SalesChannelID: channelID,
		CreatedAt:      fromTime(now),
	}); err != nil {
		return wrapDB(err, "the api key could not be linked to channel %s", channelID)
	}
	return nil
}

// UnlinkSalesChannel removes the link; errors.NotFound when there is no link.
//
// Silently counting the removal of a link that does not exist as a success
// would never tell the caller that they wrote the wrong channel name.
func (r *Repo) UnlinkSalesChannel(ctx context.Context, apiKeyID, channelID string) error {
	if err := r.ready(); err != nil {
		return err
	}

	affected, err := r.q.UnlinkAPIKeySalesChannel(ctx, authdb.UnlinkAPIKeySalesChannelParams{
		ApiKeyID:       apiKeyID,
		SalesChannelID: channelID,
	})
	if err != nil {
		return wrapDB(err, "could not remove the channel link of the api key")
	}
	if affected == 0 {
		return errors.NotFound(CodeSalesChannelNotFound,
			"key %s is not linked to channel %s", apiKeyID, channelID)
	}
	return nil
}

// ChannelIDsOfKey returns the ids of the ACTIVE channels the key is linked to.
//
// Disabled and deleted channels are filtered out; the store identity is built
// from this list and the catalog of a disabled channel must not be visible.
func (r *Repo) ChannelIDsOfKey(ctx context.Context, apiKeyID string) ([]string, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	ids, err := r.q.ListChannelIDsForKey(ctx, apiKeyID)
	if err != nil {
		return nil, wrapDB(err, "could not read the channels of the api key")
	}
	return ids, nil
}

// ChannelsOfKey returns ALL of the channels the key is linked to.
//
// Disabled channels are included as well: the admin surface must show the link
// as it is and must not hide that a channel is disabled.
func (r *Repo) ChannelsOfKey(ctx context.Context, apiKeyID string) ([]models.SalesChannel, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.q.ListChannelsForKey(ctx, apiKeyID)
	if err != nil {
		return nil, wrapDB(err, "could not read the channels of the api key")
	}
	return toSalesChannels(rows)
}
