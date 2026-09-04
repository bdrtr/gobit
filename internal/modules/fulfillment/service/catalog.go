package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// This file holds the admin flows of the shipping CATALOG (profile, option,
// rule). The eligibility calculation is in eligibility.go, the fulfillments in
// fulfillment.go.

// CreateProfileInput is the input of a new shipping profile.
type CreateProfileInput struct {
	// Name is the profile's display name; it is required and is unique among
	// the living records.
	Name string
	// Type is the profile's type; if it is given empty, "default" is applied.
	Type string
	// Metadata is the caller's free-form extra data.
	Metadata map[string]any
}

// CreateShippingProfile creates a new shipping profile.
//
// A second profile with the same name returns errors.Conflict: the profile name
// is the only sign by which the administrator recognizes the rule, and two
// profiles with the same name would leave it ambiguous which one is being
// edited.
func (s *Service) CreateShippingProfile(
	ctx context.Context,
	in CreateProfileInput,
) (models.ShippingProfile, error) {
	name := strings.TrimSpace(in.Name)
	if err := requireText("the profile name", name); err != nil {
		return models.ShippingProfile{}, err
	}
	profileType, err := normalizeProfileType(in.Type)
	if err != nil {
		return models.ShippingProfile{}, err
	}

	return s.store.CreateShippingProfile(ctx, models.ShippingProfile{
		ID:       models.NewShippingProfileID(),
		Name:     name,
		Type:     profileType,
		Metadata: in.Metadata,
	})
}

// GetShippingProfile returns the profile by its identifier; errors.NotFound if
// absent.
func (s *Service) GetShippingProfile(ctx context.Context, id string) (models.ShippingProfile, error) {
	if err := requireID(id, models.ShippingProfileIDPrefix, "the shipping profile identifier"); err != nil {
		return models.ShippingProfile{}, err
	}
	return s.store.GetShippingProfile(ctx, id)
}

// ListProfilesInput is the input of the profile listing.
type ListProfilesInput struct {
	// Type, if given, restricts the result to profiles of that type.
	Type *string
	// Page holds the pagination parameters.
	Page Page
}

// ListShippingProfiles returns the profiles with pagination.
// The second return value is the count of ALL records matching the filter.
func (s *Service) ListShippingProfiles(
	ctx context.Context,
	in ListProfilesInput,
) ([]models.ShippingProfile, int64, error) {
	page, err := in.Page.normalize()
	if err != nil {
		return nil, 0, err
	}
	if in.Type != nil {
		if _, typeErr := normalizeProfileType(*in.Type); typeErr != nil {
			return nil, 0, typeErr
		}
	}

	return s.store.ListShippingProfiles(ctx, models.ProfileFilter{
		Type:   in.Type,
		Limit:  page.Limit,
		Offset: page.Offset,
	})
}

// UpdateProfileInput is the input of the profile update.
//
// The pointer fields preserve the distinction between "not given" and "given
// empty": a nil Name means the field WILL NOT CHANGE, while a Name pointing at
// an empty string means an invalid name was given and is rejected.
type UpdateProfileInput struct {
	// Name, if given, changes the profile's name.
	Name *string
	// Type, if given, changes the profile's type.
	Type *string
	// Metadata, if given, REPLACES the metadata (it is not merged).
	Metadata map[string]any
}

// UpdateShippingProfile updates the given fields of the profile.
func (s *Service) UpdateShippingProfile(
	ctx context.Context,
	id string,
	in UpdateProfileInput,
) (models.ShippingProfile, error) {
	if err := requireID(id, models.ShippingProfileIDPrefix, "the shipping profile identifier"); err != nil {
		return models.ShippingProfile{}, err
	}

	current, err := s.store.GetShippingProfile(ctx, id)
	if err != nil {
		return models.ShippingProfile{}, err
	}

	next := current
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if nameErr := requireText("the profile name", name); nameErr != nil {
			return models.ShippingProfile{}, nameErr
		}
		next.Name = name
	}
	if in.Type != nil {
		profileType, typeErr := normalizeProfileType(*in.Type)
		if typeErr != nil {
			return models.ShippingProfile{}, typeErr
		}
		next.Type = profileType
	}
	if in.Metadata != nil {
		next.Metadata = in.Metadata
	}

	return s.store.UpdateShippingProfile(ctx, next)
}

