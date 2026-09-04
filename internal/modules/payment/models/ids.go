package models

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// Identifier prefixes (plan Section 8). The prefix makes it readable which
// entity an identifier belongs to without looking at the table: the
// "payses_..." seen in a log does not require opening the schema.
const (
	// PaymentCollectionIDPrefix is the prefix of payment collection identifiers.
	PaymentCollectionIDPrefix = "paycol_"
	// PaymentSessionIDPrefix is the prefix of payment session identifiers.
	PaymentSessionIDPrefix = "payses_"
	// PaymentIDPrefix is the prefix of capture identifiers.
	PaymentIDPrefix = "pay_"
	// RefundIDPrefix is the prefix of refund identifiers.
	RefundIDPrefix = "refund_"
	// ManualSessionIDPrefix is the prefix of the manual provider's own session
	// identifiers. This identifier belongs to the PROVIDER and sits on the
	// module's session record as external_id.
	ManualSessionIDPrefix = "manses_"
)

// idEncoding is padding-free encoding over the Crockford Base32 alphabet. A
// 16-byte body comes down to exactly 26 characters under this encoding. Since
// the alphabet is in ascending order in ASCII, the encoded string keeps the
// same lexicographic order as the bytes it encodes; identifiers stay sortable
// by time thanks to that.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// NewPaymentCollectionID produces a new payment collection identifier.
func NewPaymentCollectionID() string { return newID(PaymentCollectionIDPrefix, time.Now()) }

// NewPaymentSessionID produces a new payment session identifier.
func NewPaymentSessionID() string { return newID(PaymentSessionIDPrefix, time.Now()) }

// NewPaymentID produces a new capture identifier.
func NewPaymentID() string { return newID(PaymentIDPrefix, time.Now()) }

// NewRefundID produces a new refund identifier.
func NewRefundID() string { return newID(RefundIDPrefix, time.Now()) }

// NewManualSessionID produces a new session identifier for the manual provider.
func NewManualSessionID() string { return newID(ManualSessionIDPrefix, time.Now()) }

// newID produces a prefixed, time-ordered and unique identifier.
//
// Its structure is the same as ULID's: a 48-bit millisecond timestamp + 80
// bits of cryptographic randomness, encoded into 26 characters with Crockford
// Base32. The timestamp coming first means the identifier itself carries
// roughly the creation order; the records also sit in their natural order
// under a primary key scan and B-tree insertions happen at the end.
//
// It has the same structure as the generator in the other modules; those
// packages are NOT IMPORTED (Principle 2.4), the structure is repeated here as
// the module's own code.
func newID(prefix string, t time.Time) string {
	ms := t.UTC().UnixMilli()
	if ms < 0 {
		// A timestamp from before 1970 is not meaningful for a record; it is
		// pulled down to the floor so that the ordering is not broken.
		ms = 0
	}

	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(ms))

	var buf [16]byte
	// UnixMilli fits into 48 bits; the first two bytes are always zero and are
	// thrown away.
	copy(buf[:6], stamp[2:])
	if _, err := rand.Read(buf[6:]); err != nil {
		// crypto/rand.Read does not return an error; should it ever do so one
		// day, the identifier rests on nanosecond resolution alone —
		// uniqueness weakens, but opening the record does not fail.
		binary.BigEndian.PutUint64(buf[8:], uint64(t.UnixNano()))
	}

	return prefix + idEncoding.EncodeToString(buf[:])
}
