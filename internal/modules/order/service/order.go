package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// CreateOrderItemInput sipariş satırının anlık görüntüsüdür.
//
// Tüm tutarlar TAM SAYI minor unit'tir (plan Bölüm 8) ve HESAPLANMIŞ hâlde
// gelir; bu modül fiyat ya da vergi hesaplamaz.
type CreateOrderItemInput struct {
	// VariantID satırın gösterdiği ürün varyantıdır; ZORUNLUDUR. product
	// modülüne aittir, varlığı burada doğrulanmaz (Prensip 2.2).
	VariantID string
	// Title satırın görünen adıdır; ZORUNLUDUR ve varyanttan KOPYALANIR.
	Title string
	// Quantity satırdaki adettir; pozitif olmalıdır.
	Quantity int64
	// UnitPrice birim fiyattır (minor unit).
	UnitPrice int64
	// Subtotal satırın ara toplamıdır: UnitPrice × Quantity.
	Subtotal int64
	// DiscountTotal satıra düşen indirimdir; POZİTİF verilir ve düşülür.
	DiscountTotal int64
	// TaxTotal satıra düşen vergidir.
	TaxTotal int64
	// Total satırın toplamıdır: Subtotal - DiscountTotal + TaxTotal.
	Total int64
	// Metadata çağıranın serbest ek verisidir.
	Metadata map[string]any
}

// CreateOrderInput yeni bir siparişin girdisidir; sepetin ANLIK GÖRÜNTÜSÜDÜR.
//
// order modülü cart modülünü BİLMEZ ve import ETMEZ (ADR 0001). Sepeti okuyup
// bu görüntüyü kuran taraf complete_cart WORKFLOW'udur (ADR 0006); bu yüzden
// satırlar ve toplamlar burada HESAPLANMIŞ hâlde gelir.
type CreateOrderInput struct {
	// RegionID siparişin bölgesidir; ZORUNLUDUR.
	RegionID string
	// CustomerID siparişin sahibidir; OPSİYONELDİR. Boş bırakılırsa sipariş
	// MİSAFİRE aittir.
	CustomerID string
	// Email siparişin iletişim adresidir; opsiyoneldir ama misafir siparişinde
	// tek takip yoludur.
	Email string
	// CurrencyCode ISO 4217 kodudur; ZORUNLUDUR.
	CurrencyCode string
	// CartID siparişin doğduğu sepettir; opsiyoneldir ve yalnızca KÖKENİ
	// belgeler.
	CartID string
	// IdempotencyKey aynı siparişin iki kez yazılmasını engeller; opsiyoneldir.
	//
	// Verildiğinde çağrı İDEMPOTENT olur: aynı anahtarla ikinci kez çağrılırsa
	// yeni sipariş açılmaz, MEVCUT sipariş dönülür. Saga bir adımı yeniden
	// deneyebildiği için (plan Bölüm 2.6) bu alan complete_cart akışında
	// doldurulmalıdır; boş bırakılırsa her çağrı yeni bir sipariş üretir.
	IdempotencyKey string
	// Subtotal satır ara toplamlarının toplamıdır (minor unit).
	Subtotal int64
	// DiscountTotal toplam indirimdir; POZİTİF verilir ve toplamdan düşülür.
	DiscountTotal int64
	// TaxTotal toplam vergidir (minor unit).
	TaxTotal int64
	// ShippingTotal toplam kargo tutarıdır (minor unit).
	ShippingTotal int64
	// Total ödenecek tutardır:
	// Subtotal - DiscountTotal + TaxTotal + ShippingTotal.
	Total int64
	// Items siparişin satırlarıdır; EN AZ BİR satır gerekir.
	Items []CreateOrderItemInput
	// Metadata çağıranın serbest ek verisidir.
	Metadata map[string]any
}

