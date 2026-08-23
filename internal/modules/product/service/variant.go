package service

import (
	"context"
	"math"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
)

// CreateOptionInput bir seçeneğin (ve değerlerinin) girdisidir.
type CreateOptionInput struct {
	Title  string
	Values []string
	Rank   int32
}

// CreateVariantInput yeni bir varyantın girdisidir.
//
// Varyantın seçenek değerleri İKİ yoldan verilebilir:
//
//   - OptionValueIDs: değerlerin kimlikleri bilindiğinde (varyant mevcut bir
//     ürüne eklenirken).
//   - Options: seçenek BAŞLIĞI -> DEĞER eşlemesi. Ürün ve seçenekleri aynı
//     istekte oluşturulurken değerlerin kimlikleri henüz bilinmez; bu yol o
//     durumda tek pratik çözümdür.
//
// İkisi birden verilebilir; aynı seçeneğe iki farklı değer düşerse istek
// reddedilir (sessizce biri seçilseydi hangi varyantın oluştuğu tahmin edilemezdi).
type CreateVariantInput struct {
	Title           string
	SKU             *string
	Barcode         *string
	EAN             *string
	UPC             *string
	ManageInventory *bool
	AllowBackorder  *bool
	Weight          *int32
	Rank            *int32
	Metadata        map[string]any
	OptionValueIDs  []string
	Options         map[string]string
}

// UpdateVariantInput bir varyantın kısmi güncellemesidir; nil alan değişmez.
type UpdateVariantInput struct {
	Title           *string
	SKU             *string
	Barcode         *string
	EAN             *string
	UPC             *string
	ManageInventory *bool
	AllowBackorder  *bool
	Weight          *int32
	Rank            *int32
	Metadata        map[string]any
	// OptionValueIDs nil ise seçenek değerleri değişmez; verilirse varyantın
	// TÜM seçenek değerleri bu kümeyle değiştirilir.
	OptionValueIDs []string
}

// ListVariantsOptions varyant listelemesinin ölçütleridir.
type ListVariantsOptions struct {
	ProductID *string
	Limit     int
	Offset    int
	// WithOptionValues true ise varyantların seçenek değerleri TEK sorguyla
	// doldurulur.
	WithOptionValues bool
}

// binding varyantın bir seçenekteki değerini gösterir.
type binding struct {
	optionID string
	valueID  string
}

// CreateVariant ürüne varyant ekler.
//
// Ürün yoksa (ya da silinmişse) errors.NotFound döner: varyantın sahibi
// olmadan yazılması, hiçbir listede görünmeyen bir kayıt üretirdi.
//
// Ürün kontrolü varyantla AYNI İŞLEMDE ve satır kilidiyle yapılır. İşlemin
// dışında yapılsaydı eşzamanlı bir DeleteProduct kontrol ile INSERT arasına
// girebilirdi: silme SOFT olduğu için product_variant üzerindeki foreign key
// boşluğu kapatmaz ve ortaya deleted_at'i NULL olan, sahibi silinmiş bir
// varyant çıkardı — admin uçlarında ve "variant.query" sağlayıcısında görünen,
// ama hiçbir ürüne bağlı olmayan bir kayıt. Kilit iki isteği sıraya dizer
// (bkz. repository.Store.GetProductForUpdate).
func (s *Service) CreateVariant(ctx context.Context, productID string, in CreateVariantInput) (models.Variant, error) {
	if _, err := requireID("product_id", productID); err != nil {
		return models.Variant{}, err
	}

	var created models.Variant
	err := s.repo.InTx(ctx, func(ctx context.Context, tx repository.Store) error {
		if _, err := tx.GetProductForUpdate(ctx, productID); err != nil {
			return err
		}
		v, err := createVariantTx(ctx, tx, productID, in, 0)
		if err != nil {
			return err
		}
		created = v
		return nil
	})
	if err != nil {
		return models.Variant{}, err
	}

	return s.GetVariant(ctx, created.ID)
}

// GetVariant varyantı seçenek değerleriyle birlikte döner.
func (s *Service) GetVariant(ctx context.Context, id string) (models.Variant, error) {
	if _, err := requireID("id", id); err != nil {
		return models.Variant{}, err
	}

	variant, err := s.repo.GetVariant(ctx, id)
	if err != nil {
		return models.Variant{}, err
	}

	variants := []models.Variant{variant}
	if err := s.attachVariantOptionValues(ctx, variants); err != nil {
		return models.Variant{}, err
	}
	return variants[0], nil
}

