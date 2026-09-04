package service

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// Id prefixes (plan Section 8: prefixed ids).
//
// The prefix carries the kind of the id within the id itself: when you see
// "variant_01J..." in a log line or in a link table you know which table to
// look at. It also makes an id of the wrong kind slipping into the wrong end
// (a product id handed to a place that expects a variant) visible to the eye.
const (
	prefixProduct     = "prod_"
	prefixVariant     = "variant_"
	prefixOption      = "popt_"
	prefixOptionValue = "poptval_"
	prefixCategory    = "pcat_"
	prefixCollection  = "pcol_"
	prefixTag         = "ptag_"
	prefixImage       = "pimg_"
)

// idEncoding is the unpadded encoding over the Crockford Base32 alphabet.
//
// A 16-byte body comes to exactly 26 characters in this alphabet.
//
// Because the alphabet is in ascending order in ASCII, the encoded string keeps
// the SAME lexicographic order as the bytes it encodes; since the timestamp
// comes first, the ids stay sortable by creation order.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// newID produces a time-ordered, unique id with the given prefix.
//
// Its shape is the same as a ULID: a 48-bit millisecond timestamp + 80 bits of
// cryptographic randomness, encoded into 26 characters with Crockford Base32.
// The timestamp coming first means the id itself carries roughly the creation
// order; the rows also sit in their natural order in the primary key index and
// none of the scattered write load that random UUIDs create in a B-tree
// appears.
//
// Id GENERATION lives in the module itself (the generator in the core is NOT
// IMPORTED): module isolation requires resisting the urge to set up a shared
// helper package.
func newID(prefix string) string {
	return prefix + idEncoding.EncodeToString(idBytes(time.Now()))
}

// idBytes produces the 16-byte body of the id.
func idBytes(t time.Time) []byte {
	ms := t.UTC().UnixMilli()
	if ms < 0 {
		// A timestamp before 1970 is meaningless for a catalog; it is clamped
		// to the floor so that it does not break the ordering.
		ms = 0
	}

	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(ms))

	buf := make([]byte, 16)
	// UnixMilli fits into 48 bits; the first two bytes are always zero and are
	// dropped.
	copy(buf[:6], stamp[2:])
	if _, err := rand.Read(buf[6:]); err != nil {
		// crypto/rand.Read does not return an error; should it ever do so, the
		// id falls back to nanosecond resolution — uniqueness weakens but
		// opening a record does not fail.
		binary.BigEndian.PutUint64(buf[8:], uint64(t.UnixNano()))
	}
	return buf
}
