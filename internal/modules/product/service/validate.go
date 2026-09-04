package service

import (
	"math"
	"strings"
	"unicode"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// Paging limits. The store and the admin APIs share the same limits.
const (
	// DefaultLimit is the page size used when the client gives no limit.
	DefaultLimit = 20
	// MaxLimit is the largest number of records a single request can return.
	// The limit is applied by clamping (not as an error): a client asking for a
	// larger page is a valid request, it is only not granted.
	MaxLimit = 100
)

// Field length limits. There is no limit on the database text columns; the only
// place the limit lives is here, and its purpose is to stop huge bodies from
// spreading into memory and into the indexes.
const (
	maxHandleLen      = 128
	maxTitleLen       = 255
	maxValueLen       = 255
	maxURLLen         = 2048
	maxDescriptionLen = 16 * 1024
)

// int32From narrows a rank/index value to an int32 SAFELY.
//
// Had the narrowing been silent, the rank of the 2^31-th image would turn
// negative; the ordering of such a list breaks visibly but its cause stays
// invisible.
func int32From(n int) int32 {
	switch {
	case n < 0:
		return 0
	case n > math.MaxInt32:
		return math.MaxInt32
	default:
		return int32(n)
	}
}

// Error codes; an API consumer can look at them with errors.CodeOf.
const (
	codeInvalidInput = "product_invalid_input"
	codeHandleTaken  = "product_handle_taken"
	codeNotFound     = "product_not_found"
	codeLinkFailed   = "product_link_failed"
	codeQueryFailed  = "product_query_failed"
	codeNotReady     = "product_service_not_ready"
)

// invalid builds a validation error.
func invalid(format string, a ...any) error {
	return errors.Invalid(codeInvalidInput, format, a...)
}

// requireText validates a mandatory text field and returns its trimmed form.
//
// The trimming is deliberate: an incoming "  Shirt " and "Shirt" are the same
// product, and the leading space only breaks sorting and search. For ids (the
// ends of a link) NO trimming is done; there a silent correction is a data
// drift (see core/link).
func requireText(field, value string, maxLen int) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", invalid("%s is required", field)
	}
	if len(trimmed) > maxLen {
		return "", invalid("%s can be at most %d characters (given: %d)", field, maxLen, len(trimmed))
	}
	return trimmed, nil
}

// requireID validates a mandatory id field.
//
// The id is NOT TRIMMED: an id carrying leading/trailing whitespace would land
// on different rows in the link table and in its own table. The corruption is
// rejected instead of being corrected silently.
func requireID(field, value string) (string, error) {
	if value == "" {
		return "", invalid("%s is required", field)
	}
	if strings.TrimSpace(value) != value {
		return "", invalid("%s cannot contain leading or trailing whitespace", field)
	}
	if len(value) > maxHandleLen {
		return "", invalid("%s can be at most %d characters", field, maxHandleLen)
	}
	return value, nil
}

// resolveHandle validates the handle; if it was left empty it derives it from
// the title.
//
// The derived handle is subject to the LENGTH LIMIT as well. Because the title
// can be up to maxTitleLen (255), the slug derived from it easily exceeds
// maxHandleLen (128); a handle that exceeds it would make the product
// UNREACHABLE in the storefront, because /store/v1/products/{handle} validates
// the identifier with the same limit of 128 and returns 422 — the record is
// created but its address never opens.
//
// The derived slug is therefore TRUNCATED; a handle the client gave EXPLICITLY
// is not truncated, it is rejected. The distinction is deliberate: derivation
// is a convenience to begin with and shortening it does not change a value the
// client sent, whereas silently shortening a given handle would separate the
// intended address from the recorded one. If the result of the truncation
// collides with another product, ensureHandleFree returns an explicit Conflict;
// no silent overwrite happens.
func resolveHandle(handle, title string) (string, error) {
	h := strings.TrimSpace(handle)
	if h != "" {
		return validateHandle(h)
	}

	generated := truncateHandle(slugify(title))
	if generated == "" {
		return "", invalid("the handle was left empty and could not be derived from the title (%q)", title)
	}
	return validateHandle(generated)
}

// truncateHandle truncates the derived slug to the maximum handle length.
//
// A slug contains only ASCII letters, digits and dashes (see slugify), so
// truncating on a byte boundary cannot split a rune in two. A trailing dash
// left over from the truncation is dropped: a handle cannot end with a dash,
// and if it did validateHandle would reject a derived value.
func truncateHandle(slug string) string {
	if len(slug) <= maxHandleLen {
		return slug
	}
	return strings.TrimRight(slug[:maxHandleLen], "-")
}

