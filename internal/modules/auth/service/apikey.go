package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// CreateAPIKeyInput is the write input of an API key.
//
// THE PLAINTEXT KEY IS NOT IN THIS STRUCT: the key is not produced here, it is
// produced by [Service.CreateAPIKey] and is handed out only as the RETURN VALUE
// of that call.
type CreateAPIKeyInput struct {
	// Type is the type of the key: [models.APIKeyPublishable] or
	// [models.APIKeySecret]. It is required.
	Type models.APIKeyType
	// Title is the human-readable name of the key; it is required.
	Title string
	// Scopes are the key's privileges.
	//
	// It is meaningful ONLY for secret keys; if nil is given
	// [models.ScopeAdmin] is applied. Filling it in on a publishable key is
	// REJECTED (see [Service.CreateAPIKey]).
	Scopes []string
	// CreatedBy is the identity of whoever produced the key; it may be left
	// empty.
	CreatedBy string
	// SalesChannelIDs are the channels the publishable key will be attached to.
	//
	// It is meaningful ONLY for publishable keys; filling it in on a secret key
	// is REJECTED.
	SalesChannelIDs []string
}

// CreateAPIKey produces a new API key and returns the PLAINTEXT once.
//
// # The plaintext lives only here
//
// The second return value is the plain form of the key and its only copy: only
// the SHA-256 digest is written to the database (for the reasoning see
// [models.HashToken]). The caller has to forget it once it has been passed on
// to the client — it can never be read from anywhere again. A lost key cannot
// be brought back; the thing to do is to revoke it and produce a new one.
//
// # Type rules
//
// A publishable key CARRIES NO SCOPE and is attached to sales channels; a
// secret key carries scopes and is not attached to a channel. An input that
// mixes the two is not silently corrected, it is REJECTED: a "publishable but
// admin scoped" key would mean an administrative identity placed in the
// browser.
//
// # The key and its links are born together
//
// If one of the channel links cannot be established the key IS NOT WRITTEN
// either. A half-finished write could not be taken back: the plaintext is
// handed out only on the return of a successful call, not on a failed one —
// what would remain behind is a record nobody knows about, one that can never
// be used and never be completed.
//
// # Scope cannot be escalated
//
// The produced key cannot carry a scope THE CALLER DOES NOT HAVE ITSELF (see
// [requireGrantableScopes]); otherwise a key carrying only "orders:read" could
// produce a fully privileged successor for itself in a single request.
//
// # There is no expiry date
//
// API keys DO NOT expire and this is deliberate: a key with a lifetime means an
// integration that silently stops working when its time is up, and that comes
// back as a storefront breaking at midnight. Closing a key happens through an
// EXPLICIT action ([Service.RevokeAPIKey]); the answer to "is it still in use"
// is in the [models.APIKey.LastUsedAt] field.
func (s *Service) CreateAPIKey(
	ctx context.Context,
	in CreateAPIKeyInput,
) (models.APIKey, string, error) {
	if err := s.ready(); err != nil {
		return models.APIKey{}, "", err
	}
	if !in.Type.Valid() {
		return models.APIKey{}, "", errors.Invalid(CodeInvalidInput,
			"the api key type has to be %q or %q, %q given",
			models.APIKeyPublishable, models.APIKeySecret, in.Type)
	}
	if err := requireText("the api key title", in.Title); err != nil {
		return models.APIKey{}, "", err
	}
	if err := checkLen("the api key title", in.Title, models.MaxNameLen); err != nil {
		return models.APIKey{}, "", err
	}

	scopes, err := s.keyScopes(in)
	if err != nil {
		return models.APIKey{}, "", err
	}
	// The check is made over the RESOLVED scopes, not over the raw slice in the
	// input: on a secret key a nil scope list opens up to [models.ScopeAdmin]
	// and a request saying "I granted nothing" would produce a fully privileged
	// key.
	if err := requireGrantableScopes(ctx, scopes); err != nil {
		return models.APIKey{}, "", err
	}
	if in.Type == models.APIKeySecret && len(in.SalesChannelIDs) > 0 {
		return models.APIKey{}, "", errors.Invalid(CodeAPIKeyTypeMismatch,
			"a secret key cannot be attached to a sales channel; the channel link belongs to publishable keys")
	}
	for _, channelID := range in.SalesChannelIDs {
		if err := requireID(channelID, models.SalesChannelIDPrefix, "the sales channel identifier"); err != nil {
			return models.APIKey{}, "", err
		}
	}

	plaintext, err := models.NewToken(in.Type)
	if err != nil {
		return models.APIKey{}, "", errors.Wrap(err, errors.KindInternal, CodeInvalidInput,
			"the api key could not be produced")
	}

	now := s.clock()
	created, err := s.createKeyWithChannels(ctx, models.APIKey{
		ID:        models.NewAPIKeyID(now),
		Type:      in.Type,
		Title:     in.Title,
		TokenHash: models.HashToken(plaintext),
		Redacted:  models.RedactToken(plaintext),
		Scopes:    scopes,
		CreatedBy: in.CreatedBy,
		CreatedAt: now,
	}, in.SalesChannelIDs, now)
	if err != nil {
		return models.APIKey{}, "", err
	}

	// Neither the key itself nor its digest IS LOGGED; the identifier and the
	// type are enough to trace it.
	s.log.InfoContext(ctx, "api key created",
		slog.String("api_key_id", created.ID),
		slog.String("type", created.Type.String()),
		slog.Int("sales_channel_count", len(in.SalesChannelIDs)),
	)
	return created, plaintext, nil
}

