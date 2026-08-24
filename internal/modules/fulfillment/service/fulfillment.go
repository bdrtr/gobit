package service

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// Bu dosya GÖNDERİNİN yaşam döngüsüdür: oluşturma, iptal (saga telafisi),
// kargoya verme ve teslim bildirimi.

// FulfillmentItemInput gönderiye giren tek bir kalemin girdisidir.
type FulfillmentItemInput struct {
	// LineItemID sipariş satırının kimliğidir; zorunludur ve bu modülde
	// DOĞRULANMAZ (Prensip 2.2).
	LineItemID string
	// Quantity gönderiye giren adettir; pozitif olmalıdır.
	Quantity int64
}

// CreateFulfillmentInput yeni bir gönderinin girdisidir.
type CreateFulfillmentInput struct {
	// Reference siparişin kimliğidir; zorunludur ve bu modülde DOĞRULANMAZ.
	Reference string
	// ShippingOptionID kullanılacak kargo seçeneğidir; zorunludur.
	ShippingOptionID string
	// IdempotencyKey aynı gönderinin iki kez oluşturulmasını engeller;
	// zorunludur.
	IdempotencyKey string
	// Items gönderiye giren kalemlerdir; boş olabilir (örn. dijital ürünün
	// kargosuz gönderisi ya da kalem dökümü olmayan toplu sevkiyat).
	Items []FulfillmentItemInput
	// Data sağlayıcıya iletilecek serbest veridir (adres, şube vb.).
	Data map[string]any
	// Metadata çağıranın serbest ek verisidir.
	Metadata map[string]any
}

