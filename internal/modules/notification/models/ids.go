// Package models holds the domain models of the notification module.
//
// The types carry only the DATA and its own internal consistency; no database
// or HTTP detail is known here.
package models

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// DeliveryIDPrefix is the identifier prefix of delivery log records
// (plan Section 8: prefixed, time-ordered identifiers).
//
// The prefix tells at a single glance which record an identifier belongs to,
// and it turns a call made with an identifier of the wrong kind into an
// explicit validation error instead of a "not found".
const DeliveryIDPrefix = "notif_"

// idBodyLen is the character count of the body beyond the prefix: 16 bytes
// encoded as unpadded Crockford Base32 come out to exactly 26 characters.
const idBodyLen = 26

// idEncoding is the unpadded encoding over the Crockford Base32 alphabet.
// Because the alphabet is in ascending order in ASCII, the encoded string
// keeps the same lexicographic order as the bytes it encodes; identifiers stay
// sortable by time that way, and "ORDER BY created_at DESC, id DESC" behaves
// stably on equal stamps too.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// NewID produces a time-ordered and unique identifier with the given prefix.
//
// Its structure is the same as a ULID: a 48-bit millisecond timestamp + 80
// bits of cryptographic randomness, encoded into 26 characters with Crockford
// Base32.
//
// The generator in the other modules has the same structure; module isolation
// means those packages are NOT imported (Principle 2.4, ADR 0001), so the
// generator is repeated here.
func NewID(prefix string, t time.Time) string {
	ms := t.UTC().UnixMilli()
	if ms < 0 {
		// A timestamp before 1970 is not meaningful for a record; it is
		// pulled down to the floor so the ordering is not broken.
		ms = 0
	}

	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(ms))

	var buf [16]byte
	// UnixMilli fits into 48 bits; the first two bytes are always zero and
	// are dropped.
	copy(buf[:6], stamp[2:])
	if _, err := rand.Read(buf[6:]); err != nil {
		// crypto/rand.Read does not return an error; should it ever do so,
		// the identifier rests on nanosecond resolution alone — uniqueness
		// weakens, but opening the record does not fail.
		binary.BigEndian.PutUint64(buf[8:], uint64(t.UnixNano()))
	}

	return prefix + idEncoding.EncodeToString(buf[:])
}

// NewDeliveryID produces a new delivery log identifier.
func NewDeliveryID(t time.Time) string { return NewID(DeliveryIDPrefix, t) }

// IDBodyLength returns the length of the body beyond the prefix; it is the
// single source of truth for tests and for validation.
func IDBodyLength() int { return idBodyLen }
