package service

import (
	"context"
	"math"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
)

// SetInventoryLevel bir kalemin bir lokasyondaki FİZİKSEL adedini yazar.
//
// Seviye yoksa oluşturulur, varsa stocked_quantity mutlak olarak güncellenir;
// rezerve adet DEĞİŞMEZ. Yeni fiziksel adet rezerve adedin altına düşerse
// errors.Conflict döner: söz verilmiş stok, sayım düzeltmesiyle sessizce
// buharlaşamaz — önce rezervasyonlar serbest bırakılmalıdır.
//
// Tüm iş kalem kilidi altında ve tek işlemde yapılır. Kilit, aynı (kalem,
// lokasyon) için eşzamanlı iki oluşturmanın benzersiz indekse çarpmasını da
// önler: satırı yaratacak olan yarışı kilitte kazanır, diğeri bekler ve var
// olan satırı görüp günceller.
func (s *Service) SetInventoryLevel(ctx context.Context, itemID, locationID string, stockedQty int64) (models.InventoryLevel, error) {
	if err := requireIDs(itemID, locationID); err != nil {
		return models.InventoryLevel{}, err
	}
	if stockedQty < 0 {
		return models.InventoryLevel{}, errors.Invalid(CodeInvalidInput,
			"stok adedi negatif olamaz: %d", stockedQty)
	}

	var out models.InventoryLevel
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		if err := s.store.LockInventoryItem(ctx, itemID); err != nil {
			return err
		}

		level, err := s.store.LockInventoryLevel(ctx, itemID, locationID)
		if err != nil {
			// Kalem bu noktada kilitli ve var olduğu doğrulanmıştır; buradaki
			// tek "bulunamadı" olasılığı seviyenin henüz olmamasıdır.
			if !errors.HasKind(err, errors.KindNotFound) {
				return err
			}
			created, createErr := s.store.CreateInventoryLevel(ctx, models.InventoryLevel{
				ID:              models.NewInventoryLevelID(),
				InventoryItemID: itemID,
				LocationID:      locationID,
				StockedQuantity: stockedQty,
			})
			if createErr != nil {
				return createErr
			}
			out = created
			return nil
		}

		if stockedQty < level.ReservedQuantity {
			return errors.Conflict(CodeInsufficientStock,
				"fiziksel adet (%d) rezerve adedin (%d) altına indirilemez; önce rezervasyonları serbest bırakın",
				stockedQty, level.ReservedQuantity)
		}

		updated, err := s.store.UpdateInventoryLevelQuantities(ctx, level.ID, stockedQty, level.ReservedQuantity)
		if err != nil {
			return err
		}
		out = updated
		return nil
	})
	if err != nil {
		return models.InventoryLevel{}, err
	}
	return out, nil
}

// AdjustInventory fiziksel adedi delta kadar artırır ya da azaltır.
//
// Sonuç NEGATİFE DÜŞEMEZ ve rezerve adedin altına inemez; her iki durumda da
// errors.Conflict döner ve hiçbir şey yazılmaz. Okuma satır kilidi altında
// yapıldığı için eşzamanlı iki düzeltme birbirinin yazdığını ezmez.
//
// Kilitler kalem -> seviye sırasında alınır (bkz. [Store] "Kilit sırası");
// kalem yoksa errors.NotFound döner.
func (s *Service) AdjustInventory(ctx context.Context, itemID, locationID string, delta int64) (models.InventoryLevel, error) {
	if err := requireIDs(itemID, locationID); err != nil {
		return models.InventoryLevel{}, err
	}
	if delta == 0 {
		return models.InventoryLevel{}, errors.Invalid(CodeInvalidInput, "delta sıfır olamaz")
	}

	var out models.InventoryLevel
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		if err := s.store.LockInventoryItemShared(ctx, itemID); err != nil {
			return err
		}

		level, err := s.store.LockInventoryLevel(ctx, itemID, locationID)
		if err != nil {
			return err
		}

		newStocked, err := addQuantity(level.StockedQuantity, delta)
		if err != nil {
			return err
		}
		// Tek kontrol iki kuralı birden karşılar: rezerve adet veritabanı
		// kısıtı gereği asla negatif olamadığı için "newStocked >= reserved"
		// koşulu "newStocked >= 0" koşulunu da kapsar. İkinci bir if yazmak,
		// hiçbir girdinin ulaşamayacağı ölü bir dal bırakırdı.
		if newStocked < level.ReservedQuantity {
			return errors.Conflict(CodeInsufficientStock,
				"stok bu kadar düşürülemez: sonuç %d olurdu (mevcut %d, rezerve %d); satılabilir adet negatife düşemez",
				newStocked, level.StockedQuantity, level.ReservedQuantity)
		}

		updated, err := s.store.UpdateInventoryLevelQuantities(ctx, level.ID, newStocked, level.ReservedQuantity)
		if err != nil {
			return err
		}
		out = updated
		return nil
	})
	if err != nil {
		return models.InventoryLevel{}, err
	}
	return out, nil
}

