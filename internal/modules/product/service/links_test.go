package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestDefinitionsAreTheCrossModuleContract link tanımlarının sözleşmeye
// birebir uyduğunu doğrular.
//
// Bu test bir "değişmesin" testidir ve bilinçlidir: adlar, uçlar ve kardinalite
// pricing ile inventory modüllerinin güvendiği tek sözleşmedir. Ucun modül
// alanının "variant" olması da sözleşmenin parçasıdır — Query bir link'i
// çözerken o alanı ENTITY ADI olarak okur ve sağlayıcıyı "<ad>.query" ile arar;
// "product" yazılsaydı varyant kimlikleri ürün sağlayıcısına sorulur ve
// genişletme sessizce boş dönerdi.
func TestDefinitionsAreTheCrossModuleContract(t *testing.T) {
	t.Parallel()

	defs := service.Definitions()
	require.Len(t, defs, 3)

	byName := map[string]link.LinkDefinition{}
	for _, def := range defs {
		byName[def.Name] = def
		require.NoError(t, def.Validate(), "%q tanımı çekirdeğin doğrulamasından geçmeli", def.Name)
	}

	priceSet, ok := byName["product_variant_price_set"]
	require.True(t, ok, "fiyat bağı bu adla bildirilmeli")
	assert.Equal(t, link.LinkSide{Module: "product", Entity: "variant", Field: "variant_id"}, priceSet.From)
	assert.Equal(t, link.LinkSide{Module: "pricing", Entity: "price_set", Field: "price_set_id"}, priceSet.To)
	assert.Equal(t, link.OneToOne, priceSet.Cardinality)

	inventory, ok := byName["product_variant_inventory"]
	require.True(t, ok, "stok bağı bu adla bildirilmeli")
	assert.Equal(t, link.LinkSide{Module: "product", Entity: "variant", Field: "variant_id"}, inventory.From)
	assert.Equal(t, link.LinkSide{Module: "inventory", Entity: "inventory_item", Field: "inventory_item_id"}, inventory.To)
	assert.Equal(t, link.OneToOne, inventory.Cardinality)

	salesChannel, ok := byName["product_sales_channel"]
	require.True(t, ok, "satış kanalı bağı bu adla bildirilmeli")
	assert.Equal(t, link.LinkSide{Module: "product", Entity: "product", Field: "product_id"}, salesChannel.From,
		"bağ VARYANT değil ÜRÜN düzeyindedir")
	assert.Equal(t, link.LinkSide{Module: "sales_channel", Entity: "sales_channel", Field: "sales_channel_id"},
		salesChannel.To,
		"To ucu auth'un MODÜL adını değil ENTITY adını taşımalı; sağlayıcı o adla kayıtlıdır")
	assert.Equal(t, link.ManyToMany, salesChannel.Cardinality,
		"bir ürün çok kanalda, bir kanal çok üründe olabilir")
}

// TestSetVariantPriceSetReplacesExisting fiyat kümesi değiştirilirken eski
// bağın kaldırıldığını doğrular.
//
// Kaldırılmasaydı OneToOne kardinalitesi yüzünden link servisi çakışma döner ve
// "fiyatı değiştir" isteği hata gibi görünürdü.
func TestSetVariantPriceSetReplacesExisting(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	svc := newService(t, newMemStore(), links, nil)
	ctx := context.Background()

	product := seedProduct(t, svc, "tisort", "Tişört")
	variantID := product.Variants[0].ID

	require.NoError(t, svc.SetVariantPriceSet(ctx, variantID, "pset_1"))
	require.NoError(t, svc.SetVariantPriceSet(ctx, variantID, "pset_2"))

	assert.Equal(t, []string{"pset_2"}, links.linked(service.LinkVariantPriceSet, variantID),
		"varyant tek bir fiyat kümesine bağlı kalmalı")
	assert.Contains(t, links.deletes, service.LinkVariantPriceSet+"|"+variantID+"|pset_1",
		"eski bağ açıkça kaldırılmalı")
}