// CreateOrder sepetin anlık görüntüsünden bir sipariş üretir.
//
// # Doğrulama katmanları
//
// Sıra ucuzdan pahalıya doğrudur ve TAMAMI yazmadan önce yapılır; kısmen
// yazılmış sipariş diye bir şey yoktur.
//
//  1. Kimlik ve metin alanları: bölge, para birimi, e-posta, satır başlıkları.
//  2. Aralık: her tutar negatif olamaz ve üst sınırı aşamaz (taşma koruması).
//  3. Kapsam: sipariş EN AZ BİR satır taşımalıdır. Satırsız bir siparişten
//     doğan şey, hiçbir şeyin satılmadığı bir siparişdir.
//  4. Satır ara toplamı: Subtotal = UnitPrice × Quantity. Yanlış adetle
//     fiyatlanmış bir satır başka hiçbir kapıda yakalanmazdı.
//  5. Satır kimliği: Total = Subtotal - DiscountTotal + TaxTotal ve satır
//     indirimi ara toplamı aşamaz.
//  6. Sipariş ara toplamı: Subtotal, satırların ara toplamlarının TOPLAMIDIR.
//     İndirim ve vergi sipariş düzeyinde de doğabileceği için (kampanya, kargo
//     vergisi) yalnızca ara toplam bu kurala tabidir.
//  7. Sipariş kimliği: Total = Subtotal - DiscountTotal + TaxTotal +
//     ShippingTotal ve indirim ara toplamı aşamaz.
//
// Yedinci maddedeki iki kontrolün BİRLİKTE bulunması şarttır: kimlik tek başına
// yetmez, çünkü aşırı bir indirim vergi ve kargo tarafından yutulduğunda kimlik
// SAĞLANIR. Örnek: subtotal=1000, discount=3000, shipping=2500 -> total=500;
// müşteri 1000'lik mala 2500'lük kargoyla birlikte 500 öder ve kimlik kontrolü
// bunu görmez.
//
// Doğrulamanın burada da yapılması, hesabı başka bir modülün yapmasına rağmen
// gereklidir: sipariş tutarın KALICI kaydıdır ve yanlış yazılmış bir tutar
// sonradan düzeltilemez (kayıt değişmez). Bir saga adımının yanlış hesabı
// sessizce siparişe yazılmamalıdır.
//
// # Harcama limiti
//
// Müşterinin bir harcama limiti varsa (kuralın kaynağı için bkz.
// [SpendingPolicy]) sipariş, limitin ÜSTÜNE çıkıyorsa açılmaz ve çağrı
// errors.Conflict döner ([CodeSpendingLimitExceeded]).
//
// HARCAMA NASIL SAYILIR: müşterinin, kuralın bildirdiği pencere içinde verilmiş
// siparişlerinin toplamıdır. İPTAL EDİLMİŞ ve yumuşak silinmiş siparişler
// toplama girmez; 'pending' olanlar GİRER (ödemesi düşen bir sipariş saga
// tarafından iptal edilir ve o anda kendini toplamdan çıkarır). Sipariş başına
// İADE EDİLEN tutar toplamdan DÜŞÜLÜR — para şirkete geri döndüyse bütçe de
// geri dönmelidir. Sorgunun tamamı ve gerekçeleri queries/spending.sql
// belgesindedir.
//
// KONTROL NEREDE: siparişin yazıldığı işlemin içinde ve müşteri kilidi altında
// (bkz. [Service.writeOrder]). Bu, "önce kontrol et sonra yaz" yarışını
// yapısal olarak kapatır: aynı müşteri için gelen ikinci sipariş bekler ve
// toplamı birincinin yazdığı satırla birlikte okur.
//
// PARA BİRİMİ: siparişin para birimi limitin para biriminden farklıysa sipariş
// açılmaz ([CodeSpendingCurrencyMismatch]). Çevrim YAPILMAZ; gerekçe için bkz.
// [spendingRule.checkCurrency].
//
// # İdempotency
//
// [CreateOrderInput.IdempotencyKey] verildiyse çağrı idempotenttir: aynı
// anahtarla ikinci çağrı yeni sipariş açmaz, mevcut siparişi döner. Koruma iki
// katlıdır — önce anahtar aranır (ucuz yol), sonra veritabanındaki benzersiz
// indeks yarışan iki eşzamanlı çağrıdan birini reddeder ve reddedilen çağrı
// kaydı yeniden okuyup döner (bkz. [Service.replayedOrder]).
//
// Tekrarlanan çağrı harcamayı İKİNCİ KEZ saymaz: ucuz yol siparişi anahtarından
// bulur ve limit kontrolüne hiç girmez.
//
// # Sıra: yaz -> yayımla
//
// Sipariş, satırları, özeti ve numarasının doğrulaması TEK işlemdedir. Yani
// commit edilmiş bir sipariş her zaman TAMDIR: numarası geçerlidir, bölgesi ve
// (varsa) müşterisi kendi sütunlarında yazılıdır ve bir daha geri alınmaz.
// Yazmadan önce hiçbir yan etki üretilmez; hiçbir telafi yolu gerekmemesinin
// sebebi budur.
//
// "order.placed" olayı EN SONDA, sipariş kesinleşmişken yayımlanır; yayım
// hatası siparişi düşürmez (gerekçe: [Service.publishOrderPlaced]).
func (s *Service) CreateOrder(ctx context.Context, in CreateOrderInput) (models.Order, error) {
	normalized, err := normalizeCreateOrder(in)
	if err != nil {
		return models.Order{}, err
	}

	// Ucuz yol: anahtar zaten kullanılmışsa hiç yazmaya kalkışma.
	if normalized.IdempotencyKey != "" {
		existing, lookupErr := s.store.GetOrderByIdempotencyKey(ctx, normalized.IdempotencyKey)
		switch {
		case lookupErr == nil:
			s.log.InfoContext(ctx, "aynı idempotency anahtarıyla sipariş zaten var, mevcut kayıt dönülüyor",
				"order_id", existing.ID, "display_id", existing.DisplayID)
			return existing, nil
		case !errors.IsNotFound(lookupErr):
			return models.Order{}, lookupErr
		}
	}

	// Harcama kuralı hiçbir satır yazılmadan ÖNCE okunur: para birimi
	// uyuşmazlığı gibi kesin bir ret, arkasında hiçbir iz bırakmamalıdır.
	// Kuralın harcamaya UYGULANMASI ise yazma işleminin içinde, müşteri kilidi
	// altında yapılır (bkz. spending.go).
	rule, err := s.spendingRuleFor(ctx, normalized.CustomerID)
	if err != nil {
		return models.Order{}, err
	}
	if err := rule.checkCurrency(normalized.CurrencyCode); err != nil {
		return models.Order{}, err
	}

	created, err := s.writeOrder(ctx, normalized, rule)
	if err != nil {
		if replay, ok := s.replayedOrder(ctx, normalized.IdempotencyKey, err); ok {
			return replay, nil
		}
		return models.Order{}, err
	}

	s.publishOrderPlaced(ctx, created, len(normalized.Items))
	return created, nil
}

