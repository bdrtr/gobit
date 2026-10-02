package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// CodeSalesChannelRevised refuses a correction of a sales channel when
// another writer changed it since the caller read it (ADR 0352).
const CodeSalesChannelRevised = "auth_sales_channel_revised"

// ReviseSalesChannel writes the channel's name, description and whether it is
// disabled only while they are the ones the caller read, and refuses with
// [CodeSalesChannelRevised] when another writer changed them since (ADR
// 0352). The terms are checked as the update checks them.
func (s *Service) ReviseSalesChannel(
	ctx context.Context, id string, read, next models.ChannelTerms,
) (models.SalesChannel, error) {
	if err := s.ready(); err != nil {
		return models.SalesChannel{}, err
	}
	if err := requireID(id, models.SalesChannelIDPrefix, "the sales channel identifier"); err != nil {
		return models.SalesChannel{}, err
	}
	if err := requireText("the sales channel name", next.Name); err != nil {
		return models.SalesChannel{}, err
	}
	if err := checkLen("the sales channel name", next.Name, models.MaxNameLen); err != nil {
		return models.SalesChannel{}, err
	}
	if err := checkLen("the sales channel description", next.Description, models.MaxDescriptionLen); err != nil {
		return models.SalesChannel{}, err
	}

	channel, revised, err := s.repo.ReviseSalesChannel(ctx, id, read, next, s.clock())
	if err != nil || revised {
		return channel, err
	}

	// Nothing was written: the channel is gone, or it was revised since.
	if _, err := s.repo.GetSalesChannel(ctx, id); err != nil {
		return models.SalesChannel{}, err
	}

	return models.SalesChannel{}, errors.Conflict(CodeSalesChannelRevised,
		"sales channel %s was revised since it was read; draw the list again", id)
}
