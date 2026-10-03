package service

import (
	"math"
	"strings"
	"unicode"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// Length bounds. An unbounded text field is the cheapest way to write megabytes
// of data into a table with a single request.
const (
	// maxNameLen is the maximum byte length of a rate name.
	maxNameLen = 255
	// maxCodeLen is the maximum byte length of a reconciliation code.
	maxCodeLen = 64
	// maxIDLen is the upper bound for ids coming from outside; core/link and the
	// other modules apply the same bound.
	maxIDLen = 255
)

// NormalizeCountryCode validates an ISO 3166-1 alpha-2 country code and turns
// it into UPPER case.
//
// The accepted form is exactly two LETTERS. Leading/trailing whitespace is
// trimmed (the code is already normalized by converting it to upper case; a
// separate strictness for whitespace would be inconsistent), but no character
// other than a letter is accepted.
//
// Only the FORMAT is checked. Whether the code is defined in ISO is not known
// in this module: the country list is the region module's data and tax cannot
// import it (ADR 0001). The distinction matters — "XX" is a formally valid but
// undefined code, and in this module it only leads to the result "no tax
// region".
//
// It is exported because the same normalization is used both on the service's
// inputs and on the cross-module surface (see interop.go); the two places
// diverging would mean a code that passes through one path does not pass
// through the other.
func NormalizeCountryCode(code string) (string, error) {
	trimmed := strings.TrimSpace(code)
	// Length is measured in RUNES, not BYTES: otherwise a single two-byte
	// character would pass the length check and get stuck at the letter check,
	// and the message would report the wrong reason.
	if len([]rune(trimmed)) != models.CountryCodeLength {
		return "", errors.Invalid(CodeInvalidInput,
			"the country code has to be exactly %d letters (ISO 3166-1 alpha-2), %q was given",
			models.CountryCodeLength, code)
	}
	// The ASCII check is made BEFORE the conversion to upper case. The order is
	// critical: Unicode's simple upper-case mapping moves some NON-ASCII letters
	// onto ASCII letters (the dotless i, U+0131, -> "I"); had the check been made
	// afterwards, a dotless i followed by "s" would silently become "IS"
	// (Iceland).
	for _, r := range trimmed {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return "", errors.Invalid(CodeInvalidInput,
				"the country code can only contain ASCII letters (ISO 3166-1 alpha-2), %q was given", code)
		}
	}
	return strings.ToUpper(trimmed), nil
}

// NormalizeProvinceCode validates a state/province code and turns it into UPPER
// case.
//
// An empty input returns an empty output and is NOT AN ERROR: the province code
// is optional and its absence means "country level". On a non-empty input the
// accepted alphabet is ASCII letters, digits and the hyphen — the in-country
// part of ISO 3166-2 (e.g. "CA" in "US-CA"), Canadian provinces and Turkey's
// license plate codes ("34") fall into this set. The code can also start WITH
// A DIGIT; the only restriction is that the first character is NOT a hyphen (a
// value like "-CA" is a sign that the separator was copied by mistake).
func NormalizeProvinceCode(code string) (string, error) {
	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		return "", nil
	}

	runes := []rune(trimmed)
	if len(runes) > models.MaxProvinceCodeLength {
		return "", errors.Invalid(CodeInvalidInput,
			"the province code can be at most %d characters, %q was given",
			models.MaxProvinceCodeLength, code)
	}
	for i, r := range runes {
		alnum := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if alnum {
			continue
		}
		if r == '-' && i > 0 {
			continue
		}
		return "", errors.Invalid(CodeInvalidInput,
			"the province code can contain ASCII letters, digits and hyphens and cannot start with a hyphen, %q was given", code)
	}
	return strings.ToUpper(trimmed), nil
}

// normalizeName validates a rate's name and trims its leading/trailing
// whitespace.
func normalizeName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", errors.Invalid(CodeInvalidInput, "the tax rate name cannot be empty")
	}
	if len(trimmed) > maxNameLen {
		return "", errors.Invalid(CodeInvalidInput,
			"the tax rate name can be at most %d bytes, %d bytes were given", maxNameLen, len(trimmed))
	}
	for _, r := range trimmed {
		// Control characters (line breaks included) are not a name, and they
		// break logs and the admin UI.
		if unicode.IsControl(r) {
			return "", errors.Invalid(CodeInvalidInput, "the tax rate name cannot contain control characters")
		}
	}
	return trimmed, nil
}