// ListVariants ölçütlere uyan varyantları sayfalı döner.
func (s *Service) ListVariants(ctx context.Context, opts ListVariantsOptions) (ListResult[models.Variant], error) {
	limit, offset, err := normalizePaging(opts.Limit, opts.Offset)
	if err != nil {
		return ListResult[models.Variant]{}, err
	}

	filter := repository.VariantFilter{ProductID: opts.ProductID, Limit: limit, Offset: offset}
	variants, err := s.repo.ListVariants(ctx, filter)
	if err != nil {
		return ListResult[models.Variant]{}, err
	}
	count, err := s.repo.CountVariants(ctx, filter)
	if err != nil {
		return ListResult[models.Variant]{}, err
	}
	if opts.WithOptionValues {
		if err := s.attachVariantOptionValues(ctx, variants); err != nil {
			return ListResult[models.Variant]{}, err
		}
	}

	return ListResult[models.Variant]{Items: variants, Count: count, Offset: offset, Limit: limit}, nil
}

// UpdateVariant varyantı kısmi olarak günceller.
func (s *Service) UpdateVariant(ctx context.Context, id string, in UpdateVariantInput) (models.Variant, error) {
	if _, err := requireID("id", id); err != nil {
		return models.Variant{}, err
	}

	patch := repository.VariantPatch{
		SKU:             in.SKU,
		Barcode:         in.Barcode,
		EAN:             in.EAN,
		UPC:             in.UPC,
		ManageInventory: in.ManageInventory,
		AllowBackorder:  in.AllowBackorder,
		Weight:          in.Weight,
		Rank:            in.Rank,
		Metadata:        in.Metadata,
	}
	if in.Title != nil {
		title, err := requireText("title", *in.Title, maxTitleLen)
		if err != nil {
			return models.Variant{}, err
		}
		patch.Title = &title
	}

	err := s.repo.InTx(ctx, func(ctx context.Context, tx repository.Store) error {
		updated, err := tx.UpdateVariant(ctx, id, patch)
		if err != nil {
			return err
		}
		if in.OptionValueIDs == nil {
			return nil
		}
		return replaceOptionValues(ctx, tx, updated.ProductID, id, in.OptionValueIDs)
	})
	if err != nil {
		return models.Variant{}, err
	}

	return s.GetVariant(ctx, id)
}

// DeleteVariant varyantı SOFT siler ve fiyat/stok bağlarını temizler.
func (s *Service) DeleteVariant(ctx context.Context, id string) error {
	if _, err := requireID("id", id); err != nil {
		return err
	}
	if err := s.repo.SoftDeleteVariant(ctx, id); err != nil {
		return err
	}

	s.cleanupVariantLinks(ctx, id)
	return nil
}

// SetVariantOptionValues varyantın seçenek değerlerini verilen kümeyle değiştirir.
func (s *Service) SetVariantOptionValues(ctx context.Context, variantID string, valueIDs []string) error {
	if _, err := requireID("variant_id", variantID); err != nil {
		return err
	}

	variant, err := s.repo.GetVariant(ctx, variantID)
	if err != nil {
		return err
	}
	return s.repo.InTx(ctx, func(ctx context.Context, tx repository.Store) error {
		return replaceOptionValues(ctx, tx, variant.ProductID, variantID, valueIDs)
	})
}

// CreateOption ürüne seçenek (ve verilen değerlerini) ekler.
//
// Dönen kayıt SAKLANAN satırdır, bellekteki model değil: zaman damgaları
// yalnızca veritabanında üretilir ve modelin sıfır damgalarını dönmek
// yanıtta "0001-01-01T00:00:00Z" üretirdi. Diğer tüm create uçları da
// veritabanının döndürdüğü satırı döner; bu sözleşme ortaktır.
func (s *Service) CreateOption(ctx context.Context, productID string, in CreateOptionInput) (models.Option, error) {
	if _, err := requireID("product_id", productID); err != nil {
		return models.Option{}, err
	}
	options, err := buildOptions(productID, []CreateOptionInput{in})
	if err != nil {
		return models.Option{}, err
	}

	var created models.Option
	err = s.repo.InTx(ctx, func(ctx context.Context, tx repository.Store) error {
		// Ürünün varlığı İŞLEMİN İÇİNDE ve satır kilidiyle doğrulanır.
		// Dışarıda doğrulanırsa araya giren bir DELETE /admin/v1/products/{id}
		// sonrası sahibi silinmiş ama deleted_at'i NULL olan bir seçenek kalır;
		// silme SOFT olduğu için foreign key bu boşluğu kapatmaz
		// (bkz. CreateVariant'taki aynı desen).
		if _, err := tx.GetProductForUpdate(ctx, productID); err != nil {
			return err
		}

		stored, err := writeOptions(ctx, tx, options)
		if err != nil {
			return err
		}
		created = stored[0]
		return nil
	})
	if err != nil {
		return models.Option{}, err
	}
	return created, nil
}