// atomicAPIKeyWriter is a repository that can write the key and the channel
// links in a SINGLE database transaction.
//
// The capability was NOT made MANDATORY on the [Repository] interface:
// transaction management is a guarantee only a real database repository can
// give, and had it been put on the interface, testing the service with a fake
// repository would have required writing a transaction imitation into that
// repository too. If the repository offers the capability it is used; if it
// does not, the same result is achieved by COMPENSATION (see
// [Service.createKeyWithChannels]).
type atomicAPIKeyWriter interface {
	CreateAPIKeyWithChannels(
		ctx context.Context,
		k models.APIKey,
		channelIDs []string,
	) (models.APIKey, error)
}

// createKeyWithChannels writes the key and the channel links under the "all or
// nothing" rule.
//
// The reasoning for the rule is in the [Service.CreateAPIKey] godoc: had the
// key row stayed in place when a link could not be established, it would have
// been an unrecoverable piece of garbage.
func (s *Service) createKeyWithChannels(
	ctx context.Context,
	key models.APIKey,
	channelIDs []string,
	now time.Time,
) (models.APIKey, error) {
	if writer, ok := s.repo.(atomicAPIKeyWriter); ok {
		return writer.CreateAPIKeyWithChannels(ctx, key, channelIDs)
	}

	created, err := s.repo.CreateAPIKey(ctx, key)
	if err != nil {
		return models.APIKey{}, err
	}
	for _, channelID := range channelIDs {
		linkErr := s.repo.LinkSalesChannel(ctx, created.ID, channelID, now)
		if linkErr == nil {
			continue
		}
		// In a repository that cannot open a transaction the rollback is done by
		// COMPENSATION: the key that was written is deleted. If the deletion
		// fails too the error IS NOT SWALLOWED, it is logged; the error returned
		// to the caller has to be the failure of the link — the cleanup itself
		// is not a problem the caller can solve.
		if delErr := s.repo.DeleteAPIKey(ctx, created.ID, now); delErr != nil {
			s.log.ErrorContext(ctx, "the api key whose channel link failed could not be rolled back",
				slog.String("api_key_id", created.ID), slog.Any("error", delErr))
		}
		return models.APIKey{}, linkErr
	}
	return created, nil
}

// keyScopes validates the scopes in the input against the type and applies the
// default.
func (s *Service) keyScopes(in CreateAPIKeyInput) ([]string, error) {
	scopes, err := normalizeScopes(in.Scopes)
	if err != nil {
		return nil, err
	}

	if in.Type == models.APIKeyPublishable {
		if len(scopes) > 0 {
			return nil, errors.Invalid(CodeAPIKeyTypeMismatch,
				"a publishable key cannot carry scopes; scopes belong to secret keys")
		}
		// The difference between nil and an empty slice disappears here: the
		// scope list of a publishable key is ALWAYS empty.
		return []string{}, nil
	}
	if scopes == nil {
		return []string{models.ScopeAdmin}, nil
	}
	return scopes, nil
}

// CodeScopeEscalation reports that the caller tried to grant a scope it does
// not have itself.
//
// The constant stands next to the place that enforces it, not in the block of
// its siblings in service.go: whoever reads the code sees the rule and its name
// together.
const CodeScopeEscalation = "auth_scope_escalation"

