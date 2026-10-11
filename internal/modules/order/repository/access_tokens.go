package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/order/repository/orderdb"
)

// This file keeps the digests of the tokens an order hands out (ADR 0439).
// What a token is and how one is compared are models/access_token.go's; the
// statements are queries/order_access_tokens.sql.

// RecordAccessToken keeps the digest of a token issued for the order; an order
// that does not exist answers NotFound, from the foreign key.
func (r *Repository) RecordAccessToken(ctx context.Context, orderID string, digest []byte) error {
	err := r.queries(ctx).InsertOrderAccessToken(ctx, orderdb.InsertOrderAccessTokenParams{
		OrderID:   orderID,
		TokenHash: digest,
	})
	if err != nil {
		return classify(err, codeQueryFailed, "could not record the order's access token")
	}

	return nil
}

// AccessTokenDigests returns the digests of every token the order issued; an
// order that issued none, or does not exist, has none.
func (r *Repository) AccessTokenDigests(ctx context.Context, orderID string) ([][]byte, error) {
	digests, err := r.queries(ctx).ListOrderAccessTokenHashes(ctx, orderID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not read the order's access tokens")
	}

	return digests, nil
}

// WithdrawAccessTokens deletes every token of the given orders and returns how
// many there were.
func (r *Repository) WithdrawAccessTokens(ctx context.Context, orderIDs []string) (int64, error) {
	if len(orderIDs) == 0 {
		return 0, nil
	}

	withdrawn, err := r.queries(ctx).DeleteOrderAccessTokens(ctx, orderIDs)
	if err != nil {
		return 0, classify(err, codeQueryFailed, "could not withdraw the orders' access tokens")
	}

	return withdrawn, nil
}