// ListInventoryLevels kalemin tüm lokasyonlardaki stok seviyelerini döner.
// Kalem yoksa errors.NotFound döner; seviyesi olmayan kalem için boş dilim.
func (s *Service) ListInventoryLevels(ctx context.Context, itemID string) ([]models.InventoryLevel, error) {
	if err := requireText("inventory_item_id", itemID); err != nil {
		return nil, err
	}
	// Varlık kontrolü, olmayan bir kalem için "stok yok" yerine "kalem yok"
	// denmesini sağlar; ikisi çağıran için farklı şeylerdir.
	if _, err := s.store.GetInventoryItem(ctx, itemID); err != nil {
		return nil, err
	}
	return s.store.ListInventoryLevels(ctx, itemID)
}

// AvailableQuantity kalemin TÜM lokasyonlardaki satılabilir toplamını döner.
//
// Toplam, her seviyenin stocked - reserved farkından türetilir. Kalem yoksa
// errors.NotFound döner; hiç seviyesi olmayan kalem için 0.
func (s *Service) AvailableQuantity(ctx context.Context, itemID string) (int64, error) {
	levels, err := s.ListInventoryLevels(ctx, itemID)
	if err != nil {
		return 0, err
	}

	var total int64
	for _, level := range levels {
		total += level.Available()
	}
	return total, nil
}

// AvailableQuantities verilen kalemlerin satılabilir toplamlarını TEK sorguda
// döner. Hiç seviyesi olmayan kalem sonuçta sıfırla yer alır.
//
// Query sağlayıcısı bunu kullanır: product'ın mağaza listelemesi, kaç ürün
// olursa olsun stok için tek tur yapar (N+1 yok).
func (s *Service) AvailableQuantities(ctx context.Context, itemIDs []string) (map[string]int64, error) {
	if len(itemIDs) == 0 {
		return map[string]int64{}, nil
	}

	found, err := s.store.AvailableByItemIDs(ctx, itemIDs)
	if err != nil {
		return nil, err
	}

	out := make(map[string]int64, len(itemIDs))
	for _, id := range itemIDs {
		out[id] = found[id]
	}
	return out, nil
}

