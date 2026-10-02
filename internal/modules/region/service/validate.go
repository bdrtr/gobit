package service

import (
	"math"
	"strings"
	"unicode"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// maxNameLen is the maximum byte length of a region name. An unbounded name is
// the cheapest way to write megabytes of text into the table with a single
// request.
const maxNameLen = 255

// maxIDLen is the upper bound on the accepted id length. Because ids also go
// into the unique index of the link table, the bound is kept consistent with
// that index.
const maxIDLen = 255

// NormalizeCurrencyCode validates an ISO 4217 currency code and converts it to
// UPPER case.
//
// The accepted form is exactly three LETTERS. Leading/trailing whitespace is
// trimmed (the code is already normalized by being converted to upper case; a
// separate strictness for whitespace would be inconsistent), but no character
// other than a letter is accepted.
//
// Only the FORM is checked; whether the code is defined is known only from the
// reference table in the database, and it is checked there by the foreign key.
// The distinction matters: "abc" is a formally valid but undefined code, and
// both cases return errors.Invalid; they differ only in their messages.
//
// It is exported because the same normalization is used both on the service
// inputs and on the cross-module surface (see interop.go), and the two places
// diverging would mean that a code passing through one path does not pass
// through the other.
func NormalizeCurrencyCode(code string) (string, error) {
	return normalizeAlphaCode(code, models.CurrencyCodeLength, "the currency code", "ISO 4217")
}

// NormalizeCountryCode validates an ISO 3166-1 alpha-2 country code and
// converts it to UPPER case.
//
// The accepted form is exactly two LETTERS; the rationale for the rule is the
// same as for [NormalizeCurrencyCode].
func NormalizeCountryCode(code string) (string, error) {
	return normalizeAlphaCode(code, models.CountryCodeLength, "the country code", "ISO 3166-1 alpha-2")
}

// normalizeAlphaCode validates a fixed-length alphabetic code and converts it
// to upper case.
//
// The ASCII check is made BEFORE the upper-case conversion, and only on the
// trimmed ORIGINAL runes. The order is critical: Unicode's simple upper-case
// mapping moves some NON-ASCII letters onto ASCII letters (the dotless i,
// U+0131, -> "I"; the long s "ſ" -> "S"). Had the check been made AFTER the
// conversion, a dotless i followed by "s" would silently become "IS" (Iceland),
// a dotless i followed by "ls" would become "ILS", and the function's "ASCII
// letters only" promise would not hold.
//
// Length is measured in RUNES, not BYTES: an input with the same byte length as
// "TRY" that is not three characters (e.g. a two-byte letter and an ASCII one)
// would otherwise pass the length check and get stuck at the letter check —
// the message would then report the wrong reason.
func normalizeAlphaCode(code string, length int, label, standard string) (string, error) {
	trimmed := strings.TrimSpace(code)
	if len([]rune(trimmed)) != length {
		return "", errors.Invalid(CodeInvalidInput,
			"%s has to be exactly %d letters (%s), %q given", label, length, standard, code)
	}
	for _, r := range trimmed {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return "", errors.Invalid(CodeInvalidInput,
				"%s can only contain ASCII letters (%s), %q given", label, standard, code)
		}
	}
	return strings.ToUpper(trimmed), nil
}

// normalizeName validates a region name and trims its leading/trailing
// whitespace.
func normalizeName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", errors.Invalid(CodeInvalidInput, "the region name cannot be empty")
	}
	if len(trimmed) > maxNameLen {
		return "", errors.Invalid(CodeInvalidInput,
			"the region name can be at most %d bytes, %d bytes given", maxNameLen, len(trimmed))
	}
	for _, r := range trimmed {
		// Control characters (line breaks included) are not part of a name, and
		// they break the log and the admin interface.
		if unicode.IsControl(r) {
			return "", errors.Invalid(CodeInvalidInput, "the region name cannot contain a control character")
		}
	}
	return trimmed, nil
}

// validateTaxRate validates that the tax rate is within the allowed range.
//
// The rate is in BASIS POINTS (2000 = 20%). The upper bound is 100%: a larger
// rate is a data entry error and would silently double the cart total.
func validateTaxRate(rate int32) error {
	if rate < models.MinTaxRate {
		return errors.Invalid(CodeInvalidInput,
			"the tax rate cannot be negative, %d given (basis points)", rate)
	}
	if rate > models.MaxTaxRate {
		return errors.Invalid(CodeInvalidInput,
			"the tax rate can be at most %d basis points (100%%), %d given", models.MaxTaxRate, rate)
	}
	return nil
}

// requireRegionID validates that a region id is usable and of the RIGHT TYPE.
//
// The prefix check is deliberate: the reason prefixed ids exist is that an id
// of the wrong type (e.g. a customer id standing in for a region) comes back
// not as "not found" but as a validation error that says what it is.
func requireRegionID(id string) error {
	const label = "the region id"
	if id == "" {
		return errors.Invalid(CodeInvalidInput, "%s cannot be empty", label)
	}
	if strings.TrimSpace(id) != id {
		return errors.Invalid(CodeInvalidInput, "%s cannot contain leading/trailing whitespace: %q", label, id)
	}
	if len(id) > maxIDLen {
		return errors.Invalid(CodeInvalidInput,
			"%s can be at most %d bytes, %d bytes given", label, maxIDLen, len(id))
	}
	if !strings.HasPrefix(id, models.RegionIDPrefix) {
		return errors.Invalid(CodeInvalidInput,
			"%s has to start with the %q prefix, %q given", label, models.RegionIDPrefix, id)
	}
	return nil
}

// normalizePaging converts the paging parameters into applicable values.
//
// If the limit is 0 or negative the default is applied, and if it exceeds
// [MaxLimit] the maximum value is applied; clamping is NOT an error, but the
// applied value is reported back in the result (see [Page]). A negative offset,
// on the other hand, is a request that cannot be corrected, and it is rejected.
func normalizePaging(limit, offset int32) (outLimit, outOffset int32, err error) {
	if offset < 0 {
		return 0, 0, errors.Invalid(CodeInvalidInput, "the offset cannot be negative, %d given", offset)
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
// The fields of the Query layer's [query.ListOptions] are int; on a 64-bit
// platform a huge value coming from there would WRAP when converted to int32
// and could produce a negative limit. Clamping makes that wraparound
// impossible; the bound itself is brought down to [MaxLimit] in normalizePaging
// anyway.
func clampToInt32(value int) int32 {
	if value > math.MaxInt32 {
		return math.MaxInt32
	}
	if value < math.MinInt32 {
		return math.MinInt32
	}
	return int32(value)
}
