package models

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// ID prefixes (plan Section 8). The prefix makes an id readable even though the
// id does not carry which entity it belongs to: an "invres_..." seen in a log
// needs no look at the table.
const (
	// StockLocationIDPrefix is the prefix of stock location ids.
	StockLocationIDPrefix = "sloc_"
	// InventoryItemIDPrefix is the prefix of inventory item ids.
	InventoryItemIDPrefix = "invitem_"
	// InventoryLevelIDPrefix is the prefix of inventory level ids.
	InventoryLevelIDPrefix = "invlevel_"
	// ReservationIDPrefix is the prefix of reservation ids.
	ReservationIDPrefix = "invres_"
)

// idEncoding is the padless encoding over the Crockford Base32 alphabet. A
// 16-byte body comes down to exactly 26 characters under this encoding. The
// alphabet is in ascending order in ASCII, so the encoded string keeps the same
// lexicographic order as the bytes it encodes; ids stay sortable by time
// because of that.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// NewStockLocationID produces a new stock location id.
func NewStockLocationID() string { return newID(StockLocationIDPrefix, time.Now()) }

// NewInventoryItemID produces a new inventory item id.
func NewInventoryItemID() string { return newID(InventoryItemIDPrefix, time.Now()) }

// NewInventoryLevelID produces a new inventory level id.
func NewInventoryLevelID() string { return newID(InventoryLevelIDPrefix, time.Now()) }

// NewReservationID produces a new reservation id.
func NewReservationID() string { return newID(ReservationIDPrefix, time.Now()) }

// newID produces a prefixed, time-ordered and unique id.
//
// Its structure is the same as ULID's: a 48-bit millisecond timestamp + 80 bits
// of cryptographic randomness, encoded into 26 characters with Crockford
// Base32. The timestamp coming first means the id itself carries roughly the
// creation order; the records then also sit in their natural order under a
// primary key scan and B-tree inserts happen at the end.
//
// It has the same structure as the generator in
// internal/core/workflow/pgstore/ids.go; that package is NOT IMPORTED (it is
// not the core's published surface), the structure is repeated here as the
// module's own code.
func newID(prefix string, t time.Time) string {
	ms := t.UTC().UnixMilli()
	if ms < 0 {
		// A timestamp from before 1970 is not meaningful for a record; it is
		// pulled down to the floor so the ordering does not break.
		ms = 0
	}

	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(ms))

	var buf [16]byte
	// UnixMilli fits in 48 bits; the first two bytes are always zero and are dropped.
	copy(buf[:6], stamp[2:])
	if _, err := rand.Read(buf[6:]); err != nil {
		// crypto/rand.Read does not return an error; should it return one some
		// day anyway, the id rests on nanosecond resolution alone — uniqueness
		// weakens but opening the record does not fail.
		binary.BigEndian.PutUint64(buf[8:], uint64(t.UnixNano()))
	}

	return prefix + idEncoding.EncodeToString(buf[:])
}
