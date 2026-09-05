package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// This file defines when and HOW a session drops.
//
// # THERE IS NO SESSION RECORD
//
// The token holds no state: there is no table called "open sessions" on the
// server and there is no way to invalidate a SINGLE token that has been
// produced. Doing that would require putting a "jti" into the token and keeping
// a blacklist store that every request reads and whose expired records are
// cleaned up regularly — that price, which means giving up the token being
// stateless, buys no new capability today (see [Service.Logout]).
//
// Instead, a SINGLE timestamp is kept per identity: [sessionAnchor]. A token is
// rejected if it was produced before the anchor. This is why revocation is
// always WHOLESALE — when the anchor moves forward, the tokens on all of the
// user's devices drop at the same moment.
//
// # The anchor is per IDENTITY, not per user
//
// The auth_identity table keeps a row per provider, so a user can have more
// than one anchor. Both ends see this multiplicity and apply THE SAME rule:
// logout advances all of them ([Service.Logout]), while token verification
// reads the NEWEST one ([latestAnchor], Repository.SessionAnchor). Had only one
// of them selected by provider the other would run for nothing — either the
// anchor logout writes would never be read, or the anchor that is read would
// never be written.
//
// # Only THE OWNER'S OWN WILL advances the anchor
//
// Two operations advance the anchor and both of them are things the account
// owner does knowingly: a password change ([Service.SetPassword]) and a logout
// ([Service.Logout]). The login counters DO NOT TOUCH the anchor; had they done
// so, a single failed login attempt would be a targeted denial-of-service tool
// dropping all of the victim's sessions (the reasoning is in
// queries/identities.sql).

// sessionAnchor returns the identity's session anchor: tokens produced BEFORE
// this moment are invalid.
//
// # Why updated_at
//
// There is NO separate "sessions_revoked_at" column on the table and none is
// needed: in this table updated_at already moves only in the two writes that
// have to advance the anchor (UpdatePasswordHash, RevokeSessions). The queries
// that write the login counters deliberately do not touch it.
//
// The function is a single line but it names A CONTRACT: should the choice of
// column change later on (if, for example, a separate column really is added),
// this is the only place to touch.
func sessionAnchor(identity models.AuthIdentity) time.Time {
	return identity.UpdatedAt
}

// latestAnchor returns the NEWEST session anchor of a set of identities; the
// zero time if the set is empty.
//
// The NEWEST one is picked, not the oldest. Anchors advance per provider and
// they do not all advance together: a password change writes only the emailpass
// row. Taking the oldest would mean a single row that never advances rendering
// the whole revocation ineffective. The same choice is made on the reading side
// in SQL (queries/identities.sql, GetSessionAnchor); the two ends apply the
// same rule.
//
// An empty set is a case for the caller to eliminate: the logout of a user who
// has no identity is rejected in the repository layer with errors.NotFound.
func latestAnchor(identities []models.AuthIdentity) time.Time {
	var latest time.Time
	// Iteration goes over the index: [models.AuthIdentity] is a large struct and
	// iterating by value would produce a needless copy on every round.
	for i := range identities {
		if anchor := sessionAnchor(identities[i]); anchor.After(latest) {
			latest = anchor
		}
	}
	return latest
}

// Logout closes the caller's sessions and returns the moment the revocation
// rests on.
//
// # ALL sessions drop; a single device cannot be picked
//
// This endpoint closes NOT one device but all of the caller's sessions: an
// administrator logging out from their phone has also closed the session on
// their laptop. The limit is real and must not be hidden — a user who thinks "I
// logged out" needs to know what they actually did.
//
// Dropping a single device HAS NOT BEEN ADDED because the token is stateless:
// saying "drop that token" means a jti-based blacklist, that is, A NEW STORE
// that is read on every request and whose expired records are cleaned up. The
// need of today — "I lost my device, log me out everywhere" — is already met by
// wholesale revocation; the distinction will be added when it is really needed
// (see the head of the file).
//
// # ALL providers drop
//
// The anchor is advanced on ALL of the user's identity rows, not only on the
// [models.ProviderEmailPass] one. There is NO observable difference today: that
// is the only provider, so the number of advanced rows is one and the endpoint
// gives the same answer. What is gained is the closing of a future silent hole
// — the day OAuth is added, a logout that picks a single provider WOULD NOT
// DROP the tokens obtained from the other provider, and it would do so without
// saying anything: the user who received a 204 would still be logged in.
//
// The other end of the chain applies the same rule: when a token is verified,
// the anchor is read not from a single provider but from the user's newest
// identity (see [Service.principalFromToken]). Had only this side changed, the
// extra anchor that is written would never be read and the change would be good
// for nothing.
//
// # It requires ONLY AN IDENTITY
//
// Closing one's own session is not a privilege: a user with no scopes at all
// must be able to log out too. Had a scope been put on the endpoint, the token
// in the hands of an administrator whose scopes had been taken back would
// become impossible to close until it expired — that is, losing one's scopes
// would also make one's session impossible to close.
//
// # An API key CANNOT log out
//
// If the caller is an API key the request is rejected with a typed error
// ([CodeNoSession]). A key has no session: it arrives not with a token but with
// a permanent secret, and that secret would keep working after this call too.
// Returning success in silence would leave the caller with the ILLUSION that
// the key had been closed; the way to close a key is the
// POST /admin/v1/api-keys/{id}/revoke endpoint.
//
// # The edge case
//
// The comparison has second resolution: a token produced in the SAME second as
// the logout survives (the reasoning is in [parsedToken.issuedBefore]). The
// opposite choice would drop the FRESH token of a user who logs back in
// immediately after logging out. The accepted price is that the effect of the
// logout is delayed by at most the end of that second; next to the 12-hour
// token lifetime that is immeasurable.
//
// The returned moment is the anchor WRITTEN to the identity: the client can see
// for itself whether a token in its hands was produced before this moment.
func (s *Service) Logout(ctx context.Context, principalID, principalKind string) (time.Time, error) {
	if err := s.ready(); err != nil {
		return time.Time{}, err
	}
	if principalKind != PrincipalKindUser {
		return time.Time{}, errors.Invalid(CodeNoSession,
			"a caller of kind %q has no session that could be closed; "+
				"an api key is revoked from the POST /admin/v1/api-keys/{id}/revoke endpoint",
			principalKind)
	}
	if err := requireID(principalID, models.UserIDPrefix, "the user identifier"); err != nil {
		return time.Time{}, err
	}

	// If there is no identity record at all errors.NotFound is returned. This
	// path is normally impossible: the caller's token was verified by reading
	// its anchor (see [Service.principalFromToken]). Even so, success IS NOT
	// RETURNED in silence — had it been, a fault in which the logout wrote
	// nothing (a deleted identity) would stay invisible behind a 204 response.
	identities, err := s.repo.RevokeSessions(ctx, principalID, s.clock())
	if err != nil {
		return time.Time{}, err
	}

	revokedAt := latestAnchor(identities)
	s.log.InfoContext(ctx, "admin sessions closed",
		slog.String("user_id", principalID),
		slog.Time("revoked_at", revokedAt),
		// How many identities were advanced is logged: the day a second provider
		// is added, this is the only place the logout really touching all of them
		// can be seen from.
		slog.Int("identity_count", len(identities)),
	)
	return revokedAt, nil
}
