package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/review/models"
)

// MaxSummaryProducts bounds the products one summary read names: a listing's
// page, with room (ADR 0449).
const MaxSummaryProducts = 100

// SummarizeProducts returns each named product's count and average of
// approved reviews, in the order named, a product without one counted zero
// (ADR 0449). A name given twice is answered once. It is the storefront's
// listing read of [Service.Summarize]: one statement for a page of cards.
func (s *Service) SummarizeProducts(ctx context.Context, productIDs []string) ([]models.Summary, error) {
	ids := make([]string, 0, len(productIDs))
	seen := make(map[string]bool, len(productIDs))
	for _, id := range productIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, errors.Invalid(CodeInvalidInput, "a product id cannot be empty")
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, errors.Invalid(CodeInvalidInput, "at least one product id is required")
	}
	if len(ids) > MaxSummaryProducts {
		return nil, errors.Invalid(CodeInvalidInput,
			"at most %d products are summarized at once; %d were named", MaxSummaryProducts, len(ids))
	}

	found, err := s.repo.SummarizeMany(ctx, ids)
	if err != nil {
		return nil, err
	}

	out := make([]models.Summary, 0, len(ids))
	for _, id := range ids {
		summary, ok := found[id]
		if !ok {
			summary = models.Summary{ProductID: id}
		}
		out = append(out, summary)
	}

	return out, nil
}