// writeOrder siparişi, satırlarını ve özetini TEK işlemde yazar.
//
// Üçü ayrılamaz: satırsız bir sipariş "hiçbir şeyin satılmadığı sipariş",
// özetsiz bir sipariş ise okuyanın NULL ile sıfırı ayırt etmek zorunda kaldığı
// bir kayıt olurdu. İşlemin herhangi bir adımı düşerse hiçbiri yazılmaz.
//
// Sipariş NUMARASI da burada, İŞLEMİN İÇİNDE doğrulanır. Numara veritabanının
// IDENTITY sütunundan gelir; sıfır ya da negatif bir değer sütunun ya da
// sequence'ın bozulduğu anlamına gelir ve müşterinin hiçbir yerde bulamayacağı
// bir sipariş demektir. Kontrolün işlemin içinde olması, böyle bir siparişin
// bir an bile GÖRÜNÜR olmamasını sağlar: commit hiç gerçekleşmez.
//
// # Harcama limiti de burada uygulanır
//
// İşlemin İLK işi harcama limitidir ([Service.enforceSpendingLimit]) ve bu
// zorunludur: limit, henüz yazılmamış bu siparişi de kapsayan bir TOPLAMA
// bakar. Kontrol işlemin dışında yapılsaydı iki eşzamanlı sipariş toplamı aynı
// anda okur, ikisi de limitin altında görünür ve ikisi de yazılırdı.
func (s *Service) writeOrder(ctx context.Context, in CreateOrderInput, rule spendingRule) (models.Order, error) {
	var created models.Order

	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		if err := s.enforceSpendingLimit(ctx, rule, in); err != nil {
			return err
		}

		order, err := s.store.CreateOrder(ctx, models.Order{
			ID:             models.NewOrderID(),
			Status:         models.OrderPending,
			RegionID:       in.RegionID,
			CustomerID:     in.CustomerID,
			Email:          in.Email,
			CurrencyCode:   in.CurrencyCode,
			CartID:         in.CartID,
			IdempotencyKey: in.IdempotencyKey,
			Subtotal:       in.Subtotal,
			DiscountTotal:  in.DiscountTotal,
			TaxTotal:       in.TaxTotal,
			ShippingTotal:  in.ShippingTotal,
			Total:          in.Total,
			Metadata:       in.Metadata,
		})
		if err != nil {
			return err
		}
		if !models.ValidDisplayID(order.DisplayID) {
			return errors.Internal(CodeDisplayIDInvalid,
				"sipariş kullanılabilir bir numara almadı (display_id=%d); yazma geri alındı: %s",
				order.DisplayID, order.ID)
		}

		// Döngü indeksle gezilir: satır girdisi büyüktür ve değerle kopyalamak
		// her tur birkaç yüz baytı boşuna taşır.
		for i := range in.Items {
			if _, err := s.store.CreateLineItem(ctx, models.OrderLineItem{
				ID:            models.NewLineItemID(),
				OrderID:       order.ID,
				VariantID:     in.Items[i].VariantID,
				Title:         in.Items[i].Title,
				Quantity:      in.Items[i].Quantity,
				UnitPrice:     in.Items[i].UnitPrice,
				Subtotal:      in.Items[i].Subtotal,
				DiscountTotal: in.Items[i].DiscountTotal,
				TaxTotal:      in.Items[i].TaxTotal,
				Total:         in.Items[i].Total,
				Metadata:      in.Items[i].Metadata,
			}); err != nil {
				return err
			}
		}

		if _, err := s.store.CreateSummary(ctx, models.OrderSummary{
			ID:      models.NewSummaryID(),
			OrderID: order.ID,
		}); err != nil {
			return err
		}

		created = order
		return nil
	})
	if err != nil {
		return models.Order{}, err
	}
	return created, nil
}