// ListOptions ürünün seçeneklerini değerleriyle birlikte döner.
func (s *Service) ListOptions(ctx context.Context, productID string) ([]models.Option, error) {
	if _, err := requireID("product_id", productID); err != nil {
		return nil, err
	}

	options, err := s.repo.ListOptionsByProductIDs(ctx, []string{productID})
	if err != nil {
		return nil, err
	}
	return s.attachOptionValues(ctx, options)
}

// AddOptionValue mevcut bir seçeneğe değer ekler.
//
// Yeni değer listenin SONUNA konur: sırası, seçeneğin mevcut en büyük sırasının
// bir fazlasıdır. Sıra doldurulmasaydı sıfır değer (0) yazılırdı ve okuma
// sıraya göre yapıldığı için "S(0), M(1), L(2)" tanımlı bir seçeneğe eklenen
// "XL" listenin sonuna değil BAŞINA düşerdi: "S, XL, M, L".
func (s *Service) AddOptionValue(ctx context.Context, optionID, value string) (models.OptionValue, error) {
	if _, err := requireID("option_id", optionID); err != nil {
		return models.OptionValue{}, err
	}
	clean, err := requireText("value", value, maxValueLen)
	if err != nil {
		return models.OptionValue{}, err
	}
	option, err := s.repo.GetOption(ctx, optionID)
	if err != nil {
		return models.OptionValue{}, err
	}
	existing, err := s.repo.ListOptionValuesByOptionIDs(ctx, []string{option.ID})
	if err != nil {
		return models.OptionValue{}, err
	}

	return s.repo.CreateOptionValue(ctx, models.OptionValue{
		ID:       newID(prefixOptionValue),
		OptionID: option.ID,
		Value:    clean,
		Rank:     nextRank(existing),
	})
}

// nextRank verilen değerlerin ARDINA eklenecek sırayı üretir.
//
// Boş listede 0'dır. Taşma math.MaxInt32'de doyurulur: taşan bir sıra negatife
// döner ve "sona ekle" isteği değeri listenin başına taşırdı.
func nextRank(values []models.OptionValue) int32 {
	highest := int32(-1)
	for i := range values {
		if values[i].Rank > highest {
			highest = values[i].Rank
		}
	}
	if highest == math.MaxInt32 {
		return math.MaxInt32
	}
	return highest + 1
}

// DeleteOption seçeneği SOFT siler.
func (s *Service) DeleteOption(ctx context.Context, id string) error {
	if _, err := requireID("id", id); err != nil {
		return err
	}
	return s.repo.SoftDeleteOption(ctx, id)
}

// buildOptions seçenek girdilerini doğrular ve kimlikli modellere çevirir.
func buildOptions(productID string, in []CreateOptionInput) ([]models.Option, error) {
	out := make([]models.Option, 0, len(in))
	seenTitles := make(map[string]struct{}, len(in))

	for i, opt := range in {
		title, err := requireText("options[].title", opt.Title, maxTitleLen)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(title)
		if _, dup := seenTitles[key]; dup {
			return nil, invalid("aynı seçenek başlığı iki kez verildi: %q", title)
		}
		seenTitles[key] = struct{}{}

		rank := opt.Rank
		if rank == 0 {
			rank = int32From(i)
		}
		option := models.Option{
			ID:        newID(prefixOption),
			ProductID: productID,
			Title:     title,
			Rank:      rank,
		}

		seenValues := make(map[string]struct{}, len(opt.Values))
		for j, raw := range opt.Values {
			value, err := requireText("options[].values[]", raw, maxValueLen)
			if err != nil {
				return nil, err
			}
			if _, dup := seenValues[strings.ToLower(value)]; dup {
				return nil, invalid("%q seçeneğinde aynı değer iki kez verildi: %q", title, value)
			}
			seenValues[strings.ToLower(value)] = struct{}{}

			option.Values = append(option.Values, models.OptionValue{
				ID:          newID(prefixOptionValue),
				OptionID:    option.ID,
				Value:       value,
				Rank:        int32From(j),
				OptionTitle: title,
			})
		}
		out = append(out, option)
	}
	return out, nil
}

