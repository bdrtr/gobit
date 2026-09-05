package service

import (
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// currencyCodeLength is the number of letters in an ISO 4217 code.
const currencyCodeLength = 3

// maxRuleValues is the largest number of values a single rule may take.
const maxRuleValues = 100

// requireText validates a required text field.
func requireText(label, value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.Invalid(CodeInvalidInput, "%s cannot be empty", label)
	}
	return checkTextLen(label, value)
}

// checkTextLen validates the length bound of a text field.
func checkTextLen(label, value string) error {
	if len(value) > maxTextLen {
		return errors.Invalid(CodeInvalidInput,
			"%s can be at most %d bytes: %d", label, maxTextLen, len(value))
	}
	return nil
}

// requireID validates that an identifier is filled in and carries the expected
// prefix.
//
// The prefix check is cheap and catches a call made with the identifier of the
// wrong entity without going to the database at all: looking a fulfillment up
// with "sprof_..." is not NotFound but directly Invalid, and it is diagnosable.
func requireID(value, prefix, label string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return errors.Invalid(CodeInvalidInput, "%s cannot be empty", label)
	}
	if !strings.HasPrefix(trimmed, prefix) {
		return errors.Invalid(CodeInvalidInput,
			"%s has to start with the prefix %q: %q", label, prefix, value)
	}
	return checkTextLen(label, trimmed)
}

// normalizeCurrency validates the currency code and converts it to UPPER case.
//
// The code is stored upper case everywhere; otherwise "try" and "TRY" would
// behave like two different currencies and the shipping fee would silently
// diverge from the cart's currency.
func normalizeCurrency(code string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	if len(normalized) != currencyCodeLength {
		return "", errors.Invalid(CodeInvalidInput,
			"the currency has to be a three-letter ISO 4217 code: %q", code)
	}
	for _, r := range normalized {
		if r < 'A' || r > 'Z' {
			return "", errors.Invalid(CodeInvalidInput,
				"the currency can only contain letters: %q", code)
		}
	}
	return normalized, nil
}

// requireAmount validates that a shipping amount is within the allowed range.
//
// The upper bound is not arbitrary: the shipping fee is added to the order
// total and the total has to fit into an int64 (see [models.MaxAmount]). An
// unbounded amount could silently wrap to negative while being summed.
func requireAmount(label string, amount int64) error {
	if amount < models.MinAmount || amount > models.MaxAmount {
		return errors.Invalid(CodeInvalidInput,
			"%s has to be between %d and %d: %d", label, models.MinAmount, models.MaxAmount, amount)
	}
	return nil
}

// requireRange validates that a counter-like field is between zero and
// upperBound.
//
// The upper bound is not arbitrary: the count and the weight are MULTIPLIED by
// the shipping fee, and an unbounded value could overflow the product out of
// int64 and turn it into a negative fee. The bound stops here, that is, where
// the input enters the module; the provider's arithmetic has to defend itself
// independently of this check as well (see the manual package).
func requireRange(label string, value, upperBound int64) error {
	if value < 0 || value > upperBound {
		return errors.Invalid(CodeInvalidInput,
			"%s has to be between 0 and %d: %d", label, upperBound, value)
	}
	return nil
}

// normalizeProfileType validates the profile type; if it is given empty, the
// default is applied.
func normalizeProfileType(value string) (models.ProfileType, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return models.ProfileDefault, nil
	}
	profileType := models.ProfileType(trimmed)
	if !profileType.Valid() {
		return "", errors.Invalid(CodeInvalidInput,
			"%q is not a recognized shipping profile type; the valid ones are: %s, %s, %s",
			value, models.ProfileDefault, models.ProfileGiftCard, models.ProfileCustom)
	}
	return profileType, nil
}

// normalizePriceType validates the price type; if it is given empty, the
// default is applied.
func normalizePriceType(value string) (models.PriceType, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return models.PriceFlat, nil
	}
	priceType := models.PriceType(trimmed)
	if !priceType.Valid() {
		return "", errors.Invalid(CodeInvalidInput,
			"%q is not a recognized price type; the valid ones are: %s, %s",
			value, models.PriceFlat, models.PriceCalculated)
	}
	return priceType, nil
}

// normalizeStatus validates the fulfillment status.
func normalizeStatus(value string) (models.FulfillmentStatus, error) {
	status := models.FulfillmentStatus(strings.TrimSpace(value))
	if !status.Valid() {
		return "", errors.Invalid(CodeInvalidInput,
			"%q is not a recognized fulfillment status; the valid ones are: %s, %s, %s, %s",
			value, models.StatusPending, models.StatusShipped,
			models.StatusDelivered, models.StatusCanceled)
	}
	return status, nil
}

// validateRuleInput validates a rule input and returns its normalized values.
//
// The number of values is checked ACCORDING TO THE OPERATOR: only "in" and
// "nin" take more than one value. Had two values handed to an operator that
// expects a single one been swallowed silently, the second value would never be
// evaluated and the administrator would only notice that the condition they
// believed they had set is not running once the orders flowed wrong.
func validateRuleInput(attribute, operator string, values []string) (models.RuleOperator, []string, error) {
	if err := requireText("the rule field", attribute); err != nil {
		return "", nil, err
	}

	op := models.RuleOperator(strings.TrimSpace(operator))
	if !op.Valid() {
		return "", nil, errors.Invalid(CodeInvalidInput,
			"%q is not a recognized rule operator; the valid ones are: eq, ne, in, nin, gt, gte, lt, lte",
			operator)
	}

	if len(values) == 0 {
		return "", nil, errors.Invalid(CodeInvalidInput, "a rule has to contain at least one value")
	}
	if len(values) > maxRuleValues {
		return "", nil, errors.Invalid(CodeInvalidInput,
			"a rule can contain at most %d values: %d", maxRuleValues, len(values))
	}
	if !op.MultiValue() && len(values) != 1 {
		return "", nil, errors.Invalid(CodeInvalidInput,
			"the %q operator takes a single value, %d values were given", op, len(values))
	}

	out := make([]string, 0, len(values))
	for i, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return "", nil, errors.Invalid(CodeInvalidInput,
				"a rule value cannot be empty (value %d)", i+1)
		}
		if err := checkTextLen("the rule value", trimmed); err != nil {
			return "", nil, err
		}
		out = append(out, trimmed)
	}
	return op, out, nil
}
