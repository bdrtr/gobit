package service

import (
	"context"
	"strconv"
	"time"

	"github.com/bdrtr/gobit/internal/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// EventOrderPlaced sipariş oluşturulduğunda yayımlanan olayın adıdır
// (plan Faz 6 DoD).
//
// Ad MODÜLLER ARASI SÖZLEŞMEDİR: aboneler (bildirim gönderimi, muhasebe
// aktarımı, arama indeksi) tam olarak bu adı dinler ve Redis backend'inde ad
// aynı zamanda stream adıdır. Değişmesi, tüm abonelerin sessizce olay almayı
// bırakması demektir.
const EventOrderPlaced = "order.placed"

// Olayın Data alanındaki anahtarlar.
//
// Anahtarlar da sözleşmedir ve tüketicilerle birlikte değişmelidir. Yük
// bilinçli olarak DAR tutulmuştur: abonenin ihtiyaç duyduğu her alanı olaya
// koymak, olayı siparişin ikinci bir kopyası hâline getirir ve iki gösterimin
// ayrışmasına yol açar. Aboneye lazım olan ayrıntı için elinde order_id vardır
// ve siparişi okuyabilir.
//
// # Her alan DİZEDİR — sayısal olanlar da
//
// Yükün TÜM değerleri string tipindedir; tutar ve sayaçlar ondalıksız onluk
// dize olarak taşınır (örn. "6100"). Kural, veri yolunun taşıma biçiminden
// doğar: [eventbus.EventBus]'ın üretimdeki Redis Streams backend'i Data'yı
// json.Marshal ile yazar ve okurken map[string]any içine çözer. JSON'un tek
// sayı tipi vardır, dolayısıyla int64 olarak konan bir alan aboneye float64
// olarak ulaşır — oysa aynı alan InMemory backend'inde int64 kalır. Sonuç iki
// katlıydı:
//
//  1. Sözleşmeye göre yazılmış bir abone (e.Data["total"].(int64))
//     geliştirmede çalışır, ÜRETİMDE düşerdi.
//  2. 2^53 minor unit üstündeki tutarlar float64'te sessizce yuvarlanırdı;
//     yani para float üzerinden geçerdi (plan Bölüm 8: float ASLA).
//
// Dize, iki backend'de de AYNI Go tipini ve TAM değeri verir. Abone tarafı
// strconv.ParseInt ile okur; dönüşümün hata dönebilmesi, sessiz yuvarlamaya
// yeğdir.
//
// E-posta BİLİNÇLİ OLARAK yoktur: olaylar Redis'e yazılır ve orada kalıcıdır;
// kişisel veriyi kalıcı bir akışa koymak, siparişin kendisinde zaten duran bir
// bilgi için gereksiz bir yayılımdır (plan Bölüm 8: hassas veri taşınmaz).
const (
	// EventFieldOrderID siparişin kimliğidir.
	EventFieldOrderID = "order_id"
	// EventFieldDisplayID siparişin insan okunur numarasıdır; ondalıksız dize.
	EventFieldDisplayID = "display_id"
	// EventFieldStatus siparişin durumudur.
	EventFieldStatus = "status"
	// EventFieldRegionID siparişin bölgesidir.
	EventFieldRegionID = "region_id"
	// EventFieldCustomerID siparişin müşterisidir; misafir siparişinde boştur.
	EventFieldCustomerID = "customer_id"
	// EventFieldCurrencyCode siparişin para birimidir.
	EventFieldCurrencyCode = "currency_code"
	// EventFieldTotal siparişin toplam tutarıdır: minor unit TAM SAYI,
	// ondalıksız dize olarak taşınır (örn. "6100").
	EventFieldTotal = "total"
	// EventFieldItemCount siparişteki satır sayısıdır; ondalıksız dize.
	EventFieldItemCount = "item_count"
	// EventFieldPlacedAt siparişin verildiği andır (RFC 3339, UTC).
	EventFieldPlacedAt = "placed_at"
)

// publishOrderPlaced sipariş yazıldıktan SONRA "order.placed" olayını yayımlar.
//
// # Neden yazma işleminden SONRA
//
// Olay, işlem içinde yayımlansaydı bir abone olayı işlem daha commit edilmeden
// alabilir, veritabanında olmayan bir siparişi okumaya çalışır ve NotFound
// görürdü. Yayım commit'ten sonra yapıldığında, olayı alan herkes siparişi
// bulabilir.
//
// # Yayım hatası siparişi DÜŞÜRMEZ
//
// Karar bilinçlidir ve üç gerekçesi vardır:
//
//  1. Sipariş KAYITTIR, olay ise olmuş bir olgunun duyurusu. Redis'in bir
//     saniyelik erişilemezliği yüzünden ödemesi alınmış bir siparişi geri
//     almak, korumaya çalıştığı şeyden çok daha pahalı bir kayıp olurdu.
//  2. Yayımın BAŞARILI dönmesi teslimi zaten garanti etmez: [eventbus.EventBus]
//     sözleşmesi Publish'in handler'ları beklemediğini, InMemory backend'inin
//     EN FAZLA BİR KEZ teslim ettiğini ve süreç ölürse olayın kaybolduğunu
//     söyler. Yazmayı bu çağrıya bağlamak, var olmayan bir garanti karşılığında
//     gerçek veriyi riske atmak olurdu.
//  3. Yayım commit'ten SONRA yapıldığı için sipariş zaten yazılmıştır; hata
//     dönmek çağırana "sipariş oluşmadı" demek olurdu ve saga gereksiz yere
//     telafi (iptal) çalıştırırdı.
//
// Bunun bedeli, kaybolan olayın kaydın gerisinde kalmasıdır. Bedel GÖRÜNÜR
// kılınır: hata ERROR seviyesinde, sipariş kimliği ve numarasıyla loglanır;
// böylece kaçan olay elle ya da bir tarama işiyle yeniden yayımlanabilir.
//
// Sayısal alanların neden DİZE olarak konduğu için bkz. [EventFieldTotal] ve
// üzerindeki blok.
func (s *Service) publishOrderPlaced(ctx context.Context, order models.Order, itemCount int) {
	event := eventbus.Event{
		Name: EventOrderPlaced,
		Data: map[string]any{
			EventFieldOrderID:      order.ID,
			EventFieldDisplayID:    strconv.FormatInt(order.DisplayID, 10),
			EventFieldStatus:       order.Status.String(),
			EventFieldRegionID:     order.RegionID,
			EventFieldCustomerID:   order.CustomerID,
			EventFieldCurrencyCode: order.CurrencyCode,
			EventFieldTotal:        strconv.FormatInt(order.Total, 10),
			EventFieldItemCount:    strconv.Itoa(itemCount),
			EventFieldPlacedAt:     order.PlacedAt.UTC().Format(time.RFC3339Nano),
		},
	}

	if err := s.events.Publish(ctx, event); err != nil {
		s.log.ErrorContext(ctx, "sipariş olayı yayımlanamadı; sipariş YAZILDI",
			"event", EventOrderPlaced,
			"order_id", order.ID,
			"display_id", order.DisplayID,
			"error", err)
	}
}
