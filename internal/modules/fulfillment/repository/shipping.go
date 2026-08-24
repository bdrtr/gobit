package repository

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/repository/fulfillmentdb"
)

// Bu dosya kargo KATALOĞUNUN (profil, seçenek, kural) erişimidir.
// Gönderilerin erişimi fulfillment.go'dadır.

// --- kargo profilleri --------------------------------------------------------

// CreateShippingProfile yeni bir kargo profili kaydeder.
// Aynı ad yaşayan bir profilde kullanılıyorsa Conflict döner.
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
		return models.ShippingProfile{}, classify(err, codeQueryFailed, "kargo profili oluşturulamadı")
	}
	return toProfile(row)
}

// GetShippingProfile profili kimliğiyle döner; yoksa NotFound.
func (r *Repository) GetShippingProfile(ctx context.Context, id string) (models.ShippingProfile, error) {
	row, err := r.queries(ctx).GetShippingProfile(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ShippingProfile{}, profileNotFound(id)
		}
		return models.ShippingProfile{}, classify(err, codeQueryFailed, "kargo profili okunamadı")
	}
	return toProfile(row)
}

// LockShippingProfile profili işlem sonuna kadar YAZMA kilidiyle okur;
// yoksa NotFound.
//
// Yalnızca [Repository.WithTx] içinde çağrılmalıdır: işlemsiz bir FOR UPDATE
// kilidi deyim biter bitmez bırakılır ve hiçbir şeyi korumaz.
func (r *Repository) LockShippingProfile(ctx context.Context, id string) (models.ShippingProfile, error) {
	row, err := r.queries(ctx).LockShippingProfile(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ShippingProfile{}, profileNotFound(id)
		}
		return models.ShippingProfile{}, classify(err, codeQueryFailed, "kargo profili kilitlenemedi")
	}
	return toProfile(row)
}

// LockShippingProfileShared profili işlem sonuna kadar PAYLAŞIMLI kilitle
// okur; yoksa NotFound.
//
// Gerekçe ve kullanım [Repository.LockShippingProfile] ile aynıdır; fark,
// paralel seçenek eklemelerinin birbirini beklememesidir.
func (r *Repository) LockShippingProfileShared(
	ctx context.Context,
	id string,
) (models.ShippingProfile, error) {
	row, err := r.queries(ctx).LockShippingProfileShared(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ShippingProfile{}, profileNotFound(id)
		}
		return models.ShippingProfile{}, classify(err, codeQueryFailed, "kargo profili kilitlenemedi")
	}
	return toProfile(row)
}

// ListShippingProfiles profilleri süzerek ve sayfalayarak döner.
// İkinci dönüş değeri süzgece uyan TÜM satırların sayısıdır.
//
// Toplam AYRI bir sorgudan gelir ve listeyle aynı süzgeçleri uygular; sayfa
// aralık dışında olsa ve hiç satır dönmese de doğrudur.
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
		return nil, 0, classify(err, codeQueryFailed, "kargo profilleri listelenemedi")
	}

	total, err := r.queries(ctx).CountShippingProfiles(ctx, filter.Type)
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "kargo profilleri sayılamadı")
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

// UpdateShippingProfile profilin alanlarını MUTLAK değerlerle yazar.
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
		return models.ShippingProfile{}, classify(err, codeQueryFailed, "kargo profili güncellenemedi")
	}
	return toProfile(row)
}

// SoftDeleteShippingProfile profili yumuşak siler; yoksa NotFound.
func (r *Repository) SoftDeleteShippingProfile(ctx context.Context, id string) error {
	if _, err := r.queries(ctx).SoftDeleteShippingProfile(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return profileNotFound(id)
		}
		return classify(err, codeQueryFailed, "kargo profili silinemedi")
	}
	return nil
}

// CountAliveOptionsByProfile profile bağlı yaşayan seçenekleri sayar.
func (r *Repository) CountAliveOptionsByProfile(ctx context.Context, profileID string) (int64, error) {
	count, err := r.queries(ctx).CountAliveOptionsByProfile(ctx, profileID)
	if err != nil {
		return 0, classify(err, codeQueryFailed, "profile bağlı kargo seçenekleri sayılamadı")
	}
	return count, nil
}

// --- kargo seçenekleri -------------------------------------------------------

// CreateShippingOption yeni bir kargo seçeneği kaydeder.
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
		return models.ShippingOption{}, classify(err, codeQueryFailed, "kargo seçeneği oluşturulamadı")
	}
	return toOption(row)
}

// GetShippingOption seçeneği kimliğiyle döner; yoksa NotFound.
// Kurallar DOLDURULMAZ; onlar [Repository.ListShippingOptionRules] ile okunur.
func (r *Repository) GetShippingOption(ctx context.Context, id string) (models.ShippingOption, error) {
	row, err := r.queries(ctx).GetShippingOption(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ShippingOption{}, optionNotFound(id)
		}
		return models.ShippingOption{}, classify(err, codeQueryFailed, "kargo seçeneği okunamadı")
	}
	return toOption(row)
}