// replayedOrder yarışı kaybeden bir idempotent çağrının mevcut siparişini döner.
//
// Senaryo şudur: aynı anahtarla iki çağrı aynı anda gelir, ikisi de ucuz yolun
// aramasında kayıt bulamaz, ikisi de yazmaya kalkar ve veritabanının benzersiz
// indeksi ikincisini reddeder. Reddedilen çağrının hata dönmesi yanlış olurdu —
// istenen sipariş VARDIR ve anahtarın sözü tam olarak budur.
//
// Ölçüt hata KODU değil SINIFI'dır (Conflict) ve ayrıca anahtarın gerçekten bir
// siparişe karşılık gelmesi aranır. Kod kullanılmamasının sebebi katman
// ayrımıdır: kodu üreten repository paketi, servisin import ETMEDİĞİ bir
// implementasyondur (ADR 0001 örüntüsü; depo yalnızca [Store] arayüzüyle
// görülür). Anahtarla okuma zaten kesin ölçüttür — kayıt varsa çağrının istediği
// sipariş odur; yoksa asıl hata olduğu gibi yukarı verilir.
//
// Ölçütün SINIF olması harcama limitiyle de doğru çalışır: yarışı kaybeden
// çağrı, kilit altında artık kazananın siparişini de sayan bir toplam görür ve
// benzersizlik ihlali yerine limit aşımıyla düşebilir. İkisi de Conflict'tir ve
// ikisinde de doğru cevap aynıdır — anahtarın söz verdiği sipariş yazılmıştır.
func (s *Service) replayedOrder(ctx context.Context, key string, cause error) (models.Order, bool) {
	if key == "" || !errors.IsConflict(cause) {
		return models.Order{}, false
	}
	existing, err := s.store.GetOrderByIdempotencyKey(ctx, key)
	if err != nil {
		return models.Order{}, false
	}
	s.log.InfoContext(ctx, "eşzamanlı idempotent çağrı yarışı kaybetti, mevcut kayıt dönülüyor",
		"order_id", existing.ID, "display_id", existing.DisplayID)
	return existing, true
}

