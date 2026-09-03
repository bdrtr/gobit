package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestNewRequiresRepo deposuz kurulumun kurulum anında reddedildiğini doğrular.
func TestNewRequiresRepo(t *testing.T) {
	t.Parallel()

	_, err := service.New(service.Options{})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "depo eksikliği doğrulama hatasıdır: %v", err)
}

// TestCreateProductAssignsPrefixedIDs kimliklerin plan Bölüm 8'deki önekleri
// taşıdığını ve ürünün alt kayıtlarıyla birlikte döndüğünü doğrular.
func TestCreateProductAssignsPrefixedIDs(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	product, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Title:  "Tişört",
		Status: models.StatusPublished,
		Options: []service.CreateOptionInput{
			{Title: "Beden", Values: []string{"S", "M"}},
		},
		Variants: []service.CreateVariantInput{
			{Title: "S beden", Options: map[string]string{"Beden": "S"}},
		},
		Images: []service.CreateImageInput{{URL: "https://cdn.example/1.png"}},
	})
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(product.ID, "prod_"), "ürün kimliği prod_ ile başlamalı: %s", product.ID)
	require.Len(t, product.Variants, 1)
	assert.True(t, strings.HasPrefix(product.Variants[0].ID, "variant_"),
		"varyant kimliği variant_ ile başlamalı: %s", product.Variants[0].ID)
	require.Len(t, product.Options, 1)
	assert.True(t, strings.HasPrefix(product.Options[0].ID, "popt_"),
		"seçenek kimliği popt_ ile başlamalı: %s", product.Options[0].ID)
	require.Len(t, product.Options[0].Values, 2)
	assert.True(t, strings.HasPrefix(product.Options[0].Values[0].ID, "poptval_"),
		"seçenek değeri kimliği poptval_ ile başlamalı: %s", product.Options[0].Values[0].ID)
	require.Len(t, product.Images, 1)
	assert.True(t, strings.HasPrefix(product.Images[0].ID, "pimg_"),
		"görsel kimliği pimg_ ile başlamalı: %s", product.Images[0].ID)
}

// TestCreateProductDerivesHandleFromTitle handle verilmediğinde başlıktan
// üretildiğini doğrular (Türkçe harfler ASCII'ye çevrilir).
func TestCreateProductDerivesHandleFromTitle(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	product, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Title: "Şık Tişört  Mavi",
	})
	require.NoError(t, err)
	assert.Equal(t, "sik-tisort-mavi", product.Handle)
}

// TestCreateProductDerivedHandleStaysAddressable uzun bir başlıktan üretilen
// handle'ın ürünü vitrinde ERİŞİLEBİLİR bıraktığını doğrular.
//
// Başlık 255 karaktere kadar olabilir, handle ise 128'e; üretilen slug
// kırpılmasaydı ürün oluşur ama /store/v1/products/{handle} 422 döner ve kayıt
// kendi adresinden hiç açılamazdı.
func TestCreateProductDerivedHandleStaysAddressable(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()

	title := strings.Repeat("uzun baslik ", 20) + "son"
	require.Len(t, title, 243, "başlık sınırın (255) altında ama handle sınırının (128) çok üstünde olmalı")

	product, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Title:  title,
		Status: models.StatusPublished,
	})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(product.Handle), 128, "üretilen handle sınırın içinde kalmalı")
	assert.NotEmpty(t, product.Handle)
	assert.False(t, strings.HasSuffix(product.Handle, "-"), "kırpma sonda tire bırakmamalı")

	// Asıl iddia: ürün kendi handle'ıyla vitrinde açılabilmeli.
	fetched, err := svc.GetStoreProduct(ctx, product.Handle, nil)
	require.NoError(t, err, "üretilen handle ile ürün okunabilmeli")
	assert.Equal(t, product.ID, fetched.ID)
}

