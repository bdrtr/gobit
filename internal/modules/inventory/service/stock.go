package service

import (
	"context"
	"math"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
)

// SetInventoryLevel writes the PHYSICAL quantity of an item at a location.
//
// The level is created when there is none, and otherwise stocked_quantity is
// updated to the absolute value given; the reserved quantity DOES NOT CHANGE.
// A new physical quantity below the reserved one is errors.Conflict: promised
// stock cannot evaporate silently through a stock count — the reservations have
// to be released first.
//
// The whole of the work is done under the item lock, in one transaction. The
// lock also keeps two concurrent creations for the same (item, location) from
// colliding on the unique index: whichever is going to create the row wins the
// race at the lock, and the other waits, then sees the existing row and updates
// it.
//
// The first step of the lock order is the LOCATION
// ([Service.requireOpenLocation]): a closed location takes no stock, and the
// close locks that same row exclusively, so this write either finishes before
// the close or waits and then finds the location closed (ADR 0055).
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
		if err := s.requireOpenLocation(ctx, locationID); err != nil {
			return err
		}
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
			created, createErr := s.openLevel(ctx, itemID, locationID, stockedQty,
				models.MovementStockCount)
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

		updated, err := s.writeQuantities(ctx, level, stockedQty, level.ReservedQuantity,
			models.MovementStockCount, "", "", "")
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

// AdjustInventory raises or lowers the physical quantity by delta, as an
// OPERATOR'S correction: breakage, a recount, a transfer recorded by hand.
//
// The result CANNOT GO NEGATIVE and cannot fall below the reserved quantity; in
// either case errors.Conflict comes back and nothing is written. The read is
// made under the row lock, so two concurrent corrections do not overwrite each
// other.
//
// The locks are taken location -> item -> level (see the lock order section on
// [Store]); a missing location or item is errors.NotFound, and a closed
// location is errors.Conflict (ADR 0055).
//
// The movement it leaves in the ledger says "adjustment". Goods coming BACK
// from a customer are the same arithmetic and a different fact, so they have
// their own entry point ([Service.RestockInventory]) rather than a reason
// parameter on this one: a reason a caller passes is a reason a caller can get
// wrong, and the admin endpoint would have to invent one for every request.
func (s *Service) AdjustInventory(ctx context.Context, itemID, locationID string, delta int64) (models.InventoryLevel, error) {
	if delta == 0 {
		return models.InventoryLevel{}, errors.Invalid(CodeInvalidInput, "delta sıfır olamaz")
	}

	return s.adjust(ctx, itemID, locationID, delta, models.MovementAdjustment)
}

// RestockInventory puts returned goods BACK at a location.
//
// It is [Service.AdjustInventory]'s arithmetic under a different name, and the
// name is the point: the ledger has to be able to tell a warehouse correction
// from goods a customer sent back, and nothing in a positive delta says which
// one it was.
//
// It is NOT the undoing of a reservation. A confirmed reservation cannot be
// released — the units left the count for good — so a return is an ARRIVAL, and
// two calls add the stock twice because two calls mean two physical arrivals.
// The caller is responsible for calling it once per receipt; the return record
// is what makes that possible, since a return can only be received once.
//
// The quantity has to be POSITIVE. The check lives here rather than in the
// cross-module surface that calls it, because that surface's own rule is that
// it translates signatures and holds no rules of its own.
func (s *Service) RestockInventory(ctx context.Context, itemID, locationID string, quantity int64) (models.InventoryLevel, error) {
	if quantity <= 0 {
		return models.InventoryLevel{}, errors.Invalid(CodeInvalidInput,
			"the restocked quantity has to be positive: %d (item %s)", quantity, itemID)
	}

	return s.adjust(ctx, itemID, locationID, quantity, models.MovementReturnRestock)
}

