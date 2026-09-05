package models

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// Identifier prefixes (plan Section 8). A prefix makes which entity an
// identifier belongs to readable without looking at a table: for the "oli_..."
// seen in a log it is clear which table to look at.
const (
	// OrderIDPrefix is the prefix of order identifiers.
	OrderIDPrefix = "order_"
	// LineItemIDPrefix is the prefix of order line identifiers.
	LineItemIDPrefix = "oli_"
	// SummaryIDPrefix is the prefix of order summary identifiers.
	//
	// Plan Section 8 does not count a prefix for this entity; "osum_" (order
	// summary) was chosen here. "sum_" was not preferred, because on its own
	// the prefix would not say which module's record it is.
	SummaryIDPrefix = "osum_"
	// OrderAddressIDPrefix prefixes an order address identifier.
	OrderAddressIDPrefix = "oaddr_"
	// ReturnIDPrefix is the prefix of return identifiers.
	ReturnIDPrefix = "ret_"
	// ReturnItemIDPrefix is the prefix of a return line identifier.
	ReturnItemIDPrefix = "retitem_"
	// ExchangeIDPrefix is the prefix of exchange identifiers.
	ExchangeIDPrefix = "exch_"
	// ClaimIDPrefix is the prefix of claim record identifiers.
	ClaimIDPrefix = "claim_"
)

// idEncoding is the unpadded encoding over the Crockford Base32 alphabet. A
// 16-byte body comes down to exactly 26 characters under this encoding. Because
// the alphabet is in ascending order in ASCII, the encoded string keeps the same
// lexicographic order as the bytes it encodes; that is what keeps identifiers
// sortable by time.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// IDBodyLen is the number of characters of the body that follows the prefix.
//
// It is exported because the FORMAT of an identifier is a contract: an order
// identifier travels in the log, in the support record and in the saga's
// snapshot. The tests verify the format against this constant.
const IDBodyLen = 26

// NewOrderID produces a new order identifier.
func NewOrderID() string { return newID(OrderIDPrefix, time.Now()) }

// NewLineItemID produces a new order line identifier.
func NewLineItemID() string { return newID(LineItemIDPrefix, time.Now()) }

// NewSummaryID produces a new order summary identifier.
func NewSummaryID() string { return newID(SummaryIDPrefix, time.Now()) }

// NewOrderAddressID produces a new order address identifier.
func NewOrderAddressID() string { return newID(OrderAddressIDPrefix, time.Now()) }

// NewReturnItemID produces a new return line identifier.
func NewReturnItemID() string { return newID(ReturnItemIDPrefix, time.Now()) }

// NewReturnID produces a new return identifier.
func NewReturnID() string { return newID(ReturnIDPrefix, time.Now()) }

// NewExchangeID produces a new exchange identifier.
func NewExchangeID() string { return newID(ExchangeIDPrefix, time.Now()) }

// NewClaimID produces a new claim record identifier.
func NewClaimID() string { return newID(ClaimIDPrefix, time.Now()) }

// newID produces a prefixed, time-ordered and unique identifier.
//
// Its structure is the same as ULID's: a 48-bit millisecond timestamp + 80 bits
// of cryptographic randomness, encoded into 26 characters with Crockford
// Base32. The timestamp being at the front means that the identifier itself
// carries roughly the creation order; the records also stand in natural order
// under a primary key scan and B-tree insertions happen at the end.
//
// It has the same structure as the generator in the other modules; those
// packages are NOT IMPORTED (ADR 0001), the structure is repeated here as the
// module's own code.
func newID(prefix string, t time.Time) string {
	ms := t.UTC().UnixMilli()
	if ms < 0 {
		// A timestamp before 1970 is not meaningful for a record; it is
		// clamped to the floor so that the ordering is not broken.
		ms = 0
	}

	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(ms))

	var buf [16]byte
	// UnixMilli fits into 48 bits; the first two bytes are always zero and are
	// dropped.
	copy(buf[:6], stamp[2:])
	if _, err := rand.Read(buf[6:]); err != nil {
		// crypto/rand.Read does not return an error; should it ever do so, the
		// identifier rests on nanosecond resolution alone — uniqueness weakens
		// but opening the record does not fail.
		binary.BigEndian.PutUint64(buf[8:], uint64(t.UnixNano()))
	}

	return prefix + idEncoding.EncodeToString(buf[:])
}
