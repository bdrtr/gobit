package service

import (
	"math"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/condition"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// currencyCodeLen is the length of an ISO 4217 alphabetic code.
const currencyCodeLen = 3

// maxIDLen is the upper bound on the length of an accepted identifier. Because
// identifiers also enter the unique index on the link table, the bound is kept
// consistent with it.
const maxIDLen = 255

// Coupon code bounds.
//
// The code is the name the customer types and the operator refers to; so it
// has to be short enough for a person to type and long enough to mean
// something. The allowed characters are letters, digits, hyphen and underscore:
// whitespace and punctuation are the one thing that silently breaks a code
// while it is read out over the phone or pasted into an email.
const (
	// MinCodeLen is the shortest length of a coupon code.
	MinCodeLen = 3
	// MaxCodeLen is the longest length of a coupon code.
	MaxCodeLen = 64
	// MaxCodesPerCompute is the maximum number of coupons a single computation
	// can be given.
	//
	// The bound has to exist: every code enters a database query and a rule
	// evaluation, and an unbounded list would keep the computation busy with a
	// single request.
	MaxCodesPerCompute = 20
)

// Text field bounds.
const (
	// MaxNameLen is the maximum length of a campaign name.
	MaxNameLen = 255
	// MaxDescriptionLen is the maximum length of a description.
	MaxDescriptionLen = 2000
	// MaxIdentifierLen is the maximum length of a campaign's business identifier.
	MaxIdentifierLen = 128
	// MaxReferenceLen is the maximum length of a usage reference.
	MaxReferenceLen = 255
	// MaxAttributeLen is the maximum length of a rule's field name.
	MaxAttributeLen = 128
	// MaxRuleValues is the maximum number of values a rule can have.
	MaxRuleValues = 100
	// MaxMetadataKeys is the maximum number of metadata keys.
	MaxMetadataKeys = 64
	// MaxMetadataValueLen is the maximum length of a metadata value.
	MaxMetadataValueLen = 512
)

// normalizeCode validates a coupon code and converts it to UPPER case.
//
// Converting to upper case is a STORAGE decision: coupon codes must not be
// case-sensitive — the customer who types "summer20" and the one who types
// "SUMMER20" use the same coupon. Had the case been kept, two codes could differ
// by case alone and the customer would never realize they got the wrong one.
func normalizeCode(code string) (string, error) {
	trimmed := strings.ToUpper(strings.TrimSpace(code))
	if len(trimmed) < MinCodeLen {
		return "", errors.Invalid(CodeInvalidInput,
			"coupon code has to be at least %d characters, %q given", MinCodeLen, code)
	}
	if len(trimmed) > MaxCodeLen {
		return "", errors.Invalid(CodeInvalidInput,
			"coupon code can be at most %d characters, %d characters given", MaxCodeLen, len(trimmed))
	}
	for _, r := range trimmed {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return "", errors.Invalid(CodeInvalidInput,
				"coupon code can only contain letters, digits, hyphens and underscores, %q given", code)
		}
	}
	return trimmed, nil
}

// normalizeCurrency validates a currency code and converts it to UPPER case.
//
// The accepted form is the ISO 4217 alphabetic code: exactly three letters.
func normalizeCurrency(code string) (string, error) {
	trimmed := strings.ToUpper(strings.TrimSpace(code))
	if len(trimmed) != currencyCodeLen {
		return "", errors.Invalid(CodeInvalidInput,
			"currency code has to be exactly %d letters (ISO 4217), %q given", currencyCodeLen, code)
	}
	for _, r := range trimmed {
		if r < 'A' || r > 'Z' {
			return "", errors.Invalid(CodeInvalidInput,
				"currency code can only contain letters (ISO 4217), %q given", code)
		}
	}
	return trimmed, nil
}

// validateAmount validates that an amount is within the allowed range.
//
// The upper bound is overflow protection: the largest intermediate product of
// the computation is amount × [models.BasisPointDenominator] and it has to fit
// into an int64.
func validateAmount(label string, amount int64) error {
	if amount < models.MinAmount {
		return errors.Invalid(CodeInvalidInput,
			"%s cannot be negative, %d given (minor unit)", label, amount)
	}
	if amount > models.MaxAmount {
		return errors.Invalid(CodeInvalidInput,
			"%s can be at most %d (minor unit), %d given", label, models.MaxAmount, amount)
	}
	return nil
}

// validateQuantity validates that a quantity is within the allowed range.
func validateQuantity(label string, quantity int64) error {
	if quantity < models.MinQuantity {
		return errors.Invalid(CodeInvalidInput,
			"%s has to be at least %d, %d given", label, models.MinQuantity, quantity)
	}
	if quantity > models.MaxQuantity {
		return errors.Invalid(CodeInvalidInput,
			"%s can be at most %d, %d given", label, models.MaxQuantity, quantity)
	}
	return nil
}

