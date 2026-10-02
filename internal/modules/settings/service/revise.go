package service

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/settings/models"
)

// CodeProfileRevised refuses a store profile written since the caller read it
// (ADR 0336).
const CodeProfileRevised = "settings_store_profile_revised"

// ReviseProfile writes the shop's identity from the profile the caller read,
// named by the moment it was last written, nil when the caller read none, and
// refuses with [CodeProfileRevised] when it was written since (ADR 0336). The
// profile is checked as [Service.SetProfile] checks it.
func (s *Service) ReviseProfile(
	ctx context.Context, readUpdatedAt *time.Time, in SetProfileInput,
) (models.StoreProfile, error) {
	if err := s.ready(); err != nil {
		return models.StoreProfile{}, err
	}
	profile, err := buildProfile(in)
	if err != nil {
		return models.StoreProfile{}, err
	}

	written, ok, err := s.store.ReviseProfile(ctx, readUpdatedAt, profile)
	if err != nil {
		return models.StoreProfile{}, err
	}
	if !ok {
		return models.StoreProfile{}, errors.Conflict(CodeProfileRevised,
			"the store profile was written since it was read; draw the page again")
	}

	return written, nil
}
