package service

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
)

// This file is the CROSS-MODULE surface of the settings module (ADR 0001,
// ADR 0006).
//
// The consumer is the invoicing flow, which cannot import this module. The
// answer therefore crosses as a JSON document whose schema is declared here, the
// way region's, promotion's and order's do; the consumer declares its own narrow
// interface and this type satisfies it structurally.

// CodeInteropResponseInvalid reports that the profile could not be turned into
// JSON.
const CodeInteropResponseInvalid = "settings_interop_response_invalid"

// Interop turns the settings service into a PRIMITIVE cross-module surface.
type Interop struct {
	svc *Service
}

// NewInterop builds the cross-module surface for the given service.
func NewInterop(svc *Service) *Interop { return &Interop{svc: svc} }

// storeProfileJSON is the schema of [Interop.StoreProfileJSON]'s answer.
//
// It is the PRINTED identity and nothing else: no timestamps, no identifier. A
// consumer prints these six fields, and a field it cannot print is one it would
// have to decide what to do with.
//
//	{
//	  "legal_name": "Example Trading Ltd",
//	  "tax_number": "1234567890",
//	  "tax_office": "Central",
//	  "email": "billing@example.com",
//	  "address": "1 Example Street\n34710 Example City",
//	  "country_code": "TR"
//	}
type storeProfileJSON struct {
	LegalName   string `json:"legal_name"`
	TaxNumber   string `json:"tax_number"`
	TaxOffice   string `json:"tax_office"`
	Email       string `json:"email"`
	Address     string `json:"address"`
	CountryCode string `json:"country_code"`
}

// StoreProfileJSON returns the shop's identity as a document.
//
// It answers errors.NotFound when the shop has not said who it is. That is the
// answer the caller has to act on rather than paper over: an invoice issued
// against an absent profile would name nobody.
//
// The counterpart on the consumer side:
//
//	type StoreProfile interface {
//	    StoreProfileJSON(ctx context.Context) (json.RawMessage, error)
//	}
func (i *Interop) StoreProfileJSON(ctx context.Context) (json.RawMessage, error) {
	profile, err := i.svc.GetProfile(ctx)
	if err != nil {
		return nil, err
	}

	payload, marshalErr := json.Marshal(storeProfileJSON{
		LegalName:   profile.LegalName,
		TaxNumber:   profile.TaxNumber,
		TaxOffice:   profile.TaxOffice,
		Email:       profile.Email,
		Address:     profile.Address,
		CountryCode: profile.CountryCode,
	})
	if marshalErr != nil {
		return nil, errors.Wrap(marshalErr, errors.KindInternal, CodeInteropResponseInvalid,
			"the store profile could not be converted to JSON")
	}

	return payload, nil
}
