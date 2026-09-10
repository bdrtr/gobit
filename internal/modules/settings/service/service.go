// Package service holds the settings module's rules.
//
// There is one record and two verbs, and the rules are about what may be written
// on it rather than about when: an identity has no lifecycle, it is simply the
// current answer to "who is selling".
package service

import (
	"context"
	"log/slog"
	"regexp"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/settings/models"
)

// Error codes; the calling side can look at these with errors.CodeOf.
const (
	// CodeInvalidInput reports that the input did not pass validation.
	CodeInvalidInput = "settings_invalid_input"
	// CodeUnconfigured reports that the service has not been set up.
	CodeUnconfigured = "settings_service_unconfigured"
)

// Field bounds.
//
// They are generous and they exist to stop a broken client rather than to
// express a rule: a legal name is not long, and a printed address is not a
// document.
const (
	// MaxNameLen is the longest legal name, tax number or tax office.
	MaxNameLen = 200
	// MaxEmailLen is the longest e-mail address.
	MaxEmailLen = 320
	// MaxAddressLen is the longest printed address.
	MaxAddressLen = 2000
)

// countryPattern is the ISO 3166-1 alpha-2 shape the schema also holds.
var countryPattern = regexp.MustCompile(`^[A-Z]{2}$`)

// Store is what the service needs from the data layer.
type Store interface {
	// GetProfile returns the shop's profile; NotFound when it has none.
	GetProfile(ctx context.Context) (models.StoreProfile, error)
	// SetProfile writes the profile, replacing what was there.
	SetProfile(ctx context.Context, profile models.StoreProfile) (models.StoreProfile, error)
}

// Service is the settings module's business layer.
type Service struct {
	store Store
	log   *slog.Logger
}

// Options are the service's dependencies.
type Options struct {
	// Logger is where the service logs; nil discards.
	Logger *slog.Logger
}

// New builds the service over the given store.
func New(store Store, opts Options) *Service {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Service{store: store, log: log}
}

// SetProfileInput is the shop's identity as it is written.
//
// Every field is given on every write. The record is replaced rather than
// patched, and the reason is what it holds: a half-written identity is a
// document that names a shop that does not exist.
type SetProfileInput struct {
	// LegalName is the name documents are issued under; it is REQUIRED.
	LegalName string
	// TaxNumber, TaxOffice, Email and Address may all be empty.
	TaxNumber string
	TaxOffice string
	Email     string
	Address   string
	// CountryCode is the ISO 3166-1 alpha-2 country; it is REQUIRED.
	CountryCode string
}

// GetProfile returns the shop's profile; NotFound when it has not been written.
func (s *Service) GetProfile(ctx context.Context) (models.StoreProfile, error) {
	if err := s.ready(); err != nil {
		return models.StoreProfile{}, err
	}

	return s.store.GetProfile(ctx)
}

// SetProfile writes the shop's identity, replacing what was there.
//
// The values are TRIMMED before they are validated and before they are stored.
// A legal name of three spaces passes a "not empty" check and prints as nothing,
// which is the state the required field exists to prevent; and a trailing space
// in a tax number is a difference no human meant.
func (s *Service) SetProfile(
	ctx context.Context, in SetProfileInput,
) (models.StoreProfile, error) {
	if err := s.ready(); err != nil {
		return models.StoreProfile{}, err
	}

	profile, err := buildProfile(in)
	if err != nil {
		return models.StoreProfile{}, err
	}

	return s.store.SetProfile(ctx, profile)
}

// buildProfile validates the input and turns it into the record to write.
func buildProfile(in SetProfileInput) (models.StoreProfile, error) {
	name := strings.TrimSpace(in.LegalName)
	if name == "" {
		return models.StoreProfile{}, errors.Invalid(CodeInvalidInput,
			"the shop's legal name is required; a document cannot be issued under no name")
	}
	if len(name) > MaxNameLen {
		return models.StoreProfile{}, errors.Invalid(CodeInvalidInput,
			"the legal name can be at most %d characters", MaxNameLen)
	}

	country := strings.ToUpper(strings.TrimSpace(in.CountryCode))
	if !countryPattern.MatchString(country) {
		return models.StoreProfile{}, errors.Invalid(CodeInvalidInput,
			"the country has to be an ISO 3166-1 alpha-2 code, %q given", in.CountryCode)
	}

	fields := map[string]struct {
		value string
		limit int
	}{
		"tax number": {strings.TrimSpace(in.TaxNumber), MaxNameLen},
		"tax office": {strings.TrimSpace(in.TaxOffice), MaxNameLen},
		"e-mail":     {strings.TrimSpace(in.Email), MaxEmailLen},
		"address":    {strings.TrimSpace(in.Address), MaxAddressLen},
	}
	for label, field := range fields {
		if len(field.value) > field.limit {
			return models.StoreProfile{}, errors.Invalid(CodeInvalidInput,
				"the %s can be at most %d characters", label, field.limit)
		}
	}

	return models.StoreProfile{
		LegalName:   name,
		TaxNumber:   fields["tax number"].value,
		TaxOffice:   fields["tax office"].value,
		Email:       fields["e-mail"].value,
		Address:     fields["address"].value,
		CountryCode: country,
	}, nil
}

// ready reports whether the service was built with a store.
func (s *Service) ready() error {
	if s == nil || s.store == nil {
		return errors.Unavailable(CodeUnconfigured, "the settings service is not set up")
	}

	return nil
}
