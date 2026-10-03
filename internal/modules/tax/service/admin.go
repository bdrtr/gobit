package service

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// AdminSurface is the tax module's panel surface (ADR 0378). Only
// primitives and JSON cross it, as across every surface the panel resolves
// (ADR 0001).
type AdminSurface struct {
	svc *Service
}

// NewAdminSurface builds the panel's surface over the service.
func NewAdminSurface(svc *Service) *AdminSurface { return &AdminSurface{svc: svc} }

// panelRate is a rate's terms as the panel reads and writes them; the json
// tags are the contract with the panel, which cannot import this package, and
// spell the fields the tax region provider publishes on a rate.
type panelRate struct {
	Name    string `json:"name"`
	RateBps int32  `json:"rate_bps"`
}

// ReviseTaxRate corrects the rate from the terms the operator read, both as
// JSON, and refuses when another writer changed them since (ADR 0378).
func (a *AdminSurface) ReviseTaxRate(ctx context.Context, id string, read, next json.RawMessage) error {
	var was, will panelRate
	if err := json.Unmarshal(read, &was); err != nil {
		return errors.Invalid(CodeInvalidInput, "the rate read could not be read: %v", err)
	}
	if err := json.Unmarshal(next, &will); err != nil {
		return errors.Invalid(CodeInvalidInput, "the rate written could not be read: %v", err)
	}
	_, err := a.svc.ReviseTaxRate(ctx, id, models.TaxRateTerms(was), models.TaxRateTerms(will))

	return err
}
