package manual

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// currencyCodeLength is the number of letters in an ISO 4217 code.
const currencyCodeLength = 3

// shipmentData holds the Data fields that steer the provider's behavior.
//
// Unrecognized fields are IGNORED: Data is the free-form data the caller hands
// to the provider (address, branch, item list) and a field the provider does
// not understand is not an error.
//
// The amount fields are POINTERS: the distinction between a component with a
// zero amount and "the field was never given at all" has to be preserved. With
// a value type the two would be indistinguishable and an explicit zero (free)
// price could not be given through [DataKeyQuoteAmount].
type shipmentData struct {
	Outcome           string `json:"manual_outcome"`
	QuoteAmount       *int64 `json:"manual_quote_amount"`
	BaseAmount        *int64 `json:"manual_base_amount"`
	PerItemAmount     *int64 `json:"manual_per_item_amount"`
	PerKilogramAmount *int64 `json:"manual_per_kilogram_amount"`
	TrackingNumber    string `json:"manual_tracking_number"`
	TrackingURL       string `json:"manual_tracking_url"`
}

// parseData resolves the behavior and price keys out of the free-form data.
//
// The input is a map (the Data field of the core contract); it is encoded to
// JSON and decoded back. The intermediate step lets the same parsing run both
// from the map and from the raw body stored in the ledger, and keeps the two
// paths from drifting apart.
func parseData(data map[string]any) (shipmentData, error) {
	if len(data) == 0 {
		return shipmentData{}, nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return shipmentData{}, errors.Wrap(err, errors.KindInvalid, CodeDataInvalid,
			"shipment data could not be encoded")
	}
	return parseRawData(raw)
}

// parseRawData resolves the behavior and price keys out of a raw JSON body.
func parseRawData(raw []byte) (shipmentData, error) {
	var out shipmentData
	if len(raw) == 0 || string(raw) == "null" {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return shipmentData{}, errors.Wrap(err, errors.KindInvalid, CodeDataInvalid,
			"shipment data could not be parsed")
	}

	out.Outcome = strings.TrimSpace(out.Outcome)
	switch out.Outcome {
	case "", OutcomeOK, OutcomeError:
	default:
		return shipmentData{}, errors.Invalid(CodeInvalidInput,
			"%q is not a recognized %s value; it must be %q or %q",
			out.Outcome, DataKeyOutcome, OutcomeOK, OutcomeError)
	}

	for _, field := range []struct {
		key   string
		value *int64
	}{
		{DataKeyQuoteAmount, out.QuoteAmount},
		{DataKeyBaseAmount, out.BaseAmount},
		{DataKeyPerItemAmount, out.PerItemAmount},
		{DataKeyPerKilogramAmount, out.PerKilogramAmount},
	} {
		if field.value == nil {
			continue
		}
		if *field.value < models.MinAmount || *field.value > models.MaxAmount {
			return shipmentData{}, errors.Invalid(CodeInvalidInput,
				"%s must be between %d and %d: %d",
				field.key, models.MinAmount, models.MaxAmount, *field.value)
		}
	}
	return out, nil
}

// quoteAmount computes the fee from the price components.
//
// It is a PURE function: it touches neither the database, nor the clock, nor
// logging. That is why every branch of the formula can be exercised one by one
// (see the [Provider.Quote] godoc).
//
// The computation uses INTEGER arithmetic and every step is checked for
// OVERFLOW. The check is required: the item count and the weight come from
// outside, and an overflowing product would silently produce a NEGATIVE
// shipping fee — that is, an order that pays money to the customer.
//
// The check also covers the INTERMEDIATE STEPS: had the rounding to kilograms
// added first and divided afterwards, (totalWeight + 999) would OVERFLOW and
// the resulting NEGATIVE kilogram count would slip through [mulChecked]'s
// check, which only looks at the positive operand, and turn the result
// negative. That is why the rounding is written without an overflow (divide
// first, add one if there is a remainder) and both helpers reject a negative
// operand EXPLICITLY.
//
// If the result is not between [models.MinAmount] and [models.MaxAmount] an
// error is returned; the lower-bound check is required too, because a check
// that only looked at the upper bound would silently let a negative total
// through.
func quoteAmount(config shipmentData, itemCount, totalWeight int64) (int64, error) {
	if config.QuoteAmount != nil {
		return *config.QuoteAmount, nil
	}

	total := valueOrZero(config.BaseAmount)

	perItem := valueOrZero(config.PerItemAmount)
	if perItem > 0 && itemCount > 0 {
		part, err := mulChecked(perItem, itemCount, DataKeyPerItemAmount)
		if err != nil {
			return 0, err
		}
		total, err = addChecked(total, part)
		if err != nil {
			return 0, err
		}
	}

	perKilogram := valueOrZero(config.PerKilogramAmount)
	if perKilogram > 0 && totalWeight > 0 {
		// Every started kilogram is charged; the direction is UP and the
		// rationale is in the [Provider.Quote] godoc. The rounding DIVIDES
		// first and adds one if there is a remainder: the
		// (totalWeight + gramsPerKilogram - 1) form would OVERFLOW when the
		// weight is near the top of int64 and would produce a negative
		// kilogram count.
		kilograms := totalWeight / gramsPerKilogram
		if totalWeight%gramsPerKilogram != 0 {
			kilograms++
		}
		part, err := mulChecked(perKilogram, kilograms, DataKeyPerKilogramAmount)
		if err != nil {
			return 0, err
		}
		total, err = addChecked(total, part)
		if err != nil {
			return 0, err
		}
	}

	if total < models.MinAmount || total > models.MaxAmount {
		return 0, errors.Invalid(CodeInvalidInput,
			"the computed shipping fee must be between %d and %d: %d",
			models.MinAmount, models.MaxAmount, total)
	}
	return total, nil
}

// valueOrZero turns a pointer amount into a value; nil is zero.
func valueOrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

// mulChecked multiplies two NON-NEGATIVE integers while checking for overflow.
//
// A negative operand is rejected EXPLICITLY. Had the check only looked at
// "b > MaxInt64/a", a negative b would pass it unconditionally and the product
// would produce a negative fee; that would mean the code itself did not enforce
// the godoc's "two positive integers" assumption.
func mulChecked(a, b int64, label string) (int64, error) {
	if a < 0 || b < 0 {
		return 0, errors.Invalid(CodeInvalidInput,
			"neither side of the %s product may be negative: %d x %d", label, a, b)
	}
	if a != 0 && b > math.MaxInt64/a {
		return 0, errors.Invalid(CodeInvalidInput,
			"the %s product overflows: %d x %d", label, a, b)
	}
	return a * b, nil
}

// addChecked adds two NON-NEGATIVE integers while checking for overflow.
//
// A negative operand is rejected EXPLICITLY; the rationale is the same as for
// [mulChecked].
func addChecked(a, b int64) (int64, error) {
	if a < 0 || b < 0 {
		return 0, errors.Invalid(CodeInvalidInput,
			"neither side of the shipping fee sum may be negative: %d + %d", a, b)
	}
	if b > math.MaxInt64-a {
		return 0, errors.Invalid(CodeInvalidInput,
			"the shipping fee sum overflows: %d + %d", a, b)
	}
	return a + b, nil
}
