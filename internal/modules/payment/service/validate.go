package service

import (
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// currencyCodeLength is the number of letters in an ISO 4217 code.
const currencyCodeLength = 3

// requireText validates a required text field.
func requireText(label, value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.Invalid(CodeInvalidInput, "%s cannot be empty", label)
	}
	return checkTextLen(label, value)
}

// checkTextLen validates a text field's length limit.
func checkTextLen(label, value string) error {
	if len(value) > maxTextLen {
		return errors.Invalid(CodeInvalidInput,
			"%s can be at most %d bytes: %d", label, maxTextLen, len(value))
	}
	return nil
}

// normalizeCurrency validates a currency code and converts it to UPPER case.
//
// The code is stored in upper case everywhere; otherwise "try" and "TRY" would
// behave as two different currencies and the amounts would silently diverge.
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

// requireAmount validates that an amount is within the permitted range.
//
// The upper limit is not arbitrary: the collection's held, captured and
// refunded amounts are subject to the same ceiling and their sum has to fit
// into an int64 (see [models.MaxAmount]). An unbounded amount could silently
// wrap around to negative during the summing.
func requireAmount(label string, amount int64) error {
	if amount < models.MinAmount || amount > models.MaxAmount {
		return errors.Invalid(CodeInvalidInput,
			"%s has to be between %d and %d: %d", label, models.MinAmount, models.MaxAmount, amount)
	}
	return nil
}

// invalidStatus builds the shared error for an unrecognized collection status.
func invalidStatus(value string) error {
	return errors.Invalid(CodeInvalidInput,
		"%q is not a recognized payment collection status; the valid ones are: %s",
		value, strings.Join(collectionStatusNames(), ", "))
}

// collectionStatusNames returns the valid collection statuses in a fixed order.
//
// The order is fixed on purpose and follows the payment's life cycle; an error
// message reads more easily that way than from an alphabetical or random list.
func collectionStatusNames() []string {
	return []string{
		models.CollectionNotPaid.String(),
		models.CollectionAwaiting.String(),
		models.CollectionAuthorized.String(),
		models.CollectionPartiallyCaptured.String(),
		models.CollectionCaptured.String(),
		models.CollectionPartiallyRefunded.String(),
		models.CollectionRefunded.String(),
		models.CollectionCanceled.String(),
	}
}

// requireOptionalAmount validates an amount for which zero means "not
// given".
//
// In the provider contract, too, zero means "all of it" (see
// core/provider: Capture and Refund). The service surface keeps the same
// meaning so that the caller does not have to learn two different zero rules.
func requireOptionalAmount(label string, amount int64) error {
	if amount == 0 {
		return nil
	}
	if amount < 0 {
		return errors.Invalid(CodeInvalidInput, "%s cannot be negative: %d", label, amount)
	}
	return requireAmount(label, amount)
}

// checkReference validates a refund's cause reference (ADR 0187): optional,
// bounded like the free-text fields, and without surrounding space, because it
// is a key the caller reads back rather than prose.
func checkReference(reference string) error {
	if err := checkTextLen("reference", reference); err != nil {
		return err
	}
	if reference != strings.TrimSpace(reference) {
		return errors.Invalid(CodeInvalidInput,
			"the refund reference is an id and cannot carry surrounding space: %q", reference)
	}
	return nil
}
