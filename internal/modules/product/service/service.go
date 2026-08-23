// Package service product modülünün iş kurallarını barındırır.
//
// Katmanın sözleşmesi: girdiler doğrulanır, kimlikler burada üretilir, veri
// erişimi [repository.Store] üzerinden yapılır ve dışarıya HER ZAMAN
// core/errors tipli hatalar döner. HTTP durum kodu seçimi API katmanının değil,
// bu katmanın döndürdüğü hata sınıfının işidir.
//
// # Başka modüllerin verisi
//
// Fiyat (pricing) ve stok (inventory) bu modülde YOKTUR ve o modüller İMPORT
// EDİLMEZ (Prensip 2.4, ADR 0001). İhtiyaç duyulan yüzeyler bu pakette dar
// arayüzler olarak tanımlanır ([Linker], [Grapher]) ve somut uygulamaları
// container'dan ADLA çözülür. Store listelemesi fiyat ve stoğu link'ler
// üzerinden Query katmanıyla toplar (ADR 0004).
package service

import (
	"context"
	"log/slog"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
)

// Linker product'ın çekirdeğin link servisinden ihtiyaç duyduğu DAR yüzeydir.
//
// Tam LinkService yerine yalnızca kullanılan üç metot istenir: sözleşme
// büyüdükçe bu modülün testleri ve sahteleri etkilenmez.
type Linker interface {
	// Create fromID ile toID arasında bağ kurar; aynı çift için no-op'tur.
	Create(ctx context.Context, name, fromID, toID string) error
	// Delete bağı kaldırır; bağ yoksa no-op'tur.
	Delete(ctx context.Context, name, fromID, toID string) error
	// List fromID'ye bağlı toID'leri döner.
	List(ctx context.Context, name, fromID string) ([]string, error)
}

// Grapher cross-module okuma yüzeyidir (çekirdeğin Query katmanı).
//
// Store listelemesi varyantların fiyat ve stok kayıtlarını bununla toplar;
// pricing ve inventory modülleri ne import edilir ne de adları bilinir —
// bilinen tek şey link adlarıdır.
type Grapher interface {
	// Graph spec'e göre kök kayıtları çeker ve genişletmeleri uygular.
	Graph(ctx context.Context, spec query.GraphSpec) ([]query.Record, error)
}

// Options servis kurulumunun bağımlılıklarıdır.
type Options struct {
	// Repo zorunludur.
	Repo repository.Store
	// Links link tanımlarını kullanan uçlar için gereklidir; nil verilirse
	// link uçları tipli bir "hazır değil" hatası döner.
	Links Linker
	// Query store listelemesinin fiyat/stok genişletmesi içindir; nil
	// verilirse listeleme fiyatsız ve stoksuz çalışır.
	Query Grapher
	// Logger nil verilirse loglar atılır.
	Logger *slog.Logger
}

// Service product modülünün public servisidir.
//
// Container'a "product.service" adıyla kaydedilir. Tüm metodları
// goroutine-güvenlidir (durum tutmaz; durum veritabanındadır).
type Service struct {
	repo  repository.Store
	links Linker
	graph Grapher
	log   *slog.Logger
}