// CreateFulfillment sağlayıcıda bir gönderi açar ve kaydını üretir.
//
// # Sıra: önce KAYIT, sonra sağlayıcı
//
// Gönderi satırı sağlayıcıya GİTMEDEN ÖNCE yazılır ve sağlayıcıya verilen
// Reference bu satırın kimliğidir. Sebep çekirdek sözleşmesinde yazılıdır
// (internal/core/provider): Reference, mutabakatta iki sistemi eşleştiren
// alandır. Önce sağlayıcıya gidilseydi ve yanıt kaybolsaydı, hangi kaydın
// karşılığı olduğu bilinemeyen bir kargo etiketi kalırdı.
//
// # Kalan risk: BELİRSİZ sağlayıcı hatası
//
// Yukarıdaki sıra, hatanın KESİN olduğu durumda tamdır. Sağlayıcı hata
// dönerse işlemin tamamı geri alınır ve ortada gönderi kalmaz
// (TestSaglayiciHatasiGonderiBirakmaz bunu sabitler). Gerçek bir AĞ
// sağlayıcısında ise hata belirsiz olabilir: etiket basılmış, yanıt zaman
// aşımına uğramıştır. O durumda satır geri alınır ama sağlayıcının tarafında
// Reference=ful_X kalır ve ful_X hiç var olmaz; yeniden denemede aynı anahtarla
// YENİ bir ful_Y satırı açılır ve sağlayıcı eski gönderiyi döner.
//
// Sonuç açıkça kabul edilmiştir: BELİRSİZ hata sonrası mutabakat Reference
// üzerinden KURULAMAZ, eşleştirme [models.Fulfillment.ExternalID] üzerinden
// yapılmalıdır — sağlayıcının kendi kimliği her iki tarafta da aynı kalan tek
// alandır. Aynı sınıf risk payment modülünün capture pivotunda da belgelidir
// (internal/workflows/checkout/doc.go). Kutudan çıkan manuel sağlayıcı bu
// işleme KATILDIĞI için orada böyle bir belirsizlik oluşamaz; risk yalnızca
// süreç dışı sağlayıcılarda görünür ve testler onu gösteremez.
//
// # İdempotency
//
// Aynı IdempotencyKey ile ikinci çağrı YENİ gönderi açmaz, mevcut gönderiyi
// döner. Yarış tek bir deyimde çözülür: satır ON CONFLICT DO NOTHING ile
// yazılır, kaybeden taraf kazananın işlemi bitene kadar BEKLER ve sonra
// tamamlanmış satırı okur. Anahtar aynı ama referans, seçenek YA DA KALEM
// LİSTESİ farklıysa errors.Conflict döner — idempotency "aynı isteği
// tekrarlamak" demektir, "farklı bir isteği eski anahtarla göndermek" değil.
//
// Kalem listesinin de karşılaştırılması şarttır: yalnızca iki alana bakılsaydı,
// düzeltilmiş bir kalem dökümüyle gelen ikinci istek sessizce yutulur, çağıran
// yazıldığını sanır ve gönderi eski kalemleriyle kalırdı. Karşılaştırma
// KÜMEDİR: sıra farkı bir fark sayılmaz, çünkü aynı kalem kümesi hangi sırada
// verilirse verilsin aynı gönderidir.
//
// # Sağlayıcı çağrısı işlemin İÇİNDEDİR
//
// Gerekçe paket belgesindedir: kilit sağlayıcıdan önce bırakılsaydı ikinci
// çağıran, henüz sağlayıcı kimliği yazılmamış YARIM bir gönderi okurdu.
func (s *Service) CreateFulfillment(
	ctx context.Context,
	in CreateFulfillmentInput,
) (models.Fulfillment, error) {
	reference := strings.TrimSpace(in.Reference)
	if err := requireText("referans", reference); err != nil {
		return models.Fulfillment{}, err
	}
	if err := requireID(in.ShippingOptionID, models.ShippingOptionIDPrefix,
		"kargo seçeneği kimliği"); err != nil {
		return models.Fulfillment{}, err
	}
	key := strings.TrimSpace(in.IdempotencyKey)
	if err := requireText("idempotency anahtarı", key); err != nil {
		return models.Fulfillment{}, err
	}
	items, err := normalizeItems(in.Items)
	if err != nil {
		return models.Fulfillment{}, err
	}
	optionID := strings.TrimSpace(in.ShippingOptionID)

	var out models.Fulfillment
	err = s.store.WithTx(ctx, func(ctx context.Context) error {
		option, err := s.store.GetShippingOption(ctx, optionID)
		if err != nil {
			return err
		}
		provider, err := s.providers.Get(option.ProviderID)
		if err != nil {
			return err
		}

		created, inserted, err := s.store.InsertFulfillmentIfAbsent(ctx, models.Fulfillment{
			ID:               models.NewFulfillmentID(),
			Reference:        reference,
			ShippingOptionID: option.ID,
			ProviderID:       option.ProviderID,
			Status:           models.StatusPending,
			IdempotencyKey:   key,
			Metadata:         in.Metadata,
		})
		if err != nil {
			return err
		}
		if !inserted {
			existing, err := s.store.FulfillmentByIdempotencyKey(ctx, key)
			if err != nil {
				return err
			}
			if existing.Reference != reference || existing.ShippingOptionID != option.ID {
				return errors.Conflict(CodeIdempotencyMismatch,
					"aynı idempotency anahtarı farklı bir gönderi için kullanıldı: mevcut %s/%s, istenen %s/%s",
					existing.Reference, existing.ShippingOptionID, reference, option.ID)
			}
			out, err = s.withItems(ctx, existing)
			if err != nil {
				return err
			}
			if saved, wanted := savedItemsKey(out.Items), requestedItemsKey(items); saved != wanted {
				return errors.Conflict(CodeIdempotencyMismatch,
					"aynı idempotency anahtarı farklı bir kalem listesiyle kullanıldı: mevcut [%s], istenen [%s] (%s)",
					saved, wanted, existing.ID)
			}
			s.log.DebugContext(ctx, "mevcut gönderi döndürüldü", "gonderi", existing.ID, "anahtar", key)
			return nil
		}

		result, err := provider.Create(ctx, coreprovider.CreateFulfillmentInput{
			Reference:      created.ID,
			OptionID:       option.ID,
			IdempotencyKey: key,
			Data:           mergeProviderData(option.Data, in.Data),
		})
		if err != nil {
			return err
		}
		if strings.TrimSpace(result.ID) == "" {
			return errors.Internal(CodeProviderContract,
				"sağlayıcı %q boş bir gönderi kimliği döndü (%s)", option.ProviderID, created.ID)
		}
		status, err := providerStatus(result.Status, option.ProviderID)
		if err != nil {
			return err
		}

		now := s.now()
		updated, err := s.store.UpdateFulfillmentProviderResult(ctx,
			created.ID, result.ID, status,
			result.TrackingNumber, result.TrackingURL, result.Data,
			stampFor(status, models.StatusShipped, now),
			stampFor(status, models.StatusDelivered, now),
			stampFor(status, models.StatusCanceled, now),
		)
		if err != nil {
			return err
		}

		updated.Items = make([]models.FulfillmentItem, 0, len(items))
		for _, item := range items {
			saved, err := s.store.CreateFulfillmentItem(ctx, models.FulfillmentItem{
				ID:            models.NewFulfillmentItemID(),
				FulfillmentID: updated.ID,
				LineItemID:    item.LineItemID,
				Quantity:      item.Quantity,
			})
			if err != nil {
				return err
			}
			updated.Items = append(updated.Items, saved)
		}

		out = updated
		return nil
	})
	if err != nil {
		return models.Fulfillment{}, err
	}
	return out, nil
}