// GetOrder siparişi satırları ve özetiyle birlikte döner.
//
// Çocuklar İKİ sabit sorguyla getirilir; satır sayısı ne olursa olsun sorgu
// sayısı değişmez (N+1 yoktur). Sipariş yoksa ya da yumuşak silinmişse
// errors.NotFound döner.
//
// Üç sorgu TEK ANLIK GÖRÜNTÜ üzerinde koşar ([Store.WithReadTx]); kilit alınmaz.
// Sağlanan tek şey, üçünün de siparişin AYNI hâlini görmesidir: işlemsiz hâlinde
// başlık YENİ durumu taşırken özet ESKİ tutarları gösterebilirdi.
func (s *Service) GetOrder(ctx context.Context, orderID string) (models.OrderDetail, error) {
	if err := requireID("order_id", orderID); err != nil {
		return models.OrderDetail{}, err
	}
	return s.loadDetail(ctx, func(ctx context.Context) (models.Order, error) {
		return s.store.GetOrder(ctx, orderID)
	})
}

// GetOrderByDisplayID siparişi insan okunur numarasıyla, satırları ve özetiyle
// birlikte döner.
//
// Destek akışının giriş kapısıdır: müşterinin elinde kimlik değil numara olur.
func (s *Service) GetOrderByDisplayID(ctx context.Context, displayID int64) (models.OrderDetail, error) {
	if !models.ValidDisplayID(displayID) {
		return models.OrderDetail{}, errors.Invalid(CodeInvalidInput,
			"display_id en az %d olmalı: %d", models.MinDisplayID, displayID)
	}
	return s.loadDetail(ctx, func(ctx context.Context) (models.Order, error) {
		return s.store.GetOrderByDisplayID(ctx, displayID)
	})
}

// loadDetail siparişi ve çocuklarını tek anlık görüntü üzerinde okur.
//
// Siparişi bulan sorgu parametredir çünkü tek fark odur: kimlikle ve numarayla
// okuma aynı çocuk sorgularını, aynı yalıtım düzeyinde yapar. İkisini ayrı
// yazmak, birine eklenen bir alanın diğerinde unutulduğu klasik ayrışmayı
// davet ederdi.
func (s *Service) loadDetail(ctx context.Context, find func(ctx context.Context) (models.Order, error)) (models.OrderDetail, error) {
	var detail models.OrderDetail

	err := s.store.WithReadTx(ctx, func(ctx context.Context) error {
		order, err := find(ctx)
		if err != nil {
			return err
		}
		items, err := s.store.ListLineItems(ctx, order.ID)
		if err != nil {
			return err
		}
		summary, err := s.store.GetSummary(ctx, order.ID)
		if err != nil {
			return err
		}
		detail = models.OrderDetail{Order: order, Items: items, Summary: summary}
		return nil
	})
	if err != nil {
		return models.OrderDetail{}, err
	}
	return detail, nil
}

// ListOrdersInput sipariş listelemesinin girdisidir.
type ListOrdersInput struct {
	// CustomerID verilirse yalnızca o müşterinin siparişleri döner.
	CustomerID *string
	// RegionID verilirse yalnızca o bölgenin siparişleri döner.
	RegionID *string
	// Status verilirse siparişler duruma göre süzülür.
	Status *models.OrderStatus
	// Page sayfalama parametreleridir.
	Page Page
}

// ListOrders siparişleri sayfalayarak döner (çocuklarını YÜKLEMEDEN).
//
// İkinci dönüş değeri filtreye uyan TÜM satırların sayısıdır. Satırlar burada
// yüklenmez: sayfa başına onlarca siparişin çocuklarını getirmek listeyi ağır ve
// N+1'e açık hâle getirirdi. Çocuklar yalnızca [Service.GetOrder] ile gelir.
func (s *Service) ListOrders(ctx context.Context, in ListOrdersInput) ([]models.Order, int64, error) {
	page, err := in.Page.normalize()
	if err != nil {
		return nil, 0, err
	}

	filter := models.OrderFilter{Limit: page.Limit, Offset: page.Offset}
	if in.CustomerID != nil {
		if err := requireID("customer_id", *in.CustomerID); err != nil {
			return nil, 0, err
		}
		filter.CustomerID = in.CustomerID
	}
	if in.RegionID != nil {
		if err := requireID("region_id", *in.RegionID); err != nil {
			return nil, 0, err
		}
		filter.RegionID = in.RegionID
	}
	if in.Status != nil {
		if !in.Status.Valid() {
			return nil, 0, errors.Invalid(CodeInvalidInput,
				"tanımsız sipariş durumu: %q", in.Status.String())
		}
		filter.Status = in.Status
	}
	return s.store.ListOrders(ctx, filter)
}

