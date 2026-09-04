// Package models holds the domain models of the file module.
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

// UploadIDPrefix is the identifier prefix of upload records
// (plan Section 8: prefixed, time-ordered identifiers).
//
// The prefix tells at a single glance which record an identifier belongs to,
// and turns a call made with an identifier of the wrong kind into an explicit
// validation error instead of a "not found".
const UploadIDPrefix = "upl_"

// idBodyLen is the character count of the body outside the prefix: 16 bytes
// encoded unpadded with Crockford Base32 come down to exactly 26 characters.
const idBodyLen = 26

// idEncoding is the unpadded encoding over the Crockford Base32 alphabet.
// Because the alphabet is in ascending order in ASCII, the encoded string keeps
// the same lexicographic order as the bytes it encodes; identifiers stay
// sortable by time thanks to this, and "ORDER BY created_at DESC, id DESC"
// works stably on equal stamps too.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// NewID produces a time-ordered and unique identifier with the given prefix.
//
// Its structure is the same as ULID's: a 48-bit millisecond timestamp + 80 bits
// of cryptographic randomness, encoded into 26 characters with Crockford
// Base32.
//
// The generator in the other modules has the same structure; because of module
// isolation those packages ARE NOT IMPORTED (Principle 2.4, ADR 0001), the
// generator is repeated here.
//
// The prefix may also be given EMPTY and that is deliberate: the provider that
// produces the storage key (see internal/modules/file/local) wants a body
// without a prefix — a key is not a record identifier and must not be confused
// with one.
func NewID(prefix string, t time.Time) string {
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

// NewUploadID produces a new upload record identifier.
func NewUploadID(t time.Time) string { return NewID(UploadIDPrefix, t) }

// IDBodyLength returns the length of the body outside the prefix; it is the
// single source of truth for the tests and for validation.
func IDBodyLength() int { return idBodyLen }
