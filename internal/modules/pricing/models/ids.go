package models

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// ID prefixes (plan Section 8: prefixed, sortable identifiers).
// The prefix tells at a single glance which record an id belongs to when you
// look at it, and it makes a call made with the wrong id visible in the log.
const (
	// PriceSetIDPrefix is the prefix of price set ids.
	PriceSetIDPrefix = "pset_"
	// PriceIDPrefix is the prefix of price ids.
	PriceIDPrefix = "price_"
	// PriceListIDPrefix is the prefix of price list ids.
	PriceListIDPrefix = "plist_"
	// PriceRuleIDPrefix is the prefix of price rule ids.
	PriceRuleIDPrefix = "prule_"
)

// idBodyLen is the number of characters in the body excluding the prefix: 16
// bytes encoded as Crockford Base32 without padding come to exactly 26
// characters.
const idBodyLen = 26

// idEncoding is padding-free encoding over the Crockford Base32 alphabet. Since
// the alphabet is in ascending ASCII order, the encoded string keeps the same
// lexicographic order as the bytes it encodes; ids stay sortable by time
// because of this, and "ORDER BY id" naturally yields creation order.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// NewID produces a time-ordered, unique id with the given prefix.
//
// Its structure is the same as ULID: a 48-bit millisecond timestamp + 80 bits
// of cryptographic randomness, encoded into 26 characters with Crockford
// Base32. The timestamp coming first means the id itself carries roughly the
// creation order.
//
// The generator in internal/core/workflow/pgstore has the same structure; for
// the sake of module isolation that package is NOT imported (Principle 2.4),
// the generator is repeated here.
func NewID(prefix string, t time.Time) string {
	ms := t.UTC().UnixMilli()
	if ms < 0 {
		// A timestamp before 1970 is not meaningful for a record; it is
		// clamped to the floor so ordering is not broken.
		ms = 0
	}

	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(ms))

	var buf [16]byte
	// UnixMilli fits in 48 bits; the first two bytes are always zero and are
	// dropped.
	copy(buf[:6], stamp[2:])
	if _, err := rand.Read(buf[6:]); err != nil {
		// crypto/rand.Read does not return an error; should it one day do so,
		// the id rests on nanosecond resolution alone — uniqueness weakens but
		// opening a record does not fail.
		binary.BigEndian.PutUint64(buf[8:], uint64(t.UnixNano()))
	}

	return prefix + idEncoding.EncodeToString(buf[:])
}

// NewPriceSetID produces a new price set id.
func NewPriceSetID(t time.Time) string { return NewID(PriceSetIDPrefix, t) }

// NewPriceID produces a new price id.
func NewPriceID(t time.Time) string { return NewID(PriceIDPrefix, t) }

// NewPriceListID produces a new price list id.
func NewPriceListID(t time.Time) string { return NewID(PriceListIDPrefix, t) }

// NewPriceRuleID produces a new price rule id.
func NewPriceRuleID(t time.Time) string { return NewID(PriceRuleIDPrefix, t) }

// IDBodyLength returns the length of the body excluding the prefix; it is the
// single source of truth for tests and for validation.
func IDBodyLength() int { return idBodyLen }
