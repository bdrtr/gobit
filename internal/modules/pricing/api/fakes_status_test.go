package api_test

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// SwitchPriceListStatus moves the list from the status read; the API tests
// do not reach it, and the service's own test holds the rule.
func (m *memRepo) SwitchPriceListStatus(
	_ context.Context, id string, from, to models.PriceListStatus, _ func() time.Time,
) (models.PriceList, bool, error) {
	list, ok := m.lists[id]
	if !ok {
		return models.PriceList{}, false, errors.NotFound("price_list_not_found", "price list not found: %s", id)
	}
	if list.Status != from {
		return list, false, nil
	}
	list.Status = to
	m.lists[id] = list

	return list, true, nil
}
