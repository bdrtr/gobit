//go:build integration

package promotion_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// TestThePanelAddsAndRemovesARule is ADR 0315 against a real PostgreSQL: a
// rule the surface adds is on the promotion's page with its id, removing it
// through its own promotion takes it off, and removing it through another
// promotion is not found and leaves it.
func TestThePanelAddsAndRemovesARule(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	surface := promotion.NewAdminSurface(svc)
	promo := activePromotion(ctx, t, svc, service.PromotionInput{})
	other := activePromotion(ctx, t, svc, service.PromotionInput{})

	require.NoError(t, surface.AddPromotionRule(ctx, promo.ID, "target", "category_tree_ids", "any_in", []string{"pcat_shoes"}))

	type page struct {
		Rules []struct {
			ID        string   `json:"id"`
			Type      string   `json:"type"`
			Attribute string   `json:"attribute"`
			Operator  string   `json:"operator"`
			Values    []string `json:"values"`
		} `json:"rules"`
	}
	read := func(id string) page {
		t.Helper()

		raw, err := surface.PromotionJSON(ctx, id)
		require.NoError(t, err)
		var p page
		require.NoError(t, json.Unmarshal(raw, &p))

		return p
	}

	added := read(promo.ID)
	require.Len(t, added.Rules, 1)
	rule := added.Rules[0]
	assert.NotEmpty(t, rule.ID)
	assert.Equal(t, "target", rule.Type)
	assert.Equal(t, "category_tree_ids", rule.Attribute)
	assert.Equal(t, "any_in", rule.Operator)
	assert.Equal(t, []string{"pcat_shoes"}, rule.Values)

	err := surface.RemovePromotionRule(ctx, other.ID, rule.ID)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a rule is removed through its own promotion only: %v", err)
	assert.Len(t, read(promo.ID).Rules, 1, "and is left where it is")

	require.NoError(t, surface.RemovePromotionRule(ctx, promo.ID, rule.ID))
	assert.Empty(t, read(promo.ID).Rules)

	err = surface.AddPromotionRule(ctx, promo.ID, "target", "category_tree_ids", "any_in", nil)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a rule holds a value: %v", err)
}
