package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// This file carries the two surfaces auth opens to the OUTSIDE.
//
// # 1. The authentication surface
//
// [Interop] satisfies the core's corehttp.Authenticator interface
// STRUCTURALLY and is registered in the container under the name
// "auth.interop". The core resolves it BY NAME; it does NOT import the auth
// module (Principle 2.4, ADR 0001).
//
// corehttp IS NOT A MODULE, IT IS THE CORE, which is why it can be imported
// from here. The corehttp.Principal type is NOT REDEFINED in this package: a
// second type carrying the same name breaks structural compatibility and the
// core interface could not be satisfied.
//
// # 2. The cross-module primitive surface
//
// Other modules (e.g. product's catalog filtering by sales channel) CANNOT
// import auth; this is why the methods opened to them use ONLY primitive and
// stdlib types, and the consumer redefines the same signature in its own
// package:
//
//	// in the product module, WITHOUT importing auth:
//	type SalesChannelReader interface {
//	    ActiveSalesChannelIDs(ctx context.Context) ([]string, error)
//	}
//	channels, err := container.Resolve[SalesChannelReader](c, "auth.service")
//
// The surface is deliberately NARROW: every method added here is a contract
// auth can never change again. If all the fields of a channel are needed, the
// right path is not a new primitive method but the Query layer (see
// provider.go).

// Principal.Kind values; this is the vocabulary the core expects.
const (
	// PrincipalKindUser reports that the identity is an admin user.
	PrincipalKindUser = "user"
	// PrincipalKindAPIKey reports that the identity is an API key.
	PrincipalKindAPIKey = "api_key"
)

// schemeBearer is the only Authorization scheme the admin surface accepts.
const schemeBearer = "bearer"

// jwtSegmentCount is the number of dot-separated segments of a JWT.
const jwtSegmentCount = 3

// Interop is the authentication surface auth opens to the core.
//
// It satisfies the core's corehttp.Authenticator interface structurally; the
// interface is defined on the CONSUMER side (in the core), this type only
// carries the signature (ADR 0001).
type Interop struct {
	svc *Service
}

var _ corehttp.Authenticator = (*Interop)(nil)

// NewInterop produces the authenticator that runs on the given service.
func NewInterop(svc *Service) *Interop {
	return &Interop{svc: svc}
}

// AuthenticateAdmin resolves the identity of the admin surface.
//
// # Accepted credentials and their ORDER
//
// The scheme can only be "Bearer". The credential is one of two shapes and
// they are tried in order:
//
//  1. SESSION TOKEN (JWT) — the normal, human path: the admin logs in, gets a
//     token, and travels with the token. It is tried first because this is the
//     common one. The token itself does not require a LOOKUP; once its
//     signature is verified, two indexed reads are made (does the owner still
//     exist, was the session dropped after the token — see
//     [Service.principalFromToken]).
//  2. SECRET API KEY — the machine-to-machine path: scripts and integrations.
//
// The order IS NOT TRIAL AND ERROR. The two shapes are syntactically disjoint:
// a JWT contains exactly two dots, an API key starts with "sk_" and contains
// no dots. The branching is therefore definite; running both of them in order
// and taking the first success would mean making an unnecessary database
// lookup on every wrong credential and letting a publishable key be probed on
// the admin surface.
//
// A publishable key IS NOT ACCEPTED HERE: the "pk_" prefix neither resembles a
// JWT nor starts with "sk_", so it is eliminated at the very first step. Even
// if it were not eliminated, the type check inside [Service.authenticateKey]
// stands as the second gate.
//
// Every failure returns errors.Unauthorized; the reason is logged by the
// core's middleware and IS NOT LEAKED to the client.
func (i *Interop) AuthenticateAdmin(
	ctx context.Context,
	scheme, credential string,
) (corehttp.Principal, error) {
	if i == nil || i.svc == nil {
		return corehttp.Principal{}, errors.Unavailable(CodeUnconfigured, "auth service is not configured")
	}
	if !strings.EqualFold(scheme, schemeBearer) {
		return corehttp.Principal{}, errors.Unauthorized(CodeInvalidCredentials,
			"the %q scheme is not supported; \"Bearer\" expected", scheme)
	}

	credential = strings.TrimSpace(credential)
	switch {
	case looksLikeJWT(credential):
		return i.svc.principalFromToken(ctx, credential)
	case strings.HasPrefix(credential, models.SecretKeyPrefix):
		return i.svc.principalFromSecretKey(ctx, credential)
	default:
		return corehttp.Principal{}, errors.Unauthorized(CodeInvalidCredentials,
			"the credential is in neither session token nor secret api key form")
	}
}