// normalizeCode validates the reconciliation code; an empty input returns an
// empty output.
//
// Blank means "no code" and is turned into SQL NULL in the repository. Counting
// the empty string as a code would mean two rates without a code colliding in
// the uniqueness index within the region.
func normalizeCode(code string) (string, error) {
	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		return "", nil
	}
	if len(trimmed) > maxCodeLen {
		return "", errors.Invalid(CodeInvalidInput,
			"the tax rate code can be at most %d bytes, %d bytes were given", maxCodeLen, len(trimmed))
	}
	for _, r := range trimmed {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", errors.Invalid(CodeInvalidInput,
				"the tax rate code cannot contain whitespace or control characters: %q", code)
		}
	}
	return trimmed, nil
}

// validateRateBps verifies that the rate is within the allowed range.
//
// The rate is in BASIS POINTS (2000 = 20%). The upper bound is 100%: a larger
// rate is a data entry error and would silently double the cart total.
func validateRateBps(rateBps int32) error {
	if rateBps < models.MinRateBps {
		return errors.Invalid(CodeInvalidInput,
			"the tax rate cannot be negative, %d was given (basis points)", rateBps)
	}
	if rateBps > models.MaxRateBps {
		return errors.Invalid(CodeInvalidInput,
			"the tax rate can be at most %d basis points (100%%), %d was given",
			models.MaxRateBps, rateBps)
	}
	return nil
}

// requireID verifies that an id coming from outside is usable and of the RIGHT
// KIND.
//
// The prefix check is deliberate: the reason prefixed ids exist is that an id
// of the wrong kind (e.g. a rate id standing in for a region) comes back not
// as "not found" but as a validation error that says what it is.
func requireID(id, prefix, label string) error {
	if id == "" {
		return errors.Invalid(CodeInvalidInput, "%s cannot be empty", label)
	}
	if strings.TrimSpace(id) != id {
		return errors.Invalid(CodeInvalidInput, "%s cannot contain leading/trailing whitespace: %q", label, id)
	}
	if len(id) > maxIDLen {
		return errors.Invalid(CodeInvalidInput,
			"%s can be at most %d bytes, %d bytes were given", label, maxIDLen, len(id))
	}
	if !strings.HasPrefix(id, prefix) {
		return errors.Invalid(CodeInvalidInput,
			"%s has to start with the %q prefix, %q was given", label, prefix, id)
	}
	return nil
}

// requireReferenceID verifies that the FOREIGN id a rule looks at is usable.
//
// NO prefix check is made: the id belongs to another module (product, product
// type, shipping option), and repeating those modules' prefix contract here
// would mean tax silently refusing rules when a module changed its prefix
// (ADR 0001 — tax does not know those modules).
func requireReferenceID(id string) error {
	if id == "" {
		return errors.Invalid(CodeInvalidInput, "the rule reference id cannot be empty")
	}
	if strings.TrimSpace(id) != id {
		return errors.Invalid(CodeInvalidInput,
			"the rule reference id cannot contain leading/trailing whitespace: %q", id)
	}
	if len(id) > maxIDLen {
		return errors.Invalid(CodeInvalidInput,
			"the rule reference id can be at most %d bytes, %d bytes were given", maxIDLen, len(id))
	}
	return nil
}

// normalizePaging turns the paging parameters into applicable values.
//
// If the limit is 0 or negative the default applies, and if it exceeds
// [MaxLimit] the maximum applies; clamping is NOT an error, but the value
// applied is reported back in the result (see [Page]). A negative offset, on
// the other hand, is a request that cannot be corrected and is refused.
func normalizePaging(limit, offset int32) (outLimit, outOffset int32, err error) {
	if offset < 0 {
		return 0, 0, errors.Invalid(CodeInvalidInput, "the offset cannot be negative, %d was given", offset)
	}
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	return limit, offset, nil
}

// clampToInt32 clamps an int value into the int32 range.
//
// The Query layer's ListOptions fields are int; on a 64-bit platform a huge
// value coming from there would WRAP when converted to int32 and could produce
// a negative limit. The clamp makes that wrapping impossible; the bound itself
// is brought down to [MaxLimit] in normalizePaging anyway.
func clampToInt32(value int) int32 {
	if value > math.MaxInt32 {
		return math.MaxInt32
	}
	if value < math.MinInt32 {
		return math.MinInt32
	}
	return int32(value)
}
