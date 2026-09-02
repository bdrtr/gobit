package service

import "context"

// Bu dosya inventory modülünün MODÜLLER ARASI yüzeyidir (ADR 0001, ADR 0006).
//
// internal/workflows altındaki saga'lar stok ayırmak ve geri bırakmak zorunda,
// ama ne o paketler bu modülü ne bu modül onları import edebilir. Çözüm
// region/cart/payment/order modüllerindekiyle aynıdır: yalnızca İLKEL ve
// stdlib tipleri kullanan bir yüzey yayımlamak. Tüketici kendi dar arayüzünü
// tanımlar, bu tip onu YAPISAL olarak karşılar ve container'dan adla çözülür.
//
// Yüzey BİLİNÇLİ OLARAK dardır ve akışların ihtiyacına göre seçilmiştir:
// stoğu ayır ([Interop.Reserve]), ayrılanı geri bırak
// ([Interop.ReleaseReservation]), düşülmüş stoğa çevir
// ([Interop.ConfirmReservation]), satılabilir toplamı sor
// ([Interop.AvailableQuantity]) ve yeterli stoğu olan lokasyonları listele
// ([Interop.LocationsWithStock]). Buraya eklenen her metot bir SÖZLEŞMEDİR:
// tüketici onu kendi paketinde birebir aynı imzayla yazar ve uyumu derleyici
// değil, yalnızca testler denetleyebilir.
//
// LocationsWithStock yüzeye "hangi depodan gönderelim" sorusunu TAŞIMAZ, o bir
// kargo kararıdır ve fulfillment'a aittir. Bu yüzey yalnızca stok olgusunu —
// hangi lokasyonlarda yeterli adet olduğunu — bildirir; kararı verecek olan
// modül adayları buradan alır. İkisini tek metotta birleştirmek, stok
// sorgusunu kargo politikasına bağımlı kılardı.

// Interop stok servisini modüller arası ilkel yüzeye çevirir.
//
// Hiçbir karar vermez: yalnızca imzayı çevirir. Eşzamanlılık, kilit sırası ve
// yetersiz stok kuralları [Service] üzerinde kalır; buraya kural eklemek aynı
// kuralın iki yerde ayrışması demek olurdu.
//
// Container'a "inventory.interop" adıyla kaydedilir.
type Interop struct {
	svc *Service
}

// NewInterop verilen servis için modüller arası yüzeyi kurar.
func NewInterop(svc *Service) *Interop { return &Interop{svc: svc} }

// Reserve stoğu ayırır ve rezervasyon kimliğini döner.
//
// Yeterli stok yoksa errors.Conflict döner; saga bunu "sipariş verilemez"
// olarak yorumlar. Ayırma VERİTABANI düzeyinde serileştirilir, dolayısıyla
// eşzamanlı iki çağrı aynı son adedi ALAMAZ.
func (i *Interop) Reserve(
	ctx context.Context,
	inventoryItemID, locationID string,
	quantity int64,
	lineItemID string,
) (reservationID string, err error) {
	res, err := i.svc.Reserve(ctx, ReserveInput{
		InventoryItemID: inventoryItemID,
		LocationID:      locationID,
		Quantity:        quantity,
		LineItemID:      lineItemID,
	})
	if err != nil {
		return "", err
	}
	return res.ID, nil
}

// ReleaseReservation ayrılan stoğu geri bırakır.
//
// SAGA TELAFİSİDİR ve İDEMPOTENTTİR: zaten bırakılmış bir rezervasyon için
// ikinci çağrı hata VERMEZ. Telafi zinciri bir adımı yeniden çalıştırabilir;
// ikinci çağrının patlaması, telafinin yarıda kalması demek olurdu.
func (i *Interop) ReleaseReservation(ctx context.Context, reservationID string) error {
	return i.svc.ReleaseReservation(ctx, reservationID)
}

// ConfirmReservation rezervasyonu düşülmüş stoğa çevirir.
//
// Sipariş kesinleştiğinde çağrılır; bu noktadan sonra stok geri bırakılmaz,
// iade ayrı bir akıştır.
func (i *Interop) ConfirmReservation(ctx context.Context, reservationID string) error {
	return i.svc.ConfirmReservation(ctx, reservationID)
}

// AvailableQuantity kalemin tüm lokasyonlardaki kullanılabilir adedini döner.
func (i *Interop) AvailableQuantity(ctx context.Context, inventoryItemID string) (int64, error) {
	return i.svc.AvailableQuantity(ctx, inventoryItemID)
}

// LocationsWithStock kalemden en az quantity adet ayrılabilen lokasyonların
// kimliklerini artan sırada döner.
//
// Dönen sıra bir OLGUNUN sırasıdır, tercih sırası DEĞİLDİR: adayları tercih
// sırasına fulfillment dizer ve sıradaki ilk çalışan depoyu sepet akışı
// kullanır. Hiçbir lokasyon yetmiyorsa boş dilim
// döner, hata değil; saga bunu kendi bağlamında Conflict'e çevirir. Ayrıntılı
// gerekçe için bkz. [Service.LocationsWithStock].
func (i *Interop) LocationsWithStock(
	ctx context.Context,
	inventoryItemID string,
	quantity int64,
) ([]string, error) {
	return i.svc.LocationsWithStock(ctx, inventoryItemID, quantity)
}
