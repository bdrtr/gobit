package service

import (
	"context"
	"log/slog"

	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// SalesChannelInput is the write input of a sales channel.
type SalesChannelInput struct {
	// Name is the channel's display name; it is required and it is unique among
	// the live channels.
	Name string
	// Description is the channel's description; it may be left empty.
	Description string
	// IsDisabled makes the channel open disabled.
	IsDisabled bool
	// Metadata is free structured context; it may be left empty.
	Metadata map[string]any
}

// CreateSalesChannel creates a new sales channel.
//
// If the name is already in use errors.Conflict is returned.
func (s *Service) CreateSalesChannel(ctx context.Context, in SalesChannelInput) (models.SalesChannel, error) {
	if err := s.ready(); err != nil {
		return models.SalesChannel{}, err
	}
	if err := requireText("the sales channel name", in.Name); err != nil {
		return models.SalesChannel{}, err
	}
	if err := checkLen("the sales channel name", in.Name, models.MaxNameLen); err != nil {
		return models.SalesChannel{}, err
	}
	if err := checkLen("the sales channel description", in.Description, models.MaxDescriptionLen); err != nil {
		return models.SalesChannel{}, err
	}

	now := s.clock()
	created, err := s.repo.CreateSalesChannel(ctx, models.SalesChannel{
		ID:          models.NewSalesChannelID(now),
		Name:        in.Name,
		Description: in.Description,
		IsDisabled:  in.IsDisabled,
		Metadata:    in.Metadata,
		CreatedAt:   now,
	})
	if err != nil {
		return models.SalesChannel{}, err
	}

	s.log.InfoContext(ctx, "sales channel created",
		slog.String("sales_channel_id", created.ID),
	)
	return created, nil
}

// GetSalesChannel returns the channel with the given identifier;
// errors.NotFound if there is none.
func (s *Service) GetSalesChannel(ctx context.Context, id string) (models.SalesChannel, error) {
	if err := s.ready(); err != nil {
		return models.SalesChannel{}, err
	}
	if err := requireID(id, models.SalesChannelIDPrefix, "the sales channel identifier"); err != nil {
		return models.SalesChannel{}, err
	}
	return s.repo.GetSalesChannel(ctx, id)
}

// ListSalesChannelsInput is the input of a channel listing.
type ListSalesChannelsInput struct {
	// Name, if given, restricts the result to the channel with this name.
	Name *string
	// IsDisabled, if given, filters by the disabled/enabled distinction.
	IsDisabled *bool
	// Limit is the page size; [DefaultLimit] is applied if it is 0.
	Limit int64
	// Offset is the number of records to skip.
	Offset int64
}

// ListSalesChannels returns the filtered and paginated list of channels.
func (s *Service) ListSalesChannels(
	ctx context.Context,
	in ListSalesChannelsInput,
) (Page[models.SalesChannel], error) {
	if err := s.ready(); err != nil {
		return Page[models.SalesChannel]{}, err
	}

	limit, offset, err := normalizePaging(in.Limit, in.Offset)
	if err != nil {
		return Page[models.SalesChannel]{}, err
	}

	items, total, err := s.repo.ListSalesChannels(ctx, models.SalesChannelFilter{
		Name:       in.Name,
		IsDisabled: in.IsDisabled,
	}, limit, offset)
	if err != nil {
		return Page[models.SalesChannel]{}, err
	}
	return Page[models.SalesChannel]{Items: items, Count: total, Limit: limit, Offset: offset}, nil
}

// UpdateSalesChannelInput is the partial update input of a channel.
//
// A nil field means "do not touch", a filled field means "write this value".
type UpdateSalesChannelInput struct {
	// Name is the channel's new name.
	Name *string
	// Description is the channel's new description.
	Description *string
	// IsDisabled is the channel's new enabled state.
	//
	// Disabling a channel means the publishable keys attached to it NOT SEEING
	// that channel; a key that has no other channel cannot establish a store
	// identity after this operation (see [Service.authenticatePublishable]).
	IsDisabled *bool
	// Metadata is the new metadata map; it replaces the whole column.
	Metadata map[string]any
}

// UpdateSalesChannel updates the given fields of the channel.
func (s *Service) UpdateSalesChannel(
	ctx context.Context,
	id string,
	in UpdateSalesChannelInput,
) (models.SalesChannel, error) {
	if err := s.ready(); err != nil {
		return models.SalesChannel{}, err
	}
	if err := requireID(id, models.SalesChannelIDPrefix, "the sales channel identifier"); err != nil {
		return models.SalesChannel{}, err
	}
	if in.Name != nil {
		if err := requireText("the sales channel name", *in.Name); err != nil {
			return models.SalesChannel{}, err
		}
		if err := checkLen("the sales channel name", *in.Name, models.MaxNameLen); err != nil {
			return models.SalesChannel{}, err
		}
	}
	if in.Description != nil {
		if err := checkLen("the sales channel description", *in.Description, models.MaxDescriptionLen); err != nil {
			return models.SalesChannel{}, err
		}
	}

	return s.repo.UpdateSalesChannel(ctx, id, models.SalesChannelPatch{
		Name:        in.Name,
		Description: in.Description,
		IsDisabled:  in.IsDisabled,
		Metadata:    in.Metadata,
	}, s.clock())
}

// DeleteSalesChannel soft deletes the channel and removes the key links.
//
// Removing the links is essential: since a soft delete is an UPDATE, the
// foreign key CASCADE does not kick in and publishable keys would keep looking
// as though they were attached to a deleted channel.
func (s *Service) DeleteSalesChannel(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.SalesChannelIDPrefix, "the sales channel identifier"); err != nil {
		return err
	}
	if err := s.repo.DeleteSalesChannel(ctx, id, s.clock()); err != nil {
		return err
	}

	s.log.InfoContext(ctx, "sales channel deleted", slog.String("sales_channel_id", id))
	return nil
}
