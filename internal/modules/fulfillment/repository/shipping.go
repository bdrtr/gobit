package repository

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/repository/fulfillmentdb"
)

// This file is the access to the shipping CATALOG (profile, option, rule).
// The access to the fulfillments is in fulfillment.go.

// --- shipping profiles -------------------------------------------------------

// CreateShippingProfile records a new shipping profile.
// If the same name is in use by a living profile it returns Conflict.
func (r *Repository) CreateShippingProfile(
	ctx context.Context,
	profile models.ShippingProfile,
) (models.ShippingProfile, error) {
	meta, err := fromJSONMap(profile.Metadata)
	if err != nil {
		return models.ShippingProfile{}, err
	}

	row, err := r.queries(ctx).CreateShippingProfile(ctx, fulfillmentdb.CreateShippingProfileParams{
		ID:       profile.ID,
		Name:     profile.Name,
		Type:     profile.Type.String(),
		Metadata: meta,
	})
	if err != nil {
		return models.ShippingProfile{}, classify(err, codeQueryFailed, "could not create shipping profile")
	}
	return toProfile(row)
}

// GetShippingProfile returns the profile by its identifier; NotFound if there is
// none.
func (r *Repository) GetShippingProfile(ctx context.Context, id string) (models.ShippingProfile, error) {
	row, err := r.queries(ctx).GetShippingProfile(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ShippingProfile{}, profileNotFound(id)
		}
		return models.ShippingProfile{}, classify(err, codeQueryFailed, "could not read shipping profile")
	}
	return toProfile(row)
}

// LockShippingProfile reads the profile with a WRITE lock held until the end of
// the transaction; NotFound if there is none.
//
// It must only be called inside [Repository.WithTx]: a FOR UPDATE lock without a
// transaction is released as soon as the statement ends and protects nothing.
func (r *Repository) LockShippingProfile(ctx context.Context, id string) (models.ShippingProfile, error) {
	row, err := r.queries(ctx).LockShippingProfile(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ShippingProfile{}, profileNotFound(id)
		}
		return models.ShippingProfile{}, classify(err, codeQueryFailed, "could not lock shipping profile")
	}
	return toProfile(row)
}

// LockShippingProfileShared reads the profile with a SHARED lock held until the
// end of the transaction; NotFound if there is none.
//
// The reasoning and the usage are the same as [Repository.LockShippingProfile];
// the difference is that parallel option insertions do not wait for each other.
func (r *Repository) LockShippingProfileShared(
	ctx context.Context,
	id string,
) (models.ShippingProfile, error) {
	row, err := r.queries(ctx).LockShippingProfileShared(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ShippingProfile{}, profileNotFound(id)
		}
		return models.ShippingProfile{}, classify(err, codeQueryFailed, "could not lock shipping profile")
	}
	return toProfile(row)
}

// ListShippingProfiles returns the profiles filtered and paginated.
// The second return value is the count of ALL rows matching the filter.
//
// The total comes from a SEPARATE query and applies the same filters as the
// list; it is correct even when the page is out of range and no rows come back.
func (r *Repository) ListShippingProfiles(
	ctx context.Context,
	filter models.ProfileFilter,
) ([]models.ShippingProfile, int64, error) {
	rows, err := r.queries(ctx).ListShippingProfiles(ctx, fulfillmentdb.ListShippingProfilesParams{
		Type:      filter.Type,
		RowLimit:  filter.Limit,
		RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not list shipping profiles")
	}

	total, err := r.queries(ctx).CountShippingProfiles(ctx, filter.Type)
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not count shipping profiles")
	}

	out := make([]models.ShippingProfile, 0, len(rows))
	for i := range rows {
		profile, convErr := toProfile(rows[i])
		if convErr != nil {
			return nil, 0, convErr
		}
		out = append(out, profile)
	}
	return out, total, nil
}

// UpdateShippingProfile writes the profile's fields with ABSOLUTE values.
func (r *Repository) UpdateShippingProfile(
	ctx context.Context,
	profile models.ShippingProfile,
) (models.ShippingProfile, error) {
	meta, err := fromJSONMap(profile.Metadata)
	if err != nil {
		return models.ShippingProfile{}, err
	}

	row, err := r.queries(ctx).UpdateShippingProfile(ctx, fulfillmentdb.UpdateShippingProfileParams{
		ID:       profile.ID,
		Name:     profile.Name,
		Type:     profile.Type.String(),
		Metadata: meta,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ShippingProfile{}, profileNotFound(profile.ID)
		}
		return models.ShippingProfile{}, classify(err, codeQueryFailed, "could not update shipping profile")
	}
	return toProfile(row)
}

// SoftDeleteShippingProfile soft deletes the profile; NotFound if there is none.
func (r *Repository) SoftDeleteShippingProfile(ctx context.Context, id string) error {
	if _, err := r.queries(ctx).SoftDeleteShippingProfile(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return profileNotFound(id)
		}
		return classify(err, codeQueryFailed, "could not delete shipping profile")
	}
	return nil
}

