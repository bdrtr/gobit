package service

import (
	"crypto/sha256"
	"encoding/hex"
)

// cartEventKeyBytes is how much of the digest a cart event's id carries: 128
// bits, which no two carts share by chance.
const cartEventKeyBytes = 16

// cartEventKey names a cart in its events' ids without carrying its id
// (ADR 0439).
//
// The id is the cart's access at the storefront, and an event id leaves the
// installation in every webhook envelope. A digest of it is the same for the
// cart's two events, so a receiver pairs a cart's opening with its completion,
// and it cannot be turned back into the id: the id carries 80 random bits.
func cartEventKey(cartID string) string {
	sum := sha256.Sum256([]byte(cartID))

	return hex.EncodeToString(sum[:cartEventKeyBytes])
}