// requireGrantableScopes verifies that the scopes to be granted are present ON
// THE CALLER as well.
//
// The cheapest road to privilege escalation is an endpoint that hands out
// scopes accepting the handed-out scope without question: a secret key carrying
// "orders:read" could produce a {"scopes":["admin"]} key for itself in a single
// request. The corehttp.RequireScope on the route already closes these
// endpoints down to admin today; the check here does not stand IN ITS PLACE but
// TOGETHER WITH IT — should the scope map of the endpoints ever be loosened,
// the door stays shut here.
//
// # A call with no identity is allowed
//
// If there is no identity in the context the check is not applied, and this is
// not a hole: the only legitimate caller that carries no identity is the
// process itself — the seed step that creates the first administrator inherits
// its privileges from nobody, so it can escalate nobody either. Every request
// arriving over HTTP passes through corehttp.RequireAdmin and therefore has an
// identity in its context; if it did not, the admin surface would already be
// wide open and there would be nothing left to close here.
func requireGrantableScopes(ctx context.Context, scopes []string) error {
	if len(scopes) == 0 {
		return nil
	}

	principal, ok := corehttp.PrincipalFromContext(ctx)
	if !ok {
		return nil
	}

	for _, scope := range scopes {
		if !principal.HasScope(scope) {
			// The error is KindForbidden, not KindUnauthorized: the caller has
			// been recognized, its request has been understood and it has been
			// rejected. Had 401 been returned the client would try to refresh
			// its credentials and hit the same wall over and over again.
			return errors.Forbidden(CodeScopeEscalation,
				"the %q scope cannot be granted because you do not have it yourself", scope)
		}
	}
	return nil
}

// GetAPIKey returns the key with the given identifier; errors.NotFound if there
// is none.
//
// The returned record DOES NOT CONTAIN the plaintext; only its redacted form
// ([models.APIKey.Redacted]) is there.
func (s *Service) GetAPIKey(ctx context.Context, id string) (models.APIKey, error) {
	if err := s.ready(); err != nil {
		return models.APIKey{}, err
	}
	if err := requireID(id, models.APIKeyIDPrefix, "the api key identifier"); err != nil {
		return models.APIKey{}, err
	}
	return s.repo.GetAPIKey(ctx, id)
}

// ListAPIKeysInput is the input of a key listing.
type ListAPIKeysInput struct {
	// Type, if given, restricts the result to keys of this type.
	Type *models.APIKeyType
	// Revoked, if given, filters by the revoked/not revoked distinction.
	Revoked *bool
	// Limit is the page size; [DefaultLimit] is applied if it is 0.
	Limit int64
	// Offset is the number of records to skip.
	Offset int64
}

// ListAPIKeys returns the filtered and paginated list of keys.
func (s *Service) ListAPIKeys(ctx context.Context, in ListAPIKeysInput) (Page[models.APIKey], error) {
	if err := s.ready(); err != nil {
		return Page[models.APIKey]{}, err
	}

	limit, offset, err := normalizePaging(in.Limit, in.Offset)
	if err != nil {
		return Page[models.APIKey]{}, err
	}
	if in.Type != nil && !in.Type.Valid() {
		return Page[models.APIKey]{}, errors.Invalid(CodeInvalidInput,
			"the api key type has to be %q or %q, %q given",
			models.APIKeyPublishable, models.APIKeySecret, *in.Type)
	}

	items, total, err := s.repo.ListAPIKeys(ctx, models.APIKeyFilter{
		Type:    in.Type,
		Revoked: in.Revoked,
	}, limit, offset)
	if err != nil {
		return Page[models.APIKey]{}, err
	}
	return Page[models.APIKey]{Items: items, Count: total, Limit: limit, Offset: offset}, nil
}

// RevokeAPIKey revokes the key; a revoked key is never accepted again.
//
// REVOKING IS NOT DELETING: the record stays in the list and it is visible when
// and by whom it was closed. Only this way can the question "which key was
// open" be answered after a leak.
func (s *Service) RevokeAPIKey(ctx context.Context, id, revokedBy string) (models.APIKey, error) {
	if err := s.ready(); err != nil {
		return models.APIKey{}, err
	}
	if err := requireID(id, models.APIKeyIDPrefix, "the api key identifier"); err != nil {
		return models.APIKey{}, err
	}

	revoked, err := s.repo.RevokeAPIKey(ctx, id, revokedBy, s.clock())
	if err != nil {
		return models.APIKey{}, err
	}

	s.log.InfoContext(ctx, "api key revoked",
		slog.String("api_key_id", revoked.ID),
		slog.String("revoked_by", revokedBy),
	)
	return revoked, nil
}

