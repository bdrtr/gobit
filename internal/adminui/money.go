package adminui

// Money as the panel PRINTS it: an amount is an integer in minor units, and
// turning it into something a human reads needs the currency's scale. Three
// screens need that — the product detail, the variant edit form and the sales
// report — so the scale read, the formatting and the price view are lifted out
// of any one of them. Keeping them here is also what keeps the "never guess a
// scale, never divide in floating point" rule in ONE place rather than in each
// screen that prints a number.

import (
	"context"
	"strconv"
	"strings"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// priceView is one price of a variant, ready to print.
type priceView struct {
	// Amount is the formatted amount, or the raw minor-unit integer when the
	// currency's scale is unknown.
	Amount string
	// Currency is the ISO 4217 code.
	Currency string
	// Minor reports that Amount is a RAW minor-unit integer rather than a
	// scaled amount. The template says so next to the number; see
	// [formatAmount] for why an unknown scale is never guessed.
	Minor bool
}

// currencyScales maps a currency code to its number of decimal digits.
//
// # Why the panel reads regions to print a price
//
// An amount is an INTEGER in minor units. Turning it into something a human
// reads needs the currency's scale, and that scale is NOT two for every
// currency: ISO 4217 has 0-digit currencies (JPY, KRW), 2-digit ones (most) and
// 3-digit ones (KWD, BHD). A presentation layer that assumed 100 would show the
// wrong amount for two of the three classes — and show it confidently.
//
// The scale lives in the region module's currency table and reaches the panel
// through the same read layer as everything else. When it cannot be read the
// map comes back EMPTY rather than defaulting to two: [formatAmount] then
// prints the raw minor-unit integer and says so. A missing region module
// degrades the display, it does not corrupt it.
func (u *UI) currencyScales(ctx context.Context) map[string]int {
	scales := map[string]int{}

	regions, err := u.catalog.Graph(ctx, query.GraphSpec{
		Entity: EntityRegion,
		Fields: []string{fieldID, fieldCurrencyCod, fieldCurrency},
	})
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx,
			"the panel could not read the currency scales; amounts will be shown in minor units",
			"error", err)

		return scales
	}

	for _, rec := range regions {
		currency, ok := rec[fieldCurrency].(map[string]any)
		if !ok {
			continue
		}
		code := stringValue(currency[fieldCurrencyCod])
		if code == "" {
			code = stringValue(currency["code"])
		}
		digits, ok := intValue(currency[fieldDecimals])
		if code == "" || !ok {
			continue
		}
		scales[strings.ToUpper(code)] = digits
	}

	return scales
}

// pricesOf reads a variant's prices out of the price-set expansion.
func pricesOf(rec query.Record, scales map[string]int) []priceView {
	set, ok := rec[keyPriceSet].(query.Record)
	if !ok {
		return nil
	}

	raw, ok := set[fieldPrices].([]map[string]any)
	if !ok {
		return nil
	}

	out := make([]priceView, 0, len(raw))
	for _, price := range raw {
		amount, ok := intValue(price[fieldAmount])
		if !ok {
			continue
		}
		code := strings.ToUpper(stringValue(price[fieldCurrencyCod]))
		text, exact := formatAmount(int64(amount), code, scales)
		out = append(out, priceView{Amount: text, Currency: code, Minor: !exact})
	}

	return out
}

// formatAmount turns a minor-unit integer into a readable amount.
//
// The second result reports whether the currency's scale was KNOWN. When it was
// not, the raw integer is returned unchanged and the caller marks it as minor
// units; guessing two digits would print 1000 JPY as "10.00" and 1000 KWD as
// "10.00" while the right answers are "1000" and "1.000".
//
// The arithmetic stays in integers throughout. Dividing by a power of ten in
// floating point is exactly the operation plan Section 8 forbids for money: at
// large amounts the result is no longer the number that was stored.
func formatAmount(minor int64, code string, scales map[string]int) (string, bool) {
	digits, ok := scales[code]
	if !ok || digits < 0 {
		return strconv.FormatInt(minor, 10), false
	}
	if digits == 0 {
		return strconv.FormatInt(minor, 10), true
	}

	negative := minor < 0
	if negative {
		minor = -minor
	}

	text := strconv.FormatInt(minor, 10)
	if len(text) <= digits {
		text = strings.Repeat("0", digits-len(text)+1) + text
	}

	split := len(text) - digits
	out := text[:split] + "." + text[split:]
	if negative {
		out = "-" + out
	}

	return out, true
}