// TestSetVariantPriceSetIsIdempotent aynı bağın ikinci kez kurulmasının
// gereksiz silme üretmediğini doğrular.
func TestSetVariantPriceSetIsIdempotent(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	svc := newService(t, newMemStore(), links, nil)
	ctx := context.Background()

	product := seedProduct(t, svc, "tisort", "Tişört")
	variantID := product.Variants[0].ID

	require.NoError(t, svc.SetVariantPriceSet(ctx, variantID, "pset_1"))
	require.NoError(t, svc.SetVariantPriceSet(ctx, variantID, "pset_1"))

	assert.Equal(t, []string{"pset_1"}, links.linked(service.LinkVariantPriceSet, variantID))
	assert.Empty(t, links.deletes, "aynı hedefe yeniden bağlamak eski bağı kaldırmamalı")
}

// TestSetVariantPriceSetKeepsExistingLinkWhenTargetIsTaken çakışmayla düşen bir
// yeniden bağlamanın varyantın MEVCUT bağını bozmadığını doğrular.
//
// OneToOne'ın TO ucu da benzersizdir: başka bir varyanta bağlı bir fiyat kümesi
// istendiğinde Create çakışma döner. Eski bağ o noktada zaten silinmiş olursa
// istek 409 döner ("değişmedi" diye okunur) ama varyant fiyatsız kalır ve vitrin
// onu öyle yayınlar — sessiz veri kaybı. Bu yüzden düşen istek eski bağı geri
// kurmak zorundadır.
func TestSetVariantPriceSetKeepsExistingLinkWhenTargetIsTaken(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	svc := newService(t, newMemStore(), links, nil)
	ctx := context.Background()

	product := seedProduct(t, svc, "tisort", "Tişört")
	first := product.Variants[0].ID
	second, err := svc.CreateVariant(ctx, product.ID, service.CreateVariantInput{Title: "İkinci"})
	require.NoError(t, err)

	require.NoError(t, svc.SetVariantPriceSet(ctx, first, "pset_1"))
	require.NoError(t, svc.SetVariantPriceSet(ctx, second.ID, "pset_2"))

	// pset_1 zaten "first" varyantına bağlı; "second" onu isteyemez.
	err = svc.SetVariantPriceSet(ctx, second.ID, "pset_1")
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "çakışma bekleniyordu: %v", err)

	assert.Equal(t, []string{"pset_2"}, links.linked(service.LinkVariantPriceSet, second.ID),
		"düşen istek varyantın mevcut fiyat bağını bozmamalı")
	assert.Equal(t, []string{"pset_1"}, links.linked(service.LinkVariantPriceSet, first),
		"hedefin asıl sahibi de etkilenmemeli")

	// Bağ okunabilir kalmalı: vitrin bu varyantı fiyatsız yayınlamamalı.
	linkIDs, err := svc.VariantLinkIDs(ctx, second.ID)
	require.NoError(t, err)
	require.NotNil(t, linkIDs.PriceSetID)
	assert.Equal(t, "pset_2", *linkIDs.PriceSetID)
}

// TestSetVariantInventoryItemKeepsExistingLinkWhenTargetIsTaken aynı telafinin
// stok bağında da çalıştığını doğrular; iki uç aynı yardımcıyı paylaşır ama
// sözleşme her ikisi için de bağlayıcıdır.
func TestSetVariantInventoryItemKeepsExistingLinkWhenTargetIsTaken(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	svc := newService(t, newMemStore(), links, nil)
	ctx := context.Background()

	product := seedProduct(t, svc, "tisort", "Tişört")
	first := product.Variants[0].ID
	second, err := svc.CreateVariant(ctx, product.ID, service.CreateVariantInput{Title: "İkinci"})
	require.NoError(t, err)

	require.NoError(t, svc.SetVariantInventoryItem(ctx, first, "invitem_1"))
	require.NoError(t, svc.SetVariantInventoryItem(ctx, second.ID, "invitem_2"))

	err = svc.SetVariantInventoryItem(ctx, second.ID, "invitem_1")
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "çakışma bekleniyordu: %v", err)

	assert.Equal(t, []string{"invitem_2"}, links.linked(service.LinkVariantInventory, second.ID),
		"düşen istek varyantın mevcut stok bağını bozmamalı")
}

