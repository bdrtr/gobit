package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// This file is the order's half of its storefront access (ADR 0439): the
// tokens it issues, and the two facts a storefront route decides a caller's
// access on. The decision itself is the api package's, beside the identity
// the routes ask.

// codeAccessTokenFailed reports that no token could be made.
const codeAccessTokenFailed = "order_access_token_failed"

// StorefrontAccess is what a storefront order route decides a caller's access
// on.
type StorefrontAccess struct {
	// CustomerID is the order's customer; empty on a guest's order, which no
	// identity can prove.
	CustomerID string
	// TokenMatches says the token the request presented is one the order
	// issued.
	TokenMatches bool
}

// StorefrontAccess reads the order's customer and whether token is one of the
// order's access tokens; NotFound when there is no such order.
//
// An empty token is asked nothing: a request that presented none matches
// none, and reading the digests for it would only spend a query.
func (s *Service) StorefrontAccess(ctx context.Context, orderID, token string) (StorefrontAccess, error) {
	if err := requireID("order_id", orderID); err != nil {
		return StorefrontAccess{}, err
	}

	order, err := s.store.GetOrder(ctx, orderID)
	if err != nil {
		return StorefrontAccess{}, err
	}
	access := StorefrontAccess{CustomerID: order.CustomerID}
	if token == "" {
		return access, nil
	}

	digests, err := s.store.AccessTokenDigests(ctx, orderID)
	if err != nil {
		return StorefrontAccess{}, err
	}
	access.TokenMatches = models.AccessTokenMatches(token, digests)

	return access, nil
}

// IssueAccessToken makes a new access token for the order, keeps its digest
// and returns the plaintext, which is the only copy; NotFound when there is no
// such order.
//
// Every caller gets its own token, and an earlier one stays valid: the
// completion's answer holds one and every mail naming the order another, and
// a mail sent later must not close the page the shopper has open.
func (s *Service) IssueAccessToken(ctx context.Context, orderID string) (string, error) {
	if err := requireID("order_id", orderID); err != nil {
		return "", err
	}

	plaintext, digest, err := models.NewAccessToken()
	if err != nil {
		return "", errors.Wrap(err, errors.KindInternal, codeAccessTokenFailed,
			"no access token could be made for order %s", orderID)
	}
	if err := s.store.RecordAccessToken(ctx, orderID, digest); err != nil {
		return "", err
	}

	return plaintext, nil
}
