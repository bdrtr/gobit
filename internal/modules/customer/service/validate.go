package service

import (
	"strings"
	"unicode"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// countryCodeLen is the length of an ISO 3166-1 alpha-2 code.
const countryCodeLen = 2

// maxIDLen is the upper bound on the length of an accepted identifier. Because
// identifiers also enter the unique index on the link table, the bound is kept
// consistent with it.
const maxIDLen = 255

// normalizeEmail validates the e-mail address and converts it into its storage
// form.
//
// The validation is DELIBERATELY narrow: instead of writing a full RFC 5322
// parser, only "there is a single @, both sides are filled, the domain contains
// a dot, there is no whitespace" is checked. A stricter pattern would reject
// valid but unusual addresses (with a plus sign, with a hyphen, with a long
// TLD) and would leave the customer unable to sign up; a looser pattern, on the
// other hand, would get caught by the CHECK constraint in the migration and
// return a meaningless database error to the client. The pattern expresses
// exactly the same requirement as that constraint.
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
	// At least one dot is looked for in the domain and the dot cannot be at
	// either end: the difference between "a@b" and "a@b." is that the second
	// one can never be delivered to.
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", errors.Invalid(CodeInvalidInput,
			"the email domain is invalid, %q given", email)
	}
	return normalized, nil
}

// normalizeCountryCode validates the country code and converts it to UPPER
// case.
//
// The accepted form is ISO 3166-1 alpha-2: exactly two letters. Whether the
// code actually corresponds to an existing country is NOT checked HERE; the
// owner of the country list is the region module and customer cannot import it
// (ADR 0001).
func normalizeCountryCode(code string) (string, error) {
	normalized := models.NormalizeCountryCode(code)
	if len(normalized) != countryCodeLen {
		return "", errors.Invalid(CodeInvalidInput,
			"the country code has to be exactly %d letters (ISO 3166-1 alpha-2), %q given", countryCodeLen, code)
	}
	for _, r := range normalized {
		if r < 'A' || r > 'Z' {
			return "", errors.Invalid(CodeInvalidInput,
				"the country code can only contain letters (ISO 3166-1 alpha-2), %q given", code)
		}
	}
	return normalized, nil
}

// requireID validates that the identifier is not empty, and validates its
// prefix and its length.
//
// The prefix check is cheap type safety: a group id passed in place of a
// customer id is caught without going to the database at all, and the error
// says what was expected instead of "not found".
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

// normalizePaging converts the paging parameters into applicable values.
//
// If a limit of 0 is given the default is applied. A negative limit/offset and
// a limit exceeding [MaxLimit], on the other hand, ARE NOT CORRECTED, they are
// rejected: a silently clipped limit reports the page size to the client wrong
// and the paging loop reads the same records over again.
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

// checkLen validates the length bound of a text field.
func checkLen(label, value string, limit int) error {
	if len(value) > limit {
		return errors.Invalid(CodeInvalidInput,
			"%s can be at most %d bytes, %d bytes given", label, limit, len(value))
	}
	return nil
}

// requireText validates that a text field is filled.
func requireText(label, value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.Invalid(CodeInvalidInput, "%s cannot be empty", label)
	}
	return nil
}

// validatePerson validates the length bounds of the person fields.
func validatePerson(firstName, lastName, phone string) error {
	if err := checkLen("first name", firstName, models.MaxNameLen); err != nil {
		return err
	}
	if err := checkLen("last name", lastName, models.MaxNameLen); err != nil {
		return err
	}
	return checkLen("phone", phone, models.MaxPhoneLen)
}

// validatePatchPerson validates the person fields of a partial update.
//
// nil fields are skipped: the distinction between "do not touch" and "write
// empty" is kept, and no length error is produced for a field that was not
// given.
func validatePatchPerson(patch models.CustomerPatch) error {
	if patch.FirstName != nil {
		if err := checkLen("first name", *patch.FirstName, models.MaxNameLen); err != nil {
			return err
		}
	}
	if patch.LastName != nil {
		if err := checkLen("last name", *patch.LastName, models.MaxNameLen); err != nil {
			return err
		}
	}
	if patch.Phone != nil {
		if err := checkLen("phone", *patch.Phone, models.MaxPhoneLen); err != nil {
			return err
		}
	}
	return nil
}
