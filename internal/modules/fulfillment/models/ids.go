package models

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// Identifier prefixes (plan Section 8). A prefix makes it readable which entity
// an identifier belongs to without looking at the table: a "sopt_..." seen in a
// log needs no schema lookup.
const (
	// FulfillmentIDPrefix is the prefix of fulfillment identifiers.
	FulfillmentIDPrefix = "ful_"
	// ShippingOptionIDPrefix is the prefix of shipping option identifiers.
	ShippingOptionIDPrefix = "sopt_"
	// ShippingProfileIDPrefix is the prefix of shipping profile identifiers.
	ShippingProfileIDPrefix = "sprof_"
	// ShippingOptionRuleIDPrefix is the prefix of shipping option rule
	// identifiers.
	//
	// Plan Section 8 does not list a prefix for this entity. "prule_" CANNOT be
	// taken: the pricing module uses it for price rules, and giving the same
	// prefix to two different entities would leave it ambiguous which table an
	// identifier in a log belongs to. "sorule_" (shipping option rule) was
	// chosen.
	ShippingOptionRuleIDPrefix = "sorule_"
	// FulfillmentItemIDPrefix is the prefix of fulfillment item identifiers.
	//
	// Plan Section 8 does not list a prefix for this entity either; "fulitem_"
	// was chosen, and the full word is used so that it is not confused with the
	// fulfillment identifier's prefix ("ful_").
	FulfillmentItemIDPrefix = "fulitem_"
	// ManualShipmentIDPrefix is the prefix of the manual provider's own shipment
	// identifiers. This identifier belongs to the PROVIDER and sits in the
	// module's fulfillment record as external_id.
	ManualShipmentIDPrefix = "manful_"
)

// idEncoding is the unpadded encoding over the Crockford Base32 alphabet. A
// 16-byte body comes down to exactly 26 characters under this encoding. Because
// the alphabet is in ascending order in ASCII, the encoded string preserves the
// same lexicographic order as the bytes it encodes; that is what keeps
// identifiers sortable by time.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// NewFulfillmentID produces a new fulfillment identifier.
func NewFulfillmentID() string { return newID(FulfillmentIDPrefix, time.Now()) }

// NewShippingOptionID produces a new shipping option identifier.
func NewShippingOptionID() string { return newID(ShippingOptionIDPrefix, time.Now()) }

// NewShippingProfileID produces a new shipping profile identifier.
func NewShippingProfileID() string { return newID(ShippingProfileIDPrefix, time.Now()) }

// NewShippingOptionRuleID produces a new shipping option rule identifier.
func NewShippingOptionRuleID() string { return newID(ShippingOptionRuleIDPrefix, time.Now()) }

// NewFulfillmentItemID produces a new fulfillment item identifier.
func NewFulfillmentItemID() string { return newID(FulfillmentItemIDPrefix, time.Now()) }

// NewManualShipmentID produces a new shipment identifier for the manual
// provider.
func NewManualShipmentID() string { return newID(ManualShipmentIDPrefix, time.Now()) }

// newID produces a prefixed, time-ordered and unique identifier.
//
// Its structure is the same as a ULID's: a 48-bit millisecond timestamp + 80
// bits of cryptographic randomness, encoded to 26 characters with Crockford
// Base32. Having the timestamp up front means the identifier itself carries
// roughly the creation order; records also sit in their natural order on a
// primary key scan and B-tree insertions happen at the end.
//
// It has the same structure as the generator in the other modules; those
// packages are NOT IMPORTED (Principle 2.4), the structure is repeated here as
// the module's own code.
func newID(prefix string, t time.Time) string {
	ms := t.UTC().UnixMilli()
	if ms < 0 {
		// A timestamp before 1970 is not meaningful for a record; it is pulled
		// down to the floor so that ordering is not broken.
		ms = 0
	}

	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(ms))

	var buf [16]byte
	// UnixMilli fits in 48 bits; the first two bytes are always zero and are
	// dropped.
	copy(buf[:6], stamp[2:])
	if _, err := rand.Read(buf[6:]); err != nil {
		// crypto/rand.Read does not return an error; should it ever do so, the
		// identifier rests on nanosecond resolution alone — uniqueness weakens
		// but opening a record does not fail.
		binary.BigEndian.PutUint64(buf[8:], uint64(t.UnixNano()))
	}

	return prefix + idEncoding.EncodeToString(buf[:])
}
