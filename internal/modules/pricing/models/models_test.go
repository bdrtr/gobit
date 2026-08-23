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

// TestIDsArePrefixedAndFixedLength kimliklerin önekli ve sabit uzunlukta
// olduğunu kanıtlar (plan Bölüm 8).
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
			assert.True(t, strings.HasPrefix(tc.id, tc.prefix), "kimlik %q önekiyle başlamalı", tc.prefix)
			assert.Len(t, strings.TrimPrefix(tc.id, tc.prefix), models.IDBodyLength())
		})
	}
}

// TestIDsAreUnique aynı milisaniyede üretilen kimliklerin çakışmadığını
// kanıtlar.
func TestIDsAreUnique(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	seen := make(map[string]struct{}, 1000)
	for range 1000 {
		id := models.NewPriceID(now)
		_, dup := seen[id]
		require.False(t, dup, "aynı anda üretilen kimlikler benzersiz olmalı: %s", id)
		seen[id] = struct{}{}
	}
}

// TestIDsSortByTime kimliklerin SÖZLÜKSEL sırasının zaman sırasını koruduğunu
// kanıtlar; "ORDER BY id" bu sayede oluşturma sırasını verir.
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

// TestIDHandlesPreEpochTime 1970 öncesi bir zaman damgasının sıralamayı
// bozmadığını kanıtlar.
func TestIDHandlesPreEpochTime(t *testing.T) {
	old := models.NewPriceID(time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC))
	epoch := models.NewPriceID(time.Unix(0, 0).UTC())

	assert.Len(t, strings.TrimPrefix(old, models.PriceIDPrefix), models.IDBodyLength())
	assert.Equal(t,
		strings.TrimPrefix(old, models.PriceIDPrefix)[:6],
		strings.TrimPrefix(epoch, models.PriceIDPrefix)[:6],
		"epoch öncesi zaman damgası tabana çekilmeli")
}

// TestPriceListTypePriority öncelik sıralamasını kanıtlar; seçim kuralının ilk
// ölçütü buna dayanır.
func TestPriceListTypePriority(t *testing.T) {
	assert.Greater(t, models.PriceListOverride.Priority(), models.PriceListSale.Priority())
	assert.Greater(t, models.PriceListSale.Priority(), models.PriceListType("").Priority())
	assert.Equal(t, 0, models.PriceListType("bogus").Priority(),
		"tanımsız tür taban fiyatın önüne geçemez")
}

// TestPriceListInfoUsable kullanılabilirlik denetiminin her dalını kanıtlar.
func TestPriceListInfoUsable(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	before := now.Add(-time.Hour)
	after := now.Add(time.Hour)

	for _, tc := range []struct {
		name string
		info models.PriceListInfo
		want bool
	}{
		{"aktif ve penceresiz", models.PriceListInfo{Status: models.PriceListActive}, true},
		{"taslak", models.PriceListInfo{Status: models.PriceListDraft}, false},
		{"sonlandırılmış", models.PriceListInfo{Status: models.PriceListExpired}, false},
		{"durum tanımsız", models.PriceListInfo{Status: models.PriceListStatus("bogus")}, false},
		{"pencere içinde", models.PriceListInfo{
			Status: models.PriceListActive, StartsAt: &before, EndsAt: &after}, true},
		{"başlangıçtan önce", models.PriceListInfo{
			Status: models.PriceListActive, StartsAt: &after}, false},
		{"bitişten sonra", models.PriceListInfo{
			Status: models.PriceListActive, EndsAt: &before}, false},
		{"yalnızca alt sınır, sağlanır", models.PriceListInfo{
			Status: models.PriceListActive, StartsAt: &before}, true},
		{"yalnızca üst sınır, sağlanır", models.PriceListInfo{
			Status: models.PriceListActive, EndsAt: &after}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.info.Usable(now))
		})
	}
}

// TestRuleOperatorClassification işleç sınıflandırmasını kanıtlar; doğrulama ve
// eşleştirme bu üç yükleme dayanır.
func TestRuleOperatorClassification(t *testing.T) {
	for _, op := range []models.RuleOperator{
		models.OpEq, models.OpNe, models.OpIn, models.OpNin,
		models.OpGt, models.OpGte, models.OpLt, models.OpLte,
	} {
		assert.True(t, op.Valid(), "%s tanımlı olmalı", op)
	}
	assert.False(t, models.RuleOperator("regex").Valid())
	assert.False(t, models.RuleOperator("").Valid())

	for _, op := range []models.RuleOperator{models.OpGt, models.OpGte, models.OpLt, models.OpLte} {
		assert.True(t, op.Numeric(), "%s sayısal olmalı", op)
		assert.False(t, op.MultiValue(), "%s tek değer almalı", op)
	}
	for _, op := range []models.RuleOperator{models.OpEq, models.OpNe} {
		assert.False(t, op.Numeric())
		assert.False(t, op.MultiValue())
	}
	for _, op := range []models.RuleOperator{models.OpIn, models.OpNin} {
		assert.False(t, op.Numeric())
		assert.True(t, op.MultiValue(), "%s çok değer almalı", op)
	}
	assert.False(t, models.RuleOperator("regex").Numeric())
}

// TestPriceListStatusValid durum doğrulamasını kanıtlar.
func TestPriceListStatusValid(t *testing.T) {
	assert.True(t, models.PriceListDraft.Valid())
	assert.True(t, models.PriceListActive.Valid())
	assert.True(t, models.PriceListExpired.Valid())
	assert.False(t, models.PriceListStatus("bogus").Valid())
	assert.False(t, models.PriceListStatus("").Valid())
}

// TestPriceListTypeValid tür doğrulamasını kanıtlar.
func TestPriceListTypeValid(t *testing.T) {
	assert.True(t, models.PriceListSale.Valid())
	assert.True(t, models.PriceListOverride.Valid())
	assert.False(t, models.PriceListType("bogus").Valid())
	assert.False(t, models.PriceListType("").Valid())
}
