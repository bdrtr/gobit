package service

import (
	"strings"
	"unicode"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
)

// countryCodeLen is the length of an ISO 3166-1 alpha-2 code.
const countryCodeLen = 2

// currencyCodeLen is the length of an ISO 4217 code.
const currencyCodeLen = 3

// maxIDLen is the upper bound on an accepted id's length. Since ids also go
// into the unique index in the link table, the bound is kept in line with it.
const maxIDLen = 255

// normalizeEmail validates an e-mail address and turns it into the storage
// form.
//
// The validation is DELIBERATELY narrow: instead of a full RFC 5322 parser, it
// checks only "there is a single @, both sides are filled, the domain has a
// dot, there is no whitespace". A stricter pattern would reject valid but
// unusual addresses, and a looser one would trip the CHECK constraint in the
// migration and hand the client a meaningless database error.
//
// The same validator exists in the customer module too; because of module
// isolation (Principle 2.4) that package cannot be imported, and the logic is
// repeated here.
func normalizeEmail(email string) (string, error) {
	normalized := models.NormalizeEmail(email)
	if normalized == "" {
		return "", errors.Invalid(CodeInvalidInput, "the email address cannot be empty")
	}
	if len(normalized) > models.MaxEmailLen {
		return "", errors.Invalid(CodeInvalidInput,
			"the email address can be at most %d bytes, %d bytes given", models.MaxEmailLen, len(normalized))
	}
	if strings.ContainsFunc(normalized, unicode.IsSpace) {
		return "", errors.Invalid(CodeInvalidInput, "the email address cannot contain whitespace: %q", email)
	}

	local, domain, found := strings.Cut(normalized, "@")
	if !found || local == "" || domain == "" {
		return "", errors.Invalid(CodeInvalidInput,
			"the email address has to be in the \"name@domain.tld\" form, %q given", email)
	}
	if strings.Contains(domain, "@") {
		return "", errors.Invalid(CodeInvalidInput,
			"the email address cannot contain more than one @, %q given", email)
	}
	// At least one dot is required in the domain, and the dot cannot be at
	// either end: the difference between "a@b" and "a@b." is that the second
	// can never be delivered.
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", errors.Invalid(CodeInvalidInput,
			"the email domain is invalid, %q given", email)
	}
	return normalized, nil
}

// normalizeCountryCode validates a country code and turns it to UPPER case.
//
// An EMPTY value is valid and comes back empty: the company address is not
// required, and most records are opened before the billing address is
// settled. Whether the code corresponds to a country that really exists is NOT
// checked HERE; the region module owns the country list and b2b cannot import
// it (ADR 0001).
func normalizeCountryCode(code string) (string, error) {
	normalized := models.NormalizeCountryCode(code)
	if normalized == "" {
		return "", nil
	}
	if len(normalized) != countryCodeLen {
		return "", errors.Invalid(CodeInvalidInput,
			"the country code has to be exactly %d letters (ISO 3166-1 alpha-2), %q given", countryCodeLen, code)
	}
	if !onlyLettersAToZ(normalized) {
		return "", errors.Invalid(CodeInvalidInput,
			"the country code can only contain letters (ISO 3166-1 alpha-2), %q given", code)
	}
	return normalized, nil
}

// normalizeCurrencyCode validates a currency code and turns it to UPPER case.
//
// Unlike the country code it is REQUIRED: a spending limit is an integer and
// cannot be compared without knowing which currency it is in. Whether the code
// is a currency that is really defined is not checked here; that list belongs
// to the region module.
func normalizeCurrencyCode(code string) (string, error) {
	normalized := models.NormalizeCurrencyCode(code)
	if normalized == "" {
		return "", errors.Invalid(CodeInvalidInput, "the currency code cannot be empty")
	}
	if len(normalized) != currencyCodeLen {
		return "", errors.Invalid(CodeInvalidInput,
			"the currency code has to be exactly %d letters (ISO 4217), %q given", currencyCodeLen, code)
	}
	if !onlyLettersAToZ(normalized) {
		return "", errors.Invalid(CodeInvalidInput,
			"the currency code can only contain letters (ISO 4217), %q given", code)
	}
	return normalized, nil
}

// onlyLettersAToZ reports whether the string consists only of the letters
// A-Z.
//
// unicode.IsLetter is NOT USED: the Turkish capital S with a cedilla is a
// letter too, but ISO codes are ASCII only, and letting it through would trip
// the pattern constraint in the database with an incomprehensible error.
func onlyLettersAToZ(s string) bool {
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// normalizeResetPeriod validates a reset period.
//
// An empty value falls back to [models.ResetNever]: giving no period means "no
// reset" and is the most restrictive option — counting an unknown value as
// monthly would silently switch on a reset nobody asked for.
func normalizeResetPeriod(period string) (models.SpendingResetPeriod, error) {
	trimmed := models.SpendingResetPeriod(strings.TrimSpace(period))
	if trimmed == "" {
		return models.ResetNever, nil
	}
	if !trimmed.Valid() {
		return "", errors.Invalid(CodeInvalidInput,
			"the spending limit reset period has to be %q, %q or %q, %q given",
			models.ResetMonthly, models.ResetYearly, models.ResetNever, period)
	}
	return trimmed, nil
}

// requireID verifies that an id is not empty, and checks its prefix and its
// length.
//
// The prefix check is cheap type safety: a company id passed in place of an
// employee id is caught without ever going to the database, and the error says
// what was expected instead of "not found".
func requireID(id, prefix, label string) error {
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
	if !strings.HasPrefix(id, prefix) {
		return errors.Invalid(CodeInvalidInput,
			"%s has to start with the %q prefix, %q given", label, prefix, id)
	}
	return nil
}

// normalizePaging turns the paging parameters into applicable values.
//
// If a limit of 0 is given the default applies. A negative limit/offset and a
// limit above [MaxLimit], however, are NOT CORRECTED but rejected: a silently
// clipped limit misreports the page size to the client, and the paging loop
// reads the same records again.
func normalizePaging(limit, offset int64) (outLimit, outOffset int64, err error) {
	if limit < 0 {
		return 0, 0, errors.Invalid(CodeInvalidInput, "the limit cannot be negative, %d given", limit)
	}
	if offset < 0 {
		return 0, 0, errors.Invalid(CodeInvalidInput, "the offset cannot be negative, %d given", offset)
	}
	if limit > MaxLimit {
		return 0, 0, errors.Invalid(CodeInvalidInput,
			"the limit can be at most %d, %d given", MaxLimit, limit)
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	return limit, offset, nil
}

// checkLen verifies the length bound of a text field.
func checkLen(label, value string, limit int) error {
	if len(value) > limit {
		return errors.Invalid(CodeInvalidInput,
			"%s can be at most %d bytes, %d bytes given", label, limit, len(value))
	}
	return nil
}

// requireText verifies that a text field is filled.
func requireText(label, value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.Invalid(CodeInvalidInput, "%s cannot be empty", label)
	}
	return nil
}

// validateSpendingLimit verifies that a spending limit is meaningful.
//
// nil means "unlimited" and is valid. A negative limit, however, is not a
// bound but a meaningless number: every comparison would exceed it, and the
// employee would silently become unable to buy anything at all.
func validateSpendingLimit(limit *int64) error {
	if limit != nil && *limit < 0 {
		return errors.Invalid(CodeInvalidInput,
			"the spending limit cannot be negative, %d given (leave the field empty for unlimited)", *limit)
	}
	return nil
}