// ListOrdersByIDs verilen kimliklerin siparişlerini TEK sorguda döner.
// Bulunamayan kimlik için kayıt dönmez; bu bir hata değildir.
func (s *Service) ListOrdersByIDs(ctx context.Context, ids []string) ([]models.Order, error) {
	if len(ids) == 0 {
		return []models.Order{}, nil
	}
	return s.store.OrdersByIDs(ctx, ids)
}

// CancelOrder siparişi iptal eder ve SAGA TELAFİSİDİR; İDEMPOTENTTİR.
//
// # Neden idempotent
//
// Bu metot complete_cart saga'sının create_order adımının Compensate'idir ve
// saga bir telafiyi yeniden çalıştırabilir (plan Bölüm 2.6, core/workflow'un
// "en iyi çaba telafisi" davranışı). Zaten iptal edilmiş bir siparişte hata
// dönmek, telafinin ikinci turunun tüm saga'yı başarısız göstermesi olurdu —
// oysa istenen durum ZATEN sağlanmıştır. Bu yüzden ikinci çağrı sessizce
// başarılıdır ve DEBUG seviyesinde loglanır.
//
// İlk iptalin gerekçesi KORUNUR; ikinci çağrının gerekçesi yazılmaz. İptal
// GERÇEKTEN ilk çağrıda olmuştur ve gerekçe o anın kaydıdır; üzerine yazmak,
// telafinin tekrarını iptalin sebebi gibi göstermek olurdu.
//
// # Neden tamamlanmış sipariş Conflict
//
// Tamamlanmış (ya da arşivlenmiş) bir siparişin ödemesi tahsil edilmiş ve
// sevkiyat kararı verilmiştir. Onu "iptal" damgasıyla kapatmak, tahsil edilmiş
// bir tutarı hiçbir siparişe bağlı olmayan bir tutar hâline getirirdi; doğru yol
// iade/değişimdir ([Service.CreateReturn]), iptal değil. Ayrıca saga yalnızca
// KENDİ oluşturduğu, hâlâ 'pending' olan siparişi telafi eder: buraya
// tamamlanmış bir siparişle gelinmesi, dünyanın saga'nın varsaydığından farklı
// olduğu anlamına gelir ve sessizce yutulmamalı, GÖRÜNÜR olmalıdır.
func (s *Service) CancelOrder(ctx context.Context, orderID, reason string) error {
	if err := requireID("order_id", orderID); err != nil {
		return err
	}
	if err := checkTextLen("reason", reason); err != nil {
		return err
	}

	return s.store.WithTx(ctx, func(ctx context.Context) error {
		order, err := s.store.LockOrder(ctx, orderID)
		if err != nil {
			return err
		}

		switch order.Status {
		case models.OrderCanceled:
			s.log.DebugContext(ctx, "sipariş zaten iptal edilmiş, işlem yapılmadı",
				"order_id", orderID, "display_id", order.DisplayID)
			return nil
		case models.OrderCompleted, models.OrderArchived:
			return errors.Conflict(CodeNotPending,
				"tamamlanmış sipariş iptal edilemez (%s, durum: %s); iade/değişim yolu kullanılmalı",
				orderID, order.Status)
		case models.OrderPending:
			// Aşağıda ele alınır.
		default:
			return errors.Internal(CodeInconsistentState,
				"bilinmeyen sipariş durumu %q (%s)", order.Status, orderID)
		}

		_, err = s.store.CancelOrder(ctx, orderID, reason)
		return err
	})
}