// New verilen bağımlılıklarla servisi kurar.
//
// Repo verilmezse hata döner: deposuz bir katalog servisi her çağrıda
// patlardı ve bunun kurulumda görülmesi gerekir.
func New(opts Options) (*Service, error) {
	if opts.Repo == nil {
		return nil, errors.Invalid(codeNotReady, "product servisi depo olmadan kurulamaz")
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{repo: opts.Repo, links: opts.Links, graph: opts.Query, log: log}, nil
}

// ListResult sayfalı bir listenin sonucudur.
//
// Count, limit/offset'ten BAĞIMSIZ toplam kayıt sayısıdır; API zarfındaki
// "count" alanının kaynağı budur.
type ListResult[T any] struct {
	Items  []T
	Count  int
	Offset int
	Limit  int
}

// CreateProductInput yeni bir ürünün girdisidir.
//
// Handle boş bırakılırsa başlıktan üretilir. Options/Variants/Images alanları
// doluysa ürün ve alt kayıtları TEK İŞLEMDE yazılır: yarım kalmış bir katalog
// kaydı (varyantsız ürün ya da sahipsiz varyant) oluşmaz.
type CreateProductInput struct {
	Handle        string
	Title         string
	Subtitle      *string
	Description   *string
	Thumbnail     *string
	Status        models.Status
	IsGiftcard    bool
	Discountable  *bool
	Weight        *int32
	Length        *int32
	Height        *int32
	Width         *int32
	Material      *string
	OriginCountry *string
	CollectionID  *string
	Metadata      map[string]any
	Options       []CreateOptionInput
	Variants      []CreateVariantInput
	Images        []CreateImageInput
	TagIDs        []string
	CategoryIDs   []string
}

// CreateImageInput ürüne eklenecek görselin girdisidir.
type CreateImageInput struct {
	URL      string
	Rank     int32
	Metadata map[string]any
}

// UpdateProductInput bir ürünün kısmi güncellemesidir.
//
// PATCH sözleşmesi: nil bırakılan alan DEĞİŞMEZ. Bir alanı NULL'a çekmek bu
// uçtan yapılamaz; bu, "verilmedi" ile "boşalt" ayrımının JSON'da tek bir
// null ile ifade edilememesinin kabul edilmiş bedelidir.
type UpdateProductInput struct {
	Handle        *string
	Title         *string
	Subtitle      *string
	Description   *string
	Thumbnail     *string
	Status        *models.Status
	Discountable  *bool
	Weight        *int32
	Length        *int32
	Height        *int32
	Width         *int32
	Material      *string
	OriginCountry *string
	CollectionID  *string
	Metadata      map[string]any
	TagIDs        []string
	CategoryIDs   []string
}

// ListProductsOptions ürün listelemesinin ölçütleridir.
type ListProductsOptions struct {
	Status       *models.Status
	CollectionID *string
	Handle       *string
	Search       *string
	Limit        int
	Offset       int
	// WithRelations true ise varyantlar, seçenekler, görseller, etiketler ve
	// kategoriler TOPLU sorgularla doldurulur (ürün başına sorgu yapılmaz).
	WithRelations bool
}

// CreateProduct ürün oluşturur ve alt kayıtlarıyla birlikte döner.
//
// Handle benzersizdir: kullanımdaysa errors.Conflict döner. Çakışma iki
// katmanda birden yakalanır — burada okunabilir bir mesajla, veritabanında
// kısmi benzersiz indeksle. İkincisi eşzamanlı iki isteğin arasından
// geçemeyeceğinin tek gerçek garantisidir.
func (s *Service) CreateProduct(ctx context.Context, in CreateProductInput) (models.Product, error) {
	product, err := s.buildProduct(in)
	if err != nil {
		return models.Product{}, err
	}
	if err := s.ensureHandleFree(ctx, product.Handle, ""); err != nil {
		return models.Product{}, err
	}

	options, err := buildOptions(product.ID, in.Options)
	if err != nil {
		return models.Product{}, err
	}
	images, err := buildImages(product.ID, in.Images)
	if err != nil {
		return models.Product{}, err
	}
	tagIDs, err := uniqueIDs("tag_ids", in.TagIDs)
	if err != nil {
		return models.Product{}, err
	}
	categoryIDs, err := uniqueIDs("category_ids", in.CategoryIDs)
	if err != nil {
		return models.Product{}, err
	}

	err = s.repo.InTx(ctx, func(ctx context.Context, tx repository.Store) error {
		if _, err := tx.CreateProduct(ctx, product); err != nil {
			return err
		}
		if _, err := writeOptions(ctx, tx, options); err != nil {
			return err
		}
		for _, img := range images {
			if _, err := tx.CreateImage(ctx, img); err != nil {
				return err
			}
		}
		if len(tagIDs) > 0 {
			if err := tx.SetProductTags(ctx, product.ID, tagIDs); err != nil {
				return err
			}
		}
		if len(categoryIDs) > 0 {
			if err := tx.SetProductCategories(ctx, product.ID, categoryIDs); err != nil {
				return err
			}
		}
		for i, v := range in.Variants {
			if _, err := createVariantTx(ctx, tx, product.ID, v, int32From(i)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return models.Product{}, err
	}

	return s.GetProduct(ctx, product.ID)
}

// GetProduct ürünü ilişkili kayıtlarıyla birlikte döner.
//
// Silinmiş ürün BULUNAMAZ sayılır (errors.NotFound); soft delete'in okuma
// tarafındaki karşılığı budur.
func (s *Service) GetProduct(ctx context.Context, id string) (models.Product, error) {
	if _, err := requireID("id", id); err != nil {
		return models.Product{}, err
	}

	product, err := s.repo.GetProduct(ctx, id)
	if err != nil {
		return models.Product{}, err
	}

	products := []models.Product{product}
	if err := s.attachRelations(ctx, products); err != nil {
		return models.Product{}, err
	}
	return products[0], nil
}

// GetProductByHandle ürünü handle'ına göre döner.
func (s *Service) GetProductByHandle(ctx context.Context, handle string) (models.Product, error) {
	if _, err := requireID("handle", handle); err != nil {
		return models.Product{}, err
	}

	product, err := s.repo.GetProductByHandle(ctx, handle)
	if err != nil {
		return models.Product{}, err
	}

	products := []models.Product{product}
	if err := s.attachRelations(ctx, products); err != nil {
		return models.Product{}, err
	}
	return products[0], nil
}

// ListProducts ölçütlere uyan ürünleri sayfalı döner.
func (s *Service) ListProducts(ctx context.Context, opts ListProductsOptions) (ListResult[models.Product], error) {
	limit, offset, err := normalizePaging(opts.Limit, opts.Offset)
	if err != nil {
		return ListResult[models.Product]{}, err
	}

	filter := repository.ProductFilter{
		CollectionID: opts.CollectionID,
		Handle:       opts.Handle,
		Search:       opts.Search,
		Limit:        limit,
		Offset:       offset,
	}
	if opts.Status != nil {
		status, err := normalizeStatus(*opts.Status)
		if err != nil {
			return ListResult[models.Product]{}, err
		}
		value := status.String()
		filter.Status = &value
	}

	products, err := s.repo.ListProducts(ctx, filter)
	if err != nil {
		return ListResult[models.Product]{}, err
	}
	count, err := s.repo.CountProducts(ctx, filter)
	if err != nil {
		return ListResult[models.Product]{}, err
	}
	if opts.WithRelations {
		if err := s.attachRelations(ctx, products); err != nil {
			return ListResult[models.Product]{}, err
		}
	}

	return ListResult[models.Product]{Items: products, Count: count, Offset: offset, Limit: limit}, nil
}

// UpdateProduct ürünü kısmi olarak günceller.
func (s *Service) UpdateProduct(ctx context.Context, id string, in UpdateProductInput) (models.Product, error) {
	if _, err := requireID("id", id); err != nil {
		return models.Product{}, err
	}

	patch, err := buildProductPatch(in)
	if err != nil {
		return models.Product{}, err
	}
	if patch.Handle != nil {
		if err := s.ensureHandleFree(ctx, *patch.Handle, id); err != nil {
			return models.Product{}, err
		}
	}

	tagIDs, err := uniqueIDs("tag_ids", in.TagIDs)
	if err != nil {
		return models.Product{}, err
	}
	categoryIDs, err := uniqueIDs("category_ids", in.CategoryIDs)
	if err != nil {
		return models.Product{}, err
	}

	err = s.repo.InTx(ctx, func(ctx context.Context, tx repository.Store) error {
		if _, err := tx.UpdateProduct(ctx, id, patch); err != nil {
			return err
		}
		// nil dilim "dokunma", boş dilim "hepsini kaldır" demektir; ayrım
		// ancak işaretçisiz bir dilimin nil'i ile korunabilir.
		if in.TagIDs != nil {
			if err := tx.SetProductTags(ctx, id, tagIDs); err != nil {
				return err
			}
		}
		if in.CategoryIDs != nil {
			if err := tx.SetProductCategories(ctx, id, categoryIDs); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return models.Product{}, err
	}

	return s.GetProduct(ctx, id)
}

// DeleteProduct ürünü ve alt kayıtlarını SOFT siler.
//
// Varyantların fiyat/stok bağları da temizlenir: silinen bir varyantın
// link'i kalırsa başka modüllerin sorguları var olmayan bir varyanta çıkar.
// Link temizliği veritabanı işleminin DIŞINDADIR (link tabloları çekirdeğe
// aittir); bu yüzden başarısızlığı silmeyi geri almaz, uyarı olarak loglanır
// ve yetim bağlar zararsızdır — kimlikler yeniden kullanılmaz.
func (s *Service) DeleteProduct(ctx context.Context, id string) error {
	if _, err := requireID("id", id); err != nil {
		return err
	}

	variantIDs, err := s.repo.ListVariantIDsByProduct(ctx, id)
	if err != nil {
		return err
	}

	err = s.repo.InTx(ctx, func(ctx context.Context, tx repository.Store) error {
		if err := tx.SoftDeleteProduct(ctx, id); err != nil {
			return err
		}
		return tx.SoftDeleteProductChildren(ctx, id)
	})
	if err != nil {
		return err
	}

	for _, variantID := range variantIDs {
		s.cleanupVariantLinks(ctx, variantID)
	}
	return nil
}

// buildProduct girdiyi doğrular ve yazılacak ürün modelini üretir.
func (s *Service) buildProduct(in CreateProductInput) (models.Product, error) {
	title, err := requireText("title", in.Title, maxTitleLen)
	if err != nil {
		return models.Product{}, err
	}
	handle, err := resolveHandle(in.Handle, title)
	if err != nil {
		return models.Product{}, err
	}
	status, err := normalizeStatus(in.Status)
	if err != nil {
		return models.Product{}, err
	}

	subtitle, err := trimOptional(in.Subtitle, "subtitle", maxValueLen)
	if err != nil {
		return models.Product{}, err
	}
	description, err := trimOptional(in.Description, "description", maxDescriptionLen)
	if err != nil {
		return models.Product{}, err
	}
	thumbnail, err := trimOptional(in.Thumbnail, "thumbnail", maxURLLen)
	if err != nil {
		return models.Product{}, err
	}
	material, err := trimOptional(in.Material, "material", maxValueLen)
	if err != nil {
		return models.Product{}, err
	}
	originCountry, err := trimOptional(in.OriginCountry, "origin_country", maxValueLen)
	if err != nil {
		return models.Product{}, err
	}

	discountable := true
	if in.Discountable != nil {
		discountable = *in.Discountable
	}

	return models.Product{
		ID:            newID(prefixProduct),
		Handle:        handle,
		Title:         title,
		Subtitle:      subtitle,
		Description:   description,
		Thumbnail:     thumbnail,
		Status:        status,
		IsGiftcard:    in.IsGiftcard,
		Discountable:  discountable,
		Weight:        in.Weight,
		Length:        in.Length,
		Height:        in.Height,
		Width:         in.Width,
		Material:      material,
		OriginCountry: originCountry,
		CollectionID:  in.CollectionID,
		Metadata:      in.Metadata,
	}, nil
}

// buildProductPatch güncelleme girdisini doğrular ve depo yamasına çevirir.
func buildProductPatch(in UpdateProductInput) (repository.ProductPatch, error) {
	patch := repository.ProductPatch{
		Discountable:  in.Discountable,
		Weight:        in.Weight,
		Length:        in.Length,
		Height:        in.Height,
		Width:         in.Width,
		Material:      in.Material,
		OriginCountry: in.OriginCountry,
		CollectionID:  in.CollectionID,
		Metadata:      in.Metadata,
	}

	if in.Title != nil {
		title, err := requireText("title", *in.Title, maxTitleLen)
		if err != nil {
			return repository.ProductPatch{}, err
		}
		patch.Title = &title
	}
	if in.Handle != nil {
		handle, err := validateHandle(*in.Handle)
		if err != nil {
			return repository.ProductPatch{}, err
		}
		patch.Handle = &handle
	}
	if in.Status != nil {
		status, err := normalizeStatus(*in.Status)
		if err != nil {
			return repository.ProductPatch{}, err
		}
		value := status.String()
		patch.Status = &value
	}

	subtitle, err := trimOptional(in.Subtitle, "subtitle", maxValueLen)
	if err != nil {
		return repository.ProductPatch{}, err
	}
	description, err := trimOptional(in.Description, "description", maxDescriptionLen)
	if err != nil {
		return repository.ProductPatch{}, err
	}
	thumbnail, err := trimOptional(in.Thumbnail, "thumbnail", maxURLLen)
	if err != nil {
		return repository.ProductPatch{}, err
	}
	patch.Subtitle = subtitle
	patch.Description = description
	patch.Thumbnail = thumbnail

	return patch, nil
}

// buildImages görsel girdilerini doğrular ve modele çevirir.
func buildImages(productID string, in []CreateImageInput) ([]models.Image, error) {
	out := make([]models.Image, 0, len(in))
	for i, img := range in {
		url, err := requireText("images[].url", img.URL, maxURLLen)
		if err != nil {
			return nil, err
		}
		rank := img.Rank
		if rank == 0 {
			// Sıra verilmediyse gönderim sırası korunur; aksi hâlde tüm
			// görseller aynı sırada kalır ve listeleme rastgele görünürdü.
			rank = int32From(i)
		}
		out = append(out, models.Image{
			ID:        newID(prefixImage),
			ProductID: productID,
			URL:       url,
			Rank:      rank,
			Metadata:  img.Metadata,
		})
	}
	return out, nil
}

// ensureHandleFree handle'ın başka bir üründe kullanılmadığını doğrular.
//
// exceptID güncellenen ürünün kendi kimliğidir; kendi handle'ı çakışma sayılmaz.
func (s *Service) ensureHandleFree(ctx context.Context, handle, exceptID string) error {
	existing, err := s.repo.GetProductByHandle(ctx, handle)
	switch {
	case err == nil:
		if existing.ID == exceptID {
			return nil
		}
		return errors.Conflict(codeHandleTaken,
			"handle zaten kullanımda: %q (ürün: %s)", handle, existing.ID)
	case errors.IsNotFound(err):
		return nil
	default:
		return err
	}
}

// attachRelations verilen ürünlerin ilişkili kayıtlarını TOPLU doldurur.
//
// Sorgu sayısı ürün sayısından BAĞIMSIZDIR: kaç ürün olursa olsun varyantlar,
// seçenekler, seçenek değerleri, varyant-değer bağları, görseller, etiketler ve
// kategoriler için sabit sayıda sorgu yapılır. Ürün başına sorgu N+1 demek olurdu.
func (s *Service) attachRelations(ctx context.Context, products []models.Product) error {
	if len(products) == 0 {
		return nil
	}
	ids := make([]string, 0, len(products))
	for i := range products {
		ids = append(ids, products[i].ID)
	}

	variants, err := s.repo.ListVariantsByProductIDs(ctx, ids)
	if err != nil {
		return err
	}
	if err := s.attachVariantOptionValues(ctx, variants); err != nil {
		return err
	}

	options, err := s.repo.ListOptionsByProductIDs(ctx, ids)
	if err != nil {
		return err
	}
	options, err = s.attachOptionValues(ctx, options)
	if err != nil {
		return err
	}

	images, err := s.repo.ListImagesByProductIDs(ctx, ids)
	if err != nil {
		return err
	}
	tags, err := s.repo.ListTagsByProductIDs(ctx, ids)
	if err != nil {
		return err
	}
	categories, err := s.repo.ListCategoriesByProductIDs(ctx, ids)
	if err != nil {
		return err
	}

	variantsByProduct := groupBy(variants, func(v models.Variant) string { return v.ProductID })
	optionsByProduct := groupBy(options, func(o models.Option) string { return o.ProductID })

	for i := range products {
		id := products[i].ID
		products[i].Variants = variantsByProduct[id]
		products[i].Options = optionsByProduct[id]
		products[i].Images = images[id]
		products[i].Tags = tags[id]
		products[i].Categories = categories[id]
	}
	return nil
}

// attachVariantOptionValues varyantların seçenek değerlerini TEK sorguda doldurur.
func (s *Service) attachVariantOptionValues(ctx context.Context, variants []models.Variant) error {
	if len(variants) == 0 {
		return nil
	}
	ids := make([]string, 0, len(variants))
	for i := range variants {
		ids = append(ids, variants[i].ID)
	}

	values, err := s.repo.ListVariantOptionValues(ctx, ids)
	if err != nil {
		return err
	}
	for i := range variants {
		variants[i].OptionValues = values[variants[i].ID]
	}
	return nil
}

// attachOptionValues seçeneklerin değerlerini TEK sorguda doldurur.
func (s *Service) attachOptionValues(ctx context.Context, options []models.Option) ([]models.Option, error) {
	if len(options) == 0 {
		return options, nil
	}
	ids := make([]string, 0, len(options))
	for i := range options {
		ids = append(ids, options[i].ID)
	}

	values, err := s.repo.ListOptionValuesByOptionIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byOption := groupBy(values, func(v models.OptionValue) string { return v.OptionID })
	for i := range options {
		options[i].Values = byOption[options[i].ID]
	}
	return options, nil
}

// groupBy dilimi verilen anahtara göre gruplar; sıra korunur.
func groupBy[T any](items []T, key func(T) string) map[string][]T {
	out := make(map[string][]T, len(items))
	for _, item := range items {
		k := key(item)
		out[k] = append(out[k], item)
	}
	return out
}
