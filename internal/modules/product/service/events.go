package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// Katalog alan olaylarının adları (plan Bölüm 5.4).
//
// Adlar MODÜLLER ARASI SÖZLEŞMEDİR: aboneler (arama indeksi, vitrin önbelleği,
// dış katalog aktarımı) tam olarak bu adları dinler ve Redis backend'inde ad
// aynı zamanda stream adıdır. Bir adın değişmesi, tüm abonelerin sessizce olay
// almayı bırakması demektir — kimse hata görmez, yalnızca indeks eskir.
//
// Kapsam ürünün KENDİ alanlarıdır. Varyant, seçenek ve görsel yazmaları bu üç
// olayı ÜRETMEZ; gerekirse doğru adım "product.updated"ı oradan da yayımlamak
// değil, ayrı bir ad eklemektir: aynı adın iki farklı anlamı olması, aboneyi
// "bu olay neyi değiştirdi" sorusunu yükten yanıtlayamaz duruma sokar.
const (
	// EventProductCreated yeni bir ürün yazıldığında yayımlanır.
	EventProductCreated = "product.created"
	// EventProductUpdated ürünün kendi alanları güncellendiğinde yayımlanır.
	EventProductUpdated = "product.updated"
	// EventProductDeleted ürün SOFT silindiğinde yayımlanır.
	EventProductDeleted = "product.deleted"
)

// Olayların Data alanındaki anahtarlar.
//
// Anahtarlar da sözleşmedir ve tüketicilerle birlikte değişmelidir. Yük
// bilinçli olarak DARDIR: abonenin isteyebileceği her alanı (başlık, handle,
// koleksiyon, varyantlar) olaya koymak, olayı ürünün ikinci bir kopyası hâline
// getirir ve iki gösterim ayrışır — olayı işleyen indeks bir gün kayıtta
// olmayan bir başlığı gösterir. Abonenin elinde product_id vardır ve kaydı
// okuyabilir; vitrin gösteriminin tamamına toplu erişim için "product.interop"
// yüzeyi de buradadır (bkz. interop.go).
//
// # Her alan DİZEDİR — sayısal olanlar da
//
// Bugünkü iki alan da doğal olarak metindir; kural yine de BURADA yazılıdır,
// çünkü yüke eklenecek İLK sayısal alan (varyant adedi, sürüm numarası) onu
// ihlal etmeye en yatkın yerdir. Gerekçenin tamamı
// internal/modules/order/service/events.go içinde yazılıdır ve TEKRARLANMAZ:
// özeti, Redis backend'inin Data'yı JSON'a çevirmesi yüzünden int64 konan bir
// alanın aboneye float64 olarak ulaşması — yani sözleşmeye göre yazılmış bir
// abonenin geliştirmede çalışıp ÜRETİMDE düşmesidir.
//
// # status neden yükte
//
// Dar tutma kuralının bilinçli istisnasıdır. Abonenin bu olayla vereceği karar
// çoğunlukla "indeksle mi, indeksten DÜŞÜR mü"dür ve o karar yalnızca duruma
// bağlıdır. Alan olmasaydı taslak ürünler üzerinde yapılan toplu bir
// güncelleme, aboneyi olay başına bir okuma yapıp sonucu ATMAK zorunda
// bırakırdı; en sık durum en pahalı yol olurdu.
//
// Bedeli, değerin bayatlayabilmesidir: alan olayın ANINDAKİ durumu söyler, ŞU
// ANKİ durumu değil. Kesin karar veren abone yine kaydı okur — status ona
// yalnızca okumaya değmeyecek olayları ucuza eleme hakkı verir.
//
// # Kalıcı akışa kişisel veri konmaz
//
// Katalog verisi zaten kişisel değildir; kural yine de yazılıdır çünkü olay
// kalıcı bir akışa (Redis stream) yazılır ve oraya konan bir alan geri
// alınamaz. Yüke bir gün "oluşturan kullanıcı" eklemek isteyen kişi bu satırı
// görmelidir.
const (
	// EventFieldProductID ürünün kimliğidir; ÜÇ olayda da bulunur.
	EventFieldProductID = "product_id"
	// EventFieldStatus ürünün olay ANINDAKİ yayın durumudur ("draft",
	// "published" ya da "archived"); silme olayında BULUNMAZ.
	EventFieldStatus = "status"
)