// AuthenticateStore resolves the identity of the store surface.
//
// Only a PUBLISHABLE key is accepted. A secret key is REJECTED here, and this
// is ensured by two independent gates: the "sk_" prefix does not match the
// expected "pk_" prefix, and the type field on the record does not hold
// either.
//
// A publishable key IS NOT A SECRET; its only job is to bind the request to a
// sales channel. This is why NO SCOPE is put on the returned identity: a
// request arriving from the store surface cannot cross to an admin endpoint
// even if scopes are written on the key record.
//
// A revoked key and a key left with no enabled channel are rejected (see
// [Service.authenticatePublishable]).
func (i *Interop) AuthenticateStore(ctx context.Context, key string) (corehttp.Principal, error) {
	if i == nil || i.svc == nil {
		return corehttp.Principal{}, errors.Unavailable(CodeUnconfigured, "auth service is not configured")
	}

	apiKey, channelIDs, err := i.svc.authenticatePublishable(ctx, strings.TrimSpace(key))
	if err != nil {
		return corehttp.Principal{}, err
	}

	return corehttp.Principal{
		ID:   apiKey.ID,
		Kind: PrincipalKindAPIKey,
		// The scope list is DELIBERATELY empty; the value on the record is not
		// even read. This is the THIRD gate of the rule that a publishable key
		// carries no scope (the first two: rejection at creation, the type
		// check).
		Scopes:          nil,
		SalesChannelIDs: channelIDs,
	}, nil
}

// principalFromToken builds an identity from a session token.
//
// # Scopes are read from the database, NOT from the token
//
// The token carries a "scopes" claim, but the scope decision IS NOT MADE by
// looking at it: the current scopes on the user's record are read. Otherwise,
// after an admin's scope had been taken back, the token in their hands would
// keep working with the old scope until it expired (12 hours by default). The
// list in the token is only a copy that serves the client in drawing its
// interface.
//
// # Whether the user still exists is asked
//
// A token with a valid signature is not accepted if its owner HAS BEEN
// DELETED. The query is a single read over the primary key; its cost is
// nothing next to the cost of a deleted admin being able to stay inside for 12
// hours.
//
// # Logout and password change DROP the token
//
// A token is rejected if its owner's session anchor moved forward AFTER the
// token was produced. Two operations move the anchor forward: logout
// ([Service.Logout]) and password change ([Service.SetPassword]). Without this
// check a leaked admin token would keep producing a fully privileged identity
// for [DefaultJWTTTL] (12 hours by default) even if both of them were done —
// that is, neither "I logged out" nor "I changed my password" would take
// anything back.
//
// The comparison is between the token's "iat" claim and the identity's
// [sessionAnchor] value; the edge case brought by second resolution and the
// choice made there are spelled out in the [parsedToken.issuedBefore] godoc.
//
// # The anchor is NOT chosen PER PROVIDER
//
// The value read is the user's NEWEST anchor, not that of a single provider
// (Repository.SessionAnchor). No selection can be made because the token
// CARRIES no claim saying which provider it was obtained from; and looking at
// a fixed provider would be inconsistent with logout — logout moves all rows
// forward ([Service.Logout]), and if verification looked at a single row, then
// on the day OAuth is added that provider's tokens would continue to be
// accepted after a logout as well.
//
// The ambiguity is resolved in favor of SECURITY: a revocation at one provider
// drops the other's tokens as well. The opposite choice (the oldest anchor)
// would mean a single row that never moves forward rendering the whole
// revocation ineffective. If per-provider precision is genuinely needed, the
// path is to add a provider claim to the token.
//
// Because there is a single provider today, the value read is the SAME as the
// old one; what is gained is the consistency on the day the second row is
// added.
//
// If there is NO login identity at all, the token is rejected. This path is
// normally impossible — a token is produced only by [Service.Login], that is,
// for a user who has an identity — but if the identity has been deleted, no
// value remains to say when the token became invalid either; accepting in such
// a case would be leaving the door open to bypassing the check by deleting the
// identity.
//
// # Cost
//
// The anchor read IS NOT AN EXTRA ROUND TRIP: this path was already making one
// database read per request (scopes are read from the record, not from the
// token, see above). The second read is indexed
// (auth_identity_user_provider_uniq, with the user_id prefix) and, because the
// number of providers is countable by hand, the ordering is over a handful of
// rows; it takes no measurable place in the request's budget. In return,
// revocation takes effect IMMEDIATELY.
func (s *Service) principalFromToken(ctx context.Context, raw string) (corehttp.Principal, error) {
	if err := s.ready(); err != nil {
		return corehttp.Principal{}, err
	}

	parsed, err := s.parseToken(raw)
	if err != nil {
		return corehttp.Principal{}, err
	}

	user, err := s.repo.GetUser(ctx, parsed.Subject)
	if err != nil {
		if errors.IsNotFound(err) {
			return corehttp.Principal{}, errors.Unauthorized(CodeTokenInvalid,
				"the user in the token no longer exists: %s", parsed.Subject)
		}
		return corehttp.Principal{}, err
	}

	anchor, err := s.repo.SessionAnchor(ctx, user.ID)
	if err != nil {
		if errors.IsNotFound(err) {
			return corehttp.Principal{}, errors.Unauthorized(CodeTokenInvalid,
				"the login identity the token rests on no longer exists: %s", user.ID)
		}
		return corehttp.Principal{}, err
	}
	if parsed.issuedBefore(anchor) {
		return corehttp.Principal{}, errors.Unauthorized(CodeTokenInvalid,
			"the token was produced before a logout or a password change: %s", user.ID)
	}

	return corehttp.Principal{
		ID:     user.ID,
		Kind:   PrincipalKindUser,
		Scopes: user.Scopes,
	}, nil
}