// TestCreateProductValidations girdinin reddedildiği durumları doğrular.
func TestCreateProductValidations(t *testing.T) {
	t.Parallel()

	cases := map[string]service.CreateProductInput{
		"başlık boş":          {Title: "   "},
		"geçersiz durum":      {Title: "Tişört", Status: models.Status("yayinda")},
		"geçersiz handle":     {Title: "Tişört", Handle: "Büyük Harf"},
		"boş varyant başlığı": {Title: "Tişört", Variants: []service.CreateVariantInput{{Title: ""}}},
		"boş seçenek başlığı": {Title: "Tişört", Options: []service.CreateOptionInput{{Title: " "}}},
		"tekrarlanan seçenek": {Title: "Tişört", Options: []service.CreateOptionInput{{Title: "Beden"}, {Title: "beden"}}},
		"tekrarlanan değer":   {Title: "Tişört", Options: []service.CreateOptionInput{{Title: "Beden", Values: []string{"S", "s"}}}},
		"boşluklu etiket kimliği": {
			Title:  "Tişört",
			TagIDs: []string{"ptag_1\n"},
		},
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc := newService(t, newMemStore(), newFakeLinker(), nil)

			_, err := svc.CreateProduct(context.Background(), in)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "doğrulama hatası bekleniyordu: %v", err)
		})
	}
}

// TestCreateProductHandleConflict aynı handle'ın ikinci kez kullanılamadığını
// ve hatanın Conflict sınıfında olduğunu doğrular.
func TestCreateProductHandleConflict(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()

	_, err := svc.CreateProduct(ctx, service.CreateProductInput{Handle: "tisort", Title: "Tişört"})
	require.NoError(t, err)

	_, err = svc.CreateProduct(ctx, service.CreateProductInput{Handle: "tisort", Title: "Başka Tişört"})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "çakışma hatası bekleniyordu: %v", err)
	assert.Equal(t, "product_handle_taken", errors.CodeOf(err))
}

// TestCreateProductConflictFromStore ön kontrolün ATLANDIĞI durumda bile
// çakışmanın depodan (veritabanı kısıtının karşılığından) geldiğini doğrular.
//
// Bu, eşzamanlı iki isteğin arasından geçen senaryonun birim testteki
// karşılığıdır: ön kontrol "boş" der, yazma yine de çakışır.
func TestCreateProductConflictFromStore(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	ctx := context.Background()

	_, err := svc.CreateProduct(ctx, service.CreateProductInput{Handle: "tisort", Title: "Tişört"})
	require.NoError(t, err)

	// Ön kontrol artık "bulunamadı" diyor; tek savunma depo kısıtı.
	store.fail("GetProductByHandle", errors.NotFound("product_not_found", "yok"))

	_, err = svc.CreateProduct(ctx, service.CreateProductInput{Handle: "tisort", Title: "Başka Tişört"})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "depodan gelen çakışma korunmalı: %v", err)
}

// TestCreateProductIsSingleTransaction ürün ve alt kayıtlarının TEK işlemde
// yazıldığını doğrular.
func TestCreateProductIsSingleTransaction(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)

	_, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Title:    "Tişört",
		Options:  []service.CreateOptionInput{{Title: "Beden", Values: []string{"S", "M"}}},
		Variants: []service.CreateVariantInput{{Title: "S"}, {Title: "M"}},
		Images:   []service.CreateImageInput{{URL: "https://cdn.example/1.png"}},
	})
	require.NoError(t, err)

	assert.Equal(t, 1, store.callCount("InTx"),
		"ürün, seçenekler, varyantlar ve görseller tek işlemde yazılmalı")
}

// TestCreateProductRollsBackOnVariantFailure varyant yazımı patladığında
// hatanın çağırana ulaştığını doğrular.
func TestCreateProductRollsBackOnVariantFailure(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	store.fail("CreateVariant", errors.Internal("db", "varyant yazılamadı"))
	svc := newService(t, store, newFakeLinker(), nil)

	_, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Title:    "Tişört",
		Variants: []service.CreateVariantInput{{Title: "S"}},
	})
	require.Error(t, err)
	assert.Equal(t, "db", errors.CodeOf(err))
}

// TestCreateVariantBindsOptionValuesByTitle varyantın seçenek değerlerine
// başlık üzerinden bağlandığını doğrular.
func TestCreateVariantBindsOptionValuesByTitle(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	product, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Title:   "Tişört",
		Options: []service.CreateOptionInput{{Title: "Beden", Values: []string{"S", "M"}}},
		Variants: []service.CreateVariantInput{
			{Title: "S beden", Options: map[string]string{"beden": "s"}},
		},
	})
	require.NoError(t, err)

	require.Len(t, product.Variants, 1)
	require.Len(t, product.Variants[0].OptionValues, 1)
	assert.Equal(t, "S", product.Variants[0].OptionValues[0].Value)
	assert.Equal(t, "Beden", product.Variants[0].OptionValues[0].OptionTitle)
}