// DeleteShippingProfile soft-deletes the profile.
//
// A profile that STILL HOLDS an option cannot be deleted (errors.Conflict): had
// the deletion gone through silently, the shipping rule of the products bound to
// that profile would vanish and the customer would see no option at all. The
// administrator has to remove the options first.
//
// The check and the deletion happen in a single transaction and while the
// profile row is LOCKED. A single transaction alone would not have been enough:
// because a soft delete updates a non-key column it only takes FOR NO KEY
// UPDATE, and that lock DOES NOT CONFLICT with the FOR KEY SHARE an interleaving
// option INSERT takes for its foreign key — under READ COMMITTED the two
// transactions complete without waiting for each other and a LIVE option bound
// to a deleted profile would be left behind (reproduced on real Postgres). The
// FOR UPDATE lock of [Store.LockShippingProfile] conflicts with the shared lock
// option creation takes and thereby serializes the two paths (see
// [Service.CreateShippingOption]).
func (s *Service) DeleteShippingProfile(ctx context.Context, id string) error {
	if err := requireID(id, models.ShippingProfileIDPrefix, "the shipping profile identifier"); err != nil {
		return err
	}

	return s.store.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.store.LockShippingProfile(ctx, id); err != nil {
			return err
		}
		count, err := s.store.CountAliveOptionsByProfile(ctx, id)
		if err != nil {
			return err
		}
		if count > 0 {
			return errors.Conflict(CodeProfileInUse,
				"%d options are bound to the shipping profile; they have to be removed first (%s)", count, id)
		}
		return s.store.SoftDeleteShippingProfile(ctx, id)
	})
}

// CreateOptionInput is the input of a new shipping option.
type CreateOptionInput struct {
	// Name is the option's display name; it is required.
	Name string
	// ProviderID is the provider that will execute the option; it is required
	// and has to be REGISTERED.
	ProviderID string
	// ShippingProfileID is the profile the option will be bound to; it is
	// required.
	ShippingProfileID string
	// PriceType says where the fee comes from; if it is given empty, "flat".
	PriceType string
	// Amount is meaningful only on "flat" options (minor unit).
	Amount int64
	// CurrencyCode is the ISO 4217 code; it is required.
	CurrencyCode string
	// RegionID is the region the option is valid in; if empty, every region.
	RegionID string
	// IsReturn says the option is for a return shipment.
	IsReturn bool
	// AdminOnly says the option will not reach the storefront surface.
	AdminOnly bool
	// Data is the configuration to be handed to the provider.
	Data map[string]any
	// Metadata is the store's free-form extra data.
	Metadata map[string]any
}

// CreateShippingOption creates a new shipping option.
//
// The provider has to be REGISTERED: an option bound to an unregistered provider
// would only blow up at the moment it is about to be shown to the customer or a
// fulfillment is about to be opened, and the error would surface long after the
// setup.
//
// The existence of the profile is verified here as well. The foreign key does
// the same check, but the message produced from a driver error does not say
// which profile was being looked for.
//
// The profile is read UNDER A SHARED LOCK and IN THE SAME TRANSACTION as the
// INSERT. Had it been read without a lock, a [Service.DeleteShippingProfile]
// running at the same time could see the profile as "empty" and delete it, and
// the option would be written bound to a deleted profile. The lock being shared
// is deliberate: parallel option insertions into the same profile DO NOT WAIT
// for each other, only the deletion path waits.
func (s *Service) CreateShippingOption(
	ctx context.Context,
	in CreateOptionInput,
) (models.ShippingOption, error) {
	option, err := s.validateOptionInput(in)
	if err != nil {
		return models.ShippingOption{}, err
	}

	var out models.ShippingOption
	err = s.store.WithTx(ctx, func(ctx context.Context) error {
		if _, lockErr := s.store.LockShippingProfileShared(ctx, option.ShippingProfileID); lockErr != nil {
			return lockErr
		}
		created, createErr := s.store.CreateShippingOption(ctx, option)
		if createErr != nil {
			return createErr
		}
		out = created
		return nil
	})
	if err != nil {
		return models.ShippingOption{}, err
	}
	return out, nil
}