// CountAliveOptionsByProfile counts the living options bound to the profile.
func (r *Repository) CountAliveOptionsByProfile(ctx context.Context, profileID string) (int64, error) {
	count, err := r.queries(ctx).CountAliveOptionsByProfile(ctx, profileID)
	if err != nil {
		return 0, classify(err, codeQueryFailed, "could not count the shipping options bound to the profile")
	}
	return count, nil
}

// --- shipping options --------------------------------------------------------

// CreateShippingOption records a new shipping option.
func (r *Repository) CreateShippingOption(
	ctx context.Context,
	option models.ShippingOption,
) (models.ShippingOption, error) {
	data, err := fromJSONMap(option.Data)
	if err != nil {
		return models.ShippingOption{}, err
	}
	meta, err := fromJSONMap(option.Metadata)
	if err != nil {
		return models.ShippingOption{}, err
	}

	row, err := r.queries(ctx).CreateShippingOption(ctx, fulfillmentdb.CreateShippingOptionParams{
		ID:                option.ID,
		Name:              option.Name,
		ProviderID:        option.ProviderID,
		ShippingProfileID: option.ShippingProfileID,
		PriceType:         option.PriceType.String(),
		Amount:            option.Amount,
		CurrencyCode:      option.CurrencyCode,
		RegionID:          option.RegionID,
		IsReturn:          option.IsReturn,
		AdminOnly:         option.AdminOnly,
		Data:              data,
		Metadata:          meta,
	})
	if err != nil {
		return models.ShippingOption{}, classify(err, codeQueryFailed, "could not create shipping option")
	}
	return toOption(row)
}

// GetShippingOption returns the option by its identifier; NotFound if there is
// none. Rules ARE NOT FILLED IN; they are read with
// [Repository.ListShippingOptionRules].
func (r *Repository) GetShippingOption(ctx context.Context, id string) (models.ShippingOption, error) {
	row, err := r.queries(ctx).GetShippingOption(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ShippingOption{}, optionNotFound(id)
		}
		return models.ShippingOption{}, classify(err, codeQueryFailed, "could not read shipping option")
	}
	return toOption(row)
}