// LocationsWithStock kalemden EN AZ quantity adet ayrılabilen lokasyonların
// kimliklerini döner.
//
// "Ayrılabilir" tanımı [Service.Reserve] ile AYNIDIR: her seviyenin
// [models.InventoryLevel.Available] değeri, yani stocked - reserved. Liste,
// [Service.AvailableQuantity]'nin topladığı seviyelerin ta kendisinden
// süzülür; ikinci bir "müsait" tanımı yazmak (örneğin yalnızca fiziksel adede
// bakan ayrı bir sorgu) listede görünen ama Reserve'de errors.Conflict alan
// lokasyonlar üretirdi.
//
// # Sıra bir OLGUDUR, politika değil
//
// Sonuç LOKASYON KİMLİĞİNE göre artan sıradadır ve bu sıra DETERMİNİSTİKTİR.
// "En çok stoklu önce" gibi bir sıra cazip görünür ama yanlıştır: hangi
// depodan gönderileceği bir KARGO KARARIDIR ve fulfillment'a aittir; bu metot
// yalnızca bir stok olgusu döner. Sıraya politika saklamak, kararı hiç
// kimsenin bakmadığı bir yerde — stok modülünün sıralamasında — verirdi.
//
// # Sonuç bir ADAY listesidir
//
// Liste kilitsiz okunur, dolayısıyla dönüş anında bayatlayabilir: araya giren
// bir sepet son adedi alabilir. Yeterliliğin TEK yetkilisi, kararını işlem
// içinde ve satır kilidi altında veren [Service.Reserve]'dir. Bu metot onun
// yerine geçmez, yalnızca Reserve'ün DENENEBİLECEĞİ lokasyonları daraltır.
//
// Hiçbir lokasyon yetmiyorsa BOŞ dilim döner, hata değil: "yeterli stok yok"
// bir arıza değil bir cevaptır ve çağıran onu kendi bağlamında (saga adımında)
// Conflict'e çevirmeyi seçer. Kalem yoksa errors.NotFound döner; "stoğu yok"
// ile "kendisi yok" çağıran için farklı durumlardır.
//
// quantity POZİTİF olmalıdır, aksi hâlde errors.Invalid döner. Sıfır ya da
// negatif bir eşik, Reserve'ün doğrudan reddedeceği bir adet için lokasyon
// listelemek olurdu: dönen her lokasyon rezervasyonda patlardı. Sessizce boş
// liste dönmek ise çağıranın hatasını "stok yok" gibi göstererek gizlerdi.
func (s *Service) LocationsWithStock(ctx context.Context, itemID string, quantity int64) ([]string, error) {
	if quantity <= 0 {
		return nil, errors.Invalid(CodeInvalidInput,
			"istenen adet pozitif olmalı: %d", quantity)
	}

	// Kalem kimliğinin doğrulaması ve varlık kontrolü ListInventoryLevels'te
	// yapılır; burada tekrarlamak aynı kuralın iki yere ayrışması olurdu.
	levels, err := s.ListInventoryLevels(ctx, itemID)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(levels))
	for _, level := range levels {
		if level.Available() >= quantity {
			out = append(out, level.LocationID)
		}
	}
	// Seviyeler (kalem, lokasyon) çiftinde benzersiz olduğu için listede
	// tekrar oluşmaz; sıralamak tek başına yeterlidir.
	slices.Sort(out)
	return out, nil
}

// ReserveInput bir rezervasyon isteğidir.
type ReserveInput struct {
	// InventoryItemID rezerve edilecek kalemdir; zorunludur.
	InventoryItemID string
	// LocationID stoğun ayrılacağı lokasyondur; zorunludur.
	LocationID string
	// Quantity ayrılacak adettir; pozitif olmalıdır.
	Quantity int64
	// LineItemID rezervasyonu isteyen sepet/sipariş satırıdır; isteğe bağlıdır.
	// cart modülüne ait bir kimliktir, burada foreign key değildir.
	LineItemID string
	// Description isteğe bağlı serbest açıklamadır.
	Description string
}

// Reserve satılabilir stoktan istenen adedi ayırır.
//
// Yeterli stok yoksa errors.Conflict (kod: [CodeInsufficientStock]) döner ve
// hiçbir şey yazılmaz. Seviye satırı işlem boyunca kilitli olduğu için son bir
// adet için yarışan iki çağrıdan TAM OLARAK BİRİ kazanır.
//
// Kilitler kalem -> seviye sırasında alınır (bkz. [Store] "Kilit sırası").
// Kalem kilidi PAYLAŞIMLIDIR: eşzamanlı rezervasyonları seri hâle getirmez,
// yalnızca kalemi yapısal olarak değiştiren akışlarla (SetInventoryLevel,
// DeleteInventoryItem) çakışır. Kalem yoksa errors.NotFound döner.
//
// Faz 6'daki complete_cart saga'sının stok adımı budur; telafisi
// [Service.ReleaseReservation]'dır.
func (s *Service) Reserve(ctx context.Context, in ReserveInput) (models.Reservation, error) {
	if err := requireIDs(in.InventoryItemID, in.LocationID); err != nil {
		return models.Reservation{}, err
	}
	if in.Quantity <= 0 {
		return models.Reservation{}, errors.Invalid(CodeInvalidInput,
			"rezervasyon adedi pozitif olmalı: %d", in.Quantity)
	}
	if err := checkTextLen("line_item_id", in.LineItemID); err != nil {
		return models.Reservation{}, err
	}
	if err := checkTextLen("description", in.Description); err != nil {
		return models.Reservation{}, err
	}

	var out models.Reservation
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		if err := s.store.LockInventoryItemShared(ctx, in.InventoryItemID); err != nil {
			return err
		}

		level, err := s.store.LockInventoryLevel(ctx, in.InventoryItemID, in.LocationID)
		if err != nil {
			return err
		}

		available := level.Available()
		if available < in.Quantity {
			return errors.Conflict(CodeInsufficientStock,
				"yetersiz stok: satılabilir %d, istenen %d (kalem: %s, lokasyon: %s)",
				available, in.Quantity, in.InventoryItemID, in.LocationID)
		}

		newReserved, err := addQuantity(level.ReservedQuantity, in.Quantity)
		if err != nil {
			return err
		}
		if _, err := s.store.UpdateInventoryLevelQuantities(ctx, level.ID, level.StockedQuantity, newReserved); err != nil {
			return err
		}

		created, err := s.store.CreateReservation(ctx, models.Reservation{
			ID:              models.NewReservationID(),
			InventoryItemID: in.InventoryItemID,
			LocationID:      in.LocationID,
			Quantity:        in.Quantity,
			LineItemID:      strings.TrimSpace(in.LineItemID),
			Description:     strings.TrimSpace(in.Description),
			Status:          models.ReservationActive,
		})
		if err != nil {
			return err
		}
		out = created
		return nil
	})
	if err != nil {
		return models.Reservation{}, err
	}
	return out, nil
}

