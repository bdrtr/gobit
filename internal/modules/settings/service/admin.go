package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bdrtr/gobit/core/errors"
)

// codeAdminProfileUnreadable reports a profile the panel's surface could not
// encode or decode.
const codeAdminProfileUnreadable = "settings_admin_profile_unreadable"

// AdminSurface is the settings module's panel surface (ADR 0336): the shop's
// identity, read and written. Only primitives and JSON cross it, as across
// every surface the panel resolves (ADR 0001).
type AdminSurface struct {
	svc *Service
}

// NewAdminSurface builds the panel's surface over the service.
func NewAdminSurface(svc *Service) *AdminSurface { return &AdminSurface{svc: svc} }

// adminProfile is the store profile as the panel reads and writes it; the
// json tags are the contract with the panel, which cannot import this
// package.
type adminProfile struct {
	LegalName   string     `json:"legal_name"`
	TaxNumber   string     `json:"tax_number"`
	TaxOffice   string     `json:"tax_office"`
	Email       string     `json:"email"`
	Address     string     `json:"address"`
	CountryCode string     `json:"country_code"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
}

// StoreProfileJSON is the shop's identity with the moment it was last
// written, or JSON null while the shop has not said who it is.
func (a *AdminSurface) StoreProfileJSON(ctx context.Context) (json.RawMessage, error) {
	profile, err := a.svc.GetProfile(ctx)
	switch {
	case errors.IsNotFound(err):
		return json.RawMessage(`null`), nil
	case err != nil:
		return nil, err
	}
	updated := profile.UpdatedAt
	body, err := json.Marshal(adminProfile{
		LegalName: profile.LegalName, TaxNumber: profile.TaxNumber, TaxOffice: profile.TaxOffice,
		Email: profile.Email, Address: profile.Address, CountryCode: profile.CountryCode, UpdatedAt: &updated,
	})
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeAdminProfileUnreadable,
			"the store profile could not be encoded")
	}

	return body, nil
}

// ReviseStoreProfile writes the shop's identity from the profile the operator
// read, named by the moment it was last written in RFC 3339, empty for none,
// and refuses when it was written since (ADR 0336). The profile travels as
// JSON, its printed fields named rather than placed.
func (a *AdminSurface) ReviseStoreProfile(ctx context.Context, readUpdatedAt string, profile json.RawMessage) error {
	var read *time.Time
	if readUpdatedAt != "" {
		at, err := time.Parse(time.RFC3339Nano, readUpdatedAt)
		if err != nil {
			return errors.Invalid(CodeInvalidInput, "the moment the profile was read is not a time: %q", readUpdatedAt)
		}
		read = &at
	}
	var next adminProfile
	if err := json.Unmarshal(profile, &next); err != nil {
		return errors.Invalid(CodeInvalidInput, "the store profile could not be read: %v", err)
	}

	_, err := a.svc.ReviseProfile(ctx, read, SetProfileInput{
		LegalName: next.LegalName, TaxNumber: next.TaxNumber, TaxOffice: next.TaxOffice,
		Email: next.Email, Address: next.Address, CountryCode: next.CountryCode,
	})

	return err
}
