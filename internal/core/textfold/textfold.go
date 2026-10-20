// Package textfold is the one fold gobit matches typed text by: lower case,
// and each Latin letter carrying a mark replaced by its ASCII base.
//
// The product module folds option values with it (ADR 0039), and the admin
// lists' search functions repeat it in SQL (ADR 0456), where
// [SQLTranslate] renders the table a migration's translate() has to carry; a
// gate in internal/arch holds every such migration to it.
package textfold

import (
	"sort"
	"strings"
	"unicode"
)

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
// [Fold] unchanged. See the product module's FoldOptionValue for why that is
// the whole design.
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

// Fold trims the ends, lower-cases with Go's Unicode rules, and replaces each
// Latin letter carrying a mark with its ASCII base. Everything else is left
// exactly as it stands.
func Fold(value string) string {
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

// SQLTranslate returns the two strings a SQL fold passes to translate() before
// lower(), so that lower(translate(v, from, to)) is [Fold] on every letter of
// the table and on ASCII, whatever the cluster's CTYPE.
//
// Each small letter of the table maps to its base, and so does its capital when
// the capital is not ASCII: lower() folds a non-ASCII capital only on a cluster
// whose CTYPE knows it (ADR 0015). Two capitals are added on their own. The
// dotted capital I (U+0130): no small letter of the table is its lower case, Go
// lowers it to an ASCII i, and lower() on a C cluster leaves it. And the ASCII
// capital I, the one ASCII letter lower() does not fold alike everywhere: a
// Turkish or Azerbaijani CTYPE lowers it to the dotless i (ADR 0460). The runes
// come in code-point order, so the strings are one fixed spelling a migration
// can be held to.
func SQLTranslate() (from, to string) {
	pairs := map[rune]rune{
		'I':      'i', // the ASCII capital I, which a Turkish CTYPE lowers to U+0131
		'\u0130': 'i', // dotted capital I (U+0130)
	}
	for small, base := range latinASCII {
		pairs[small] = base
		if capital := unicode.ToUpper(small); capital != small && capital > unicode.MaxASCII {
			pairs[capital] = base
		}
	}

	runes := make([]rune, 0, len(pairs))
	for r := range pairs {
		runes = append(runes, r)
	}
	sort.Slice(runes, func(i, j int) bool { return runes[i] < runes[j] })

	var f, t strings.Builder
	for _, r := range runes {
		f.WriteRune(r)
		t.WriteRune(pairs[r])
	}

	return f.String(), t.String()
}

// MaxQueryLen is the longest q, in runes, an admin list searches by. A
// longer one is refused rather than cut: a cut q would search for words the
// operator did not type to the end.
const MaxQueryLen = 200

// Query trims what an operator typed into a list's q. ok is false for a q
// with nothing in it, which searches for nothing and so filters nothing.
func Query(raw string) (q string, ok bool) {
	q = strings.TrimSpace(raw)

	return q, q != ""
}
