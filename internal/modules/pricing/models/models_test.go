package models_test

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// TestIDsArePrefixedAndFixedLength proves that ids are prefixed and of fixed
// length (plan Section 8).
func TestIDsArePrefixedAndFixedLength(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	for name, tc := range map[string]struct {
		id     string
		prefix string
	}{
		"price set":  {models.NewPriceSetID(now), models.PriceSetIDPrefix},
		"price":      {models.NewPriceID(now), models.PriceIDPrefix},
		"price list": {models.NewPriceListID(now), models.PriceListIDPrefix},
		"price rule": {models.NewPriceRuleID(now), models.PriceRuleIDPrefix},
	} {
		t.Run(name, func(t *testing.T) {
			assert.True(t, strings.HasPrefix(tc.id, tc.prefix), "id must start with the %q prefix", tc.prefix)
			assert.Len(t, strings.TrimPrefix(tc.id, tc.prefix), models.IDBodyLength())
		})
	}
}

// TestIDsAreUnique proves that ids produced within the same millisecond do not
// collide.
func TestIDsAreUnique(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	seen := make(map[string]struct{}, 1000)
	for range 1000 {
		id := models.NewPriceID(now)
		_, dup := seen[id]
		require.False(t, dup, "ids produced at the same moment must be unique: %s", id)
		seen[id] = struct{}{}
	}
}

// TestIDsSortByTime proves that the LEXICOGRAPHIC order of ids preserves time
// order; "ORDER BY id" yields creation order because of this.
func TestIDsSortByTime(t *testing.T) {
	base := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	ids := []string{
		models.NewPriceID(base.Add(2 * time.Second)),
		models.NewPriceID(base),
		models.NewPriceID(base.Add(time.Second)),
	}
	want := []string{ids[1], ids[2], ids[0]}

	sort.Strings(ids)
	assert.Equal(t, want, ids)
}

// TestIDHandlesPreEpochTime proves that a timestamp before 1970 does not break
// ordering.
func TestIDHandlesPreEpochTime(t *testing.T) {
	old := models.NewPriceID(time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC))
	epoch := models.NewPriceID(time.Unix(0, 0).UTC())

	assert.Len(t, strings.TrimPrefix(old, models.PriceIDPrefix), models.IDBodyLength())
	assert.Equal(t,
		strings.TrimPrefix(old, models.PriceIDPrefix)[:6],
		strings.TrimPrefix(epoch, models.PriceIDPrefix)[:6],
		"a pre-epoch timestamp must be clamped to the floor")
}

// TestPriceListTypePriority proves the priority ordering; the selection rule's
// first criterion rests on it.
func TestPriceListTypePriority(t *testing.T) {
	assert.Greater(t, models.PriceListOverride.Priority(), models.PriceListSale.Priority())
	assert.Greater(t, models.PriceListSale.Priority(), models.PriceListType("").Priority())
	assert.Equal(t, 0, models.PriceListType("bogus").Priority(),
		"an undefined type cannot get ahead of the base price")
}

// TestPriceListInfoUsable proves every branch of the usability check.
func TestPriceListInfoUsable(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	before := now.Add(-time.Hour)
	after := now.Add(time.Hour)

	for _, tc := range []struct {
		name string
		info models.PriceListInfo
		want bool
	}{
		{"active and windowless", models.PriceListInfo{Status: models.PriceListActive}, true},
		{"draft", models.PriceListInfo{Status: models.PriceListDraft}, false},
		{"expired", models.PriceListInfo{Status: models.PriceListExpired}, false},
		{"status undefined", models.PriceListInfo{Status: models.PriceListStatus("bogus")}, false},
		{"inside the window", models.PriceListInfo{
			Status: models.PriceListActive, StartsAt: &before, EndsAt: &after}, true},
		{"before the start", models.PriceListInfo{
			Status: models.PriceListActive, StartsAt: &after}, false},
		{"after the end", models.PriceListInfo{
			Status: models.PriceListActive, EndsAt: &before}, false},
		{"lower bound only, satisfied", models.PriceListInfo{
			Status: models.PriceListActive, StartsAt: &before}, true},
		{"upper bound only, satisfied", models.PriceListInfo{
			Status: models.PriceListActive, EndsAt: &after}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.info.Usable(now))
		})
	}
}

// TestRuleOperatorClassification proves the operator classification; validation
// and matching rest on these three predicates.
func TestRuleOperatorClassification(t *testing.T) {
	for _, op := range []models.RuleOperator{
		models.OpEq, models.OpNe, models.OpIn, models.OpNin,
		models.OpGt, models.OpGte, models.OpLt, models.OpLte,
	} {
		assert.True(t, op.Valid(), "%s must be defined", op)
	}
	assert.False(t, models.RuleOperator("regex").Valid())
	assert.False(t, models.RuleOperator("").Valid())

	for _, op := range []models.RuleOperator{models.OpGt, models.OpGte, models.OpLt, models.OpLte} {
		assert.True(t, op.Numeric(), "%s must be numeric", op)
		assert.False(t, op.MultiValue(), "%s must take a single value", op)
	}
	for _, op := range []models.RuleOperator{models.OpEq, models.OpNe} {
		assert.False(t, op.Numeric())
		assert.False(t, op.MultiValue())
	}
	for _, op := range []models.RuleOperator{models.OpIn, models.OpNin} {
		assert.False(t, op.Numeric())
		assert.True(t, op.MultiValue(), "%s must take multiple values", op)
	}
	assert.False(t, models.RuleOperator("regex").Numeric())
}

// TestPriceListStatusValid proves the status validation.
func TestPriceListStatusValid(t *testing.T) {
	assert.True(t, models.PriceListDraft.Valid())
	assert.True(t, models.PriceListActive.Valid())
	assert.True(t, models.PriceListExpired.Valid())
	assert.False(t, models.PriceListStatus("bogus").Valid())
	assert.False(t, models.PriceListStatus("").Valid())
}

// TestPriceListTypeValid proves the type validation.
func TestPriceListTypeValid(t *testing.T) {
	assert.True(t, models.PriceListSale.Valid())
	assert.True(t, models.PriceListOverride.Valid())
	assert.False(t, models.PriceListType("bogus").Valid())
	assert.False(t, models.PriceListType("").Valid())
}