// ReleaseReservation rezervasyonu geri alır; ayrılan adet yeniden satılabilir
// hâle gelir.
//
// SAGA TELAFİSİ BUDUR ve İDEMPOTENTTİR: zaten serbest bırakılmış bir
// rezervasyon için hata dönmez ve stoğa İKİNCİ KEZ dokunulmaz. Telafi adımı
// yeniden çalıştırılabilir olmak zorundadır — bir workflow yeniden denendiğinde
// ya da çift tetiklendiğinde ikinci çağrı akışı patlatmamalıdır.
//
// Bilinmeyen bir kimlik için errors.NotFound döner: idempotentlik "her şeyi
// sessizce yut" demek değildir; iki kez bırakılan GERÇEK bir rezervasyon ile
// hiç var olmamış bir kimlik farklı durumlardır ve ikincisi çağıran tarafta bir
// hatadır. Rezervasyon kaydı silinmediği (yalnızca durumu değiştiği) için ilk
// durum her zaman ayırt edilebilir.
//
// Onaylanmış bir rezervasyon serbest bırakılamaz (errors.Conflict): stok çoktan
// fiziksel olarak düşülmüştür, geri almak stok yaratmak olurdu.
//
// Kilitler rezervasyon -> kalem -> seviye sırasında alınır (bkz. [Store]
// "Kilit sırası"); kalem kilidi seviye kilidinden ÖNCE gelir.
func (s *Service) ReleaseReservation(ctx context.Context, reservationID string) error {
	if err := requireText("reservation_id", reservationID); err != nil {
		return err
	}

	return s.store.WithTx(ctx, func(ctx context.Context) error {
		reservation, err := s.store.LockReservation(ctx, reservationID)
		if err != nil {
			return err
		}

		switch reservation.Status {
		case models.ReservationReleased:
			s.log.DebugContext(ctx, "rezervasyon zaten serbest bırakılmış, işlem yapılmadı",
				"rezervasyon", reservationID)
			return nil
		case models.ReservationConfirmed:
			return errors.Conflict(CodeReservationNotActive,
				"onaylanmış rezervasyon serbest bırakılamaz: %s", reservationID)
		case models.ReservationActive:
			// Aşağıda ele alınır.
		default:
			return errors.Internal(CodeInconsistentState,
				"bilinmeyen rezervasyon durumu %q (%s)", reservation.Status, reservationID)
		}

		if err := s.store.LockInventoryItemShared(ctx, reservation.InventoryItemID); err != nil {
			return err
		}

		level, err := s.store.LockInventoryLevel(ctx, reservation.InventoryItemID, reservation.LocationID)
		if err != nil {
			return err
		}
		newReserved := level.ReservedQuantity - reservation.Quantity
		if newReserved < 0 {
			return errors.Internal(CodeInconsistentState,
				"rezerve adet (%d) rezervasyonun adedinden (%d) küçük (%s)",
				level.ReservedQuantity, reservation.Quantity, reservationID)
		}

		if _, err := s.store.UpdateInventoryLevelQuantities(ctx, level.ID, level.StockedQuantity, newReserved); err != nil {
			return err
		}
		return s.store.SetReservationStatus(ctx, reservationID, models.ReservationReleased)
	})
}

