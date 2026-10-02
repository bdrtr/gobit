package service

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// AdminSurface is the region module's panel surface (ADR 0362). Only
// primitives and JSON cross it, as across every surface the panel resolves
// (ADR 0001).
type AdminSurface struct {
	svc *Service
}

// NewAdminSurface builds the panel's surface over the service.
func NewAdminSurface(svc *Service) *AdminSurface { return &AdminSurface{svc: svc} }

// panelRegion is a region's terms as the panel reads and writes them; the
// json tags are the contract with the panel, which cannot import this
// package, and spell the fields the region provider publishes.
type panelRegion struct {
	Name           string `json:"name"`
	AutomaticTaxes bool   `json:"automatic_taxes"`
	TaxRate        int32  `json:"tax_rate"`
}

// ReviseRegion corrects the region from the terms the operator read, both as
// JSON, and refuses when another writer changed them since (ADR 0362).
func (a *AdminSurface) ReviseRegion(ctx context.Context, id string, read, next json.RawMessage) error {
	var was, will panelRegion
	if err := json.Unmarshal(read, &was); err != nil {
		return errors.Invalid(CodeInvalidInput, "the region read could not be read: %v", err)
	}
	if err := json.Unmarshal(next, &will); err != nil {
		return errors.Invalid(CodeInvalidInput, "the region written could not be read: %v", err)
	}
	_, err := a.svc.ReviseRegion(ctx, id, models.RegionTerms(was), models.RegionTerms(will))

	return err
}
