package service

import (
	"context"
	"strings"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
)

// LineInput is one row of a document being issued.
type LineInput struct {
	// Description is what is printed on the row.
	Description string
	// Quantity is the count sold; it has to be positive.
	Quantity int64
	// UnitPrice is the unit price (minor unit).
	UnitPrice int64
	// Subtotal is UnitPrice x Quantity.
	Subtotal int64
	// DiscountTotal is the discount on the row, carried POSITIVE.
	DiscountTotal int64
	// TaxRateBps is the rate the row was taxed at, in BASIS POINTS.
	TaxRateBps int32
	// TaxTotal is the tax on the row.
	TaxTotal int64
	// Total is Subtotal - DiscountTotal + TaxTotal.
	Total int64
}

// IssueInput is the request to bring a document into being.
//
// # Why the totals are sent AND recomputed
//
// The service adds the lines up itself and REFUSES a request whose totals do
// not match. Deriving them silently would accept a caller that lost a line —
// the document would add up and be wrong. Trusting them would accept a document
// that does not add up at all. Checking is the only one of the three that
// catches the mistake that actually happens: an assembler that dropped a row.
type IssueInput struct {
	// SeriesPrefix is the letters of the series to take the number from.
	SeriesPrefix string
	// Kind is what the document is for.
	Kind models.Kind
	// CurrencyCode is the currency of every amount on it (ISO 4217).
	CurrencyCode string
	// Seller and Buyer are the two sides, as they are to be printed.
	Seller models.Party
	Buyer  models.Party
	// Lines are the rows, in printed order.
	Lines []LineInput
	// Subtotal, DiscountTotal, TaxTotal and Total are what the caller believes
	// the document adds up to.
	Subtotal      int64
	DiscountTotal int64
	TaxTotal      int64
	Total         int64
	// Metadata is free structured context.
	Metadata map[string]any
}

// Issue brings a document into being and gives it its number.
//
// # The one guarantee
//
// Within a series the numbers run 1, 2, 3 with nothing missing and nothing
// repeated. Everything about the implementation follows from that:
//
//   - The number is taken by an UPDATE inside the SAME transaction that writes
//     the document. A failure anywhere after the number is taken rolls the
//     increment back with it, so a failed issue leaves no hole.
//   - There is no draft. A draft would need a number to be a draft OF anything,
//     and a number given to a draft that is then abandoned is exactly the hole
//     the series may not have.
//   - A canceled document keeps its number and stays in the table. Deleting it
//     would put the hole in from the other end.
//
// # Why a gap matters at all
//
// It is a legal question, not a technical one. A tax authority reading a series
// that jumps from 41 to 43 sees a document that was issued and then made to
// disappear, and the shop has to prove otherwise. That is the whole reason this
// module does not use a database sequence, which the order module does use for
// its order numbers — a sequence advances outside the transaction and a
// rollback burns its value. For an order number the hole is harmless; here it
// is the thing being prevented.
//
// # The series row is created on demand
//
// A series exists per prefix and per YEAR, and the year rolls over at midnight
// on the first of January. Requiring a human to open the new year's series
// would mean the first sale of the year fails, at the hour when nobody is
// watching. The prefix, on the other hand, is not user input — it comes from
// the installation's configuration — so a typo is a mistake made once rather
// than per document, and a series opened by one is visible in
// [Service.ListSeries] with its numbering starting at 1.
func (s *Service) Issue(ctx context.Context, in IssueInput) (models.Invoice, error) {
	if err := in.validate(); err != nil {
		return models.Invoice{}, err
	}

	now := s.now().UTC()

	var issued models.Invoice

	year := yearOf(now)
	if year == 0 {
		return models.Invoice{}, errors.Internal(CodeNumbering,
			"the clock reports a year the number format cannot carry: %d", now.Year())
	}

	err := s.repo.WithTx(ctx, func(ctx context.Context) error {
		// Opening the series and taking the number are ONE statement, so a year
		// whose first two documents are issued at the same moment cannot make
		// one of them fail: see the TakeNextNumber query for why a
		// look-then-create arrangement cannot recover from its own race.
		series, err := s.repo.TakeNextNumber(ctx, in.SeriesPrefix, year)
		if err != nil {
			return err
		}

		if series.LastNumber > maxSequence {
			// The transaction rolls back, so the number is NOT spent: the
			// series stops at its ceiling rather than rolling over into a
			// number that would repeat one from earlier in the year.
			return errors.Conflict(CodeNumbering,
				"series %s%d has reached its ceiling of %d documents for the year",
				series.Prefix, series.Year, maxSequence)
		}

		issued, err = s.repo.CreateInvoice(ctx, in.document(series, series.LastNumber, now))

		return err
	})
	if err != nil {
		return models.Invoice{}, err
	}

	return issued, nil
}

// document builds the record from the request and the number it was given.
func (in IssueInput) document(series models.Series, sequence int64, now time.Time) models.Invoice {
	doc := models.Invoice{
		ID:            models.NewInvoiceID(),
		Number:        FormatNumber(series.Prefix, series.Year, sequence),
		SeriesID:      series.ID,
		Kind:          in.Kind,
		Status:        models.StatusIssued,
		CurrencyCode:  in.CurrencyCode,
		Seller:        in.Seller,
		Buyer:         in.Buyer,
		Subtotal:      in.Subtotal,
		DiscountTotal: in.DiscountTotal,
		TaxTotal:      in.TaxTotal,
		Total:         in.Total,
		IssuedAt:      now,
		Metadata:      in.Metadata,
	}

	doc.Lines = make([]models.Line, 0, len(in.Lines))
	for i := range in.Lines {
		doc.Lines = append(doc.Lines, models.Line{
			ID:        models.NewLineID(),
			InvoiceID: doc.ID,
			// The printed order starts at 1, so a document's first row is
			// row 1 and not row 0.
			Position:      int32(i) + 1,
			Description:   in.Lines[i].Description,
			Quantity:      in.Lines[i].Quantity,
			UnitPrice:     in.Lines[i].UnitPrice,
			Subtotal:      in.Lines[i].Subtotal,
			DiscountTotal: in.Lines[i].DiscountTotal,
			TaxRateBps:    in.Lines[i].TaxRateBps,
			TaxTotal:      in.Lines[i].TaxTotal,
			Total:         in.Lines[i].Total,
		})
	}

	return doc
}