// TestCreateVariantRejectsUnknownOptionValue tanımsız bir seçenek değerinin
// sessizce atlanmadığını doğrular.
func TestCreateVariantRejectsUnknownOptionValue(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()

	product, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Title:   "Tişört",
		Options: []service.CreateOptionInput{{Title: "Beden", Values: []string{"S"}}},
	})
	require.NoError(t, err)

	_, err = svc.CreateVariant(ctx, product.ID, service.CreateVariantInput{
		Title:   "XL beden",
		Options: map[string]string{"Beden": "XL"},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "tanımsız değer doğrulama hatası vermeli: %v", err)
}

// TestCreateVariantRejectsForeignOptionValue başka bir ürünün seçenek
// değerinin bağlanamadığını doğrular.
func TestCreateVariantRejectsForeignOptionValue(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()

	foreign, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Title:   "Pantolon",
		Options: []service.CreateOptionInput{{Title: "Beden", Values: []string{"42"}}},
	})
	require.NoError(t, err)
	target, err := svc.CreateProduct(ctx, service.CreateProductInput{Title: "Tişört"})
	require.NoError(t, err)

	_, err = svc.CreateVariant(ctx, target.ID, service.CreateVariantInput{
		Title:          "Yanlış",
		OptionValueIDs: []string{foreign.Options[0].Values[0].ID},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "yabancı değer reddedilmeli: %v", err)
}

// TestCreateVariantRequiresProduct var olmayan ürüne varyant eklenemediğini
// doğrular.
func TestCreateVariantRequiresProduct(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	_, err := svc.CreateVariant(context.Background(), "prod_yok", service.CreateVariantInput{Title: "S"})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "bulunamadı bekleniyordu: %v", err)
}

// TestCreateOptionReturnsStoredRow oluşturulan seçeneğin SAKLANAN satır olarak
// döndüğünü doğrular.
//
// Zaman damgalarını veritabanı üretir; bellekteki model dönseydi yanıt
// "created_at":"0001-01-01T00:00:00Z" taşırdı (models.Option'da bu alanlarda
// omitzero yoktur) ve damgaya güvenen istemci yanlış veri okurdu. Diğer tüm
// create uçları saklanan satırı döner; bu uç sözleşmeden sapmamalı.
func TestCreateOptionReturnsStoredRow(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()
	product := seedProduct(t, svc, "tisort", "Tişört")

	option, err := svc.CreateOption(ctx, product.ID, service.CreateOptionInput{
		Title:  "Beden",
		Values: []string{"S", "M"},
	})
	require.NoError(t, err)
	assert.False(t, option.CreatedAt.IsZero(), "seçenek saklanan satır olarak dönmeli")
	assert.False(t, option.UpdatedAt.IsZero(), "seçenek saklanan satır olarak dönmeli")

	require.Len(t, option.Values, 2, "değerler de dönmeli")
	for _, value := range option.Values {
		assert.False(t, value.CreatedAt.IsZero(),
			"%q değeri saklanan satır olarak dönmeli", value.Value)
	}
}

// TestAddOptionValueAppendsToEnd sonradan eklenen değerin listenin SONUNA
// gittiğini doğrular.
//
// Sıra doldurulmasaydı yeni değer 0 alır ve okuma sıraya göre yapıldığı için
// "S, M, L" tanımlı bir seçeneğe eklenen "XL" başa düşerdi.
func TestAddOptionValueAppendsToEnd(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()
	product := seedProduct(t, svc, "tisort", "Tişört")

	option, err := svc.CreateOption(ctx, product.ID, service.CreateOptionInput{
		Title:  "Beden",
		Values: []string{"S", "M", "L"},
	})
	require.NoError(t, err)

	added, err := svc.AddOptionValue(ctx, option.ID, "XL")
	require.NoError(t, err)
	assert.Equal(t, int32(3), added.Rank, "yeni değer en büyük sıranın bir fazlasını almalı")

	options, err := svc.ListOptions(ctx, product.ID)
	require.NoError(t, err)
	require.Len(t, options, 1)

	values := make([]string, 0, len(options[0].Values))
	for _, value := range options[0].Values {
		values = append(values, value.Value)
	}
	assert.Equal(t, []string{"S", "M", "L", "XL"}, values,
		"sonradan eklenen değer listenin sonunda olmalı")
}

