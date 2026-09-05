package models

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// Identifier prefixes. A prefix makes which entity an identifier belongs to
// readable without looking at a table.
const (
	// InvoiceIDPrefix is the prefix of invoice identifiers.
	InvoiceIDPrefix = "inv_"
	// LineIDPrefix is the prefix of invoice line identifiers.
	LineIDPrefix = "invline_"
	// SeriesIDPrefix is the prefix of invoice series identifiers.
	SeriesIDPrefix = "invser_"
)

// idEncoding is the unpadded encoding over the Crockford Base32 alphabet. A
// 16-byte body comes down to exactly 26 characters under this encoding. Because
// the alphabet is in ascending order in ASCII, the encoded string keeps the same
// lexicographic order as the bytes it encodes; that is what keeps identifiers
// sortable by time.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// IDBodyLen is the number of characters of the body that follows the prefix.
const IDBodyLen = 26

// NewInvoiceID produces a new invoice identifier.
func NewInvoiceID() string { return newID(InvoiceIDPrefix, time.Now()) }

// NewLineID produces a new invoice line identifier.
func NewLineID() string { return newID(LineIDPrefix, time.Now()) }

// NewSeriesID produces a new series identifier.
func NewSeriesID() string { return newID(SeriesIDPrefix, time.Now()) }

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