// writeOptions seçenekleri ve değerlerini yazar; SAKLANAN satırları döner.
//
// Dönen satırlar veritabanının RETURNING ile verdikleridir. Zaman damgaları
// orada üretilir, bellekteki model onları taşımaz; yazılan modeli geri dönmek
// istemciye sıfır damga göstermek olurdu.
func writeOptions(ctx context.Context, tx repository.Store, options []models.Option) ([]models.Option, error) {
	stored := make([]models.Option, 0, len(options))
	for i := range options {
		option, err := tx.CreateOption(ctx, options[i])
		if err != nil {
			return nil, err
		}
		option.Values = make([]models.OptionValue, 0, len(options[i].Values))
		for j := range options[i].Values {
			value, err := tx.CreateOptionValue(ctx, options[i].Values[j])
			if err != nil {
				return nil, err
			}
			// OptionTitle bir SÜTUN DEĞİLDİR (bkz. models.OptionValue); RETURNING
			// onu doldurmaz, bu yüzden yazılan modelden taşınır.
			value.OptionTitle = options[i].Values[j].OptionTitle
			option.Values = append(option.Values, value)
		}
		stored = append(stored, option)
	}
	return stored, nil
}

// createVariantTx varyantı ve seçenek bağlarını AÇIK BİR İŞLEMDE yazar.
//
// fallbackRank, sıra verilmediğinde kullanılan gönderim sırasıdır.
func createVariantTx(
	ctx context.Context,
	tx repository.Store,
	productID string,
	in CreateVariantInput,
	fallbackRank int32,
) (models.Variant, error) {
	title, err := requireText("variants[].title", in.Title, maxTitleLen)
	if err != nil {
		return models.Variant{}, err
	}
	sku, err := trimOptional(in.SKU, "sku", maxValueLen)
	if err != nil {
		return models.Variant{}, err
	}
	barcode, err := trimOptional(in.Barcode, "barcode", maxValueLen)
	if err != nil {
		return models.Variant{}, err
	}
	ean, err := trimOptional(in.EAN, "ean", maxValueLen)
	if err != nil {
		return models.Variant{}, err
	}
	upc, err := trimOptional(in.UPC, "upc", maxValueLen)
	if err != nil {
		return models.Variant{}, err
	}

	manageInventory := true
	if in.ManageInventory != nil {
		manageInventory = *in.ManageInventory
	}
	allowBackorder := false
	if in.AllowBackorder != nil {
		allowBackorder = *in.AllowBackorder
	}
	rank := fallbackRank
	if in.Rank != nil {
		rank = *in.Rank
	}

	bindings, err := resolveBindings(ctx, tx, productID, in.OptionValueIDs, in.Options)
	if err != nil {
		return models.Variant{}, err
	}

	variant, err := tx.CreateVariant(ctx, models.Variant{
		ID:              newID(prefixVariant),
		ProductID:       productID,
		Title:           title,
		SKU:             sku,
		Barcode:         barcode,
		EAN:             ean,
		UPC:             upc,
		ManageInventory: manageInventory,
		AllowBackorder:  allowBackorder,
		Weight:          in.Weight,
		Rank:            rank,
		Metadata:        in.Metadata,
	})
	if err != nil {
		return models.Variant{}, err
	}

	for _, b := range bindings {
		if err := tx.SetVariantOptionValue(ctx, variant.ID, b.optionID, b.valueID); err != nil {
			return models.Variant{}, err
		}
	}
	return variant, nil
}

// replaceOptionValues varyantın seçenek değerlerini verilen kümeyle değiştirir.
func replaceOptionValues(ctx context.Context, tx repository.Store, productID, variantID string, valueIDs []string) error {
	bindings, err := resolveBindings(ctx, tx, productID, valueIDs, nil)
	if err != nil {
		return err
	}
	if err := tx.DeleteVariantOptionValues(ctx, variantID); err != nil {
		return err
	}
	for _, b := range bindings {
		if err := tx.SetVariantOptionValue(ctx, variantID, b.optionID, b.valueID); err != nil {
			return err
		}
	}
	return nil
}

