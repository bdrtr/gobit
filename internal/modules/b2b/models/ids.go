package models

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// Identifier prefixes (plan Section 8: prefixed, sortable identifiers).
// The prefix says at a single glance which record an identifier belongs to and
// makes a call made with the wrong identifier visible in the log.
const (
	// CompanyIDPrefix is the prefix of company identifiers.
	CompanyIDPrefix = "comp_"
	// EmployeeIDPrefix is the prefix of company employee identifiers.
	EmployeeIDPrefix = "compemp_"
	// CustomerIDPrefix is the prefix of the CUSTOMER identifier the employee
	// is bound to.
	//
	// The prefix is the customer module's data and it is REPEATED here:
	// modules cannot import each other (Principle 2.4). The price of the
	// repetition would be that, if customer changed its prefix one day, the
	// validation would silently look for the wrong thing; what is gained in
	// return is that an identifier of the wrong type can never be written
	// into the link table.
	CustomerIDPrefix = "cust_"
)

// idBodyLen is the number of characters in the body outside the prefix: 16
// bytes encoded with Crockford Base32 without padding come to exactly 26
// characters.
const idBodyLen = 26

// idEncoding is the padding-free encoding with the Crockford Base32 alphabet.
// Because the alphabet is in ascending order in ASCII, the encoded string
// preserves the same lexicographic order as the bytes it encodes; that is how
// identifiers stay sortable by time and "ORDER BY id" naturally yields the
// creation order.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// NewID produces a time-ordered and unique identifier with the given prefix.
//
// Its structure is the same as ULID's: a 48-bit millisecond timestamp + 80 bits
// of cryptographic randomness, encoded into 26 characters with Crockford
// Base32. The timestamp coming first means the identifier itself carries
// roughly the creation order.
//
// The same generator also exists in the customer, pricing and inventory
// modules; module isolation means those packages are NOT imported (Principle
// 2.4), so the generator is repeated here.
func NewID(prefix string, t time.Time) string {
	ms := t.UTC().UnixMilli()
	if ms < 0 {
		// A timestamp from before 1970 is not meaningful for a record; it is
		// pulled down to the floor so the ordering is not broken.
		ms = 0
	}

	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(ms))

	var buf [16]byte
	// UnixMilli fits in 48 bits; the first two bytes are always zero and are
	// dropped.
	copy(buf[:6], stamp[2:])
	if _, err := rand.Read(buf[6:]); err != nil {
		// crypto/rand.Read does not return an error; should it ever do so,
		// the identifier rests on nanosecond resolution alone — uniqueness
		// weakens but opening the record does not fail.
		binary.BigEndian.PutUint64(buf[8:], uint64(t.UnixNano()))
	}

	return prefix + idEncoding.EncodeToString(buf[:])
}

// NewCompanyID produces a new company identifier.
func NewCompanyID(t time.Time) string { return NewID(CompanyIDPrefix, t) }

// NewEmployeeID produces a new employee identifier.
func NewEmployeeID(t time.Time) string { return NewID(EmployeeIDPrefix, t) }

// IDBodyLength returns the length of the body outside the prefix; it is the
// single source of truth for tests and validation.
func IDBodyLength() int { return idBodyLen }