// CancelFulfillment gönderiyi iptal eder.
//
// SAGA TELAFİSİ BUDUR ve İDEMPOTENTTİR: zaten iptal edilmiş bir gönderi için
// hata dönmez, sağlayıcıya İKİNCİ KEZ gidilmez ve kayıtta değişiklik yapılmaz.
// Telafinin tekrar çalıştırılabilir olması bir tercih değil, saga'nın çalışma
// şartıdır (plan Bölüm 5.5).
//
// TESLİM EDİLMİŞ bir gönderi iptal EDİLEMEZ (errors.Conflict). Gerekçe
// [models.FulfillmentStatus.CancelAction] tablosundadır: teslim geri
// alınamayan fiziksel bir olgudur ve çaresi iptal değil İADEDİR. Kural,
// payment modülünde tahsil edilmiş bir oturumun iptal edilemeyip iade
// edilmesiyle birebir aynıdır.
//
// Bilinmeyen bir kimlik için errors.NotFound döner: idempotentlik "her şeyi
// sessizce yut" demek değildir. İki kez iptal edilen GERÇEK bir gönderi ile
// hiç var olmamış bir kimlik farklı durumlardır ve ikincisi çağıran tarafta
// bir hatadır. Kayıt silinmediği (yalnızca durumu değiştiği) için ilk durum
// her zaman ayırt edilebilir.
func (s *Service) CancelFulfillment(ctx context.Context, id string) error {
	if err := requireID(id, models.FulfillmentIDPrefix, "gönderi kimliği"); err != nil {
		return err
	}

	return s.store.WithTx(ctx, func(ctx context.Context) error {
		ful, err := s.store.LockFulfillment(ctx, id)
		if err != nil {
			return err
		}

		switch ful.Status.CancelAction() {
		case models.ActionNoop:
			s.log.DebugContext(ctx, "gönderi zaten iptal edilmiş", "gonderi", id)
			return nil
		case models.ActionConflict:
			return errors.Conflict(CodeInvalidTransition,
				"%q durumundaki gönderi iptal edilemez; iade akışı kullanılmalı: %s", ful.Status, id)
		case models.ActionProceed:
			// Aşağıda ele alınır.
		}

		provider, err := s.providers.Get(ful.ProviderID)
		if err != nil {
			return err
		}
		// Sağlayıcı kimliği boşsa gönderi sağlayıcıya hiç ulaşmamıştır ve
		// iptal edilecek bir dış kayıt yoktur; yalnızca bizim satırımız
		// kapatılır. Bu durum, sağlayıcı yanıtı gelmeden düşen bir işlemin
		// geri alınmasıyla oluşamaz (satır da geri alınır), ama elle yapılan
		// bir müdahale böyle bir satır bırakabilir.
		if strings.TrimSpace(ful.ExternalID) != "" {
			if err := provider.Cancel(ctx, ful.ExternalID); err != nil {
				return err
			}
		}

		now := s.now()
		_, err = s.store.UpdateFulfillmentStatus(ctx, ful.ID, models.StatusCanceled,
			ful.TrackingNumber, ful.TrackingURL,
			ful.ShippedAt, ful.DeliveredAt, &now)
		return err
	})
}

