// Package page carries positions in a listing.
//
// # Why a cursor exists next to limit and offset
//
// Offset pagination makes the database walk and DISCARD every row it skips, so
// the cost of a page grows with how deep it is. Measured on a 52,000-row
// product table with the listing index in place:
//
//	page          offset      keyset
//	first         0.31 ms     0.06 ms
//	~5,000 in     4.63 ms     —
//	~50,000 in   34.71 ms     0.08 ms
//
// Offset is linear in depth; a keyset seek is flat, because the ordering key
// goes into the index condition instead of into a counter. The 423x at the deep
// end is not the interesting part — the SHAPE is. A catalog that grows makes
// offset worse and leaves keyset where it was.
//
// Offset is NOT removed. A page-numbered admin screen needs to jump to page
// seven, which a cursor cannot do, and at the depths such a screen actually
// reaches offset is cheap. The two answer different questions and both are
// published.
//
// # The SQL this is meant to be used with
//
// The keyset predicate has to be written so the ordering key stays a ROW
// COMPARISON against the index. The obvious nullable form does not:
//
//	AND (@after_id::text IS NULL OR (created_at, id) < (@after_at, @after_id))
//
// It measures beautifully and then degrades in production. Postgres plans the
// statement per call for the first five executions and folds the OR away, so a
// test sees an Index Cond; on the sixth it switches to a GENERIC plan, the OR
// survives into a Filter, and the seek becomes a full index walk — 50,001 rows
// removed by filter, 4.3 ms instead of 0.065 ms. Nothing in the code changes at
// that moment, which is what makes it worth writing down.
//
// The form that holds under a generic plan replaces the branch with a sentinel:
//
//	AND (created_at, id) < (COALESCE(@after_at, 'infinity'), COALESCE(@after_id, ''))
//
// There is no OR left to survive, the comparison stays a ROW, and an absent
// cursor means "start at the top" because every real row sorts below infinity.
// For a listing ordered by id ascending the mirror image is
// `id > COALESCE(@after_id, ”)`.
package page

import (
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// CodeInvalidCursor is the error code of a cursor that cannot be read.
const CodeInvalidCursor = "invalid_cursor"

// separator divides the fields inside an encoded cursor.
//
// A newline is used because no listing name and no identifier in this
// repository contains one, which makes splitting exact rather than a guess.
const separator = "\n"

// fieldCount is how many fields an encoded cursor carries.
const fieldCount = 3

// Cursor is a position in a listing: the ordering key of the last row of a page.
//
// # Why it carries a time AND an id
//
// Most listings here order by created_at descending with the id as tiebreak,
// and BOTH are needed: two rows written in the same microsecond would otherwise
// make the position ambiguous, and a page boundary landing between them would
// either repeat a row or skip one. Listings ordered by id alone leave Time at
// its zero value.
type Cursor struct {
	// Time is the time half of the ordering key; the zero value means the
	// listing orders by id alone.
	Time time.Time
	// ID is the identifier half, which is what makes the position unambiguous.
	ID string
}

// IsZero reports that the cursor names no position, that is, the first page.
func (c Cursor) IsZero() bool { return c.ID == "" && c.Time.IsZero() }

// Encode turns a position into the opaque string a client sends back.
//
// # Why the listing name is inside it
//
// A cursor is a position in ONE listing's key space. Handed to a different
// listing it would still decode, still be a valid time and id, and quietly
// select the wrong rows — a fault that reads as missing data rather than as an
// error. The name is checked on the way back in, so the mistake is refused
// instead of served.
//
// # Why it is not signed
//
// A forged cursor selects a different page of a listing the caller is already
// allowed to read; it grants nothing that a limit and an offset would not. A
// signature would buy no access control and would add a key to manage and
// rotate. The encoding is opacity, not security, and is documented as such.
func Encode(listing string, c Cursor) string {
	raw := listing + separator + strconv.FormatInt(c.Time.UnixNano(), 10) + separator + c.ID

	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// Decode reads a cursor the client sent back for the named listing.
//
// An empty string is the first page and is NOT an error: a client walking to
// the end and asking for one more page is doing the ordinary thing.
func Decode(listing, raw string) (Cursor, error) {
	if raw == "" {
		return Cursor{}, nil
	}

	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return Cursor{}, errors.Invalid(CodeInvalidCursor,
			"the cursor is not readable; send it back exactly as it was given")
	}

	fields := strings.Split(string(decoded), separator)
	if len(fields) != fieldCount {
		return Cursor{}, errors.Invalid(CodeInvalidCursor,
			"the cursor is not readable; send it back exactly as it was given")
	}
	if fields[0] != listing {
		return Cursor{}, errors.Invalid(CodeInvalidCursor,
			"the cursor belongs to a different listing (%q), so it names no position here (%q)",
			fields[0], listing)
	}

	nanos, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return Cursor{}, errors.Invalid(CodeInvalidCursor,
			"the cursor is not readable; send it back exactly as it was given")
	}

	// The zero time is written as its own nanosecond count and has to come back
	// as the zero VALUE, not as an instant in 1754: a listing ordered by id
	// alone compares the time against a sentinel, and a non-zero time there
	// would exclude every row.
	at := time.Time{}
	if nanos != at.UnixNano() {
		at = time.Unix(0, nanos).UTC()
	}

	return Cursor{Time: at, ID: fields[2]}, nil
}

// SQLBounds returns the cursor as the two parameters the keyset predicate
// takes: a time and an id, each nil when the cursor names no position.
//
// They are returned as `any` because a typed nil pointer is not what the driver
// needs to send SQL NULL; the COALESCE in the query turns the NULLs back into
// the sentinels that mean "start at the top".
func (c Cursor) SQLBounds() (at, id any) {
	if !c.Time.IsZero() {
		at = c.Time
	}
	if c.ID != "" {
		id = c.ID
	}

	return at, id
}
