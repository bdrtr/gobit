package models

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// Identifier prefixes (plan Section 8: prefixed, sortable identifiers).
// The prefix tells at a glance which record an identifier belongs to and makes
// a call issued with the wrong identifier visible in the log.
const (
	// UserIDPrefix is the prefix of admin user identifiers.
	UserIDPrefix = "user_"
	// AuthIdentityIDPrefix is the prefix of authentication record identifiers.
	AuthIdentityIDPrefix = "authid_"
	// APIKeyIDPrefix is the prefix of API key identifiers.
	APIKeyIDPrefix = "apikey_"
	// SalesChannelIDPrefix is the prefix of sales channel identifiers.
	SalesChannelIDPrefix = "sc_"
)

// idBodyLen is the character count of the body excluding the prefix: 16 bytes
// encoded as Crockford Base32 without padding come out to exactly 26
// characters.
const idBodyLen = 26

// idEncoding is unpadded encoding over the Crockford Base32 alphabet. Because
// the alphabet is in ascending order in ASCII, the encoded string preserves the
// same lexicographic order as the bytes it encodes; identifiers stay sortable
// by time thanks to this, and "ORDER BY id" naturally yields creation order.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// NewID produces a time-ordered and unique identifier with the given prefix.
//
// Its structure is the same as ULID's: a 48-bit millisecond timestamp + 80 bits
// of cryptographic randomness, encoded into 26 characters with Crockford
// Base32. The timestamp coming first means the identifier itself carries
// roughly the creation order.
//
// CAUTION: this generator is for IDENTIFIERS, not for SECRETS. The plaintext of
// an API key carries no timestamp and is random throughout (see [NewToken]).
//
// The same generator is also found in the customer, pricing and inventory
// modules; module isolation requires that those packages are NOT imported
// (Principle 2.4), so the generator is repeated here.
func NewID(prefix string, t time.Time) string {
	ms := t.UTC().UnixMilli()
	if ms < 0 {
		// A timestamp before 1970 is not meaningful for a record; it is
		// clamped to the floor so that the ordering is not broken.
		ms = 0
	}

	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(ms))

	var buf [16]byte
	// UnixMilli fits in 48 bits; the first two bytes are always zero and are
	// dropped.
	copy(buf[:6], stamp[2:])
	if _, err := rand.Read(buf[6:]); err != nil {
		// crypto/rand.Read does not return an error; should it ever do so one
		// day, the identifier rests on nanosecond resolution alone —
		// uniqueness weakens but opening a record does not fail.
		binary.BigEndian.PutUint64(buf[8:], uint64(t.UnixNano()))
	}

	return prefix + idEncoding.EncodeToString(buf[:])
}

// NewUserID produces a new admin user identifier.
func NewUserID(t time.Time) string { return NewID(UserIDPrefix, t) }

// NewAuthIdentityID produces a new authentication record identifier.
func NewAuthIdentityID(t time.Time) string { return NewID(AuthIdentityIDPrefix, t) }

// NewAPIKeyID produces a new API key identifier.
func NewAPIKeyID(t time.Time) string { return NewID(APIKeyIDPrefix, t) }

// NewSalesChannelID produces a new sales channel identifier.
func NewSalesChannelID(t time.Time) string { return NewID(SalesChannelIDPrefix, t) }

// IDBodyLength returns the length of the body excluding the prefix; it is the
// single source of truth for tests and validation.
func IDBodyLength() int { return idBodyLen }
