package models

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// Identifier prefixes (plan Section 8: prefixed, sortable identifiers).
// The prefix tells at a single glance which record an identifier belongs to, and
// makes a call issued with the wrong identifier visible in the log.
//
// # The "prule_" prefix is SHARED with pricing
//
// The PriceRule records of the pricing module also use the "prule_" prefix. The
// collision is deliberate: plan Section 8 names this prefix for the promotion rule,
// and the two modules never see each other's identifiers (Principle 2.1 — modules
// cannot reach each other's data). The prefix is therefore not a separator BETWEEN
// modules but a type separator INSIDE a module: a price rule identifier handed to
// the promotion module passes validation, but the read returns errors.NotFound.
const (
	// CampaignIDPrefix is the prefix of campaign identifiers.
	CampaignIDPrefix = "camp_"
	// PromotionIDPrefix is the prefix of promotion identifiers.
	PromotionIDPrefix = "promo_"
	// PromotionRuleIDPrefix is the prefix of promotion rule identifiers.
	PromotionRuleIDPrefix = "prule_"
	// ApplicationMethodIDPrefix is the prefix of application method identifiers.
	//
	// Plan Section 8 names no prefix for this record; "appm_" was chosen here and it
	// collides with no prefix in the repository.
	ApplicationMethodIDPrefix = "appm_"
	// RedemptionIDPrefix is the prefix of the coupon redemption record.
	//
	// Plan Section 8 names no prefix for this record either; the ledger of the usage
	// counter emerged in this phase (see [Redemption]).
	RedemptionIDPrefix = "predeem_"
)

// idBodyLen is the number of characters in the body beyond the prefix: 16 bytes
// encoded as Crockford Base32 without padding come to exactly 26 characters.
const idBodyLen = 26

// idEncoding is the unpadded encoding over the Crockford Base32 alphabet. Because
// the alphabet is in ascending order in ASCII, the encoded string preserves the same
// lexicographic order as the bytes it encodes; identifiers therefore stay sortable
// by time and "ORDER BY id" naturally yields creation order.
//
// Sortability is NOT DECORATION in this module: the application order inside
// [github.com/bdrtr/gobit/internal/modules/promotion/service.ComputeResult] is
// determined by identifier (see the ordering rule in the service package), and the
// claim "the promotion written first is applied first" is only meaningful if the
// identifier carries the order of time.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// NewID produces a time-ordered, unique identifier with the given prefix.
//
// Its structure is the same as a ULID: a 48-bit millisecond timestamp + 80 bits of
// cryptographic randomness, encoded into 26 characters with Crockford Base32.
//
// The generators in internal/core/workflow/pgstore and in the other modules have the
// same structure; module isolation forbids importing those packages
// (Principle 2.4), so the generator is repeated here.
func NewID(prefix string, t time.Time) string {
	ms := t.UTC().UnixMilli()
	if ms < 0 {
		// A timestamp before 1970 is not meaningful for a record; it is clamped to
		// the floor so that ordering is not broken.
		ms = 0
	}

	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(ms))

	var buf [16]byte
	// UnixMilli fits in 48 bits; the first two bytes are always zero and are dropped.
	copy(buf[:6], stamp[2:])
	if _, err := rand.Read(buf[6:]); err != nil {
		// crypto/rand.Read does not return an error; should it ever do so one day,
		// the identifier rests on nanosecond resolution alone — uniqueness weakens,
		// but opening a record does not fail.
		binary.BigEndian.PutUint64(buf[8:], uint64(t.UnixNano()))
	}

	return prefix + idEncoding.EncodeToString(buf[:])
}

// NewCampaignID produces a new campaign identifier.
func NewCampaignID(t time.Time) string { return NewID(CampaignIDPrefix, t) }

// NewPromotionID produces a new promotion identifier.
func NewPromotionID(t time.Time) string { return NewID(PromotionIDPrefix, t) }

// NewPromotionRuleID produces a new promotion rule identifier.
func NewPromotionRuleID(t time.Time) string { return NewID(PromotionRuleIDPrefix, t) }

// NewApplicationMethodID produces a new application method identifier.
func NewApplicationMethodID(t time.Time) string { return NewID(ApplicationMethodIDPrefix, t) }

// NewRedemptionID produces a new redemption record identifier.
func NewRedemptionID(t time.Time) string { return NewID(RedemptionIDPrefix, t) }

// IDBodyLength returns the length of the body beyond the prefix; it is the single
// source of truth for tests and validation.
func IDBodyLength() int { return idBodyLen }