// adjust is the shared body of [Service.AdjustInventory] and
// [Service.RestockInventory]; the reason is what the two disagree about.
func (s *Service) adjust(
	ctx context.Context,
	itemID, locationID string,
	delta int64,
	reason models.MovementReason,
) (models.InventoryLevel, error) {
	if err := requireIDs(itemID, locationID); err != nil {
		return models.InventoryLevel{}, err
	}

	var out models.InventoryLevel
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		if err := s.requireOpenLocation(ctx, locationID); err != nil {
			return err
		}
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

		updated, err := s.writeQuantities(ctx, level, newStocked, level.ReservedQuantity, reason, "", "", "")
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

// AvailableQuantitiesByLocation satılabilir adedi kalem ve LOKASYON kırılımıyla
// döner.
//
// # Neden kırılım, süzülmüş toplam değil
//
// Bunu isteyen okuma Query katmanından (ADR 0004) geçen bir vitrin okumasıdır
// ve bir GENİŞLETME süzgeç taşımaz: sağlayıcıya kimlikler ve alan adları
// verilir, "hangi lokasyonları sayabilirsin" verilmez. Dolayısıyla hepsi
// döner ve çağıran, satış kanalının sevk ettiklerini toplar (ADR 0092).
//
// Boş kalan lokasyon haritada YOKTUR; olmayan bir lokasyon sıfır katkı verir.
func (s *Service) AvailableQuantitiesByLocation(
	ctx context.Context, itemIDs []string,
) (map[string]map[string]int64, error) {
	if len(itemIDs) == 0 {
		return map[string]map[string]int64{}, nil
	}

	return s.store.AvailableByItemLocation(ctx, itemIDs)
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
	// Purpose stoğun NİÇİN ayrıldığıdır; boş bırakılırsa
	// [models.PurposeSale] okunur.
	//
	// Onayın yazacağı hareket sebebini belirleyen alan budur: mal ambardan
	// çıktığında defter "satış" mı "yerine gönderim" mi olduğunu bu sözden
	// öğrenir. Sebebi onaya parametre olarak vermek reddedildi — onay saga'dan,
	// yeniden denemeden ve kurtarma yolundan çağrılıyor ve üçüncüsünde
	// çağıranın elinde o bilgi yok.
	Purpose models.ReservationPurpose
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
	purpose := in.Purpose
	if purpose == "" {
		purpose = models.PurposeSale
	}
	if !purpose.Valid() {
		return models.Reservation{}, errors.Invalid(CodeInvalidInput,
			"bilinmeyen rezervasyon amacı: %q", in.Purpose)
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
		// No movement: a reservation promises stock, it does not move it. The
		// physical count is unchanged, and inventory_reservations is already
		// this fact's record (ADR 0068).
		if _, err := s.writeQuantities(ctx, level, level.StockedQuantity, newReserved, "", "", "", ""); err != nil {
			return err
		}

		created, err := s.store.CreateReservation(ctx, models.Reservation{
			ID:              models.NewReservationID(),
			InventoryItemID: in.InventoryItemID,
			LocationID:      in.LocationID,
			Quantity:        in.Quantity,
			LineItemID:      strings.TrimSpace(in.LineItemID),
			Description:     strings.TrimSpace(in.Description),
			Purpose:         purpose,
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

		// No movement, for the mirror of Reserve's reason: the promise is given
		// back and the goods never left.
		if _, err := s.writeQuantities(ctx, level, level.StockedQuantity, newReserved, "", "", "", ""); err != nil {
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
func (s *Service) ConfirmReservation(ctx context.Context, reservationID, orderID string) error {
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

		// This is the ONE reservation transition that moves goods, so it is the
		// one that leaves a movement: the units are gone from the warehouse and
		// the row names the promise they went out against (ADR 0068).
		//
		// Hareketin sebebini SÖZÜN KENDİSİ söylüyor: satış için ayrılmış stok
		// satış olarak, bir talebi karşılamak için ayrılmış stok yerine gönderim
		// olarak düşülür. Onayın burada bir seçimi yok, çünkü seçim rezervasyon
		// yazılırken yapıldı.
		// The ORDER is written onto the movement, and it is the only thing on this
		// row that points outside the warehouse.
		//
		// It exists so that units written off later can go back to the shelf they
		// left. The reservation knew the location and is keyed to the CART's line
		// item, which an order does not carry — so without this the way back is a
		// chain through three modules, and with it it is one read.
		//
		// It may be empty: a replacement's goods leave against a claim rather than
		// an order, and a caller with no order to name says so by naming none.
		if _, err := s.writeQuantities(ctx, level, newStocked, newReserved,
			reservation.Purpose.MovementReason(), reservationID, orderID, ""); err != nil {
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

// requireOpenLocation takes the SHARED lock on the location and refuses a
// closed one. It is the first step of every flow that writes stock.
//
// Both halves carry weight. The LOCK is what a close meets: the close holds the
// same row exclusively, so a stocking transaction either commits before the
// close counts what the location holds or waits and then finds it closed. The
// REFUSAL is what keeps a closed location empty after the close — without it,
// "closed" would be true for one moment, and the availability sums, which read
// inventory_levels with no join to stock_locations, would go on offering units
// out of a warehouse the operator has retired (ADR 0055).
//
// It also gives the location's absence a NAME: before this step, stocking a
// location that does not exist reached the database and came back as a foreign
// key violation.
func (s *Service) requireOpenLocation(ctx context.Context, locationID string) error {
	location, err := s.store.LockStockLocationShared(ctx, locationID)
	if err != nil {
		return err
	}
	if location.Closed() {
		return errors.Conflict(CodeLocationClosed,
			"the location is closed and takes no stock (%s)", locationID)
	}
	return nil
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

// ReturnCanceledInventory brings a LINE's returned units up to a target.
//
// # Why it is neither an adjustment nor a restock
//
// The arithmetic is a restock's and the FACT is not. Nothing arrived: the units
// never left the building, and what changed is that the promise to send them was
// withdrawn. An operator reading the ledger to explain a month's stock is asking
// which of the three it was, and the ledger answers by reason (ADR 0068).
//
// # It takes a TARGET, not a quantity, and that is the whole correction
//
// It used to take a quantity and add it, with the cancellation's id held unique
// so a redelivered event wrote nothing. That was idempotent per ACT and the
// invariant is per LINE — and two different acts put a line's units back: a
// write-off, and the cancellation of the parcel that had been holding the rest.
// Each computed a delta from a state the other had not yet changed, so running
// them in the order nothing forbids credited the shelf with EIGHT units for a
// cancellation of five (D82).
//
// So the caller states where the total should BE, this reads where it is, and
// the difference is what moves. Whichever act arrives first does the work and the
// other finds the target already met; a redelivery finds it met too. The read and
// the write are in ONE transaction under the level's lock, so two acts arriving
// together cannot both see the old sum.
//
// A call that finds the target already met returns
// [models.ErrMovementAlreadyRecorded], which is not a failure: it says the units
// are already back.
func (s *Service) ReturnCanceledInventory(
	ctx context.Context, itemID, locationID, lineItemID string, target int64, reference string,
) (models.InventoryLevel, error) {
	if target <= 0 {
		return models.InventoryLevel{}, errors.Invalid(CodeInvalidInput,
			"the target on the shelf has to be positive: %d (item %s)", target, itemID)
	}
	if err := requireText("reference", reference); err != nil {
		return models.InventoryLevel{}, err
	}
	if err := requireText("line_item_id", lineItemID); err != nil {
		return models.InventoryLevel{}, err
	}
	if err := requireIDs(itemID, locationID); err != nil {
		return models.InventoryLevel{}, err
	}

	var out models.InventoryLevel
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		if err := s.requireOpenLocation(ctx, locationID); err != nil {
			return err
		}
		if err := s.store.LockInventoryItemShared(ctx, itemID); err != nil {
			return err
		}

		level, err := s.store.LockInventoryLevel(ctx, itemID, locationID)
		if err != nil {
			return err
		}

		// Read AFTER the locks. Before them it would be the stale sum two
		// concurrent acts would both act on.
		returned, err := s.store.ReturnedForLine(ctx, itemID, lineItemID)
		if err != nil {
			return err
		}

		quantity := target - returned
		if quantity <= 0 {
			return models.ErrMovementAlreadyRecorded
		}

		newStocked, err := addQuantity(level.StockedQuantity, quantity)
		if err != nil {
			return err
		}

		updated, err := s.writeQuantities(ctx, level, newStocked, level.ReservedQuantity,
			models.MovementCancellation, "", reference, lineItemID)
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

// SaleLocations answers where an order's units were taken from, per inventory
// item.
//
// The map is empty rather than an error for an order whose stock was never
// deducted — a saga that failed before its last step leaves reservations and no
// sale — because "nothing left from anywhere" is a true answer and a caller that
// had to tell it apart from a fault would have nothing to do with the difference.
func (s *Service) SaleLocations(ctx context.Context, orderID string) (map[string]string, error) {
	if err := requireText("order_id", orderID); err != nil {
		return nil, err
	}

	return s.store.SaleLocations(ctx, orderID)
}