// ListShippingOptions seçenekleri süzerek ve sayfalayarak döner.
// İkinci dönüş değeri süzgece uyan TÜM satırların sayısıdır.
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
		return nil, 0, classify(err, codeQueryFailed, "kargo seçenekleri listelenemedi")
	}

	total, err := r.queries(ctx).CountShippingOptions(ctx, fulfillmentdb.CountShippingOptionsParams{
		RegionID:   filter.RegionID,
		ProfileID:  filter.ProfileID,
		ProviderID: filter.ProviderID,
		PriceType:  filter.PriceType,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "kargo seçenekleri sayılamadı")
	}

	out, err := optionsFromRows(rows)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// ShippingOptionsByIDs verilen kimliklerin seçeneklerini TEK sorguda döner.
// Bulunamayan kimlik için satır dönmez; bu bir hata değildir.
func (r *Repository) ShippingOptionsByIDs(ctx context.Context, ids []string) ([]models.ShippingOption, error) {
	if len(ids) == 0 {
		return []models.ShippingOption{}, nil
	}
	rows, err := r.queries(ctx).GetShippingOptionsByIDs(ctx, ids)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "kargo seçenekleri okunamadı")
	}
	return optionsFromRows(rows)
}

// ListEligibleShippingOptions bir sepet bağlamının ADAY seçeneklerini,
// kurallarıyla birlikte döner.
//
// Kurallar ikinci bir sorgudan TOPLU olarak okunur ve seçeneklere iliştirilir;
// seçenek başına sorgu (N+1) yapılmaz. Kuralların eşleşmesi burada
// değerlendirilmez: karar servisteki saf fonksiyona aittir ve veritabanı
// olmadan sınanabilmelidir.
func (r *Repository) ListEligibleShippingOptions(
	ctx context.Context,
	filter models.EligibilityFilter,
) ([]models.ShippingOption, error) {
	profileIDs := filter.ProfileIDs
	if profileIDs == nil {
		// sqlc üretimi imza []string bekler; nil dilim cardinality() = 0
		// koşulunu karşılamayabilir, boş dilim ise kesin karşılar.
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
		return nil, classify(err, codeQueryFailed, "uygun kargo seçenekleri listelenemedi")
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
		return nil, classify(err, codeQueryFailed, "kargo seçeneği kuralları listelenemedi")
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

// UpdateShippingOption seçeneğin alanlarını MUTLAK değerlerle yazar.
//
// Sağlayıcı ve profil DEĞİŞTİRİLEMEZ: ikisi de seçeneğin kimliğine bağlı
// kararlardır ve değişmeleri, o seçenekle açılmış gönderilerin hangi
// sağlayıcıda olduğunu geçmişe dönük yanıltırdı. Değişmesi gerekiyorsa yeni
// bir seçenek açılır.
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
		return models.ShippingOption{}, classify(err, codeQueryFailed, "kargo seçeneği güncellenemedi")
	}
	return toOption(row)
}

// SoftDeleteShippingOption seçeneği yumuşak siler; yoksa NotFound.
func (r *Repository) SoftDeleteShippingOption(ctx context.Context, id string) error {
	if _, err := r.queries(ctx).SoftDeleteShippingOption(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return optionNotFound(id)
		}
		return classify(err, codeQueryFailed, "kargo seçeneği silinemedi")
	}
	return nil
}

// --- kargo seçeneği kuralları ------------------------------------------------

// CreateShippingOptionRule yeni bir kural kaydeder.
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
		return models.ShippingOptionRule{}, classify(err, codeQueryFailed, "kargo seçeneği kuralı oluşturulamadı")
	}
	return toRule(row), nil
}

// GetShippingOptionRule kuralı kimliğiyle döner; yoksa NotFound.
func (r *Repository) GetShippingOptionRule(
	ctx context.Context,
	id string,
) (models.ShippingOptionRule, error) {
	row, err := r.queries(ctx).GetShippingOptionRule(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ShippingOptionRule{}, ruleNotFound(id)
		}
		return models.ShippingOptionRule{}, classify(err, codeQueryFailed, "kargo seçeneği kuralı okunamadı")
	}
	return toRule(row), nil
}

// ListShippingOptionRules bir seçeneğin kurallarını döner.
func (r *Repository) ListShippingOptionRules(
	ctx context.Context,
	optionID string,
) ([]models.ShippingOptionRule, error) {
	rows, err := r.queries(ctx).ListShippingOptionRules(ctx, optionID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "kargo seçeneği kuralları listelenemedi")
	}
	out := make([]models.ShippingOptionRule, 0, len(rows))
	for i := range rows {
		out = append(out, toRule(rows[i]))
	}
	return out, nil
}

// SoftDeleteShippingOptionRule kuralı yumuşak siler; yoksa NotFound.
func (r *Repository) SoftDeleteShippingOptionRule(ctx context.Context, id string) error {
	if _, err := r.queries(ctx).SoftDeleteShippingOptionRule(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ruleNotFound(id)
		}
		return classify(err, codeQueryFailed, "kargo seçeneği kuralı silinemedi")
	}
	return nil
}

// optionsFromRows satır dilimini domain diliminine çevirir.
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
