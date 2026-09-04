package service

import (
	"slices"
	"strings"
	"unicode"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// maxIDLen is the upper bound of the accepted identifier length. Since
// identifiers also go into the unique index on the link table, the bound is
// kept in line with that one.
const maxIDLen = 255

// normalizeEmail validates the email address and converts it into its storage
// form.
//
// The validation is DELIBERATELY narrow: instead of writing a full RFC 5322
// parser, only "there is a single @, both sides are filled, the domain contains
// a dot, there is no whitespace" is checked. A stricter pattern would reject
// valid but unusual addresses (with a plus sign, with a dash, with a long TLD)
// and would leave the user unable to open an account; a looser pattern, on the
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
	// either end: the difference between "a@b" and "a@b." is that the second one
	// can never be delivered to.
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", errors.Invalid(CodeInvalidInput,
			"the email domain is invalid, %q given", email)
	}
	return normalized, nil
}

// requireID validates that the identifier is not empty, and validates its
// prefix and its length.
//
// The prefix check is cheap type safety: a channel identifier passed where a
// user identifier was expected is caught without going to the database at all,
// and the error says what was expected instead of "not found".
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

// requireText validates that a text field is filled in.
func requireText(label, value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.Invalid(CodeInvalidInput, "%s cannot be empty", label)
	}
	return nil
}

// normalizeScopes validates the scope list, trims it and eliminates duplicates.
//
// A nil slice means "no scopes were given" and is returned as it is; the caller
// turns that into the default. An empty name inside a non-empty slice, on the
// other hand, is REJECTED: the CHECK constraint in the database says the same
// thing, and there is no point in the client getting caught by it and seeing a
// meaningless constraint error.
func normalizeScopes(scopes []string) ([]string, error) {
	if scopes == nil {
		return nil, nil
	}
	if len(scopes) > models.MaxScopeCount {
		return nil, errors.Invalid(CodeInvalidInput,
			"at most %d scopes can be given, %d given", models.MaxScopeCount, len(scopes))
	}

	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		trimmed := strings.TrimSpace(scope)
		if trimmed == "" {
			return nil, errors.Invalid(CodeInvalidInput, "a scope name cannot be empty")
		}
		if len(trimmed) > models.MaxScopeLen {
			return nil, errors.Invalid(CodeInvalidInput,
				"a scope name can be at most %d bytes, %q given", models.MaxScopeLen, trimmed)
		}
		if !slices.Contains(out, trimmed) {
			out = append(out, trimmed)
		}
	}
	return out, nil
}

// The bounds of the password policy.
//
// The policy deliberately rests on LENGTH, not on composition: NIST SP 800-63B
// says that rules of the "at least one upper case letter, one digit, one
// symbol" kind push the user into predictable patterns ("Password1!") and do
// not increase the real entropy. Length, on the other hand, grows the search
// space directly.
const (
	// MinPasswordLen is the shortest accepted password length (in bytes).
	//
	// 12 was chosen for the admin user: this account administers the whole store
	// and it is the account whose single leak costs the most.
	MinPasswordLen = 12
	// MaxPasswordLen is the longest accepted password length (in bytes).
	//
	// 72 is the largest key length the bcrypt algorithm processes. The bound is
	// enforced EXPLICITLY: bcrypt.GenerateFromPassword rejects anything longer
	// and, had that error gone to the client in its raw form, the user would not
	// understand why their password was not accepted. CLIPPING it silently would
	// be worse still — the first 72 characters of an 80-character password would
	// be taken for the whole of it.
	MaxPasswordLen = 72
)

// validatePassword enforces the password policy.
//
// THE PASSWORD DOES NOT APPEAR IN THE ERROR MESSAGE: only its length is
// reported. Error messages fall into the log and one day get copied into a
// support ticket; the password itself must never set out on that journey.
func validatePassword(password string) error {
	if len(password) < MinPasswordLen {
		return errors.Invalid(CodeWeakPassword,
			"the password has to be at least %d characters, %d characters given", MinPasswordLen, len(password))
	}
	if len(password) > MaxPasswordLen {
		return errors.Invalid(CodeWeakPassword,
			"the password can be at most %d bytes (the bcrypt limit), %d bytes given",
			MaxPasswordLen, len(password))
	}
	if strings.TrimSpace(password) == "" {
		return errors.Invalid(CodeWeakPassword, "the password cannot consist only of whitespace")
	}
	return nil
}