// MarkShipped gönderiyi kargoya verilmiş olarak işaretler.
//
// SAĞLAYICIYA GİDİLMEZ: bu metot, kargo firmasının BİLDİRDİĞİ olguyu kaydeder
// (webhook ya da yönetici işlemi). Sağlayıcıya "bunu gönder" demek gönderiyi
// oluşturmaktır ve o [Service.CreateFulfillment]'tır; buradan sağlayıcıyı
// çağırmak, aynı olgunun iki kez tetiklenmesi demek olurdu.
//
// Zaten kargoya verilmiş bir gönderide, AYNI takip numarasıyla (ya da boş
// numarayla) yapılan ikinci çağrı hata DÖNMEZ; FARKLI bir takip numarası
// istenirse errors.Conflict döner, çünkü bu artık bir tekrar değil, yeni bir
// istektir.
func (s *Service) MarkShipped(
	ctx context.Context,
	id, trackingNumber, trackingURL string,
) (models.Fulfillment, error) {
	if err := requireID(id, models.FulfillmentIDPrefix, "gönderi kimliği"); err != nil {
		return models.Fulfillment{}, err
	}
	number := strings.TrimSpace(trackingNumber)
	if err := checkTextLen("takip numarası", number); err != nil {
		return models.Fulfillment{}, err
	}
	url := strings.TrimSpace(trackingURL)
	if err := checkTextLen("takip adresi", url); err != nil {
		return models.Fulfillment{}, err
	}

	var out models.Fulfillment
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		ful, err := s.store.LockFulfillment(ctx, id)
		if err != nil {
			return err
		}

		switch ful.Status.ShipAction() {
		case models.ActionNoop:
			if number != "" && number != ful.TrackingNumber {
				return errors.Conflict(CodeInvalidTransition,
					"gönderi %q takip numarasıyla kargoya verilmiş; %q ile yeniden verilemez (%s)",
					ful.TrackingNumber, number, id)
			}
			out = ful
			s.log.DebugContext(ctx, "gönderi zaten kargoya verilmiş", "gonderi", id)
			return nil
		case models.ActionConflict:
			return errors.Conflict(CodeInvalidTransition,
				"%q durumundaki gönderi kargoya verilemez: %s", ful.Status, id)
		case models.ActionProceed:
			// Aşağıda ele alınır.
		}

		// Boş verilen takip bilgisi MEVCUDU SİLMEZ: sağlayıcı gönderiyi
		// açarken numara vermiş olabilir ve "bilgi vermedim" ile "bilgiyi
		// kaldır" farklı isteklerdir.
		if number == "" {
			number = ful.TrackingNumber
		}
		if url == "" {
			url = ful.TrackingURL
		}

		now := s.now()
		updated, err := s.store.UpdateFulfillmentStatus(ctx, ful.ID, models.StatusShipped,
			number, url, &now, ful.DeliveredAt, ful.CanceledAt)
		if err != nil {
			return err
		}
		out = updated
		return nil
	})
	if err != nil {
		return models.Fulfillment{}, err
	}
	return out, nil
}

// MarkDelivered gönderiyi teslim edilmiş olarak işaretler.
//
// SAĞLAYICIYA GİDİLMEZ; gerekçe [Service.MarkShipped] ile aynıdır.
//
// Yalnızca KARGOYA VERİLMİŞ bir gönderi teslim edilebilir: sırayı atlamak
// shipped_at'i boş bırakır ve mutabakatta gönderinin ne zaman yola çıktığı
// cevapsız kalırdı. Zaten teslim edilmiş bir gönderide ikinci çağrı hata
// dönmez (idempotentlik).
func (s *Service) MarkDelivered(ctx context.Context, id string) (models.Fulfillment, error) {
	if err := requireID(id, models.FulfillmentIDPrefix, "gönderi kimliği"); err != nil {
		return models.Fulfillment{}, err
	}

	var out models.Fulfillment
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		ful, err := s.store.LockFulfillment(ctx, id)
		if err != nil {
			return err
		}

		switch ful.Status.DeliverAction() {
		case models.ActionNoop:
			out = ful
			s.log.DebugContext(ctx, "gönderi zaten teslim edilmiş", "gonderi", id)
			return nil
		case models.ActionConflict:
			return errors.Conflict(CodeInvalidTransition,
				"%q durumundaki gönderi teslim edilemez; önce kargoya verilmeli: %s", ful.Status, id)
		case models.ActionProceed:
			// Aşağıda ele alınır.
		}

		now := s.now()
		updated, err := s.store.UpdateFulfillmentStatus(ctx, ful.ID, models.StatusDelivered,
			ful.TrackingNumber, ful.TrackingURL, ful.ShippedAt, &now, ful.CanceledAt)
		if err != nil {
			return err
		}
		out = updated
		return nil
	})
	if err != nil {
		return models.Fulfillment{}, err
	}
	return out, nil
}

