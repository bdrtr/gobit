package models

import "strings"

// latinASCII maps the Latin letters that carry a mark to their ASCII base.
//
// # Why a written table and not a Unicode normalization
//
// Decomposing to NFD and dropping the combining marks would fold most of these
// automatically and would need golang.org/x/text as a DIRECT dependency, which
// ADR 0010 makes a decision rather than a convenience. It would also miss the
// letter this repository cares about most: the Turkish dotless i (U+0131) has no
// decomposition, so NFD leaves it alone and "kirmizi" would never match the
// dotted spelling of the same word. A written table gets that letter right and
// can be read end to end by somebody deciding whether their catalog is safe.
//
// # What is NOT here is the point of it
//
// Only letters with an unambiguous ASCII base appear. Every other rune —
// Cyrillic, Greek, Hebrew, Arabic, CJK — is absent ON PURPOSE and passes through
// [FoldOptionValue] unchanged. See that function for why that is the whole
// design.
//
// The keys are written as \u escapes so that this file carries no Turkish letter
// of its own (ADR 0012); the comment on each line names the letter and repeats
// its code point.
var latinASCII = map[rune]rune{
	'\u00e7': 'c', // c with cedilla (U+00E7)
	'\u011f': 'g', // g with breve (U+011F)
	'\u0131': 'i', // dotless i (U+0131)
	'\u00f6': 'o', // o with diaeresis (U+00F6)
	'\u015f': 's', // s with cedilla (U+015F)
	'\u00fc': 'u', // u with diaeresis (U+00FC)
	'\u00e1': 'a', // a with acute (U+00E1)
	'\u00e0': 'a', // a with grave (U+00E0)
	'\u00e2': 'a', // a with circumflex (U+00E2)
	'\u00e3': 'a', // a with tilde (U+00E3)
	'\u00e4': 'a', // a with diaeresis (U+00E4)
	'\u00e5': 'a', // a with ring (U+00E5)
	'\u00e9': 'e', // e with acute (U+00E9)
	'\u00e8': 'e', // e with grave (U+00E8)
	'\u00ea': 'e', // e with circumflex (U+00EA)
	'\u00eb': 'e', // e with diaeresis (U+00EB)
	'\u00ed': 'i', // i with acute (U+00ED)
	'\u00ec': 'i', // i with grave (U+00EC)
	'\u00ee': 'i', // i with circumflex (U+00EE)
	'\u00ef': 'i', // i with diaeresis (U+00EF)
	'\u00f1': 'n', // n with tilde (U+00F1)
	'\u00f3': 'o', // o with acute (U+00F3)
	'\u00f2': 'o', // o with grave (U+00F2)
	'\u00f4': 'o', // o with circumflex (U+00F4)
	'\u00f5': 'o', // o with tilde (U+00F5)
	'\u00fa': 'u', // u with acute (U+00FA)
	'\u00f9': 'u', // u with grave (U+00F9)
	'\u00fb': 'u', // u with circumflex (U+00FB)
	'\u00fd': 'y', // y with acute (U+00FD)
	'\u00ff': 'y', // y with diaeresis (U+00FF)
}

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
	var b strings.Builder

	lowered := strings.ToLower(strings.TrimSpace(value))
	b.Grow(len(lowered))

	for _, r := range lowered {
		if ascii, mapped := latinASCII[r]; mapped {
			b.WriteRune(ascii)

			continue
		}

		b.WriteRune(r)
	}

	return b.String()
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