// ConfirmReservation rezervasyonu düşülmüş stoğa çevirir: ayrılan adet hem
// fiziksel hem rezerve adetten düşer, satılabilir adet DEĞİŞMEZ.
//
// [Service.ReleaseReservation] gibi idempotenttir: zaten onaylanmış bir
// rezervasyon için hata dönmez. Serbest bırakılmış bir rezervasyon onaylanamaz
// (errors.Conflict); stok geri verilmiştir, düşülecek bir söz kalmamıştır.
//
// Kilitler rezervasyon -> kalem -> seviye sırasında alınır (bkz. [Store]
// "Kilit sırası"); kalem kilidi seviye kilidinden ÖNCE gelir.
func (s *Service) ConfirmReservation(ctx context.Context, reservationID string) error {
	if err := requireText("reservation_id", reservationID); err != nil {
		return err
	}

	return s.store.WithTx(ctx, func(ctx context.Context) error {
		reservation, err := s.store.LockReservation(ctx, reservationID)
		if err != nil {
			return err
		}

		switch reservation.Status {
		case models.ReservationConfirmed:
			s.log.DebugContext(ctx, "rezervasyon zaten onaylanmış, işlem yapılmadı",
				"rezervasyon", reservationID)
			return nil
		case models.ReservationReleased:
			return errors.Conflict(CodeReservationNotActive,
				"serbest bırakılmış rezervasyon onaylanamaz: %s", reservationID)
		case models.ReservationActive:
			// Aşağıda ele alınır.
		default:
			return errors.Internal(CodeInconsistentState,
				"bilinmeyen rezervasyon durumu %q (%s)", reservation.Status, reservationID)
		}

		if err := s.store.LockInventoryItemShared(ctx, reservation.InventoryItemID); err != nil {
			return err
		}

		level, err := s.store.LockInventoryLevel(ctx, reservation.InventoryItemID, reservation.LocationID)
		if err != nil {
			return err
		}
		newStocked := level.StockedQuantity - reservation.Quantity
		newReserved := level.ReservedQuantity - reservation.Quantity
		if newStocked < 0 || newReserved < 0 {
			return errors.Internal(CodeInconsistentState,
				"onay stoğu negatife düşürürdü: fiziksel %d, rezerve %d, rezervasyon %d (%s)",
				level.StockedQuantity, level.ReservedQuantity, reservation.Quantity, reservationID)
		}

		if _, err := s.store.UpdateInventoryLevelQuantities(ctx, level.ID, newStocked, newReserved); err != nil {
			return err
		}
		return s.store.SetReservationStatus(ctx, reservationID, models.ReservationConfirmed)
	})
}

// GetReservation rezervasyonu kimliğiyle döner; yoksa errors.NotFound.
func (s *Service) GetReservation(ctx context.Context, reservationID string) (models.Reservation, error) {
	if err := requireText("reservation_id", reservationID); err != nil {
		return models.Reservation{}, err
	}
	return s.store.GetReservation(ctx, reservationID)
}

// requireIDs kalem ve lokasyon kimliklerini birlikte doğrular.
func requireIDs(itemID, locationID string) error {
	if err := requireText("inventory_item_id", itemID); err != nil {
		return err
	}
	return requireText("location_id", locationID)
}

// addQuantity current'e delta ekler ve yukarı taşmayı yakalar.
//
// current DAİMA negatif değildir (veritabanı kısıtı bunu garanti eder), bu
// yüzden yalnızca yukarı taşma mümkündür: aşağı yönde en küçük sonuç
// 0 + MinInt64'tür ve o da taşmaz. Taşma sessiz bırakılsaydı sonuç negatife
// sarar, tüm adet kontrollerini ve CHECK kısıtlarını atlatabilirdi.
func addQuantity(current, delta int64) (int64, error) {
	if delta > 0 && current > math.MaxInt64-delta {
		return 0, errors.Invalid(CodeInvalidInput,
			"adet taşması: %d + %d int64 sınırını aşıyor", current, delta)
	}
	return current + delta, nil
}
