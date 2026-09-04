package repository

import (
	"slices"

	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository/authdb"
)

// This file converts the generated row types into domain models. The conversion
// lives in a single place so that when sqlc is regenerated only this file
// changes.

// toUser converts a user row into the domain model.
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

// toUsers converts a slice of user rows into domain models.
func toUsers(rows []authdb.AuthUser) ([]models.User, error) {
	out := make([]models.User, 0, len(rows))
	// Iterated by index: the row types are large and iterating by value would
	// copy hundreds of bytes on every turn.
	for i := range rows {
		user, err := toUser(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, user)
	}
	return out, nil
}

// toIdentity converts an identity row into the domain model.
//
// The PasswordHash field is carried over AS IT IS; the service hands it to the
// bcrypt comparison and nowhere else.
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

// toIdentities converts a slice of identity rows into domain models.
func toIdentities(rows []authdb.AuthIdentity) ([]models.AuthIdentity, error) {
	out := make([]models.AuthIdentity, 0, len(rows))
	// Iterated by index: the row types are large and iterating by value would
	// copy hundreds of bytes on every turn.
	for i := range rows {
		identity, err := toIdentity(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, identity)
	}
	return out, nil
}

// toAPIKey converts a key row into the domain model.
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

// toAPIKeys converts a slice of key rows into domain models.
func toAPIKeys(rows []authdb.ApiKey) []models.APIKey {
	out := make([]models.APIKey, 0, len(rows))
	// Iterated by index: the row types are large and iterating by value would
	// copy hundreds of bytes on every turn.
	for i := range rows {
		out = append(out, toAPIKey(rows[i]))
	}
	return out
}

// toSalesChannel converts a channel row into the domain model.
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

// toSalesChannels converts a slice of channel rows into domain models.
func toSalesChannels(rows []authdb.SalesChannel) ([]models.SalesChannel, error) {
	out := make([]models.SalesChannel, 0, len(rows))
	// Iterated by index: the row types are large and iterating by value would
	// copy hundreds of bytes on every turn.
	for i := range rows {
		channel, err := toSalesChannel(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, channel)
	}
	return out, nil
}

// scopes prepares the scope slice for the model.
//
// For an empty column it returns an empty slice and NOT nil, and the slice is
// COPIED: if the domain object shared the backing array of the generated row,
// an edit made on one of them would show up in the other.
func scopes(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return slices.Clone(values)
}