// TestSetVariantLinkRequiresExistingVariant var olmayan varyanta bağ
// kurulamadığını doğrular.
//
// Link servisi kimlikleri serbest dizge olarak görür ve hiçbir modülün şemasını
// tanımaz; doğrulama yapılmasaydı yazım hatası taşıyan bir kimlik sessizce
// bağlanır ve bağ hiçbir zaman çözülemezdi.
func TestSetVariantLinkRequiresExistingVariant(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	svc := newService(t, newMemStore(), links, nil)

	err := svc.SetVariantPriceSet(context.Background(), "variant_yok", "pset_1")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "bulunamadı bekleniyordu: %v", err)
	assert.Empty(t, links.linked(service.LinkVariantPriceSet, "variant_yok"),
		"doğrulama düşerken bağ kurulmamalı")
}

// TestSetVariantLinkValidatesTargetID hedef kimliğin boş ya da boşluklu
// olamayacağını doğrular.
func TestSetVariantLinkValidatesTargetID(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()
	product := seedProduct(t, svc, "tisort", "Tişört")

	err := svc.SetVariantPriceSet(ctx, product.Variants[0].ID, "")
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "boş kimlik reddedilmeli: %v", err)

	err = svc.SetVariantInventoryItem(ctx, product.Variants[0].ID, "invitem_1\n")
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "boşluk taşıyan kimlik reddedilmeli: %v", err)
}

// TestVariantLinkIDsReportsBothSides varyantın iki bağının da okunabildiğini
// doğrular.
func TestVariantLinkIDsReportsBothSides(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()
	product := seedProduct(t, svc, "tisort", "Tişört")
	variantID := product.Variants[0].ID

	linkIDs, err := svc.VariantLinkIDs(ctx, variantID)
	require.NoError(t, err)
	assert.Nil(t, linkIDs.PriceSetID)
	assert.Nil(t, linkIDs.InventoryItemID)

	require.NoError(t, svc.SetVariantPriceSet(ctx, variantID, "pset_1"))
	require.NoError(t, svc.SetVariantInventoryItem(ctx, variantID, "invitem_1"))

	linkIDs, err = svc.VariantLinkIDs(ctx, variantID)
	require.NoError(t, err)
	require.NotNil(t, linkIDs.PriceSetID)
	require.NotNil(t, linkIDs.InventoryItemID)
	assert.Equal(t, "pset_1", *linkIDs.PriceSetID)
	assert.Equal(t, "invitem_1", *linkIDs.InventoryItemID)
}

// TestClearVariantLinks bağın kaldırılabildiğini doğrular.
func TestClearVariantLinks(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	svc := newService(t, newMemStore(), links, nil)
	ctx := context.Background()
	product := seedProduct(t, svc, "tisort", "Tişört")
	variantID := product.Variants[0].ID

	require.NoError(t, svc.SetVariantInventoryItem(ctx, variantID, "invitem_1"))
	require.NoError(t, svc.ClearVariantInventoryItem(ctx, variantID))

	assert.Empty(t, links.linked(service.LinkVariantInventory, variantID))
}

// TestLinkOperationsWithoutLinker link servisi olmadan kurulan bir serviste
// bağ uçlarının tipli "hazır değil" hatası döndürdüğünü doğrular.
func TestLinkOperationsWithoutLinker(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), nil, nil)
	ctx := context.Background()

	err := svc.SetVariantPriceSet(ctx, "variant_1", "pset_1")
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "erişilemez bekleniyordu: %v", err)

	_, err = svc.VariantLinkIDs(ctx, "variant_1")
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "erişilemez bekleniyordu: %v", err)
}

// TestSetVariantLinkPreservesConflictKind link servisinden gelen kardinalite
// çakışmasının sınıfının korunduğunu doğrular.
//
// Sınıf korunmasaydı istemci düzeltilebilir bir çakışmayı 500 olarak görürdü.
func TestSetVariantLinkPreservesConflictKind(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	links.createErr = errors.Conflict("link_cardinality_violation", "uç zaten bağlı")
	svc := newService(t, newMemStore(), links, nil)
	ctx := context.Background()
	product := seedProduct(t, svc, "tisort", "Tişört")

	err := svc.SetVariantPriceSet(ctx, product.Variants[0].ID, "pset_1")
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "çakışma sınıfı korunmalı: %v", err)
	assert.Equal(t, "product_link_failed", errors.CodeOf(err))
}