// TestUpdateProductKeepsOwnHandle ürünün kendi handle'ıyla güncellenmesinin
// çakışma sayılmadığını doğrular.
func TestUpdateProductKeepsOwnHandle(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()

	product, err := svc.CreateProduct(ctx, service.CreateProductInput{Handle: "tisort", Title: "Tişört"})
	require.NoError(t, err)

	updated, err := svc.UpdateProduct(ctx, product.ID, service.UpdateProductInput{
		Handle: ptr("tisort"),
		Title:  ptr("Tişört v2"),
	})
	require.NoError(t, err)
	assert.Equal(t, "Tişört v2", updated.Title)
}

// TestUpdateProductRejectsTakenHandle başka bir ürünün handle'ının
// alınamadığını doğrular.
func TestUpdateProductRejectsTakenHandle(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()

	_, err := svc.CreateProduct(ctx, service.CreateProductInput{Handle: "tisort", Title: "Tişört"})
	require.NoError(t, err)
	other, err := svc.CreateProduct(ctx, service.CreateProductInput{Handle: "pantolon", Title: "Pantolon"})
	require.NoError(t, err)

	_, err = svc.UpdateProduct(ctx, other.ID, service.UpdateProductInput{Handle: ptr("tisort")})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "çakışma bekleniyordu: %v", err)
}

// TestDeleteProductSoftDeletesAndCleansLinks silmenin ürünü okumalardan
// düşürdüğünü ve varyantın fiyat/stok bağlarını temizlediğini doğrular.
func TestDeleteProductSoftDeletesAndCleansLinks(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	svc := newService(t, newMemStore(), links, nil)
	ctx := context.Background()

	product := seedProduct(t, svc, "tisort", "Tişört")
	variantID := product.Variants[0].ID
	require.NoError(t, svc.SetVariantPriceSet(ctx, variantID, "pset_1"))
	require.NoError(t, svc.SetVariantInventoryItem(ctx, variantID, "invitem_1"))

	require.NoError(t, svc.DeleteProduct(ctx, product.ID))

	_, err := svc.GetProduct(ctx, product.ID)
	assert.True(t, errors.IsNotFound(err), "silinen ürün okunamamalı: %v", err)
	assert.Empty(t, links.linked(service.LinkVariantPriceSet, variantID),
		"silinen ürünün varyantı fiyat kümesine bağlı kalmamalı")
	assert.Empty(t, links.linked(service.LinkVariantInventory, variantID),
		"silinen ürünün varyantı stok kalemine bağlı kalmamalı")
}

// TestDeleteProductNotFound var olmayan ürünün silinmesinin sessizce
// başarılı olmadığını doğrular.
func TestDeleteProductNotFound(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	err := svc.DeleteProduct(context.Background(), "prod_yok")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "bulunamadı bekleniyordu: %v", err)
}

// TestDeleteVariantCleansLinks varyant silindiğinde bağların temizlendiğini
// doğrular.
func TestDeleteVariantCleansLinks(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	svc := newService(t, newMemStore(), links, nil)
	ctx := context.Background()

	product := seedProduct(t, svc, "tisort", "Tişört")
	variantID := product.Variants[0].ID
	require.NoError(t, svc.SetVariantPriceSet(ctx, variantID, "pset_1"))

	require.NoError(t, svc.DeleteVariant(ctx, variantID))

	_, err := svc.GetVariant(ctx, variantID)
	assert.True(t, errors.IsNotFound(err), "silinen varyant okunamamalı: %v", err)
	assert.Empty(t, links.linked(service.LinkVariantPriceSet, variantID))
}