// resolveBindings verilen değer kimliklerini ve başlık->değer eşlemesini
// varyanta yazılacak bağlara çevirir.
//
// İki doğrulama zorunludur:
//
//   - Değer GERÇEKTEN var olmalıdır. Var olmayan bir kimlik sessizce atlansaydı
//     varyant eksik seçeneklerle oluşur ve hata ancak vitrinde görülürdü.
//   - Değer AYNI ÜRÜNÜN seçeneğinden gelmelidir. Başka bir ürünün değeri
//     bağlanabilseydi, o ürünün seçeneği silindiğinde bu varyant da bozulurdu.
func resolveBindings(
	ctx context.Context,
	store repository.Store,
	productID string,
	valueIDs []string,
	byTitle map[string]string,
) ([]binding, error) {
	byOption := make(map[string]string, len(valueIDs)+len(byTitle))

	if len(valueIDs) > 0 {
		ids, err := uniqueIDs("option_value_ids", valueIDs)
		if err != nil {
			return nil, err
		}
		refs, err := store.ListOptionValuesByIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		found := make(map[string]models.OptionValueRef, len(refs))
		for i := range refs {
			found[refs[i].ID] = refs[i]
		}
		for _, id := range ids {
			ref, ok := found[id]
			if !ok {
				return nil, invalid("seçenek değeri bulunamadı: %s", id)
			}
			if ref.ProductID != productID {
				return nil, invalid(
					"seçenek değeri %s başka bir ürüne ait (%s); varyanta yalnızca kendi ürününün değerleri bağlanabilir",
					id, ref.ProductID)
			}
			if err := bind(byOption, ref.OptionID, ref.ID, ref.OptionTitle); err != nil {
				return nil, err
			}
		}
	}

	if len(byTitle) > 0 {
		resolved, err := resolveByTitle(ctx, store, productID, byTitle)
		if err != nil {
			return nil, err
		}
		for optionID, value := range resolved {
			if err := bind(byOption, optionID, value.id, value.title); err != nil {
				return nil, err
			}
		}
	}

	out := make([]binding, 0, len(byOption))
	for optionID, valueID := range byOption {
		out = append(out, binding{optionID: optionID, valueID: valueID})
	}
	// Sıra belirli olmalıdır: aksi hâlde aynı girdi her çalıştırmada farklı
	// yazma sırası üretir ve testler kararsızlaşır.
	slices.SortFunc(out, func(a, b binding) int { return strings.Compare(a.optionID, b.optionID) })
	return out, nil
}

// bind bir seçeneğe değer atar; aynı seçeneğe FARKLI ikinci bir değer gelirse
// hata döner.
func bind(byOption map[string]string, optionID, valueID, optionTitle string) error {
	if existing, ok := byOption[optionID]; ok && existing != valueID {
		return invalid("%q seçeneği için iki farklı değer verildi (%s ve %s)", optionTitle, existing, valueID)
	}
	byOption[optionID] = valueID
	return nil
}

// titleValue başlıkla çözülmüş bir değerin kimliği ve seçenek başlığıdır.
type titleValue struct {
	id    string
	title string
}

// resolveByTitle "seçenek başlığı -> değer" eşlemesini kimliklere çevirir.
//
// Eşleştirme büyük/küçük harf duyarsızdır: istemcinin "Beden" ile "beden"
// yazması aynı seçeneği göstermelidir.
func resolveByTitle(
	ctx context.Context,
	store repository.Store,
	productID string,
	byTitle map[string]string,
) (map[string]titleValue, error) {
	options, err := store.ListOptionsByProductIDs(ctx, []string{productID})
	if err != nil {
		return nil, err
	}
	if len(options) == 0 {
		return nil, invalid("ürünün tanımlı seçeneği yok; seçenek değeri verilemez (ürün: %s)", productID)
	}

	optionIDs := make([]string, 0, len(options))
	optionByTitle := make(map[string]models.Option, len(options))
	for i := range options {
		optionIDs = append(optionIDs, options[i].ID)
		optionByTitle[strings.ToLower(options[i].Title)] = options[i]
	}

	values, err := store.ListOptionValuesByOptionIDs(ctx, optionIDs)
	if err != nil {
		return nil, err
	}
	valueKey := func(optionID, value string) string { return optionID + "\x00" + strings.ToLower(value) }
	valueByKey := make(map[string]models.OptionValue, len(values))
	for i := range values {
		valueByKey[valueKey(values[i].OptionID, values[i].Value)] = values[i]
	}

	out := make(map[string]titleValue, len(byTitle))
	for title, value := range byTitle {
		option, ok := optionByTitle[strings.ToLower(strings.TrimSpace(title))]
		if !ok {
			return nil, invalid("ürünün %q başlıklı bir seçeneği yok", title)
		}
		match, ok := valueByKey[valueKey(option.ID, strings.TrimSpace(value))]
		if !ok {
			return nil, invalid("%q seçeneğinin %q değeri tanımlı değil", option.Title, value)
		}
		out[option.ID] = titleValue{id: match.ID, title: option.Title}
	}
	return out, nil
}
