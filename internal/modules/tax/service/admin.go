package service

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// AdminSurface is the tax module's panel surface: a rate's correction
// (ADR 0378) and its trial (ADR 0395). Only
// primitives and JSON cross it, as across every surface the panel resolves
// (ADR 0001).
type AdminSurface struct {
	svc   *Service
	trial RateTrialFlow
}

// WithTrial binds the flow the panel's tax rate trial runs on and returns the
// surface; the module hands over the wrapper its endpoint uses.
func (a *AdminSurface) WithTrial(trial RateTrialFlow) *AdminSurface {
	a.trial = trial

	return a
}

// TrialTaxRateJSON is the tax rate trial the panel asks for (ADR 0395): the
// change as JSON, read strictly, and the report the endpoint answers with,
// after the same refusals ([Service.TryRate]).
func (a *AdminSurface) TrialTaxRateJSON(
	ctx context.Context, id string, from, to time.Time, change json.RawMessage,
) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(change))
	dec.DisallowUnknownFields()
	var amended RateChange
	if err := dec.Decode(&amended); err != nil {
		return nil, errors.Invalid(CodeTrialInvalidChange, "the change to try could not be read: %v", err)
	}

	return a.svc.TryRate(ctx, a.trial, id, from, to, amended)
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