// GetFulfillment gönderiyi KALEMLERİYLE birlikte döner; yoksa errors.NotFound.
func (s *Service) GetFulfillment(ctx context.Context, id string) (models.Fulfillment, error) {
	if err := requireID(id, models.FulfillmentIDPrefix, "gönderi kimliği"); err != nil {
		return models.Fulfillment{}, err
	}
	ful, err := s.store.GetFulfillment(ctx, id)
	if err != nil {
		return models.Fulfillment{}, err
	}
	return s.withItems(ctx, ful)
}

// ListFulfillmentsInput gönderi listelemesinin girdisidir.
type ListFulfillmentsInput struct {
	// Reference verilirse yalnızca o siparişin gönderileri döner.
	Reference *string
	// Status verilirse yalnızca o durumdaki gönderiler döner.
	Status *string
	// Page sayfalama parametreleridir.
	Page Page
}

// ListFulfillments gönderileri KALEMLERİYLE birlikte sayfalayarak döner.
// İkinci dönüş değeri süzgece uyan TÜM kayıtların sayısıdır.
//
// Kalemler TEK bir toplu sorguyla getirilir; gönderi başına sorgu (N+1)
// yapılmaz.
func (s *Service) ListFulfillments(
	ctx context.Context,
	in ListFulfillmentsInput,
) ([]models.Fulfillment, int64, error) {
	page, err := in.Page.normalize()
	if err != nil {
		return nil, 0, err
	}
	if in.Status != nil {
		if _, statusErr := normalizeStatus(*in.Status); statusErr != nil {
			return nil, 0, statusErr
		}
	}

	list, total, err := s.store.ListFulfillments(ctx, models.FulfillmentFilter{
		Reference: in.Reference,
		Status:    in.Status,
		Limit:     page.Limit,
		Offset:    page.Offset,
	})
	if err != nil {
		return nil, 0, err
	}
	if len(list) == 0 {
		return list, total, nil
	}

	ids := make([]string, 0, len(list))
	for i := range list {
		ids = append(ids, list[i].ID)
	}
	items, err := s.store.FulfillmentItemsByFulfillments(ctx, ids)
	if err != nil {
		return nil, 0, err
	}

	byFulfillment := make(map[string][]models.FulfillmentItem, len(list))
	for i := range items {
		byFulfillment[items[i].FulfillmentID] = append(byFulfillment[items[i].FulfillmentID], items[i])
	}
	for i := range list {
		list[i].Items = byFulfillment[list[i].ID]
	}
	return list, total, nil
}

// withItems gönderiye kalemlerini iliştirir.
func (s *Service) withItems(ctx context.Context, ful models.Fulfillment) (models.Fulfillment, error) {
	items, err := s.store.ListFulfillmentItems(ctx, ful.ID)
	if err != nil {
		return models.Fulfillment{}, err
	}
	ful.Items = items
	return ful, nil
}

// normalizeItems kalem girdilerini doğrular ve tekrarları reddeder.
//
// Aynı sipariş satırının iki kez verilmesi benzersiz indekse takılırdı; burada
// yakalanması, hatanın hangi satırdan geldiğini söyleyebilmek içindir.
func normalizeItems(in []FulfillmentItemInput) ([]FulfillmentItemInput, error) {
	if len(in) > maxItemsPerFulfillment {
		return nil, errors.Invalid(CodeInvalidInput,
			"bir gönderi en fazla %d kalem içerebilir: %d", maxItemsPerFulfillment, len(in))
	}

	seen := make(map[string]struct{}, len(in))
	out := make([]FulfillmentItemInput, 0, len(in))
	for i, item := range in {
		lineID := strings.TrimSpace(item.LineItemID)
		if err := requireText("sipariş satırı kimliği", lineID); err != nil {
			return nil, err
		}
		if item.Quantity < models.MinQuantity || item.Quantity > models.MaxQuantity {
			return nil, errors.Invalid(CodeInvalidInput,
				"%d. kalemin adedi %d ile %d arasında olmalı: %d",
				i+1, models.MinQuantity, models.MaxQuantity, item.Quantity)
		}
		if _, dup := seen[lineID]; dup {
			return nil, errors.Invalid(CodeInvalidInput,
				"aynı sipariş satırı gönderide iki kez yer alamaz: %s", lineID)
		}
		seen[lineID] = struct{}{}
		out = append(out, FulfillmentItemInput{LineItemID: lineID, Quantity: item.Quantity})
	}
	return out, nil
}

