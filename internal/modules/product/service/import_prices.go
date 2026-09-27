package service

import (
	"context"
	"strconv"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
)

// PriceSetWriter is the NARROW surface an import's price columns are written
// through (ADR 0207).
//
// Pricing owns prices and this module cannot import it (Principle 2.4), so the
// surface is declared here and satisfied STRUCTURALLY by pricing's service,
// resolved by name; internal/arch pins the two together. The link from a
// variant to its price set is this module's, and pricing never sees it.
type PriceSetWriter interface {
	// CreateEmptyPriceSet creates a price set with no prices and returns its id.
	CreateEmptyPriceSet(ctx context.Context) (string, error)
	// SetUnitBasePrices sets the base price at one unit in each currency given,
	// leaves every other price as it is, and reports whether it wrote.
	SetUnitBasePrices(ctx context.Context, priceSetID string, amountsByCurrency map[string]int64) (bool, error)
}

// ImportPrices is what the service holds for an import's prices: pricing's
// surface, and whether this installation has one.
//
// gobit's modules are chosen one by one (ADR 0025), so pricing may be absent.
// The import then refuses a file with price columns when it is sent, rather
// than refusing each of its rows when they are applied.
type ImportPrices interface {
	PriceSetWriter
	// Installed answers nil when pricing's surface is there, and an error
	// saying why not otherwise.
	Installed(ctx context.Context) error
}

// codeImportPricesAbsent refuses price columns in an installation without
// pricing.
const codeImportPricesAbsent = "product_import_prices_unavailable"

// ImportNamesPrices reports whether a file's header names a price column. The
// API asks it before the file is kept, because such a file needs the right to
// write prices as well as products.
func ImportNamesPrices(file []byte) bool {
	header, err := importHeader(file)
	if err != nil {
		return false
	}

	return len(priceColumns(header)) > 0
}

// priceColumns returns the header's price columns.
func priceColumns(header []string) []string {
	var out []string
	for _, column := range header {
		if priceColumn.MatchString(column) {
			out = append(out, column)
		}
	}

	return out
}

// checkImportPrices refuses a file with price columns in an installation that
// cannot write them.
func (s *Service) checkImportPrices(ctx context.Context, header []string) error {
	if len(priceColumns(header)) == 0 {
		return nil
	}
	if s.prices == nil {
		return errors.Invalid(codeImportPricesAbsent,
			"the file has price columns and this installation writes no prices; leave the columns out")
	}
	if err := s.prices.Installed(ctx); err != nil {
		if errors.HasKind(err, errors.KindUnavailable) {
			return errors.Wrap(err, errors.KindInvalid, codeImportPricesAbsent,
				"the file has price columns and this installation writes no prices; leave the columns out")
		}

		return err
	}

	return nil
}

// prices reads the row's non-empty price cells as minor units by currency.
func (r importRow) prices() (map[string]int64, error) {
	var out map[string]int64
	for _, column := range priceColumns(r.header) {
		value := r.text(column)
		if value == "" {
			continue
		}
		amount, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, errors.Invalid(codeImportInvalid,
				"%s is not a whole number of minor units: %q", column, value)
		}
		if out == nil {
			out = map[string]int64{}
		}
		out[strings.ToUpper(strings.TrimPrefix(column, "variant_price_"))] = amount
	}

	return out, nil
}

// importPrices writes a variant's prices, giving it a price set when it has
// none, and reports whether it wrote.
//
// The three steps are not one transaction and cannot be: the set is pricing's
// and the link is this module's. A run stopped after the set is created and
// before it is linked leaves an empty set nothing names, and the row, applied
// again, creates another; one stopped after the link finds the set and writes
// the amounts.
func (s *Service) importPrices(ctx context.Context, variantID string, amounts map[string]int64) (bool, error) {
	if len(amounts) == 0 {
		return false, nil
	}
	if s.prices == nil {
		return false, errors.Unavailable(codeImportPricesAbsent, "this installation writes no prices")
	}
	if s.links == nil {
		return false, s.linkerMissing()
	}

	setID, err := s.firstLink(ctx, LinkVariantPriceSet, variantID)
	if err != nil {
		return false, err
	}
	if setID == nil {
		created, err := s.prices.CreateEmptyPriceSet(ctx)
		if err != nil {
			return false, err
		}
		if err := s.SetVariantPriceSet(ctx, variantID, created); err != nil {
			return false, err
		}
		setID = &created
	}

	// A new set is empty, so the amounts written to it are always a change.
	return s.prices.SetUnitBasePrices(ctx, *setID, amounts)
}
