package models

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// This file carries the gift card (ADR 0208): a code that holds a balance in
// one currency, spent by whoever presents it. The provider's own session, which
// is how the balance is spent, is [TenderSession].

// GiftCard is one card. Its balance is not here: it is the sum of the card's
// [GiftCardEntry] rows, as store credit's is (ADR 0152).
type GiftCard struct {
	// ID is the "gcard_" prefixed identifier.
	ID string
	// CodeTail is the code's last four characters, which is all of it the shop
	// keeps in the clear: the code is a bearer credential and is stored as a
	// digest.
	CodeTail string
	// CurrencyCode is the one currency the card holds.
	CurrencyCode string
	// Reason is why an operator issued the card; shown to nobody else.
	Reason string
	// CreatedAt is when the card was issued (UTC).
	CreatedAt time.Time
}

// GiftCardKind says what happened to a card's balance. The vocabulary is store
// credit's, closed by the schema for the same reason.
type GiftCardKind string

// The four things that can happen to a card's balance.
const (
	// GiftCardIssue is the balance the card was issued with; positive.
	GiftCardIssue GiftCardKind = "issue"
	// GiftCardHold is a payment session putting some of it aside; NEGATIVE.
	GiftCardHold GiftCardKind = "hold"
	// GiftCardRelease is a canceled session's hold coming back; positive.
	GiftCardRelease GiftCardKind = "release"
	// GiftCardRefund is a captured payment repaid onto the card; positive.
	GiftCardRefund GiftCardKind = "refund"
)

// Valid reports whether the kind is one of the four.
func (k GiftCardKind) Valid() bool {
	switch k {
	case GiftCardIssue, GiftCardHold, GiftCardRelease, GiftCardRefund:
		return true
	default:
		return false
	}
}

// String returns the kind as text.
func (k GiftCardKind) String() string { return string(k) }

// GiftCardEntry is ONE event on a card's balance.
type GiftCardEntry struct {
	// ID is the "gcentry_" prefixed identifier.
	ID string
	// GiftCardID is the card.
	GiftCardID string
	// Amount is SIGNED minor units of the card's currency; the balance is the
	// sum of them. Its sign is decided by the kind and the schema holds it.
	Amount int64
	// Kind is what happened.
	Kind GiftCardKind
	// Reference is the payment session the row belongs to, for the three kinds
	// that have one; empty on the issue.
	Reference string
	// CreatedAt is when it happened (UTC).
	CreatedAt time.Time
}

// GiftCardCodeLength is how many characters a code has: ten random bytes in the
// identifiers' Crockford alphabet, 80 bits.
const GiftCardCodeLength = 16

// giftCardCodeGroup is how many characters the printed code groups together.
const giftCardCodeGroup = 4

// NewGiftCardCode draws a new code and returns it as printed, in groups of four
// ("ABCD-EFGH-JKMN-PQRS").
//
// Eighty bits is what makes a code safe to accept from anybody: a guesser
// trying a million codes a second against a shop holding a million cards finds
// one in about 38,000 years.
func NewGiftCardCode() string {
	var raw [10]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand does not fail on the platforms Go supports; a code drawn
		// from anything weaker would be worse than no code.
		panic("gift card code: " + err.Error())
	}
	code := idEncoding.EncodeToString(raw[:])

	var printed strings.Builder
	for i := 0; i < len(code); i += giftCardCodeGroup {
		if i > 0 {
			printed.WriteByte('-')
		}
		printed.WriteString(code[i : i+giftCardCodeGroup])
	}

	return printed.String()
}

// NormalizeGiftCardCode reads a code as a person typed it: case, dashes and
// spaces do not matter, and the letters the Crockford alphabet leaves out for
// looking like digits are read as those digits (O as 0, I and L as 1). It
// reports false for anything that cannot be a code.
func NormalizeGiftCardCode(typed string) (string, bool) {
	var out strings.Builder
	for _, r := range strings.ToUpper(typed) {
		switch r {
		case '-', ' ':
			continue
		case 'O':
			r = '0'
		case 'I', 'L':
			r = '1'
		}
		if !strings.ContainsRune(giftCardAlphabet, r) {
			return "", false
		}
		out.WriteRune(r)
	}
	if out.Len() != GiftCardCodeLength {
		return "", false
	}

	return out.String(), true
}

// giftCardAlphabet is the identifiers' Crockford alphabet.
const giftCardAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// GiftCardCodeDigest is what the shop keeps of a normalized code: its SHA-256,
// in hex. The code has 80 random bits, so a plain digest cannot be walked back
// to it and a slow hash would buy nothing but time at every payment.
func GiftCardCodeDigest(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))

	return hex.EncodeToString(sum[:])
}

// GiftCardCodeTail is the part of a normalized code an operator is shown.
func GiftCardCodeTail(normalized string) string {
	return normalized[len(normalized)-giftCardCodeGroup:]
}