// ListShippingOptions returns the options filtered and paginated.
// The second return value is the count of ALL rows matching the filter.
func (r *Repository) ListShippingOptions(
	ctx context.Context,
	filter models.OptionFilter,
) ([]models.ShippingOption, int64, error) {
	rows, err := r.queries(ctx).ListShippingOptions(ctx, fulfillmentdb.ListShippingOptionsParams{
		RegionID:   filter.RegionID,
		ProfileID:  filter.ProfileID,
		ProviderID: filter.ProviderID,
		PriceType:  filter.PriceType,
		RowLimit:   filter.Limit,
		RowOffset:  filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not list shipping options")
	}

	total, err := r.queries(ctx).CountShippingOptions(ctx, fulfillmentdb.CountShippingOptionsParams{
		RegionID:   filter.RegionID,
		ProfileID:  filter.ProfileID,
		ProviderID: filter.ProviderID,
		PriceType:  filter.PriceType,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not count shipping options")
	}

	out, err := optionsFromRows(rows)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// ShippingOptionsByIDs returns the options of the given identifiers in a SINGLE
// query. No row is returned for an identifier that is not found; that is not an
// error.
func (r *Repository) ShippingOptionsByIDs(ctx context.Context, ids []string) ([]models.ShippingOption, error) {
	if len(ids) == 0 {
		return []models.ShippingOption{}, nil
	}
	rows, err := r.queries(ctx).GetShippingOptionsByIDs(ctx, ids)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not read shipping options")
	}
	return optionsFromRows(rows)
}

// ListEligibleShippingOptions returns the CANDIDATE options of a cart context
// together with their rules.
//
// The rules are read in BULK from a second query and attached to the options; no
// query per option (N+1) is made. Whether the rules match is not evaluated here:
// the decision belongs to the pure function in the service and must be testable
// without a database.
func (r *Repository) ListEligibleShippingOptions(
	ctx context.Context,
	filter models.EligibilityFilter,
) ([]models.ShippingOption, error) {
	profileIDs := filter.ProfileIDs
	if profileIDs == nil {
		// The sqlc-generated signature expects a []string; a nil slice may not
		// satisfy the cardinality() = 0 condition, while an empty slice
		// certainly does.
		profileIDs = []string{}
	}

	rows, err := r.queries(ctx).ListEligibleShippingOptions(ctx, fulfillmentdb.ListEligibleShippingOptionsParams{
		RegionID:         filter.RegionID,
		CurrencyCode:     filter.CurrencyCode,
		IsReturn:         filter.IsReturn,
		IncludeAdminOnly: filter.IncludeAdminOnly,
		ProfileIds:       profileIDs,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not list eligible shipping options")
	}

	options, err := optionsFromRows(rows)
	if err != nil {
		return nil, err
	}
	if len(options) == 0 {
		return options, nil
	}

	ids := make([]string, 0, len(options))
	for i := range options {
		ids = append(ids, options[i].ID)
	}
	ruleRows, err := r.queries(ctx).ListShippingOptionRulesByOptions(ctx, ids)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not list shipping option rules")
	}

	byOption := make(map[string][]models.ShippingOptionRule, len(options))
	for i := range ruleRows {
		rule := toRule(ruleRows[i])
		byOption[rule.ShippingOptionID] = append(byOption[rule.ShippingOptionID], rule)
	}
	for i := range options {
		options[i].Rules = byOption[options[i].ID]
	}
	return options, nil
}

// UpdateShippingOption writes the option's fields with ABSOLUTE values.
//
// The provider and the profile CANNOT BE CHANGED: both are decisions bound to
// the option's identity and changing them would retroactively mislead about
// which provider the fulfillments opened with that option are on. If a change is
// needed, a new option is opened.
func (r *Repository) UpdateShippingOption(
	ctx context.Context,
	option models.ShippingOption,
) (models.ShippingOption, error) {
	data, err := fromJSONMap(option.Data)
	if err != nil {
		return models.ShippingOption{}, err
	}
	meta, err := fromJSONMap(option.Metadata)
	if err != nil {
		return models.ShippingOption{}, err
	}

	row, err := r.queries(ctx).UpdateShippingOption(ctx, fulfillmentdb.UpdateShippingOptionParams{
		ID:        option.ID,
		Name:      option.Name,
		PriceType: option.PriceType.String(),
		Amount:    option.Amount,
		RegionID:  option.RegionID,
		IsReturn:  option.IsReturn,
		AdminOnly: option.AdminOnly,
		Data:      data,
		Metadata:  meta,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ShippingOption{}, optionNotFound(option.ID)
		}
		return models.ShippingOption{}, classify(err, codeQueryFailed, "could not update shipping option")
	}
	return toOption(row)
}

// SoftDeleteShippingOption soft deletes the option; NotFound if there is none.
func (r *Repository) SoftDeleteShippingOption(ctx context.Context, id string) error {
	if _, err := r.queries(ctx).SoftDeleteShippingOption(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return optionNotFound(id)
		}
		return classify(err, codeQueryFailed, "could not delete shipping option")
	}
	return nil
}

// --- shipping option rules ---------------------------------------------------

// CreateShippingOptionRule records a new rule.
func (r *Repository) CreateShippingOptionRule(
	ctx context.Context,
	rule models.ShippingOptionRule,
) (models.ShippingOptionRule, error) {
	row, err := r.queries(ctx).CreateShippingOptionRule(ctx, fulfillmentdb.CreateShippingOptionRuleParams{
		ID:               rule.ID,
		ShippingOptionID: rule.ShippingOptionID,
		Attribute:        rule.Attribute,
		Operator:         rule.Operator.String(),
		RuleValues:       rule.Values,
	})
	if err != nil {
		return models.ShippingOptionRule{}, classify(err, codeQueryFailed, "could not create shipping option rule")
	}
	return toRule(row), nil
}

// GetShippingOptionRule returns the rule by its identifier; NotFound if there is
// none.
func (r *Repository) GetShippingOptionRule(
	ctx context.Context,
	id string,
) (models.ShippingOptionRule, error) {
	row, err := r.queries(ctx).GetShippingOptionRule(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ShippingOptionRule{}, ruleNotFound(id)
		}
		return models.ShippingOptionRule{}, classify(err, codeQueryFailed, "could not read shipping option rule")
	}
	return toRule(row), nil
}

// ListShippingOptionRules returns the rules of an option.
func (r *Repository) ListShippingOptionRules(
	ctx context.Context,
	optionID string,
) ([]models.ShippingOptionRule, error) {
	rows, err := r.queries(ctx).ListShippingOptionRules(ctx, optionID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not list shipping option rules")
	}
	out := make([]models.ShippingOptionRule, 0, len(rows))
	for i := range rows {
		out = append(out, toRule(rows[i]))
	}
	return out, nil
}

// SoftDeleteShippingOptionRule soft deletes the rule; NotFound if there is none.
func (r *Repository) SoftDeleteShippingOptionRule(ctx context.Context, id string) error {
	if _, err := r.queries(ctx).SoftDeleteShippingOptionRule(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ruleNotFound(id)
		}
		return classify(err, codeQueryFailed, "could not delete shipping option rule")
	}
	return nil
}

// optionsFromRows converts a row slice into a domain slice.
func optionsFromRows(rows []fulfillmentdb.ShippingOption) ([]models.ShippingOption, error) {
	out := make([]models.ShippingOption, 0, len(rows))
	for i := range rows {
		option, err := toOption(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, option)
	}
	return out, nil
}
