package pgstore

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// idPrefix is the prefix of the execution ids this package generates (plan
// Section 8: prefixed ids). "wfx" = workflow execution.
const idPrefix = "wfx_"

// idBodyLen is the character count of the body after the prefix: 16 bytes
// encoded as Crockford Base32 without padding come to exactly 26 characters.
const idBodyLen = 26

// idEncoding is Crockford Base32 without padding. The alphabet is in ascending
// ASCII order, so the encoded string keeps the same lexicographic order as the
// bytes it encodes; that is what keeps the ids sortable by time.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// newExecutionID generates a time-ordered, unique execution id.
//
// Its shape is a ULID's: a 48-bit millisecond timestamp plus 80 bits of
// cryptographic randomness, encoded to 26 Crockford Base32 characters and given
// the "wfx_" prefix. Putting the timestamp first means the id itself carries
// roughly the order of creation, so executions stay in a natural order even in
// an index scan.
//
// The id may also be supplied by the CALLER: Create uses a non-empty ID as it
// stands and only calls this when the field was left empty.
func newExecutionID(t time.Time) string {
	ms := t.UTC().UnixMilli()
	if ms < 0 {
		// A pre-1970 timestamp is meaningless for an execution; it is clamped
		// to the floor so it cannot break the ordering.
		ms = 0
	}

	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(ms))

	var buf [16]byte
	// UnixMilli fits in 48 bits; the first two bytes are always zero and are
	// dropped.
	copy(buf[:6], stamp[2:])
	if _, err := rand.Read(buf[6:]); err != nil {
		// crypto/rand.Read does not return an error; should it ever start to,
		// the id falls back to nanosecond resolution alone — uniqueness gets
		// weaker, but opening a record does not fail.
		binary.BigEndian.PutUint64(buf[8:], uint64(t.UnixNano()))
	}

	return idPrefix + idEncoding.EncodeToString(buf[:])
}