// validate refuses a request that could not produce a printable document.
func (in IssueInput) validate() error {
	if err := validatePrefix(in.SeriesPrefix); err != nil {
		return err
	}
	if !in.Kind.Valid() {
		return errors.Invalid(CodeInvalidInput, "unknown invoice kind: %q", in.Kind)
	}
	if len(in.CurrencyCode) != 3 {
		return errors.Invalid(CodeInvalidInput,
			"the currency code has to be the 3-letter ISO 4217 code: %q", in.CurrencyCode)
	}
	if strings.TrimSpace(in.Seller.Name) == "" {
		return errors.Invalid(CodeInvalidInput, "the seller's name is required")
	}
	if strings.TrimSpace(in.Buyer.Name) == "" {
		return errors.Invalid(CodeInvalidInput, "the buyer's name is required")
	}
	if len(in.Lines) == 0 {
		return errors.Invalid(CodeInvalidInput, "a document with no lines cannot be issued")
	}

	return in.validateAmounts()
}

// validateAmounts checks every line and then the document against its lines.
func (in IssueInput) validateAmounts() error {
	var subtotal, discount, tax, total int64

	for i := range in.Lines {
		line := in.Lines[i]

		switch {
		case strings.TrimSpace(line.Description) == "":
			return errors.Invalid(CodeInvalidInput, "line %d has no description", i+1)
		case line.Quantity <= 0:
			return errors.Invalid(CodeInvalidInput,
				"line %d has a quantity of %d; a document cannot carry a row of nothing",
				i+1, line.Quantity)
		case line.TaxRateBps < 0:
			return errors.Invalid(CodeInvalidInput,
				"line %d has a negative tax rate: %d", i+1, line.TaxRateBps)
		case line.DiscountTotal < 0:
			return errors.Invalid(CodeInvalidInput,
				"line %d has a negative discount; a discount is carried positive", i+1)
		case line.Total != line.Subtotal-line.DiscountTotal+line.TaxTotal:
			return errors.Invalid(CodeInvalidInput,
				"line %d does not add up: %d - %d + %d is not %d",
				i+1, line.Subtotal, line.DiscountTotal, line.TaxTotal, line.Total)
		}

		subtotal += line.Subtotal
		discount += line.DiscountTotal
		tax += line.TaxTotal
		total += line.Total
	}

	// The caller's figures are checked against the lines rather than replaced by
	// them: an assembler that lost a line would otherwise produce a document
	// that adds up perfectly and is missing a row.
	switch {
	case in.Subtotal != subtotal:
		return errors.Invalid(CodeInvalidInput,
			"the subtotal does not match the lines: given %d, the lines add to %d",
			in.Subtotal, subtotal)
	case in.DiscountTotal != discount:
		return errors.Invalid(CodeInvalidInput,
			"the discount total does not match the lines: given %d, the lines add to %d",
			in.DiscountTotal, discount)
	case in.TaxTotal != tax:
		return errors.Invalid(CodeInvalidInput,
			"the tax total does not match the lines: given %d, the lines add to %d",
			in.TaxTotal, tax)
	case in.Total != total:
		return errors.Invalid(CodeInvalidInput,
			"the total does not match the lines: given %d, the lines add to %d", in.Total, total)
	}

	return nil
}

// validatePrefix refuses a series prefix the number format could not carry.
//
// It is checked HERE rather than at transmission, because by transmission time
// the number has already been spent: a document refused for its prefix would
// leave the shop with a hole in the series it cannot fill.
//
// # What is checked and what deliberately is not
//
// The LENGTH is firm: the format reserves exactly three characters, and a
// two-character prefix produces a number of the wrong length whatever anyone
// thinks of its content.
//
// The content is checked only for what the format itself cannot carry —
// upper-case ASCII letters and digits. Refusing digits was the first version
// and it was wrong: integrators differ on whether a series code may contain
// them, the framework does not own that rule, and a framework that refused a
// prefix the shop's own integrator accepts would be wrong in a way the shop
// cannot work around. Lower case and anything outside ASCII stay refused,
// because those the format really cannot carry.
func validatePrefix(prefix string) error {
	if len(prefix) != prefixLen {
		return errors.Invalid(CodeInvalidInput,
			"the series prefix has to be exactly %d characters: %q", prefixLen, prefix)
	}

	for _, r := range prefix {
		alphanumeric := (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !alphanumeric {
			return errors.Invalid(CodeInvalidInput,
				"the series prefix has to be upper-case ASCII letters or digits: %q", prefix)
		}
	}

	return nil
}

// yearOf narrows the calendar year to the width the column carries.
//
// time.Time.Year returns an int and the column is an integer, so the conversion
// has to be written down somewhere. It is written HERE, with its bound checked,
// rather than silenced at the call site: a year outside this range means the
// clock is wrong by thousands of years, and a document dated then would be a
// worse thing to store than an error to return.
func yearOf(t time.Time) int32 {
	year := t.Year()
	if year < 1 || year > 9999 {
		// The format reserves exactly four digits for the year; anything else
		// would produce a number of the wrong length.
		return 0
	}

	return int32(year)
}
