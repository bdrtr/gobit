package models

import "github.com/bdrtr/gobit/internal/core/textfold"

// The table of letters and the fold itself live in internal/core/textfold,
// which the admin lists' search repeats in SQL (ADR 0456); this file keeps
// the argument for why an option value is matched by it.

// FoldOptionValue returns the form an option value is MATCHED by.
//
// # What it does
//
// Trims the ends, lower-cases with Go's Unicode rules, and replaces each Latin
// letter carrying a mark with its ASCII base. Everything else is left exactly as
// it stands.
//
// # Why untranslatable letters are KEPT, which is the whole decision
//
// The obvious implementation was to reuse slugify, the fold this repository
// already applies to handles, and A18 was originally written that way — "the text
// folded to ASCII the way a handle already is". Measured before it was built,
// that rule destroys catalogs:
//
//	slugify("krasnyy in Cyrillic")  -> ""
//	slugify("siniy in Cyrillic")    -> ""
//	slugify("aka in Kanji")         -> ""
//	slugify("Rod with o-slash")     -> "r-d"
//
// slugify DROPS a rune it cannot transliterate, because a URL handle has to be
// ASCII and a handle that loses a word is merely ugly. A matching form that loses
// a word is a different thing: every Cyrillic value in a shop would fold to the
// empty string, so they would all be equal to one another, and the UNIQUE index
// this decision adds would refuse to build at all on such a catalog. The rule
// looks flawless on Turkish, which is exactly why it reads as safe.
//
// Keeping the rune costs the one thing the fold was for — a Russian shopper
// typing an unaccented approximation still misses — but that is a MISS, and the
// alternative is a merge. A filter that finds nothing is a filter somebody
// retypes; a filter that reports one product's stock under another product's
// color is a wrong answer nobody can see.
//
// # Why it is not slugify's separator handling either
//
// slugify turns spaces into dashes because a handle may not contain a space.
// "navy blue" and "navy-blue" are two values a merchant may have meant to keep
// apart, and this fold keeps them apart: it changes case and marks, and nothing
// about the shape of the text.
func FoldOptionValue(value string) string {
	return textfold.Fold(value)
}

// OptionValueHandle is one option value's stored text and its matching form.
//
// It exists for the convergence pass ADR 0039 leaves open, and it carries the
// option id as well as the two strings because the failure that pass has to
// report is a COLLISION WITHIN AN OPTION: two spellings of one value that the
// SQL backfill left apart and the Go fold brings together.
type OptionValueHandle struct {
	// ID is the value's id, and the pass's keyset cursor.
	ID string
	// OptionID is the option the value belongs to, which is the scope the
	// uniqueness of the folded form is enforced in.
	OptionID string
	// Value is what the merchant typed, untouched.
	Value string
	// Folded is the matching form as it currently stands.
	Folded string
}
