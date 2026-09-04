package models

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// Identifier prefixes (plan Section 8). The prefix makes it readable which
// entity an identifier belongs to without looking at a table: for an "li_..."
// seen in a log it is obvious which table to look at.
const (
	// CartIDPrefix is the prefix of cart identifiers.
	CartIDPrefix = "cart_"
	// LineItemIDPrefix is the prefix of cart line item identifiers.
	LineItemIDPrefix = "li_"
	// AddressIDPrefix is the prefix of cart address identifiers.
	AddressIDPrefix = "addr_"
	// ShippingMethodIDPrefix is the prefix of shipping method identifiers.
	//
	// Plan Section 8 does not count a prefix for this entity; "csm_" (cart
	// shipping method) was chosen here. "sm_" was not preferred, because in
	// Phase 7 the fulfillment module will produce its own
	// ShippingOption/ShippingProfile records and the two modules' prefixes
	// would get mixed up with each other in the log.
	ShippingMethodIDPrefix = "csm_"
)

// idEncoding is the unpadded encoding over the Crockford Base32 alphabet. A
// 16-byte body comes down to exactly 26 characters with this encoding. Because
// the alphabet is in ascending order in ASCII, the encoded string keeps the same
// lexicographic order as the bytes it encodes; identifiers stay sortable by time
// thanks to this.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// NewCartID produces a new cart identifier.
func NewCartID() string { return newID(CartIDPrefix, time.Now()) }

// NewLineItemID produces a new cart line item identifier.
func NewLineItemID() string { return newID(LineItemIDPrefix, time.Now()) }

// NewAddressID produces a new cart address identifier.
func NewAddressID() string { return newID(AddressIDPrefix, time.Now()) }

// NewShippingMethodID produces a new shipping method identifier.
func NewShippingMethodID() string { return newID(ShippingMethodIDPrefix, time.Now()) }

// newID produces a prefixed, time-ordered and unique identifier.
//
// Its structure is the same as ULID's: a 48-bit millisecond timestamp + 80 bits
// of cryptographic randomness, encoded into 26 characters with Crockford
// Base32. The timestamp being at the front means the identifier itself carries
// roughly the creation order; the records also sit in their natural order in a
// primary key scan and B-tree insertions happen at the end.
//
// It has the same structure as the generator in the other modules; those
// packages ARE NOT IMPORTED (ADR 0001), the structure is repeated here as the
// module's own code.
func newID(prefix string, t time.Time) string {
	ms := t.UTC().UnixMilli()
	if ms < 0 {
		// A timestamp before 1970 is not meaningful for a record; it is pulled
		// down to the floor so that the ordering is not broken.
		ms = 0
	}

	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(ms))

	var buf [16]byte
	// UnixMilli fits in 48 bits; the first two bytes are always zero and are
	// dropped.
	copy(buf[:6], stamp[2:])
	if _, err := rand.Read(buf[6:]); err != nil {
		// crypto/rand.Read does not return an error; should it return one some
		// day anyway, the identifier rests on nanosecond resolution alone —
		// uniqueness weakens but opening a record does not fail.
		binary.BigEndian.PutUint64(buf[8:], uint64(t.UnixNano()))
	}

	return prefix + idEncoding.EncodeToString(buf[:])
}