// validateOptionInput validates the option input and produces the model to be
// recorded.
//
// It is a separate function because the validation is PURE: it does not touch
// the database and every one of its branches can be exercised one by one.
func (s *Service) validateOptionInput(in CreateOptionInput) (models.ShippingOption, error) {
	name := strings.TrimSpace(in.Name)
	if err := requireText("the option name", name); err != nil {
		return models.ShippingOption{}, err
	}
	providerID := strings.TrimSpace(in.ProviderID)
	if err := requireText("the provider identifier", providerID); err != nil {
		return models.ShippingOption{}, err
	}
	if !s.providers.Has(providerID) {
		return models.ShippingOption{}, errors.NotFound(CodeProviderNotFound,
			"the shipping provider %q is not registered; the registered ones are: %s",
			providerID, strings.Join(s.providers.IDs(), ", "))
	}
	if err := requireID(in.ShippingProfileID, models.ShippingProfileIDPrefix,
		"the shipping profile identifier"); err != nil {
		return models.ShippingOption{}, err
	}
	priceType, err := normalizePriceType(in.PriceType)
	if err != nil {
		return models.ShippingOption{}, err
	}
	amount, err := amountFor(priceType, in.Amount)
	if err != nil {
		return models.ShippingOption{}, err
	}
	currency, err := normalizeCurrency(in.CurrencyCode)
	if err != nil {
		return models.ShippingOption{}, err
	}
	regionID := strings.TrimSpace(in.RegionID)
	if err := checkTextLen("the region identifier", regionID); err != nil {
		return models.ShippingOption{}, err
	}

	return models.ShippingOption{
		ID:                models.NewShippingOptionID(),
		Name:              name,
		ProviderID:        providerID,
		ShippingProfileID: strings.TrimSpace(in.ShippingProfileID),
		PriceType:         priceType,
		Amount:            amount,
		CurrencyCode:      currency,
		RegionID:          regionID,
		IsReturn:          in.IsReturn,
		AdminOnly:         in.AdminOnly,
		Data:              in.Data,
		Metadata:          in.Metadata,
	}, nil
}

// amountFor validates the amount to be stored according to the price type.
//
// On a "calculated" option the amount has to be ZERO; a value other than zero is
// not silently zeroed out, it is REJECTED. Silent zeroing would mean the fee the
// administrator entered is never applied and they only find out through the
// invoice.
func amountFor(priceType models.PriceType, amount int64) (int64, error) {
	if priceType == models.PriceCalculated {
		if amount != 0 {
			return 0, errors.Invalid(CodeInvalidInput,
				"the amount of a calculated shipping option has to be zero; the fee comes from the provider (given: %d)",
				amount)
		}
		return 0, nil
	}
	if err := requireAmount("the shipping fee", amount); err != nil {
		return 0, err
	}
	return amount, nil
}

// GetShippingOption returns the option together with its RULES; errors.NotFound
// if absent.
func (s *Service) GetShippingOption(ctx context.Context, id string) (models.ShippingOption, error) {
	if err := requireID(id, models.ShippingOptionIDPrefix, "the shipping option identifier"); err != nil {
		return models.ShippingOption{}, err
	}

	option, err := s.store.GetShippingOption(ctx, id)
	if err != nil {
		return models.ShippingOption{}, err
	}
	rules, err := s.store.ListShippingOptionRules(ctx, id)
	if err != nil {
		return models.ShippingOption{}, err
	}
	option.Rules = rules
	return option, nil
}