// DeleteAPIKey soft deletes the key and removes its channel links.
//
// The difference from revoking is that the record also disappears from the
// lists. The operation to prefer after a leak is [Service.RevokeAPIKey];
// deletion is for cleaning up a record that was created by mistake.
func (s *Service) DeleteAPIKey(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.APIKeyIDPrefix, "the api key identifier"); err != nil {
		return err
	}
	if err := s.repo.DeleteAPIKey(ctx, id, s.clock()); err != nil {
		return err
	}

	s.log.InfoContext(ctx, "api key deleted", slog.String("api_key_id", id))
	return nil
}

// LinkSalesChannel attaches a publishable key to a sales channel.
//
// A secret key cannot be attached: the channel link determines which catalog a
// store request will see, and a secret key has no business at all on the store
// surface.
func (s *Service) LinkSalesChannel(ctx context.Context, apiKeyID, channelID string) error {
	key, err := s.linkable(ctx, apiKeyID, channelID)
	if err != nil {
		return err
	}
	if err := s.repo.LinkSalesChannel(ctx, key.ID, channelID, s.clock()); err != nil {
		return err
	}

	s.log.InfoContext(ctx, "publishable key attached to a sales channel",
		slog.String("api_key_id", key.ID),
		slog.String("sales_channel_id", channelID),
	)
	return nil
}

// UnlinkSalesChannel removes the link; errors.NotFound if there is no link.
func (s *Service) UnlinkSalesChannel(ctx context.Context, apiKeyID, channelID string) error {
	key, err := s.linkable(ctx, apiKeyID, channelID)
	if err != nil {
		return err
	}
	if err := s.repo.UnlinkSalesChannel(ctx, key.ID, channelID); err != nil {
		return err
	}

	s.log.InfoContext(ctx, "the sales channel link of the publishable key was removed",
		slog.String("api_key_id", key.ID),
		slog.String("sales_channel_id", channelID),
	)
	return nil
}

// linkable is the shared precondition check of the link operations.
func (s *Service) linkable(ctx context.Context, apiKeyID, channelID string) (models.APIKey, error) {
	if err := s.ready(); err != nil {
		return models.APIKey{}, err
	}
	if err := requireID(apiKeyID, models.APIKeyIDPrefix, "the api key identifier"); err != nil {
		return models.APIKey{}, err
	}
	if err := requireID(channelID, models.SalesChannelIDPrefix, "the sales channel identifier"); err != nil {
		return models.APIKey{}, err
	}

	key, err := s.repo.GetAPIKey(ctx, apiKeyID)
	if err != nil {
		return models.APIKey{}, err
	}
	if key.Type != models.APIKeyPublishable {
		return models.APIKey{}, errors.Invalid(CodeAPIKeyTypeMismatch,
			"only publishable keys can be attached to a sales channel, %q given", key.Type)
	}

	// The existence of the channel is verified here: a foreign key violation
	// would give the same result, but telling the client "sales channel not
	// found" instead of "constraint violation" makes the error usable.
	if _, err := s.repo.GetSalesChannel(ctx, channelID); err != nil {
		return models.APIKey{}, err
	}
	return key, nil
}

// SalesChannelsOfAPIKey returns ALL the channels the key is attached to.
//
// Disabled channels are included as well: the admin surface has to show the
// link as it is. The list used in the store identity, on the other hand,
// FILTERS disabled channels out (see [Service.authenticatePublishable]).
func (s *Service) SalesChannelsOfAPIKey(ctx context.Context, apiKeyID string) ([]models.SalesChannel, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(apiKeyID, models.APIKeyIDPrefix, "the api key identifier"); err != nil {
		return nil, err
	}
	if _, err := s.repo.GetAPIKey(ctx, apiKeyID); err != nil {
		return nil, err
	}
	return s.repo.ChannelsOfKey(ctx, apiKeyID)
}

