package models

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// Id prefixes (plan Section 8: prefixed, time-ordered ids).
//
// The prefix tells at a glance which record an id belongs to, and turns a call
// made with an id of the wrong kind into an explicit validation error instead
// of a "not found".
//
// TaxRateRuleIDPrefix is NOT in the plan's prefix list: that list counts the
// three modules of Phase 7 together and "prule_" belongs to the promotion
// module's rule record. Because the tax rule is not named separately in the
// plan, "taxrule_" was chosen here; different enough not to be confused with
// the rate's prefix ("taxrate_"), yet readably from the same family.
const (
	// TaxRegionIDPrefix is the prefix of tax region ids.
	TaxRegionIDPrefix = "taxreg_"
	// TaxRateIDPrefix is the prefix of tax rate ids.
	TaxRateIDPrefix = "taxrate_"
	// TaxRateRuleIDPrefix is the prefix of tax rate rule ids.
	TaxRateRuleIDPrefix = "taxrule_"
)

// idBodyLen is the character count of the body outside the prefix: 16 bytes
// encoded in Crockford Base32 without padding come to exactly 26 characters.
const idBodyLen = 26

// idEncoding is padding-free encoding with the Crockford Base32 alphabet.
// Because the alphabet is in ascending order in ASCII, the encoded string keeps
// the same lexicographic order as the bytes it encodes; ids therefore stay
// sortable by time and "ORDER BY id" naturally yields creation order.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// NewID produces a time-ordered, unique id with the given prefix.
//
// Its structure is the same as a ULID's: a 48-bit millisecond timestamp + 80
// bits of cryptographic randomness, encoded into 26 characters with Crockford
// Base32. The timestamp coming first means the id itself carries roughly the
// creation order; the tie-breaking rule in the tax calculation ("the oldest
// rate wins") rests on exactly that order.
//
// The generator in the other modules is identical in structure; module
// isolation means those packages are NOT imported (Principle 2.4, ADR 0001),
// so the generator is repeated here.
func NewID(prefix string, t time.Time) string {
	ms := t.UTC().UnixMilli()
	if ms < 0 {
		// A timestamp before 1970 is not meaningful for a record; it is
		// clamped to the floor so that ordering is not broken.
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
		// day, the id rests on nanosecond resolution alone — uniqueness
		// weakens, but opening a record does not fail.
		binary.BigEndian.PutUint64(buf[8:], uint64(t.UnixNano()))
	}

	return prefix + idEncoding.EncodeToString(buf[:])
}

// NewTaxRegionID produces a new tax region id.
func NewTaxRegionID(t time.Time) string { return NewID(TaxRegionIDPrefix, t) }

// NewTaxRateID produces a new tax rate id.
func NewTaxRateID(t time.Time) string { return NewID(TaxRateIDPrefix, t) }

// NewTaxRateRuleID produces a new tax rate rule id.
func NewTaxRateRuleID(t time.Time) string { return NewID(TaxRateRuleIDPrefix, t) }

// IDBodyLength returns the length of the body outside the prefix; it is the
// single source of truth for tests and validation.
func IDBodyLength() int { return idBodyLen }
