package service

import (
	"context"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// RevisePriceList renames the list, rewrites its description and moves its
// window, writing them only while they are the ones the caller read, and
// refuses with [CodePriceListMoved] when another writer changed any of them
// since (ADR 0330). The next terms are checked as a new list's are; the type,
// the status and the metadata are kept.
func (s *Service) RevisePriceList(
	ctx context.Context, id string, read, next models.PriceListTerms,
) (models.PriceList, error) {
	if err := s.ready(); err != nil {
		return models.PriceList{}, err
	}
	if err := requireID(id, models.PriceListIDPrefix, "price list id"); err != nil {
		return models.PriceList{}, err
	}
	title, err := listTitle(next.Title)
	if err != nil {
		return models.PriceList{}, err
	}
	starts, ends, err := listWindow(next.StartsAt, next.EndsAt)
	if err != nil {
		return models.PriceList{}, err
	}
	next = models.PriceListTerms{
		Title: title, Description: strings.TrimSpace(next.Description), StartsAt: starts, EndsAt: ends,
	}

	list, revised, err := s.repo.RevisePriceList(ctx, id, read, next, s.clock)
	if err != nil {
		return models.PriceList{}, err
	}
	if !revised {
		return models.PriceList{}, errors.Conflict(CodePriceListMoved,
			"price list %s was revised since it was read: it is %q from %s until %s now; draw the list again",
			id, list.Title, windowEnd(list.StartsAt, "always"), windowEnd(list.EndsAt, "open"))
	}

	return list, nil
}

// windowEnd prints one end of a list's window, or what an open end means.
func windowEnd(at *time.Time, open string) string {
	if at == nil {
		return open
	}

	return at.UTC().Format(time.RFC3339)
}