// ListOptionsAdminInput is the input of the admin listing.
type ListOptionsAdminInput struct {
	// RegionID, if given, restricts the result to the options of that region.
	RegionID *string
	// ProfileID, if given, restricts the result to the options bound to that
	// profile.
	ProfileID *string
	// ProviderID, if given, restricts the result to that provider's options.
	ProviderID *string
	// PriceType, if given, restricts the result to the options of that price
	// type.
	PriceType *string
	// Page holds the pagination parameters.
	Page Page
}

// ListShippingOptions returns the options with pagination.
//
// The rules ARE NOT FILLED IN: carrying every option's rules on the list surface
// means a second query per page and a growing response. The rules are read with
// [Service.GetShippingOption].
func (s *Service) ListShippingOptions(
	ctx context.Context,
	in ListOptionsAdminInput,
) ([]models.ShippingOption, int64, error) {
	page, err := in.Page.normalize()
	if err != nil {
		return nil, 0, err
	}
	if in.PriceType != nil {
		if _, typeErr := normalizePriceType(*in.PriceType); typeErr != nil {
			return nil, 0, typeErr
		}
	}

	return s.store.ListShippingOptions(ctx, models.OptionFilter{
		RegionID:   in.RegionID,
		ProfileID:  in.ProfileID,
		ProviderID: in.ProviderID,
		PriceType:  in.PriceType,
		Limit:      page.Limit,
		Offset:     page.Offset,
	})
}

// ListShippingOptionsByIDs returns the options of the given identifiers in a
// SINGLE query. No record is returned for an identifier that is not found; that
// is not an error.
func (s *Service) ListShippingOptionsByIDs(
	ctx context.Context,
	ids []string,
) ([]models.ShippingOption, error) {
	return s.store.ShippingOptionsByIDs(ctx, ids)
}

// UpdateOptionInput is the input of the option update.
//
// ProviderID and ShippingProfileID ARE ABSENT HERE and that is deliberate: both
// are decisions bound to the option's identity, and changing them would
// retroactively mislead about which provider the fulfillments opened with that
// option are at. If they need to change, a new option is created.
type UpdateOptionInput struct {
	// Name, if given, changes the option's name.
	Name *string
	// PriceType, if given, changes the price type.
	PriceType *string
	// Amount, if given, changes the amount (minor unit).
	Amount *int64
	// RegionID, if given, changes the region.
	RegionID *string
	// IsReturn, if given, changes the return flag.
	IsReturn *bool
	// AdminOnly, if given, changes the storefront visibility.
	AdminOnly *bool
	// Data, if given, REPLACES the provider configuration.
	Data map[string]any
	// Metadata, if given, REPLACES the metadata.
	Metadata map[string]any
}

// UpdateShippingOption updates the given fields of the option.
//
// The price type and the amount are validated TOGETHER: a request that only
// switches the type to "calculated" also has to zero out the old fixed amount
// standing on the row; otherwise the schema constraint blows up and the client
// gets an error whose cause it cannot make out.
func (s *Service) UpdateShippingOption(
	ctx context.Context,
	id string,
	in UpdateOptionInput,
) (models.ShippingOption, error) {
	if err := requireID(id, models.ShippingOptionIDPrefix, "the shipping option identifier"); err != nil {
		return models.ShippingOption{}, err
	}

	current, err := s.store.GetShippingOption(ctx, id)
	if err != nil {
		return models.ShippingOption{}, err
	}

	next := current
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if nameErr := requireText("the option name", name); nameErr != nil {
			return models.ShippingOption{}, nameErr
		}
		next.Name = name
	}
	if in.PriceType != nil {
		priceType, typeErr := normalizePriceType(*in.PriceType)
		if typeErr != nil {
			return models.ShippingOption{}, typeErr
		}
		next.PriceType = priceType
	}
	if in.Amount != nil {
		next.Amount = *in.Amount
	} else if next.PriceType == models.PriceCalculated {
		// The type was switched to "calculated" but no amount was given: the old
		// fixed amount on the row is now meaningless and is zeroed out. This is
		// not a silent loss, it is the DEFINITION of the type — on a calculated
		// option the fee comes from the provider.
		next.Amount = 0
	}
	amount, err := amountFor(next.PriceType, next.Amount)
	if err != nil {
		return models.ShippingOption{}, err
	}
	next.Amount = amount

	if in.RegionID != nil {
		regionID := strings.TrimSpace(*in.RegionID)
		if regionErr := checkTextLen("the region identifier", regionID); regionErr != nil {
			return models.ShippingOption{}, regionErr
		}
		next.RegionID = regionID
	}
	if in.IsReturn != nil {
		next.IsReturn = *in.IsReturn
	}
	if in.AdminOnly != nil {
		next.AdminOnly = *in.AdminOnly
	}
	if in.Data != nil {
		next.Data = in.Data
	}
	if in.Metadata != nil {
		next.Metadata = in.Metadata
	}

	return s.store.UpdateShippingOption(ctx, next)
}