// authenticateKey verifies a plaintext key and returns its record.
//
// # Two independent type gates
//
// The type of the key is checked TWICE and the repetition is deliberate:
//
//  1. THE PREFIX — the plaintext starting with "sk_" or "pk_". It eliminates a
//     key presented on the wrong surface without going to the database at all.
//  2. THE RECORD — the [models.APIKey.Type] field on the row that was read.
//     Even if the prefix changed one day, or a record were edited by hand, this
//     is the real type.
//
// Had a single gate been enough, a mistake in either of the two would mean a
// publishable key passing through to the admin surface.
//
// The digest comparison is done in constant time IN ADDITION TO the indexed
// lookup (see [models.TokenHashesEqual]).
//
// Every failure returns errors.Unauthorized; the detail is logged by the core's
// middleware and does not go to the client.
func (s *Service) authenticateKey(
	ctx context.Context,
	plaintext string,
	want models.APIKeyType,
) (models.APIKey, error) {
	if err := s.ready(); err != nil {
		return models.APIKey{}, err
	}

	kind, err := models.TypeForToken(plaintext)
	if err != nil || kind != want {
		return models.APIKey{}, errors.Unauthorized(CodeAPIKeyTypeMismatch,
			"this surface accepts only api keys of type %q", want)
	}

	hash := models.HashToken(plaintext)
	key, err := s.repo.GetAPIKeyByHash(ctx, hash)
	if err != nil {
		if errors.IsNotFound(err) {
			return models.APIKey{}, errors.Unauthorized(CodeInvalidCredentials,
				"the api key was not recognized")
		}
		return models.APIKey{}, err
	}
	if !models.TokenHashesEqual(key.TokenHash, hash) {
		return models.APIKey{}, errors.Unauthorized(CodeInvalidCredentials,
			"the api key was not recognized")
	}
	if key.Type != want {
		return models.APIKey{}, errors.Unauthorized(CodeAPIKeyTypeMismatch,
			"this surface accepts only api keys of type %q", want)
	}
	if key.IsRevoked() {
		return models.APIKey{}, errors.Unauthorized(CodeAPIKeyRevoked,
			"the api key has been revoked: %s", key.ID)
	}

	s.touchKey(ctx, key)
	return key, nil
}

// authenticatePublishable verifies a publishable key and returns the
// identifiers of the ENABLED sales channels it is attached to.
//
// # A key with no channel IS REJECTED
//
// A publishable key attached to no enabled channel is not accepted. The
// reasoning: an empty channel list can mean TWO THINGS downstream — "no
// channels at all" or "no channel filter". The second reading would remove the
// catalog filtering entirely and the ambiguity would be resolved in the unsafe
// direction. Falling closed at the door is better than silently opening up
// further down.
//
// Disabled and deleted channels are filtered out of the list; if all of them
// are filtered out the key is rejected as well.
func (s *Service) authenticatePublishable(ctx context.Context, plaintext string) (models.APIKey, []string, error) {
	key, err := s.authenticateKey(ctx, plaintext, models.APIKeyPublishable)
	if err != nil {
		return models.APIKey{}, nil, err
	}

	channelIDs, err := s.repo.ChannelIDsOfKey(ctx, key.ID)
	if err != nil {
		return models.APIKey{}, nil, err
	}
	if len(channelIDs) == 0 {
		return models.APIKey{}, nil, errors.Unauthorized(CodeNoSalesChannel,
			"the publishable key is not attached to an enabled sales channel: %s", key.ID)
	}
	return key, channelIDs, nil
}

// touchKey updates the last-used moment of the key APPROXIMATELY.
//
// The write is thinned out with [Options.UsageThrottle] and the decision is
// made HERE: since the row that was read is already at hand, no second round
// trip to the database is made for a record that is already up to date.
//
// An error DOES NOT AFFECT the login: the last-used moment is a statistic, not
// part of the authentication decision.
func (s *Service) touchKey(ctx context.Context, key models.APIKey) {
	now := s.clock()
	staleBefore := now.Add(-s.throttle)
	if key.LastUsedAt != nil && !key.LastUsedAt.Before(staleBefore) {
		return
	}

	if err := s.repo.MarkAPIKeyUsed(ctx, key.ID, now, staleBefore); err != nil {
		s.log.WarnContext(ctx, "the last-used moment of the api key could not be updated",
			slog.String("api_key_id", key.ID), slog.Any("error", err))
	}
}