// savedItemsKey kaydedilmiş kalemleri karşılaştırılabilir tek bir metne
// çevirir.
//
// Metin SIRALIDIR: kalem kümesi aynıysa, kalemlerin hangi sırada verildiği ya
// da veritabanından hangi sırada okunduğu bir fark sayılmamalıdır. Sipariş
// satırı kimliği gönderi içinde tek olduğu için (benzersiz indeks) sıralı metin
// kümenin kanonik hâlidir.
func savedItemsKey(items []models.FulfillmentItem) string {
	parts := make([]string, 0, len(items))
	for i := range items {
		parts = append(parts, fmt.Sprintf("%s:%d", items[i].LineItemID, items[i].Quantity))
	}
	slices.Sort(parts)
	return strings.Join(parts, " ")
}

// requestedItemsKey istenen kalemleri [savedItemsKey] ile AYNI biçimde metne
// çevirir; iki metin ancak aynı biçimde üretilirse karşılaştırılabilir.
func requestedItemsKey(items []FulfillmentItemInput) string {
	parts := make([]string, 0, len(items))
	for i := range items {
		parts = append(parts, fmt.Sprintf("%s:%d", items[i].LineItemID, items[i].Quantity))
	}
	slices.Sort(parts)
	return strings.Join(parts, " ")
}

// providerStatus çekirdek sözleşmesinin durumunu modülün durumuna çevirir.
//
// Çeviri AÇIKÇA yapılır ve tanınmayan bir değer hata döner: iki tip bugün aynı
// dizeleri taşıyor ama biri çekirdeğin sözleşmesi, diğeri bu modülün şeması.
// Doğrudan dönüştürmek, çekirdek yeni bir durum eklediğinde veritabanına
// tanımsız bir değer yazmayı denemek demek olurdu.
func providerStatus(status coreprovider.FulfillmentStatus, providerID string) (models.FulfillmentStatus, error) {
	switch status {
	case coreprovider.FulfillmentPending:
		return models.StatusPending, nil
	case coreprovider.FulfillmentShipped:
		return models.StatusShipped, nil
	case coreprovider.FulfillmentDelivered:
		return models.StatusDelivered, nil
	case coreprovider.FulfillmentCanceled:
		return models.StatusCanceled, nil
	case "":
		// Durum bildirmeyen bir sağlayıcı için en güvenli varsayım
		// "oluşturuldu, henüz yola çıkmadı"dır: gönderiyi yanlışlıkla
		// tamamlanmış saymaktansa bekleyen saymak, akışı ilerletilebilir
		// bırakır.
		return models.StatusPending, nil
	default:
		return "", errors.Internal(CodeProviderContract,
			"sağlayıcı %q tanınmayan bir gönderi durumu döndü: %q", providerID, status)
	}
}

// mergeProviderData seçeneğin yapılandırmasıyla isteğin verisini birleştirir.
//
// Seçeneğin Data alanı SAĞLAYICI YAPILANDIRMASIDIR (sözleşme numarası, ücret
// kademeleri) ve fiyat sorgusuna zaten olduğu gibi geçirilir; gönderi
// açılırken de geçirilmesi şarttır, aksi hâlde sağlayıcı hangi hesapla etiket
// basacağını bilemezdi.
//
// Çakışmada İSTEĞİN verisi kazanır: yapılandırma mağazanın sabit ayarıdır,
// istek ise o gönderiye özgüdür (adres, şube, kalem dökümü) ve daha
// belirgindir.
//
// Dönen harita YENİDİR; seçeneğin Data'sı bu yolda DEĞİŞTİRİLMEZ. Yerinde
// birleştirme, aynı seçenekle açılan bir sonraki gönderinin öncekinin
// verisini taşıması demek olurdu.
func mergeProviderData(config, request map[string]any) map[string]any {
	if len(config) == 0 && len(request) == 0 {
		return nil
	}
	out := make(map[string]any, len(config)+len(request))
	maps.Copy(out, config)
	maps.Copy(out, request)
	return out
}

// stampFor durum hedefe eşitse verilen anı, değilse nil döner.
//
// Şemadaki fulfillments_*_stamp kısıtları, yazılan durumun damgasının dolu
// olmasını ister; bu yardımcı o eşleşmeyi tek yerde kurar.
func stampFor(status, target models.FulfillmentStatus, now time.Time) *time.Time {
	if status != target {
		return nil
	}
	stamp := now
	return &stamp
}