// principalFromSecretKey builds an identity from a secret API key.
func (s *Service) principalFromSecretKey(ctx context.Context, credential string) (corehttp.Principal, error) {
	key, err := s.authenticateKey(ctx, credential, models.APIKeySecret)
	if err != nil {
		return corehttp.Principal{}, err
	}

	return corehttp.Principal{
		ID:     key.ID,
		Kind:   PrincipalKindAPIKey,
		Scopes: key.Scopes,
	}, nil
}

// looksLikeJWT reports whether the credential is in JWT shape.
//
// The check is a SHAPE check, not verification: it only decides the branching.
// The signature segment is allowed to be EMPTY and this is deliberate — the
// token of the "alg: none" attack is exactly of this shape ("header.body.")
// and it MUST REACH the verifier so that it is EXPLICITLY rejected there. Had
// it been eliminated here the request would still have got a 401, but the
// reason for the rejection would be "the shape was not recognized" and there
// would be no test proving that the algorithm check runs.
func looksLikeJWT(credential string) bool {
	if strings.HasPrefix(credential, models.SecretKeyPrefix) ||
		strings.HasPrefix(credential, models.PublishableKeyPrefix) {
		return false
	}
	parts := strings.Split(credential, ".")
	if len(parts) != jwtSegmentCount {
		return false
	}
	return parts[0] != "" && parts[1] != ""
}

// ActiveSalesChannelIDs returns the identifiers of the enabled sales channels.
//
// It is a cross-module primitive surface: the consumer (e.g. a module doing
// catalog filtering) redefines this signature in its own package and resolves
// the concrete service from the container under the name "auth.service"
// (ADR 0001).
//
// Disabled and deleted channels ARE NOT RETURNED. If there is no channel at
// all, an empty (non-nil) slice is returned.
func (s *Service) ActiveSalesChannelIDs(ctx context.Context) ([]string, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}

	disabled := false
	ids := make([]string, 0, DefaultLimit)
	for offset := int64(0); ; offset += MaxLimit {
		channels, total, err := s.repo.ListSalesChannels(ctx,
			models.SalesChannelFilter{IsDisabled: &disabled}, MaxLimit, offset)
		if err != nil {
			return nil, err
		}
		for i := range channels {
			ids = append(ids, channels[i].ID)
		}
		// It stops if the page comes back empty or the total count has been
		// reached; without the second condition one more round would be made
		// after the last page.
		if len(channels) == 0 || int64(len(ids)) >= total {
			break
		}
	}
	return ids, nil
}

// SalesChannelName returns the channel's name; errors.NotFound if there is no
// such channel.
//
// It is a cross-module primitive surface. If all the fields of the channel are
// needed, the right path is the Query layer (the "sales_channel" provider, see
// provider.go).
func (s *Service) SalesChannelName(ctx context.Context, channelID string) (string, error) {
	channel, err := s.GetSalesChannel(ctx, channelID)
	if err != nil {
		return "", err
	}
	return channel.Name, nil
}