// validateUsageLimit validates a usage limit; nil means unlimited.
func validateUsageLimit(limit *int64) error {
	if limit == nil {
		return nil
	}
	if *limit < 0 {
		return errors.Invalid(CodeInvalidInput,
			"usage limit cannot be negative, %d given", *limit)
	}
	return nil
}

// validateText validates that a text field is not empty and does not exceed its
// bound.
func validateText(label, value string, minLen, maxLen int) error {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) < minLen {
		return errors.Invalid(CodeInvalidInput, "%s cannot be empty", label)
	}
	if len(trimmed) > maxLen {
		return errors.Invalid(CodeInvalidInput,
			"%s can be at most %d bytes, %d bytes given", label, maxLen, len(trimmed))
	}
	return nil
}

// validateRuleInput validates that a rule input is consistent.
//
// The number of values depends on the operator: in/nin take many values, the
// others require a SINGLE value. The value of a numeric operator
// (gt/gte/lt/lte) has to be convertible to an integer; otherwise the rule would
// never match and would silently be a dead record.
func validateRuleInput(in RuleInput) error {
	if !in.RuleType.Valid() {
		return errors.Invalid(CodeInvalidInput, "rule type is undefined: %q", string(in.RuleType))
	}
	if err := validateText("rule field name (attribute)", in.Attribute, 1, MaxAttributeLen); err != nil {
		return err
	}
	if !in.Operator.Valid() {
		return errors.Invalid(CodeInvalidInput, "rule operator is undefined: %q", string(in.Operator))
	}
	if len(in.Values) == 0 {
		return errors.Invalid(CodeInvalidInput, "the %q rule has to contain at least one value", in.Attribute)
	}
	if len(in.Values) > MaxRuleValues {
		return errors.Invalid(CodeInvalidInput,
			"the %q rule can contain at most %d values, %d given", in.Attribute, MaxRuleValues, len(in.Values))
	}
	if !in.Operator.MultiValue() && len(in.Values) != 1 {
		return errors.Invalid(CodeInvalidInput,
			"the %q operator takes exactly one value, %d values given", string(in.Operator), len(in.Values))
	}
	for _, value := range in.Values {
		if value == "" {
			return errors.Invalid(CodeInvalidInput, "the values of the %q rule cannot be empty", in.Attribute)
		}
		if !condition.Readable(condition.Operator(in.Operator), value) {
			return errors.Invalid(CodeInvalidInput,
				"the %q operator expects an integer, %q given", string(in.Operator), value)
		}
	}
	return nil
}

// normalizeMetadata validates the metadata and returns it as a COPY.
//
// The copy is mandatory: had the caller's map been put into the model directly,
// a caller that later changed the request would have changed the written record
// as well.
func normalizeMetadata(md map[string]string) (map[string]string, error) {
	if len(md) == 0 {
		return map[string]string{}, nil
	}
	if len(md) > MaxMetadataKeys {
		return nil, errors.Invalid(CodeInvalidInput,
			"metadata can contain at most %d keys, %d given", MaxMetadataKeys, len(md))
	}

	out := make(map[string]string, len(md))
	for key, value := range md {
		if strings.TrimSpace(key) == "" {
			return nil, errors.Invalid(CodeInvalidInput, "a metadata key cannot be empty")
		}
		if len(key) > MaxAttributeLen {
			return nil, errors.Invalid(CodeInvalidInput,
				"a metadata key can be at most %d bytes, %q given", MaxAttributeLen, key)
		}
		if len(value) > MaxMetadataValueLen {
			return nil, errors.Invalid(CodeInvalidInput,
				"the %q metadata value can be at most %d bytes, %d bytes given",
				key, MaxMetadataValueLen, len(value))
		}
		out[key] = value
	}
	return out, nil
}

// requireID validates that an identifier is usable and OF THE RIGHT KIND.
//
// The prefix check is deliberate: prefixed identifiers exist so that an
// identifier of the wrong kind (e.g. a campaign id standing in for a promotion)
// comes back not as "not found" but as a validation error that says plainly
// what is wrong.
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
// If the limit is 0 or negative the default is applied, and if it exceeds
// [MaxLimit] the maximum value is applied; clamping is NOT an error, but the
// applied value is reported back in the result (see [Page]). A negative offset,
// however, is a request that cannot be corrected and is refused.
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
// The ListOptions fields of the Query layer are int; on a 64-bit platform a huge
// value coming from there would WRAP while being converted to int32 and could
// produce a negative limit. Clamping makes that wrap impossible; the bound
// itself is already brought down to [MaxLimit] in normalizePaging.
func clampToInt32(value int) int32 {
	if value > math.MaxInt32 {
		return math.MaxInt32
	}
	if value < math.MinInt32 {
		return math.MinInt32
	}
	return int32(value)
}