// DeleteShippingOption soft-deletes the option.
//
// The deletion is SOFT and that is a requirement: the fulfillments bound to the
// option are protected by ON DELETE RESTRICT, which means a physical delete
// could never remove an option that has any history. A soft delete takes the
// option out of the catalog while past fulfillments keep reading it.
func (s *Service) DeleteShippingOption(ctx context.Context, id string) error {
	if err := requireID(id, models.ShippingOptionIDPrefix, "the shipping option identifier"); err != nil {
		return err
	}
	return s.store.SoftDeleteShippingOption(ctx, id)
}

// CreateRuleInput is the input of a new shipping option rule.
type CreateRuleInput struct {
	// Attribute is the name of the field to look at in the eligibility context;
	// it is required.
	Attribute string
	// Operator is the comparison operator; it is required.
	Operator string
	// Values is the right-hand side of the comparison; it has to hold at least
	// one element.
	Values []string
}

// CreateShippingOptionRule adds a rule to an option.
//
// The existence of the option is verified here: the foreign key does the same
// thing, but the message produced from a driver error does not say which option
// was being looked for.
func (s *Service) CreateShippingOptionRule(
	ctx context.Context,
	optionID string,
	in CreateRuleInput,
) (models.ShippingOptionRule, error) {
	if err := requireID(optionID, models.ShippingOptionIDPrefix, "the shipping option identifier"); err != nil {
		return models.ShippingOptionRule{}, err
	}
	operator, values, err := validateRuleInput(in.Attribute, in.Operator, in.Values)
	if err != nil {
		return models.ShippingOptionRule{}, err
	}

	if _, err := s.store.GetShippingOption(ctx, optionID); err != nil {
		return models.ShippingOptionRule{}, err
	}

	return s.store.CreateShippingOptionRule(ctx, models.ShippingOptionRule{
		ID:               models.NewShippingOptionRuleID(),
		ShippingOptionID: optionID,
		Attribute:        strings.TrimSpace(in.Attribute),
		Operator:         operator,
		Values:           values,
	})
}

// ListShippingOptionRules returns an option's rules.
func (s *Service) ListShippingOptionRules(
	ctx context.Context,
	optionID string,
) ([]models.ShippingOptionRule, error) {
	if err := requireID(optionID, models.ShippingOptionIDPrefix, "the shipping option identifier"); err != nil {
		return nil, err
	}
	if _, err := s.store.GetShippingOption(ctx, optionID); err != nil {
		return nil, err
	}
	return s.store.ListShippingOptionRules(ctx, optionID)
}

// DeleteShippingOptionRule soft-deletes the rule.
func (s *Service) DeleteShippingOptionRule(ctx context.Context, ruleID string) error {
	if err := requireID(ruleID, models.ShippingOptionRuleIDPrefix,
		"the shipping option rule identifier"); err != nil {
		return err
	}
	return s.store.SoftDeleteShippingOptionRule(ctx, ruleID)
}
