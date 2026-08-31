package repository

import (
	"slices"

	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository/authdb"
)

// Bu dosya üretilmiş satır tiplerini domain modellerine çevirir. Dönüşüm tek
// yerde durur ki sqlc yeniden üretildiğinde yalnızca burası değişsin.

// toUser bir kullanıcı satırını domain modeline çevirir.
func toUser(row authdb.AuthUser) (models.User, error) {
	meta, err := toMetadata(row.Metadata)
	if err != nil {
		return models.User{}, err
	}
	return models.User{
		ID:        row.ID,
		Email:     row.Email,
		FirstName: row.FirstName,
		LastName:  row.LastName,
		AvatarURL: row.AvatarUrl,
		Scopes:    scopes(row.Scopes),
		Metadata:  meta,
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
		DeletedAt: toTimePtr(row.DeletedAt),
	}, nil
}

// toUsers bir kullanıcı satırı dilimini domain modellerine çevirir.
func toUsers(rows []authdb.AuthUser) ([]models.User, error) {
	out := make([]models.User, 0, len(rows))
	// Dizin ile dolaşılır: satır tipleri büyüktür ve değerle dolaşmak her
	// turda yüzlerce baytı kopyalardı.
	for i := range rows {
		user, err := toUser(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, user)
	}
	return out, nil
}

// toIdentity bir kimlik satırını domain modeline çevirir.
//
// PasswordHash alanı OLDUĞU GİBİ taşınır; servis onu yalnızca bcrypt
// karşılaştırmasına verir.
func toIdentity(row authdb.AuthIdentity) (models.AuthIdentity, error) {
	meta, err := toMetadata(row.Metadata)
	if err != nil {
		return models.AuthIdentity{}, err
	}
	return models.AuthIdentity{
		ID:               row.ID,
		UserID:           row.UserID,
		Provider:         row.Provider,
		ProviderIdentity: row.ProviderIdentity,
		PasswordHash:     row.PasswordHash,
		FailedAttempts:   int(row.FailedAttempts),
		LockedUntil:      toTimePtr(row.LockedUntil),
		LastLoginAt:      toTimePtr(row.LastLoginAt),
		Metadata:         meta,
		CreatedAt:        toTime(row.CreatedAt),
		UpdatedAt:        toTime(row.UpdatedAt),
		DeletedAt:        toTimePtr(row.DeletedAt),
	}, nil
}

// toAPIKey bir anahtar satırını domain modeline çevirir.
func toAPIKey(row authdb.ApiKey) models.APIKey {
	return models.APIKey{
		ID:         row.ID,
		Type:       models.APIKeyType(row.Type),
		Title:      row.Title,
		TokenHash:  row.TokenHash,
		Redacted:   row.Redacted,
		Scopes:     scopes(row.Scopes),
		CreatedBy:  row.CreatedBy,
		LastUsedAt: toTimePtr(row.LastUsedAt),
		RevokedAt:  toTimePtr(row.RevokedAt),
		RevokedBy:  row.RevokedBy,
		CreatedAt:  toTime(row.CreatedAt),
		UpdatedAt:  toTime(row.UpdatedAt),
		DeletedAt:  toTimePtr(row.DeletedAt),
	}
}

// toAPIKeys bir anahtar satırı dilimini domain modellerine çevirir.
func toAPIKeys(rows []authdb.ApiKey) []models.APIKey {
	out := make([]models.APIKey, 0, len(rows))
	// Dizin ile dolaşılır: satır tipleri büyüktür ve değerle dolaşmak her
	// turda yüzlerce baytı kopyalardı.
	for i := range rows {
		out = append(out, toAPIKey(rows[i]))
	}
	return out
}

// toSalesChannel bir kanal satırını domain modeline çevirir.
func toSalesChannel(row authdb.SalesChannel) (models.SalesChannel, error) {
	meta, err := toMetadata(row.Metadata)
	if err != nil {
		return models.SalesChannel{}, err
	}
	return models.SalesChannel{
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description,
		IsDisabled:  row.IsDisabled,
		Metadata:    meta,
		CreatedAt:   toTime(row.CreatedAt),
		UpdatedAt:   toTime(row.UpdatedAt),
		DeletedAt:   toTimePtr(row.DeletedAt),
	}, nil
}

// toSalesChannels bir kanal satırı dilimini domain modellerine çevirir.
func toSalesChannels(rows []authdb.SalesChannel) ([]models.SalesChannel, error) {
	out := make([]models.SalesChannel, 0, len(rows))
	// Dizin ile dolaşılır: satır tipleri büyüktür ve değerle dolaşmak her
	// turda yüzlerce baytı kopyalardı.
	for i := range rows {
		channel, err := toSalesChannel(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, channel)
	}
	return out, nil
}

// scopes yetki dilimini modele hazırlar.
//
// Boş sütun için nil DEĞİL boş dilim döner ve dilim KOPYALANIR: domain
// nesnesi üretilmiş satırın arka dizisini paylaşırsa, birinin üzerinde
// yapılan bir düzenleme diğerinde görünürdü.
func scopes(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return slices.Clone(values)
}
