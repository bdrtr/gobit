package service

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
)

// codeAdminEncodeFailed reports a listing the panel's surface could not
// encode.
const codeAdminEncodeFailed = "invoice_admin_encode_failed"

// AdminSurface is the invoice module's panel surface (ADR 0335): what the
// order page offers an invoice on. Only primitives and JSON cross it, as
// across every surface the panel resolves (ADR 0001).
type AdminSurface struct {
	svc *Service
}

// NewAdminSurface builds the panel's surface over the service.
func NewAdminSurface(svc *Service) *AdminSurface { return &AdminSurface{svc: svc} }

// adminSeries is one series as the panel offers it; the json tags are the
// contract with the panel, which cannot import this package.
type adminSeries struct {
	Prefix     string `json:"prefix"`
	Year       int32  `json:"year"`
	LastNumber int64  `json:"last_number"`
}

// SeriesJSON lists the numbering series, the latest year first, each with
// the last number it handed out.
func (a *AdminSurface) SeriesJSON(ctx context.Context) (json.RawMessage, error) {
	series, err := a.svc.ListSeries(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]adminSeries, 0, len(series))
	for _, s := range series {
		out = append(out, adminSeries{Prefix: s.Prefix, Year: s.Year, LastNumber: s.LastNumber})
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeAdminEncodeFailed,
			"the invoice series could not be encoded")
	}

	return body, nil
}