// TestListProductsPaging sayfalama sözleşmesini doğrular: varsayılan limit,
// üst sınır kırpması ve negatif değerin reddi.
func TestListProductsPaging(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()

	for _, title := range []string{"Bir", "İki", "Üç"} {
		_, err := svc.CreateProduct(ctx, service.CreateProductInput{Title: title})
		require.NoError(t, err)
	}

	result, err := svc.ListProducts(ctx, service.ListProductsOptions{})
	require.NoError(t, err)
	assert.Equal(t, service.DefaultLimit, result.Limit, "limit verilmezse varsayılan kullanılmalı")
	assert.Equal(t, 3, sayac(t, result), "count sayfadan bağımsız toplamdır")
	assert.Len(t, result.Items, 3)

	result, err = svc.ListProducts(ctx, service.ListProductsOptions{Limit: 5000})
	require.NoError(t, err)
	assert.Equal(t, service.MaxLimit, result.Limit, "sınırı aşan limit kırpılmalı")

	result, err = svc.ListProducts(ctx, service.ListProductsOptions{Limit: 2, Offset: 2})
	require.NoError(t, err)
	assert.Equal(t, 3, sayac(t, result), "count sayfalamadan etkilenmemeli")
	assert.Len(t, result.Items, 1)

	_, err = svc.ListProducts(ctx, service.ListProductsOptions{Limit: -1})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "negatif limit reddedilmeli: %v", err)

	_, err = svc.ListProducts(ctx, service.ListProductsOptions{Offset: -1})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "negatif offset reddedilmeli: %v", err)
}

// TestListProductsWithRelationsIsBatched ilişkili kayıtların ürün başına DEĞİL
// toplu okunduğunu doğrular: N+1'in birim testteki kanıtı budur.
func TestListProductsWithRelationsIsBatched(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	ctx := context.Background()

	for _, title := range []string{"Bir", "İki", "Üç", "Dört"} {
		_, err := svc.CreateProduct(ctx, service.CreateProductInput{
			Title:    title,
			Variants: []service.CreateVariantInput{{Title: "Tek"}, {Title: "Çift"}},
		})
		require.NoError(t, err)
	}

	before := map[string]int{
		"ListVariantsByProductIDs": store.callCount("ListVariantsByProductIDs"),
		"ListVariantOptionValues":  store.callCount("ListVariantOptionValues"),
		"ListOptionsByProductIDs":  store.callCount("ListOptionsByProductIDs"),
		"ListImagesByProductIDs":   store.callCount("ListImagesByProductIDs"),
	}

	result, err := svc.ListProducts(ctx, service.ListProductsOptions{WithRelations: true})
	require.NoError(t, err)
	require.Len(t, result.Items, 4)
	require.Len(t, result.Items[0].Variants, 2)

	for name, previous := range before {
		assert.Equal(t, previous+1, store.callCount(name),
			"%s ürün sayısından bağımsız olarak TEK kez çağrılmalı", name)
	}
}

// TestListProductsWithoutRelationsSkipsChildQueries ilişkiler istenmediğinde
// alt sorguların hiç yapılmadığını doğrular.
func TestListProductsWithoutRelationsSkipsChildQueries(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	ctx := context.Background()

	_, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Title:    "Tişört",
		Variants: []service.CreateVariantInput{{Title: "Tek"}},
	})
	require.NoError(t, err)

	before := store.callCount("ListVariantsByProductIDs")
	_, err = svc.ListProducts(ctx, service.ListProductsOptions{})
	require.NoError(t, err)
	assert.Equal(t, before, store.callCount("ListVariantsByProductIDs"),
		"ilişki istenmediyse varyantlar okunmamalı")
}

// TestGetProductNotFound bilinmeyen kimliğin NotFound döndürdüğünü doğrular.
func TestGetProductNotFound(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	_, err := svc.GetProduct(context.Background(), "prod_yok")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "bulunamadı bekleniyordu: %v", err)
}

// TestCreateTagRejectsDuplicate aynı etiketin ikinci kez eklenemediğini
// doğrular.
func TestCreateTagRejectsDuplicate(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()

	tag, err := svc.CreateTag(ctx, " yaz ")
	require.NoError(t, err)
	assert.Equal(t, "yaz", tag.Value, "değer kırpılmalı")
	assert.True(t, strings.HasPrefix(tag.ID, "ptag_"))

	_, err = svc.CreateTag(ctx, "yaz")
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "çakışma bekleniyordu: %v", err)
}

// TestCreateCategoryRequiresExistingParent var olmayan üst kategoriyi
// reddettiğini doğrular.
func TestCreateCategoryRequiresExistingParent(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	_, err := svc.CreateCategory(context.Background(), service.CreateCategoryInput{
		Name:     "Alt",
		ParentID: ptr("pcat_yok"),
	})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "bulunamadı bekleniyordu: %v", err)
}
