package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
)

const (
	// MaxDocumentedTax is the most documents one read of [Service.DocumentedTax]
	// returns, the order journal's ceiling; a window holding more is refused
	// rather than cut (ADR 0419).
	MaxDocumentedTax = 10000
	// MaxDocumentedTaxWindow is the widest window one read covers, the order
	// journal's quarter.
	MaxDocumentedTaxWindow = 93 * 24 * time.Hour
)

// DocumentedTax returns, without their lines, the amending documents that name
// an act and were issued or voided inside [from, to), in one currency when
// currencyCode is set (ADR 0419).
//
// The order journal books each one's tax at its issued_at and takes it back at
// its voided_at. A document issued in the window is returned whatever happened
// to it later, so a window reads the same after a later voiding; a document
// naming no act is not returned, since no act's entry is there to correct.
func (s *Service) DocumentedTax(
	ctx context.Context, from, to time.Time, currencyCode string,
) ([]models.Invoice, error) {
	if from.IsZero() || to.IsZero() || !from.Before(to) || to.Sub(from) > MaxDocumentedTaxWindow {
		return nil, errors.Invalid(CodeInvalidInput,
			"the documents that name an act are read over a window of at most %d days that ends after it begins",
			int(MaxDocumentedTaxWindow/(24*time.Hour)))
	}
	currency := strings.ToUpper(strings.TrimSpace(currencyCode))

	documents, err := s.repo.DocumentedTax(ctx, from.UTC(), to.UTC(), currency, MaxDocumentedTax+1)
	if err != nil {
		return nil, err
	}
	if len(documents) > MaxDocumentedTax {
		return nil, errors.Invalid(CodeInvalidInput,
			"the window holds more than %d documents that name an act; ask for a narrower one", MaxDocumentedTax)
	}

	return documents, nil
}

// documentedTaxJSON is one document of [Interop.DocumentedTaxJSON].
type documentedTaxJSON struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"`
	AmendmentKey string     `json:"amendment_key"`
	TaxTotal     int64      `json:"tax_total"`
	CurrencyCode string     `json:"currency_code"`
	IssuedAt     time.Time  `json:"issued_at"`
	VoidedAt     *time.Time `json:"voided_at"`
}

// DocumentedTaxJSON returns the amending documents that name an act and were
// issued or voided inside [from, to), as a JSON array of {id, kind,
// amendment_key, tax_total, currency_code, issued_at, voided_at} (ADR 0419).
//
// The order module reads it to move each document's tax between tax_payable
// and the account its act was booked to: the key is "<journal kind>:<act id>",
// as the invoicing flow wrote it (ADR 0406).
func (i *Interop) DocumentedTaxJSON(
	ctx context.Context, from, to time.Time, currencyCode string,
) (json.RawMessage, error) {
	documents, err := i.svc.DocumentedTax(ctx, from, to, currencyCode)
	if err != nil {
		return nil, err
	}

	out := make([]documentedTaxJSON, 0, len(documents))
	for k := range documents {
		document := &documents[k]
		out = append(out, documentedTaxJSON{
			ID: document.ID, Kind: document.Kind.String(), AmendmentKey: document.AmendmentKey,
			TaxTotal: document.TaxTotal, CurrencyCode: document.CurrencyCode,
			IssuedAt: document.IssuedAt.UTC(), VoidedAt: document.VoidedAt,
		})
	}

	return json.Marshal(out)
}