// CompleteOrder siparişi tamamlanmış olarak damgalar.
//
// # Neden ikinci çağrı Conflict
//
// [Service.CancelOrder]'ın aksine bu metot bir TELAFİ DEĞİL, ileri yönlü bir
// adımdır ve idempotent olması gerekmez. Tamamlanmış bir siparişi tekrar
// tamamlamak sessizce başarılı sayılsaydı, aynı siparişin iki kez kapatıldığı
// bir akış hiçbir yerde hata üretmezdi. Yeniden deneme güvenliği burada değil,
// workflow motorunun idempotency-key'inde çözülür: BAŞARIYLA biten bir adım
// tekrar çalıştırılmaz. Aynı gerekçe cart modülünün MarkCompleted'ında da
// geçerlidir; iki modülün bu konudaki davranışı bilinçli olarak aynıdır.
//
// İptal edilmiş sipariş de tamamlanamaz: iptal DURAKTIR (bkz. models
// belgesindeki geçiş şeması).
func (s *Service) CompleteOrder(ctx context.Context, orderID string) (models.Order, error) {
	return s.transition(ctx, orderID, models.OrderPending, "tamamlama",
		func(ctx context.Context, id string) (models.Order, error) {
			return s.store.CompleteOrder(ctx, id)
		})
}

// ArchiveOrder tamamlanmış bir siparişi arşive alır.
//
// Arşivleme siparişin tamamlanmışlığını geri almaz; yalnızca onu günlük
// listelerin dışına çıkarır ve [models.Order.CompletedAt] damgasına dokunmaz.
// Tamamlanmamış bir siparişi arşivlemek reddedilir: henüz kapanmamış bir işi
// görünmez kılmak, unutulmasının en kolay yolu olurdu.
func (s *Service) ArchiveOrder(ctx context.Context, orderID string) (models.Order, error) {
	return s.transition(ctx, orderID, models.OrderCompleted, "arşivleme",
		func(ctx context.Context, id string) (models.Order, error) {
			return s.store.ArchiveOrder(ctx, id)
		})
}

// transition durum geçişlerinin ORTAK ÇERÇEVESİDİR.
//
// Sırayla: tek işlem aç -> siparişi KİLİTLE -> beklenen durumda mı bak ->
// geçişi uygula. Çerçevenin tek yerde olması, hiçbir geçiş yolunun kilidi ya da
// durum kontrolünü atlayamamasını garanti eder.
//
// [Service.CancelOrder] bu çerçeveyi KULLANMAZ, çünkü tek istisnayı o taşır:
// zaten hedef durumda olan bir siparişte hata değil BAŞARI dönmek (idempotent
// telafi). Farkı çerçeveye bir bayrakla taşımak, okuyanın her çağrı yerinde
// bayrağın değerini takip etmesini gerektirirdi.
func (s *Service) transition(
	ctx context.Context,
	orderID string,
	required models.OrderStatus,
	action string,
	apply func(ctx context.Context, id string) (models.Order, error),
) (models.Order, error) {
	if err := requireID("order_id", orderID); err != nil {
		return models.Order{}, err
	}

	var updated models.Order
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		order, err := s.store.LockOrder(ctx, orderID)
		if err != nil {
			return err
		}
		if !order.Status.Valid() {
			return errors.Internal(CodeInconsistentState,
				"bilinmeyen sipariş durumu %q (%s)", order.Status, orderID)
		}
		if order.Status != required {
			return transitionError(action, orderID, required, order.Status)
		}
		updated, err = apply(ctx, orderID)
		return err
	})
	if err != nil {
		return models.Order{}, err
	}
	return updated, nil
}

// transitionError beklenmeyen durumdaki bir geçiş denemesinin tipli hatasıdır.
//
// Kod, hangi durumun BEKLENDİĞİNE göre seçilir: istemci "sipariş bekleyen
// durumda değil" ile "sipariş henüz tamamlanmamış" arasında ayrım yapabilmelidir.
func transitionError(action, orderID string, required, actual models.OrderStatus) error {
	code := CodeNotPending
	if required == models.OrderCompleted {
		code = CodeNotCompleted
	}
	return errors.Conflict(code,
		"%s uygulanamaz: sipariş %q durumunda olmalı, %q durumunda (%s)",
		action, required, actual, orderID)
}
