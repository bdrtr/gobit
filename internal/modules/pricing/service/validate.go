package service

import (
	"math"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/condition"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// currencyCodeLen is the length of an ISO 4217 alphabetic code.
const currencyCodeLen = 3

// maxIDLen is the upper bound on the accepted id length. Because ids also go
// into the unique index of the link table, the bound is kept consistent with
// that index.
const maxIDLen = 255

// normalizeCurrency validates the currency code and converts it to UPPER case.
//
// The accepted form is the ISO 4217 alphabetic code: exactly three letters.
// Leading/trailing whitespace is trimmed (the code is already normalized by
// converting it to upper case; a separate strictness for whitespace would be
// inconsistent), but no character other than a letter is accepted.
func normalizeCurrency(code string) (string, error) {
	trimmed := strings.ToUpper(strings.TrimSpace(code))
	if len(trimmed) != currencyCodeLen {
		return "", errors.Invalid(CodeInvalidInput,
			"the currency code has to be exactly %d letters (ISO 4217), %q given", currencyCodeLen, code)
	}
	for _, r := range trimmed {
		if r < 'A' || r > 'Z' {
			return "", errors.Invalid(CodeInvalidInput,
				"the currency code can contain only letters (ISO 4217), %q given", code)
		}
	}
	return trimmed, nil
}

// validateAmount validates that the amount is in the permitted range.
//
// A negative amount is rejected: a negative price is not a discount, and
// discounts are the promotion module's job. The upper bound is overflow
// protection — the product amount × quantity has to fit in an int64 (see
// [models.MaxAmount]).
func validateAmount(amount int64) error {
	if amount < models.MinAmount {
		return errors.Invalid(CodeInvalidInput,
			"the amount cannot be negative, %d given (minor unit)", amount)
	}
	if amount > models.MaxAmount {
		return errors.Invalid(CodeInvalidInput,
			"the amount can be at most %d (minor unit), %d given", models.MaxAmount, amount)
	}
	return nil
}

// normalizeQuantityRange validates the quantity range and applies the default.
//
// A min of 0 is taken as 1: "no quantity given" and "valid at every quantity"
// are the same thing. The returned upper bound is COPIED; the caller's pointer
// is not shared.
func normalizeQuantityRange(minQty int32, maxQty *int32) (outMin int32, outMax *int32, err error) {
	if minQty == 0 {
		minQty = models.MinQuantity
	}
	if minQty < models.MinQuantity {
		return 0, nil, errors.Invalid(CodeInvalidInput,
			"the minimum quantity has to be at least %d, %d given", models.MinQuantity, minQty)
	}
	if minQty > models.MaxQuantity {
		return 0, nil, errors.Invalid(CodeInvalidInput,
			"the minimum quantity can be at most %d, %d given", models.MaxQuantity, minQty)
	}
	if maxQty == nil {
		return minQty, nil, nil
	}

	limit := *maxQty
	if limit < models.MinQuantity {
		return 0, nil, errors.Invalid(CodeInvalidInput,
			"the maximum quantity has to be at least %d, %d given", models.MinQuantity, limit)
	}
	if limit > models.MaxQuantity {
		return 0, nil, errors.Invalid(CodeInvalidInput,
			"the maximum quantity can be at most %d, %d given", models.MaxQuantity, limit)
	}
	if limit < minQty {
		return 0, nil, errors.Invalid(CodeInvalidInput,
			"the maximum quantity (%d) cannot be less than the minimum quantity (%d)", limit, minQty)
	}
	return minQty, &limit, nil
}

// validatePriceListRef validates the list id given to a price.
func validatePriceListRef(id *string) error {
	if id == nil {
		return nil
	}
	return requireID(*id, models.PriceListIDPrefix, "price list id")
}

// validateRule validates that a rule input is consistent.
//
// The number of values depends on the operator: in/nin take several values,
// the others require a SINGLE value. The value of a numeric operator
// (gt/gte/lt/lte) has to convert to an integer; otherwise the rule would never
// match and would silently be a dead record.
func validateRule(in RuleInput) error {
	if strings.TrimSpace(in.Attribute) == "" {
		return errors.Invalid(CodeInvalidInput, "the rule's field name (attribute) cannot be empty")
	}
	if !in.Operator.Valid() {
		return errors.Invalid(CodeInvalidInput,
			"the rule operator is undefined: %q", string(in.Operator))
	}
	if len(in.Values) == 0 {
		return errors.Invalid(CodeInvalidInput,
			"the %q rule has to contain at least one value", in.Attribute)
	}
	if !in.Operator.MultiValue() && len(in.Values) != 1 {
		return errors.Invalid(CodeInvalidInput,
			"the %q operator takes exactly one value, %d values given", string(in.Operator), len(in.Values))
	}
	for _, value := range in.Values {
		if value == "" {
			return errors.Invalid(CodeInvalidInput,
				"the values of the %q rule cannot be empty", in.Attribute)
		}
		if !condition.Readable(condition.Operator(in.Operator), value) {
			return errors.Invalid(CodeInvalidInput,
				"the %q operator expects an integer, %q given", string(in.Operator), value)
		}
	}
	return nil
}

// refuseReservedAttribute refuses a rule a caller writes on an attribute under
// [models.ReservedAttributePrefix] (ADR 0403).
//
// The cart's metadata reaches promotions and no price, so such a rule would be
// stored, matched by the admin calculator and never charged. It is NOT part of
// [validateRule]: the writes that keep a set's other prices revalidate every
// rule they carry, and a rule written before the refusal has to ride through
// them unchanged rather than answer 422 to a panel save or an import that only
// carried it.
func refuseReservedAttribute(in RuleInput) error {
	if strings.HasPrefix(in.Attribute, models.ReservedAttributePrefix) {
		return errors.Invalid(CodeRuleAttributeReserved,
			"a price rule cannot name %q: an attribute under %q is the cart's metadata, which chooses no price",
			in.Attribute, models.ReservedAttributePrefix)
	}
	return nil
}

// refuseReservedAttributes applies [refuseReservedAttribute] to every rule of
// the prices a caller writes, with the price's and the rule's position in the
// error as [Service.buildPrices] reports them.
func refuseReservedAttributes(prices []PriceInput) error {
	for i := range prices {
		for j := range prices[i].Rules {
			if err := refuseReservedAttribute(prices[i].Rules[j]); err != nil {
				return withIndex(withIndex(err, detailRuleIndex, j), detailIndex, i)
			}
		}
	}
	return nil
}

// requireID validates that an id is usable and OF THE RIGHT TYPE.
//
// The prefix check is deliberate: prefixed ids exist so that an id of the wrong
// type (e.g. a variant id passed in place of a price set id) comes back not as
// "not found" but as a validation error that says what it is.
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
// [MaxLimit] the maximum is applied; clamping is NOT an error, but the applied
// value is reported back in the result (see [Page]). A negative offset, on the
// other hand, is a request that cannot be corrected, and it is rejected.
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
// The [query.ListOptions] fields of the Query layer are int; on a 64-bit
// platform a huge value coming from there would WRAP on its conversion to int32
// and could produce a negative limit. Clamping makes that wrap impossible; the
// bound itself is lowered to [MaxLimit] in normalizePaging anyway.
func clampToInt32(value int) int32 {
	if value > math.MaxInt32 {
		return math.MaxInt32
	}
	if value < math.MinInt32 {
		return math.MinInt32
	}
	return int32(value)
}

// The index keys in an error's details.
const (
	// detailIndex reports which PRICE was rejected.
	detailIndex = "index"
	// detailRuleIndex reports which RULE of that price was rejected.
	detailRuleIndex = "rule_index"
)

// withIndex adds to a validation error the position of the input it arose at.
//
// In a bulk write (SetPrices), knowing which price was rejected is the only
// piece of information that makes the error usable.
//
// The key comes from the caller because the indexes are NESTED: a rule error
// carries the position of both the price and the rule. If the two levels used
// the same key, [errors.Error.WithDetails] would OVERWRITE it and the outer
// price index would wipe out the inner rule index; for "prices[0].rules[3] is
// invalid" the client would see only index=0 and look for the error in the
// price itself.
func withIndex(err error, key string, index int) error {
	var typed *errors.Error
	if errors.As(err, &typed) && typed != nil {
		return typed.WithDetails(map[string]any{key: index})
	}
	return err
}