// publishProductEvent katalog olayını yazma işlemi COMMIT EDİLDİKTEN SONRA
// yayımlar.
//
// status boş verilirse olaya yazılmaz; silme olayı bu yoldan geçer. Soft
// silinmiş kayıt hiçbir okumadan dönmediği için değeri abone tarafından
// doğrulanamaz ve "indeksten düşür" eylemi zaten duruma bakmaz. Silme olayında
// handle da yoktur: onu koymak silmeden ÖNCE fazladan bir okuma gerektirirdi
// ve handle'a göre önbellek tutan bir abone o eşlemeyi zaten daha önce aldığı
// created/updated olaylarından biliyordur.
//
// # Neden yazmadan SONRA
//
// Olay işlemin içinde yayımlansaydı bir abone olayı commit'ten önce alabilir,
// veritabanında henüz olmayan ürünü okumaya çalışır ve NotFound görürdü. Yayım
// commit'ten sonra yapıldığında olayı alan herkes kaydı bulabilir.
//
// # Yayım hatası yazmayı DÜŞÜRMEZ
//
// Bir ürün güncellemesinin "olay veri yolu erişilemez" diye başarısız olması
// YANLIŞTIR:
//
//  1. Ürün KAYITTIR, olay ise olmuş bir olgunun duyurusu. Redis'in bir
//     saniyelik erişilemezliği yüzünden yönetim arayüzünün katalogu
//     düzenleyememesi, korumaya çalıştığı şeyden pahalı bir kayıptır.
//  2. Yayım commit'ten SONRA yapılır; hata dönmek çağırana "değişiklik
//     uygulanmadı" demek olurdu, oysa uygulanmıştır. Çağıran isteği tekrarlar
//     ve CreateProduct'ta bu ya ikinci bir ürün ya da handle çakışması (409)
//     üretir — veri yolunun arızası kullanıcıya katalog hatası gibi görünürdü.
//  3. Yayımın BAŞARILI dönmesi teslimi zaten garanti etmez: [eventbus.EventBus]
//     sözleşmesi Publish'in handler'ları beklemediğini ve InMemory backend'inin
//     EN FAZLA BİR KEZ teslim ettiğini söyler. Yazmayı bu çağrıya bağlamak, var
//     olmayan bir garanti karşılığında gerçek veriyi riske atmak olurdu.
//
// Sipariş tarafındaki karar da aynıdır ama gerekçelerinden biri (saga'nın
// gereksiz telafi çalıştırması) burada YOKTUR: ürün CRUD'ı bir saga içinde
// değildir. Sonucu değiştiren bu değil, ilk iki maddedir.
//
// Bedel, kaçan olayın kaydın gerisinde kalmasıdır. Bedel GÖRÜNÜR kılınır: hata
// ERROR seviyesinde, olay adı ve ürün kimliğiyle loglanır; kaçan olay elle ya
// da bir tarama işiyle yeniden yayımlanabilir.
//
// # Veri yolu kayıtlı değilse olay sessizce atlanır
//
// [Options.Events] nil olabilir ve bu YALNIZCA gömülü kullanım ile testler
// içindir: [github.com/bdrtr/gobit/internal/modules/product.Module.Register]
// veri yolunu container'dan çözer ve bulamazsa açılışı düşürür, dolayısıyla
// üretimde nil bir veri yolu oluşmaz. Aynı kalıp Query katmanında da vardır
// (bkz. [Service.enrichVariants]).
func (s *Service) publishProductEvent(ctx context.Context, name, productID string, status models.Status) {
	if s.events == nil {
		s.log.DebugContext(ctx, "olay veri yolu kayıtlı değil; katalog olayı atlandı",
			"event", name, "product_id", productID)
		return
	}

	data := map[string]any{EventFieldProductID: productID}
	if status != "" {
		data[EventFieldStatus] = status.String()
	}

	if err := s.events.Publish(ctx, eventbus.Event{Name: name, Data: data}); err != nil {
		s.log.ErrorContext(ctx, "katalog olayı yayımlanamadı; ürün YAZILDI",
			"event", name,
			"product_id", productID,
			"error", err)
	}
}