// validateHandle validates the shape of the handle.
//
// The accepted shape: lowercase letters, digits and parts separated by a SINGLE
// dash. A handle is a URL segment; if uppercase letters and spaces were
// accepted, the same product would show up at two different addresses and the
// uniqueness index would not catch it either ("Shirt" and "shirt" would be two
// different rows).
func validateHandle(handle string) (string, error) {
	if handle == "" {
		return "", invalid("the handle is required")
	}
	if len(handle) > maxHandleLen {
		return "", invalid("the handle can be at most %d characters (given: %d)", maxHandleLen, len(handle))
	}
	if handle != slugify(handle) {
		return "", invalid(
			"the handle may contain only lowercase letters, digits and dashes; it cannot start or end with a dash (given: %q, suggested: %q)",
			handle, slugify(handle))
	}
	return handle, nil
}

// slugify derives a URL-usable handle from free text.
//
// Turkish letters are folded to their ASCII counterparts: a title written as
// "Ti\u015f\u00f6rt" becomes "tisort". Otherwise the handle would either
// produce a URL full of UTF-8 escapes or become meaningless once the letters
// were dropped ("tirt").
func slugify(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	lastDash := true // prevents leading dashes
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			lastDash = false
		case turkishASCII[r] != 0:
			b.WriteRune(turkishASCII[r])
			lastDash = false
		case unicode.Is(unicode.Mn, r):
			// A combining mark is dropped and PRODUCES NO SEPARATOR: the
			// lowercase form of the dotted capital I is "i" + a combining dot,
			// and had it counted as a separator, "\u0130stanbul" would become
			// "i-stanbul".
			continue
		case unicode.IsSpace(r), r == '-', r == '_', r == '.', r == '/':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		default:
			// An untranslatable letter (Cyrillic, for instance) is dropped; the
			// remaining parts are still separated by a single dash.
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// turkishASCII holds the ASCII counterparts of the Turkish letters.
//
// Because strings.ToLower has already been applied, the lowercase letters alone
// are enough. The dotted capital I (U+0130) is not here: converted to lowercase
// it becomes "i" + a combining dot, and the combining-mark branch of slugify
// drops the dot.
//
// The keys are written as escapes so that the file itself stays ASCII; the
// trailing name of each pair says which letter it is.
var turkishASCII = map[rune]rune{
	'\u0131': 'i', // dotless i
	'\u015f': 's', // s with cedilla
	'\u011f': 'g', // g with breve
	'\u00fc': 'u', // u with diaeresis
	'\u00f6': 'o', // o with diaeresis
	'\u00e7': 'c', // c with cedilla
	'\u00e2': 'a', // a with circumflex
	'\u00ee': 'i', // i with circumflex
	'\u00fb': 'u', // u with circumflex
}

// normalizeStatus validates the product status; if it is empty it assumes draft.
func normalizeStatus(status models.Status) (models.Status, error) {
	if status == "" {
		return models.StatusDraft, nil
	}
	if !status.Valid() {
		return "", invalid("invalid product status: %q (valid values: draft, published, archived)", status)
	}
	return status, nil
}

// normalizePaging validates the paging values and clamps them to the limits.
//
// A negative value is an ERROR (it is a client mistake and correcting it
// silently would return the wrong page); a limit of 0 is pulled to the default
// and a limit above the ceiling to MaxLimit.
func normalizePaging(limit, offset int) (outLimit, outOffset int, err error) {
	if limit < 0 {
		return 0, 0, invalid("the limit cannot be negative (given: %d)", limit)
	}
	if offset < 0 {
		return 0, 0, invalid("the offset cannot be negative (given: %d)", offset)
	}
	switch {
	case limit == 0:
		limit = DefaultLimit
	case limit > MaxLimit:
		limit = MaxLimit
	}
	return limit, offset, nil
}

// trimOptional trims an optional text field; if it became empty it returns nil.
func trimOptional(v *string, field string, maxLen int) (*string, error) {
	if v == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*v)
	if trimmed == "" {
		return nil, nil
	}
	if len(trimmed) > maxLen {
		return nil, invalid("%s can be at most %d characters (given: %d)", field, maxLen, len(trimmed))
	}
	return &trimmed, nil
}

// uniqueIDs validates a slice of ids and deduplicates it, preserving the order.
func uniqueIDs(field string, ids []string) ([]string, error) {
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		clean, err := requireID(field, id)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[clean]; dup {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out, nil
}
